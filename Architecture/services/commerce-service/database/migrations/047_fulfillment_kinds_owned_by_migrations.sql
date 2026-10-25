-- 047 — the fulfillment_jobs kind CHECK leaves setup.sql.
--
-- setup.sql used to drop and re-add fulfillment_jobs_kind_check on every
-- boot, to widen databases created before the two bulk-import kinds existed.
-- A file that runs on every boot must not restate a constraint (it undoes any
-- later migration of it; see the note in setup.sql), so that widening lives
-- here and runs once.
--
-- Conditional: a database whose constraint already admits all four kinds —
-- every fresh one, whose CREATE TABLE carries them, and every one that booted
-- the old setup.sql — is left exactly as it is.

DO $$
DECLARE
    def TEXT;
BEGIN
    SELECT pg_get_constraintdef(c.oid) INTO def
      FROM pg_constraint c
     WHERE c.conrelid = 'fulfillment_jobs'::regclass
       AND c.conname = 'fulfillment_jobs_kind_check';

    IF def IS NOT NULL
       AND def LIKE '%''bulk_import_validate''%'
       AND def LIKE '%''bulk_import_execute''%' THEN
        RETURN;
    END IF;

    ALTER TABLE fulfillment_jobs DROP CONSTRAINT IF EXISTS fulfillment_jobs_kind_check; -- expand-only: replaced at once below by a strict superset (the original two kinds plus the two bulk-import kinds)
    ALTER TABLE fulfillment_jobs ADD CONSTRAINT fulfillment_jobs_kind_check -- expand-only: strict superset of every list this constraint has had
        CHECK (kind IN (
            'fulfill_paid_order',
            'process_return_approved',
            'bulk_import_validate',
            'bulk_import_execute'
        ));
END $$;
