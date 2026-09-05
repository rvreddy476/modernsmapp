-- Commerce P0 — payments-service migration 007
--
-- Three separable changes, all expand-phase only. Nothing is dropped, no
-- constraint is added that an old writer could trip, and every column is
-- nullable or defaulted so a mixed fleet keeps working while replicas roll.
--
--   1. Provider-neutral columns (D10, review §4). The schema currently
--      encodes Razorpay in its column names via `provider_ref`. A second
--      adapter cannot be added without either overloading that column or
--      renaming it under load, so the neutral names arrive now and are
--      dual-written; `provider_ref` stays as the authority until reads flip.
--
--   2. A provider-namespaced webhook inbox (A3, review R-5). The existing
--      `webhook_events` table is keyed on `event_id` alone, and the handler
--      reads that id from the BODY. Razorpay does not put an event id in the
--      payment webhook body — it sends `x-razorpay-event-id` as a HEADER —
--      so today's key is very often the empty string. The first event to
--      arrive takes the empty key, and every later payment is then treated
--      as a duplicate and silently acknowledged without recording its money
--      effect. The new table forbids an empty id and namespaces by provider
--      so two PSPs cannot collide.
--
--   3. Durable refund commands (A6, LB-8, review §2.1 5.13). Refunds are
--      currently applied to the intent BEFORE the provider is called, and a
--      provider failure is logged and swallowed — so the ledger says
--      "refunded" while the money never moved. A refund now becomes a
--      durable command that is persisted before any network I/O, carries a
--      deterministic provider idempotency key so a retry cannot double-pay,
--      and only reaches `succeeded` on a verified provider outcome.

-- ─── 1. Provider-neutral identity ────────────────────────────────────

ALTER TABLE payments.payment_intents
    ADD COLUMN IF NOT EXISTS provider            TEXT,
    ADD COLUMN IF NOT EXISTS provider_order_id   TEXT,
    ADD COLUMN IF NOT EXISTS provider_payment_id TEXT;

-- Backfill from the Razorpay-shaped column. `provider_ref` held the PSP
-- ORDER id (it is what VerifyIntent compares against razorpay_order_id),
-- so it maps to provider_order_id, not provider_payment_id.
UPDATE payments.payment_intents
   SET provider          = COALESCE(provider, 'razorpay'),
       provider_order_id = COALESCE(provider_order_id, NULLIF(provider_ref, ''))
 WHERE provider IS NULL OR provider_order_id IS NULL;

ALTER TABLE payments.payment_intents
    ALTER COLUMN provider SET DEFAULT 'razorpay';

CREATE INDEX IF NOT EXISTS idx_payment_intents_provider_order
    ON payments.payment_intents (provider, provider_order_id)
    WHERE provider_order_id IS NOT NULL;

-- The caller domain that owns this intent. Payments is shared with
-- food-service, so a bare reference UUID cannot carry authority: without
-- this column an authorization check has no way to tell a commerce order
-- from a food order (review §5 D4). Backfilled from reference_type, which
-- has always been populated by both callers.
ALTER TABLE payments.payment_intents
    ADD COLUMN IF NOT EXISTS owner_domain TEXT;

UPDATE payments.payment_intents
   SET owner_domain = CASE
        WHEN reference_type = 'order'      THEN 'commerce-service'
        WHEN reference_type = 'food_order' THEN 'food-service'
        ELSE 'unknown'
       END
 WHERE owner_domain IS NULL;

CREATE INDEX IF NOT EXISTS idx_payment_intents_owner_domain
    ON payments.payment_intents (owner_domain, reference_type, reference_id);

-- ─── 2. Provider-namespaced webhook inbox ────────────────────────────

CREATE TABLE IF NOT EXISTS payments.provider_events (
    provider    TEXT        NOT NULL,
    event_id    TEXT        NOT NULL,
    event_type  TEXT        NOT NULL,
    -- The provider's own payment/order identifiers, recorded so an
    -- operator can correlate an inbox row with a PSP dashboard entry
    -- without reparsing the payload.
    provider_order_id   TEXT,
    provider_payment_id TEXT,
    received_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (provider, event_id),
    -- R-5: an empty event id is not a valid dedupe key. Accepting one
    -- lets a single event occupy the key and mask every later payment.
    CONSTRAINT chk_provider_event_id_not_blank CHECK (length(btrim(event_id)) > 0)
);

CREATE INDEX IF NOT EXISTS idx_provider_events_received
    ON payments.provider_events (received_at DESC);

-- Carry forward whatever the old table holds so a redelivery of an event
-- already processed under the legacy key is still suppressed. Rows with a
-- blank id are deliberately skipped: they are the symptom of the defect,
-- not state worth preserving.
INSERT INTO payments.provider_events (provider, event_id, event_type, received_at)
SELECT 'razorpay', event_id, event_type, received_at
  FROM payments.webhook_events
 WHERE length(btrim(event_id)) > 0
ON CONFLICT DO NOTHING;

-- ─── 3. Durable refund commands ──────────────────────────────────────

-- Amount reserved by refund commands that are in flight. The cap check
-- must count money that is committed-but-not-yet-settled, otherwise two
-- concurrent refunds each see the full remaining balance and together
-- over-refund the intent.
ALTER TABLE payments.payment_intents
    ADD COLUMN IF NOT EXISTS refund_reserved_minor BIGINT NOT NULL DEFAULT 0;

CREATE TABLE IF NOT EXISTS payments.refund_commands (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    intent_id    UUID NOT NULL REFERENCES payments.payment_intents(id),
    amount_minor BIGINT NOT NULL CHECK (amount_minor > 0),
    currency     TEXT NOT NULL DEFAULT 'INR',
    reason       TEXT,

    -- Deterministic and unique. The SAME value goes to the provider on
    -- every attempt, so an ambiguous timeout followed by a retry produces
    -- one refund at the PSP, not two (A6). It is also the natural dedupe
    -- key for the caller: commerce derives it from (order, return/cancel),
    -- so a double-tap upstream collapses here.
    provider_idempotency_key TEXT NOT NULL UNIQUE,

    status TEXT NOT NULL DEFAULT 'pending'
        CHECK (status IN ('pending','submitted','succeeded','failed','needs_attention')),

    provider            TEXT NOT NULL DEFAULT 'razorpay',
    provider_refund_id  TEXT,

    attempts        INT NOT NULL DEFAULT 0,
    last_error      TEXT,
    next_attempt_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    requested_by TEXT NOT NULL,          -- issuing service identity
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    settled_at   TIMESTAMPTZ
);

-- The retry worker's claim query.
CREATE INDEX IF NOT EXISTS idx_refund_commands_due
    ON payments.refund_commands (next_attempt_at)
    WHERE status IN ('pending','submitted');

CREATE INDEX IF NOT EXISTS idx_refund_commands_intent
    ON payments.refund_commands (intent_id, created_at DESC);

-- Reconciliation reads this to alarm on a refund that has been in flight
-- past its SLA.
CREATE INDEX IF NOT EXISTS idx_refund_commands_unsettled
    ON payments.refund_commands (created_at)
    WHERE status <> 'succeeded';

-- Idempotency for the provider's refund webhook, keyed on the provider's
-- own refund id. Distinct from provider_events: one webhook event carries
-- one refund, but a provider may re-emit the same refund under a new event
-- id, and applying it twice would double-credit the ledger.
CREATE TABLE IF NOT EXISTS payments.provider_refunds_applied (
    provider            TEXT   NOT NULL,
    provider_refund_id  TEXT   NOT NULL,
    command_id          UUID,
    intent_id           UUID   NOT NULL,
    amount_minor        BIGINT NOT NULL CHECK (amount_minor > 0),
    applied_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (provider, provider_refund_id)
);

INSERT INTO payments.provider_refunds_applied (provider, provider_refund_id, intent_id, amount_minor, applied_at)
SELECT 'razorpay', refund_provider_ref, intent_id, amount_minor, applied_at
  FROM payments.refunds_applied
 WHERE length(btrim(refund_provider_ref)) > 0
ON CONFLICT DO NOTHING;

-- ─── Integrity constraints, NOT VALID ────────────────────────────────
--
-- Review §5.1: a validated constraint here would reject writes from
-- replicas still running the old code during the rollout. These are added
-- unvalidated; a later migration runs VALIDATE CONSTRAINT once every old
-- writer is drained and the high-watermark comparison is green.

DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint
         WHERE conrelid = 'payments.payment_intents'::regclass
           AND conname  = 'chk_refund_within_amount'
    ) THEN
        ALTER TABLE payments.payment_intents
            ADD CONSTRAINT chk_refund_within_amount
            CHECK (refunded_amount_minor + refund_reserved_minor <= amount_minor)
            NOT VALID;
    END IF;
END$$;

DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint
         WHERE conrelid = 'payments.payment_intents'::regclass
           AND conname  = 'chk_refund_reserved_non_negative'
    ) THEN
        ALTER TABLE payments.payment_intents
            ADD CONSTRAINT chk_refund_reserved_non_negative
            CHECK (refund_reserved_minor >= 0)
            NOT VALID;
    END IF;
END$$;
