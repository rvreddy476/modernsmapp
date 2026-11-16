-- 055 — every payment the buyer made that the order did not keep is recorded,
-- refunded and shown to the buyer.
--
-- Before this the buyer-facing payment notice (payment_notice.go) was derived
-- from payment_event_inbox alone: duplicate captures only, with the ORDER
-- value stated by payment.succeeded rather than the money captured (wrong on a
-- bank-offer payment), and no refund status, because the refund of a
-- duplicate happens on payments-service's ledger and commerce never recorded
-- it. Late captures, captures payments-service refused for their money, and a
-- second provider payment on one intent were invisible to the buyer.
--
--   payment_event_inbox.captured_minor
--       the money payment.succeeded says the provider captured (its
--       captured_minor). NULL on rows written before this migration, and on
--       an event that did not state it.
--
--   order_refund_commands.scope
--       'order'        the order's own refund (cancel, late capture): counted
--                      in the order's refund ledger, as every row before this
--       'owed_capture' a refund of a payment the order REFUSED (a capture
--                      whose money did not match the order). Never counted
--                      in the order's refunded total, never closed by the
--                      order's refunds, never subtracted from a cancel refund.
--   order_refund_commands.provider_failed_at / provider_failure_code
--       payments-service parked the refund (payment.refund_failed). The
--       command's own status is commerce's worker's; this is what the buyer
--       is told while a person sorts it out.
--
--   order_owed_captures
--       one row per payment owed back to the buyer: what was paid (money),
--       why, how it is being refunded, and how far that refund has got.
--       payment_notice is read from here.
--
--   order_owed_capture_refunds
--       the settled refunds of an owed payment that reached commerce as
--       payment.refunded, one row per provider refund id (a refund published
--       under two event ids counts once).
--
-- Expand-only. The new columns are nullable or carry a constant default
-- (catalog-only ADD COLUMN), added only when missing, under lock_timeout 5s.
-- An old pod never reads them. During a mixed rollout an old pod settling an
-- order's full refund closes every open command of the order, an owed_capture
-- one included; nothing creates owed_capture commands before this image.
DO $$
DECLARE
    prev_lock_timeout TEXT := current_setting('lock_timeout');
BEGIN
    IF to_regclass('payment_event_inbox') IS NOT NULL AND NOT EXISTS (
        SELECT 1 FROM pg_attribute
        WHERE attrelid = to_regclass('payment_event_inbox')
          AND attname = 'captured_minor' AND NOT attisdropped
    ) THEN
        PERFORM set_config('lock_timeout', '5s', true);
        ALTER TABLE payment_event_inbox ADD COLUMN IF NOT EXISTS captured_minor BIGINT;
        PERFORM set_config('lock_timeout', prev_lock_timeout, true);
    END IF;

    IF to_regclass('order_refund_commands') IS NOT NULL AND NOT EXISTS (
        SELECT 1 FROM pg_attribute
        WHERE attrelid = to_regclass('order_refund_commands')
          AND attname = 'scope' AND NOT attisdropped
    ) THEN
        PERFORM set_config('lock_timeout', '5s', true);
        ALTER TABLE order_refund_commands
            ADD COLUMN IF NOT EXISTS scope                 TEXT NOT NULL DEFAULT 'order',
            ADD COLUMN IF NOT EXISTS provider_failed_at    TIMESTAMPTZ,
            ADD COLUMN IF NOT EXISTS provider_failure_code TEXT;
        PERFORM set_config('lock_timeout', prev_lock_timeout, true);
    END IF;
END $$;

CREATE TABLE IF NOT EXISTS order_owed_captures (
    id               UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    order_id         UUID NOT NULL REFERENCES orders(id),
    -- One row per owed payment:
    --   intent:<intent id>              a whole intent owed back (a duplicate
    --                                   order capture, a late capture, a
    --                                   capture commerce refused)
    --   late-capture:<order id>         a late capture that stated no intent
    --   refund_required:<row id>        a payment payments-service recorded
    --                                   on its own (payment.owed_capture)
    owed_key         TEXT NOT NULL UNIQUE CHECK (length(btrim(owed_key)) > 0),
    intent_id        TEXT,
    -- payments-service's refund_required row, once it has announced one.
    payments_owed_id BIGINT,
    -- duplicate_order_capture | duplicate_capture | late_capture |
    -- late_capture_after_failed | capture_mismatch
    reason           TEXT NOT NULL CHECK (length(btrim(reason)) > 0),
    -- The MONEY the buyer paid on this payment.
    amount_minor     BIGINT NOT NULL CHECK (amount_minor >= 0),
    -- false: the amount is the order value the event stated, not money a
    -- provider confirmed (a row from before captured amounts were recorded).
    amount_exact     BOOLEAN NOT NULL DEFAULT TRUE,
    currency         TEXT NOT NULL DEFAULT 'INR',
    -- Who refunds it: payments (refund_required), order (the order's own
    -- refund command and ledger), owed_command (a scope='owed_capture'
    -- command).
    refund_via       TEXT NOT NULL CHECK (refund_via IN ('payments','order','owed_command')),
    -- What the refund path must return, in that path's own unit (money for
    -- payments, order value for the two command paths), and what it has.
    refund_due_minor BIGINT NOT NULL CHECK (refund_due_minor >= 0),
    refunded_minor   BIGINT NOT NULL DEFAULT 0 CHECK (refunded_minor >= 0),
    refund_status    TEXT NOT NULL DEFAULT 'pending'
        CHECK (refund_status IN ('pending','refunded','failed')),
    refunded_at      TIMESTAMPTZ,
    failed_at        TIMESTAMPTZ,
    failure_code     TEXT,
    detail           TEXT,
    detected_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_order_owed_captures_order  ON order_owed_captures (order_id);
CREATE INDEX IF NOT EXISTS idx_order_owed_captures_intent ON order_owed_captures (intent_id);
CREATE INDEX IF NOT EXISTS idx_order_owed_captures_payments_row
    ON order_owed_captures (payments_owed_id) WHERE payments_owed_id IS NOT NULL;

CREATE TABLE IF NOT EXISTS order_owed_capture_refunds (
    refund_id    TEXT PRIMARY KEY CHECK (length(btrim(refund_id)) > 0),
    owed_id      UUID NOT NULL REFERENCES order_owed_captures(id),
    event_id     TEXT NOT NULL,
    amount_minor BIGINT NOT NULL CHECK (amount_minor > 0),
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_order_owed_capture_refunds_owed ON order_owed_capture_refunds (owed_id);

-- Backfill what was already known, so the notice reads one table.
--
-- Duplicate captures: payment.succeeded inbox rows on an intent that is not
-- the order's bound intent (the rule payment_notice used), excluding an intent
-- the order refunds itself (a late capture). The amount is the order value the
-- event stated. On an order without a bank offer that IS the money
-- (payments-service refuses any other capture); on a bank-offer order nothing
-- recorded the money, so it is marked inexact until payments-service's
-- payment.owed_capture for it replaces it. Late captures below likewise.
INSERT INTO order_owed_captures
       (order_id, owed_key, intent_id, reason, amount_minor, amount_exact, currency,
        refund_via, refund_due_minor, detected_at)
SELECT DISTINCT ON (lower(btrim(i.intent_id)))
       i.order_id, 'intent:' || lower(btrim(i.intent_id)), lower(btrim(i.intent_id)),
       'duplicate_order_capture', GREATEST(COALESCE(i.amount_minor, 0), 0),
       (o.offer_id IS NULL AND o.captured_minor IS NULL),
       COALESCE(NULLIF(btrim(o.currency_code), ''), 'INR'),
       'payments', GREATEST(COALESCE(i.amount_minor, 0), 0), i.processed_at
  FROM payment_event_inbox i
  JOIN orders o ON o.id = i.order_id
 WHERE i.event_type = 'payment.succeeded'
   AND o.payment_intent_id IS NOT NULL
   AND btrim(COALESCE(i.intent_id, '')) <> ''
   AND lower(btrim(i.intent_id)) <> lower(o.payment_intent_id::text)
   AND NOT EXISTS (SELECT 1 FROM order_refund_commands c
                    WHERE c.order_id = i.order_id
                      AND lower(btrim(COALESCE(c.intent_id, ''))) = lower(btrim(i.intent_id)))
 ORDER BY lower(btrim(i.intent_id)), i.processed_at
ON CONFLICT (owed_key) DO NOTHING;

-- Late captures: the order's own refund command for money that arrived after
-- the order could no longer take it (p0.go's captureLate). Its status follows
-- the command.
INSERT INTO order_owed_captures
       (order_id, owed_key, intent_id, reason, amount_minor, amount_exact, currency,
        refund_via, refund_due_minor, refunded_minor, refund_status, refunded_at, failed_at, detected_at)
SELECT c.order_id,
       CASE WHEN btrim(COALESCE(c.intent_id, '')) = '' THEN 'late-capture:' || c.order_id::text
            ELSE 'intent:' || lower(btrim(c.intent_id)) END,
       NULLIF(lower(btrim(COALESCE(c.intent_id, ''))), ''),
       'late_capture', c.amount_minor, (o.offer_id IS NULL AND o.captured_minor IS NULL),
       COALESCE(NULLIF(btrim(o.currency_code), ''), 'INR'),
       'order', c.amount_minor,
       CASE WHEN c.status = 'succeeded' THEN c.amount_minor ELSE 0 END,
       CASE WHEN c.status = 'succeeded' THEN 'refunded'
            WHEN c.status = 'needs_attention' THEN 'failed'
            ELSE 'pending' END,
       CASE WHEN c.status = 'succeeded' THEN COALESCE(c.settled_at, c.updated_at) END,
       CASE WHEN c.status = 'needs_attention' THEN c.updated_at END,
       c.created_at
  FROM order_refund_commands c
  JOIN orders o ON o.id = c.order_id
 WHERE c.idempotency_key LIKE 'late-capture:%'
ON CONFLICT (owed_key) DO NOTHING;
