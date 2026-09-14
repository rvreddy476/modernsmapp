-- payments-service migration 009 — a parked refund can be seen, alarmed and resolved
--
-- The refund worker parks a refund that can never succeed as
-- `needs_attention`. Until now that was a log line and nothing else: no event,
-- no metric, and no way to close the command except by hand-editing the row.
--
--   1. `failure_code` — the machine-readable reason a command was parked
--      (provider_rejected, payment_not_found, …), written in the same statement
--      as the park. `last_error` keeps the redacted human-readable text.
--   2. `resolved` — a terminal status an operator moves a parked command to,
--      with the resolution (refunded_manually | written_off | test_data), a
--      note, who resolved it and when. The CHECK constraint is widened to allow
--      it; an old writer never writes it, so a mixed fleet keeps working.
--   3. A partial index for the operator list and the alarm gauge.
--
-- Re-runnable: every step is guarded.

ALTER TABLE payments.refund_commands
    ADD COLUMN IF NOT EXISTS failure_code    TEXT,
    ADD COLUMN IF NOT EXISTS resolution      TEXT,
    ADD COLUMN IF NOT EXISTS resolution_note TEXT,
    ADD COLUMN IF NOT EXISTS resolved_by     TEXT,
    ADD COLUMN IF NOT EXISTS resolved_at     TIMESTAMPTZ;

-- Widen the status vocabulary. Dropped and re-added in this one transaction,
-- so no write can observe the table without a status constraint.
DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint
         WHERE conrelid = 'payments.refund_commands'::regclass
           AND conname  = 'refund_commands_status_check'
           AND pg_get_constraintdef(oid) LIKE '%resolved%'
    ) THEN
        ALTER TABLE payments.refund_commands DROP CONSTRAINT IF EXISTS refund_commands_status_check;
        ALTER TABLE payments.refund_commands
            ADD CONSTRAINT refund_commands_status_check
            CHECK (status IN ('pending','submitted','succeeded','failed','needs_attention','resolved'));
    END IF;
END$$;

-- A resolved command always says how, and only a resolved command does.
DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint
         WHERE conrelid = 'payments.refund_commands'::regclass
           AND conname  = 'chk_refund_commands_resolution'
    ) THEN
        ALTER TABLE payments.refund_commands
            ADD CONSTRAINT chk_refund_commands_resolution
            CHECK (
                (status = 'resolved') = (resolution IS NOT NULL)
                AND (resolution IS NULL OR resolution IN ('refunded_manually','written_off','test_data'))
            );
    END IF;
END$$;

CREATE INDEX IF NOT EXISTS idx_refund_commands_needs_attention
    ON payments.refund_commands (created_at, id)
    WHERE status = 'needs_attention';
