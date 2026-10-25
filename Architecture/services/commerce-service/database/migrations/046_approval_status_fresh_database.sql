-- 046 — a fresh database gets the full approval vocabulary, and a database
-- that has been contracted keeps its contraction.
--
-- BootstrapSchema runs database/setup.sql and THEN the migrations. Migration
-- 001 states products_approval_status_check with the ORIGINAL list, which
-- has no 'pending', 'flagged' or 'changes_requested' (the moderator's
-- "request changes"). setup.sql used to restate the full list on every boot,
-- so a database that had booted twice had it — but a FRESH database (a new
-- environment, CI) had 001's narrow list until its first restart, and
-- POST /internal/admin/products/:id/request-changes answered 400.
--
-- setup.sql no longer restates any constraint (it was also undoing gated
-- 1001 on every boot), so this file is now the one place the full list is
-- installed. It is conditional on what is there:
--
--   already carries pending, flagged and changes_requested   nothing to do.
--       Every database that booted the old setup.sql, and every database
--       gated 1001 has contracted (1001's list has all three, no 'live').
--   missing any of them, and still permits 'live'           the full list,
--       'live' included. A fresh database after 001. 'live' stays until
--       gated 1001 removes it once the old image is drained.
--   missing any of them, and 'live' already gone            the full list
--       without 'live'. Not produced by any file in this tree; handled so
--       this migration can never put back what a contraction took out.
--
-- Either branch is a strict superset of what it replaces. The list must stay
-- in step with gated 1001 (TestApprovalVocabularyIsOwnedByTheMigrations).

DO $$
DECLARE
    def TEXT;
BEGIN
    SELECT pg_get_constraintdef(c.oid) INTO def
      FROM pg_constraint c
     WHERE c.conrelid = 'products'::regclass
       AND c.conname = 'products_approval_status_check';

    IF def IS NOT NULL
       AND def LIKE '%''pending''%'
       AND def LIKE '%''flagged''%'
       AND def LIKE '%''changes_requested''%' THEN
        RETURN;
    END IF;

    ALTER TABLE products DROP CONSTRAINT IF EXISTS products_approval_status_check; -- expand-only: replaced at once below by a strict superset of the constraint it drops (it adds the missing values and keeps 'live' exactly when the old one had it)

    IF def IS NULL OR def LIKE '%''live''%' THEN
        ALTER TABLE products ADD CONSTRAINT products_approval_status_check -- expand-only: superset of 001's list (adds pending, flagged, changes_requested; keeps live)
            CHECK (approval_status IN (
                'draft','submitted','under_review','pending','approved','rejected',
                'flagged','changes_requested','live','hidden','archived'
            ));
    ELSE
        ALTER TABLE products ADD CONSTRAINT products_approval_status_check -- expand-only: superset of a constraint 'live' was already removed from; gated 1001's list
            CHECK (approval_status IN (
                'draft','submitted','under_review','pending','approved','rejected',
                'flagged','changes_requested','hidden','archived'
            ));
    END IF;
END $$;
