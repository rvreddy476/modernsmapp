-- Audit P6 + P7: partial refunds + paise-minor amount tracking.
--
-- Before this migration:
--   * payments-service Service.InitiateRefund accepted no amount; the
--     entire intent was always marked refunded regardless of the actual
--     refund value commerce-service computed (P6 — amount cap missing).
--   * commerce-service passed refund amount as float64 rupees, then never
--     forwarded it to payments-service (P7 — precision loss at scale,
--     plus the bare DB status flip meant Razorpay's refund API was never
--     called for the correct sub-amount).
--
-- This migration adds a refunded_amount_minor int64 paise column so we
-- can track partial refunds without revisiting payment_intents.amount
-- (which is a NUMERIC(12,2) rupees column — leaving it alone in this
-- scope; see the audit report). The new `partially_refunded` status
-- is reached when refunded_amount_minor > 0 but < amount_minor; the
-- existing `refunded` status is reached only on full refunds.

ALTER TABLE payments.payment_intents
    ADD COLUMN IF NOT EXISTS refunded_amount_minor BIGINT NOT NULL DEFAULT 0;

-- Drop the legacy status CHECK constraint and replace with the expanded
-- allow-list. Postgres CHECK constraint names are predictable (table_col_check)
-- but we don't rely on that — instead drop by enumerating in a DO block so
-- the migration is idempotent across environments where the constraint may
-- have been renamed.
DO $$
DECLARE
    r RECORD;
BEGIN
    FOR r IN
        SELECT conname FROM pg_constraint
        WHERE conrelid = 'payments.payment_intents'::regclass
          AND contype = 'c'
          AND pg_get_constraintdef(oid) LIKE '%status%'
    LOOP
        EXECUTE format('ALTER TABLE payments.payment_intents DROP CONSTRAINT %I', r.conname);
    END LOOP;
END$$;

ALTER TABLE payments.payment_intents
    ADD CONSTRAINT payment_intents_status_check
    CHECK (status IN ('pending','processing','succeeded','failed','refunded','partially_refunded','disputed'));
