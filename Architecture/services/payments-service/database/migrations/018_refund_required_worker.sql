-- payments-service migration 018 — money owed back is returned, not listed
--
-- payments.refund_required (migration 008) records a capture the intent
-- cannot accept: a late capture on an intent already finalised FAILED, or a
-- duplicate capture (a second, distinct provider payment) on a paid one. The
-- row was a list for an operator and nothing refunded it. The founder rule is
-- that nothing is manual: every payment moves through payments-service on the
-- provider's signed event. So:
--
--   * the refund-required worker (service.RunRefundRequiredWorker, behind
--     PAYMENTS_REFUND_REQUIRED_WORKER) claims unresolved rows with FOR UPDATE
--     SKIP LOCKED, the claim pushing next_attempt_at out as its lease, and
--     asks the provider to refund THAT payment under an idempotency key
--     derived from the row id;
--   * the provider's signed refund webhook for that payment resolves the row
--     (ApplyRefundWebhookAtomically). It never touches the intent and never
--     publishes payment.refunded: the order was not refunded, the second
--     payment was.
--
-- New columns on payments.refund_required:
--
--   refund_attempts      worker attempts (claims) so far
--   next_attempt_at      not claimable before this time; the claim's lease
--   last_error           the redacted reason the last attempt failed
--   provider_refund_id   the refund the provider accepted for this payment
--   refund_submitted_at  when the provider first accepted it
--   refunded_minor       settled refunds of this payment, from signed events
--   parked_at            the worker stopped: the provider refused for good, or
--                        the row lacks what a refund needs. Alarmed, not retried.
--   failure_code         machine-readable reason for parked_at
--
-- payments.refund_required_refunds is the per-refund ledger of those settled
-- refunds: one row per (provider, provider_refund_id), so a refund event
-- redelivered under another event id is counted once.
--
-- Expand-only. Every new column is nullable or has a constant default, so ADD
-- COLUMN is catalog-only (no rewrite); the ALTER runs only when a column is
-- missing, with lock_timeout 5s. An old writer never reads or writes these,
-- so a mixed fleet keeps working mid-rollout.
DO $$
DECLARE
    prev_lock_timeout TEXT := current_setting('lock_timeout');
BEGIN
    IF to_regclass('payments.refund_required') IS NOT NULL AND NOT EXISTS (
        SELECT 1 FROM pg_attribute
        WHERE attrelid = to_regclass('payments.refund_required')
          AND attname = 'refunded_minor' AND NOT attisdropped
    ) THEN
        PERFORM set_config('lock_timeout', '5s', true);
        ALTER TABLE payments.refund_required
            ADD COLUMN IF NOT EXISTS refund_attempts     INT         NOT NULL DEFAULT 0,
            ADD COLUMN IF NOT EXISTS next_attempt_at     TIMESTAMPTZ NOT NULL DEFAULT '-infinity',
            ADD COLUMN IF NOT EXISTS last_error          TEXT,
            ADD COLUMN IF NOT EXISTS provider_refund_id  TEXT,
            ADD COLUMN IF NOT EXISTS refund_submitted_at TIMESTAMPTZ,
            ADD COLUMN IF NOT EXISTS refunded_minor      BIGINT      NOT NULL DEFAULT 0,
            ADD COLUMN IF NOT EXISTS parked_at           TIMESTAMPTZ,
            ADD COLUMN IF NOT EXISTS failure_code        TEXT;
        PERFORM set_config('lock_timeout', prev_lock_timeout, true);
    END IF;
END $$;

-- The worker's claim: unresolved, not parked, due.
CREATE INDEX IF NOT EXISTS idx_refund_required_due
    ON payments.refund_required (next_attempt_at, id)
    WHERE resolved_at IS NULL AND parked_at IS NULL;

CREATE TABLE IF NOT EXISTS payments.refund_required_refunds (
    provider           TEXT        NOT NULL,
    provider_refund_id TEXT        NOT NULL,
    refund_required_id BIGINT      NOT NULL REFERENCES payments.refund_required(id),
    amount_minor       BIGINT      NOT NULL CHECK (amount_minor > 0),
    currency           TEXT        NOT NULL,
    event_id           TEXT        NOT NULL,
    applied_at         TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (provider, provider_refund_id)
);

CREATE INDEX IF NOT EXISTS idx_refund_required_refunds_row
    ON payments.refund_required_refunds (refund_required_id);
