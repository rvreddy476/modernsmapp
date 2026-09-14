-- GATED — payments-service 998: enforce application_id (NOT run at boot)
--
-- Migration 010 added application_id as NULLABLE and backfilled it, because it
-- runs while old replicas are still serving and an old replica writes intents
-- and refund commands without the column. This is the enforce step.
--
-- RUN ONLY WHEN:
--   * every payments-service replica runs code that writes application_id
--     (the release that shipped 010), and no older replica is left; and
--   * SELECT * FROM payments.application_backfill_report ORDER BY id DESC
--     shows no unmapped rows, or an operator has mapped them (see
--     docs/runbooks/payments-applications.md).
--
-- WHAT IT DOES: re-runs payments.backfill_application_ids() (so rows an old
-- replica wrote after 010 are filled), refuses with an error if any row in any
-- table is still NULL, then sets NOT NULL on all six columns.
--
-- LOCKS: SET NOT NULL takes an ACCESS EXCLUSIVE lock on each table and scans it.
-- Writes to that table block for the scan. Run it in a quiet window.
--
-- ROLLBACK: ALTER TABLE payments.<table> ALTER COLUMN application_id DROP NOT NULL;

BEGIN;

SELECT * FROM payments.backfill_application_ids();

DO $$
DECLARE
    nulls BIGINT;
BEGIN
    SELECT (SELECT count(*) FROM payments.payment_intents          WHERE application_id IS NULL)
         + (SELECT count(*) FROM payments.refund_commands          WHERE application_id IS NULL)
         + (SELECT count(*) FROM payments.refund_required          WHERE application_id IS NULL)
         + (SELECT count(*) FROM payments.provider_refunds_applied WHERE application_id IS NULL)
         + (SELECT count(*) FROM payments.refunds_applied          WHERE application_id IS NULL)
         + (SELECT count(*) FROM payments.payment_holds            WHERE application_id IS NULL)
      INTO nulls;
    IF nulls > 0 THEN
        RAISE EXCEPTION 'payments 998: % row(s) still have no application_id; map them before enforcing', nulls;
    END IF;
END$$;

ALTER TABLE payments.payment_intents          ALTER COLUMN application_id SET NOT NULL;
ALTER TABLE payments.refund_commands          ALTER COLUMN application_id SET NOT NULL;
ALTER TABLE payments.refund_required          ALTER COLUMN application_id SET NOT NULL;
ALTER TABLE payments.provider_refunds_applied ALTER COLUMN application_id SET NOT NULL;
ALTER TABLE payments.refunds_applied          ALTER COLUMN application_id SET NOT NULL;
ALTER TABLE payments.payment_holds            ALTER COLUMN application_id SET NOT NULL;

COMMIT;
