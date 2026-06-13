-- Audit P7-deep: hoist PaymentIntent.amount from NUMERIC(12,2) rupees-major
-- to BIGINT paise-minor as the new source of truth.
--
-- Before this migration:
--   * payment_intents.amount is NUMERIC(12,2) rupees and is the only field
--     describing the intent value. Every comparison or arithmetic site that
--     touches it converts via math.Round(amount * 100), and every write
--     site that started in paise had to divide back by 100. Float-to-paise
--     round-trips on the wire (commerce->payments) silently lossy ₹X.YZ
--     amounts that don't survive IEEE-754 exactly.
--   * AmountMinor() in Go does the same math.Round at read time, so any
--     future caller that pre-computes an int64 paise value and stores it
--     loses the round-trip.
--
-- This migration:
--   1. Adds amount_minor BIGINT (IF NOT EXISTS — safe on re-run).
--   2. Backfills it from existing amount: ROUND(amount * 100), but only
--      where amount_minor is missing (NULL or 0) so a partial backfill that
--      gets interrupted picks up where it left off.
--   3. Pins amount_minor NOT NULL DEFAULT 0 once backfilled so future
--      INSERTs that forget the field still pass the constraint (the Go
--      writer always supplies a value, but the default keeps the column
--      compatible with old client code during the dual-write window).
--   4. LEAVES `amount` in place as a deprecated mirror — it's still
--      referenced from analytics dashboards / external readers. The Go
--      writer dual-writes both columns for one release cycle; a follow-up
--      migration drops `amount` once consumers have switched.

ALTER TABLE payments.payment_intents
    ADD COLUMN IF NOT EXISTS amount_minor BIGINT;

UPDATE payments.payment_intents
   SET amount_minor = ROUND(amount * 100)
 WHERE amount_minor IS NULL OR amount_minor = 0;

ALTER TABLE payments.payment_intents
    ALTER COLUMN amount_minor SET DEFAULT 0;

-- SET NOT NULL is only safe once every row has a non-NULL value. After the
-- backfill above that's guaranteed, but a NULL slipping in via a parallel
-- INSERT-during-deploy would block the ALTER. The UPDATE above is the
-- backfill; this guards future inserts.
DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1
        FROM information_schema.columns
        WHERE table_schema = 'payments'
          AND table_name = 'payment_intents'
          AND column_name = 'amount_minor'
          AND is_nullable = 'NO'
    ) THEN
        ALTER TABLE payments.payment_intents
            ALTER COLUMN amount_minor SET NOT NULL;
    END IF;
END$$;
