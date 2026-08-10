-- Module 1 fixes-v4 / LB-1.1 — database-enforced media reference integrity.
--
-- WHY THE FK IS THE ONLY CORRECT MECHANISM.
-- Orphan-media reclamation must not race a concurrent attach. Collapsing
-- the check and the delete into one `DELETE … WHERE NOT EXISTS (…)` does
-- NOT fix that: under READ COMMITTED the subquery is evaluated against
-- the statement snapshot, so an INSERT that commits between snapshot and
-- delete is invisible and the reference dangles. Only a foreign key makes
-- the two transactions interact — the child insert takes FOR KEY SHARE on
-- the parent row, which conflicts with the deleter's FOR UPDATE and with
-- the parent DELETE. Exactly one of them wins; the loser gets SQLSTATE
-- 23503. ON DELETE RESTRICT states the intent explicitly.
--
-- WHAT CHANGED IN v4 (Codex re-review v3, LB-1.1).
-- The previous version wrapped everything in
--   IF EXISTS (SELECT 1 FROM information_schema.tables
--              WHERE table_name = 'media_assets') THEN … END IF;
-- If post-service booted before media-service had created media_assets,
-- the DO block succeeded as a NO-OP — and `migrationrunner` records the
-- migration in `schema_migrations` inside the SAME transaction as the
-- migration body (shared/store/migrationrunner/runner.go). The migration
-- was therefore permanently marked applied while installing no
-- constraint at all, leaving precisely the data-loss race LB-1 exists to
-- close.
--
-- This version FAILS LOUDLY instead. A failed migration aborts boot
-- (runner.go returns an error, BootstrapSchema propagates it, main exits),
-- the row is never inserted, and the orchestrator restarts post-service
-- until media-service has bootstrapped its schema. That is the intended
-- hard dependency: post-service must not run without these constraints.
--
-- Every table reference below is SCHEMA-QUALIFIED so the check cannot be
-- satisfied by a same-named table in another schema on the search_path.

-- ── 1. Hard dependency: the parent table must exist. ────────────────────
DO $$
DECLARE
    parent_oid oid := to_regclass('public.media_assets');
    child_oid  oid := to_regclass('public.post_media');
    draft_oid  oid := to_regclass('public.post_draft_media');
BEGIN
    IF parent_oid IS NULL THEN
        RAISE EXCEPTION
            'migration 030 requires public.media_assets, which does not exist yet. '
            'post-service depends on media-service schema bootstrap. '
            'This migration intentionally fails so it is NOT recorded as applied; '
            'restart post-service after media-service has bootstrapped.'
            USING ERRCODE = 'undefined_table';
    END IF;
    IF child_oid IS NULL THEN
        RAISE EXCEPTION 'migration 030 requires public.post_media'
            USING ERRCODE = 'undefined_table';
    END IF;
    IF draft_oid IS NULL THEN
        RAISE EXCEPTION
            'migration 030 requires public.post_draft_media (created by migration 029)'
            USING ERRCODE = 'undefined_table';
    END IF;
END $$;

-- ── 2. Install both constraints. ───────────────────────────────────────
-- Created NOT VALID so PostgreSQL skips the scan of historical child rows
-- (which may already dangle) and neither fails nor blocks behind it. This
-- does NOT make the FK advisory: it is fully enforced for every subsequent
-- child INSERT/UPDATE and for parent DELETE — which is the race we need
-- closed. Validate later, off the critical path, after reconciling any
-- historical dangling rows:
--     ALTER TABLE public.post_media VALIDATE CONSTRAINT fk_post_media_media_asset;
DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint
        WHERE conname = 'fk_post_media_media_asset'
          AND conrelid = 'public.post_media'::regclass
    ) THEN
        ALTER TABLE public.post_media
            ADD CONSTRAINT fk_post_media_media_asset
            FOREIGN KEY (media_id) REFERENCES public.media_assets(id)
            ON DELETE RESTRICT
            NOT VALID;
    END IF;

    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint
        WHERE conname = 'fk_post_draft_media_media_asset'
          AND conrelid = 'public.post_draft_media'::regclass
    ) THEN
        ALTER TABLE public.post_draft_media
            ADD CONSTRAINT fk_post_draft_media_media_asset
            FOREIGN KEY (media_id) REFERENCES public.media_assets(id)
            ON DELETE RESTRICT
            NOT VALID;
    END IF;
END $$;

-- ── 3. Post-condition: prove both constraints exist WITH THE RIGHT SHAPE.
-- Codex LB-1.1: "checking conname alone is insufficient." This verifies
-- child table, parent table, the exact column pair, contype='f', and
-- ON DELETE RESTRICT (confdeltype='r'). If any assertion fails the
-- transaction aborts, so the migration can never be recorded as applied
-- while the integrity guarantee is absent.
DO $$
DECLARE
    r          record;
    child      text;
    parent     text;
    child_col  text;
    parent_col text;
BEGIN
    FOR r IN
        SELECT * FROM (VALUES
            ('fk_post_media_media_asset',       'public.post_media'),
            ('fk_post_draft_media_media_asset', 'public.post_draft_media')
        ) AS t(conname, child_table)
    LOOP
        SELECT c.conrelid::regclass::text,
               c.confrelid::regclass::text,
               a.attname,
               af.attname
          INTO child, parent, child_col, parent_col
          FROM pg_constraint c
          JOIN unnest(c.conkey)  WITH ORDINALITY AS ck(attnum, ord) ON TRUE
          JOIN unnest(c.confkey) WITH ORDINALITY AS fk(attnum, ord) ON fk.ord = ck.ord
          JOIN pg_attribute a  ON a.attrelid  = c.conrelid  AND a.attnum  = ck.attnum
          JOIN pg_attribute af ON af.attrelid = c.confrelid AND af.attnum = fk.attnum
         WHERE c.conname     = r.conname
           AND c.conrelid    = r.child_table::regclass
           AND c.contype     = 'f'
           AND c.confrelid   = 'public.media_assets'::regclass
           AND c.confdeltype = 'r';   -- ON DELETE RESTRICT

        IF child IS NULL THEN
            RAISE EXCEPTION
                'migration 030 post-condition FAILED: constraint % on % is missing, '
                'or is not a FOREIGN KEY to public.media_assets with ON DELETE RESTRICT',
                r.conname, r.child_table
                USING ERRCODE = 'integrity_constraint_violation';
        END IF;

        IF child_col <> 'media_id' OR parent_col <> 'id' THEN
            RAISE EXCEPTION
                'migration 030 post-condition FAILED: % references %(%) via %, expected media_id -> id',
                r.conname, parent, parent_col, child_col
                USING ERRCODE = 'integrity_constraint_violation';
        END IF;
    END LOOP;
END $$;

-- ── 4. Enforcement state. ──────────────────────────────────────────────
-- PostgreSQL 18 introduced NOT ENFORCED constraints (pg_constraint.conenforced).
-- On 18+ assert the constraints are enforced; on older versions every
-- constraint is enforced by definition, so the check is skipped. Note this
-- is deliberately NOT a `convalidated` check — NOT VALID is expected and
-- acceptable here (it only means historical rows were not rescanned).
DO $$
DECLARE
    unenforced int;
BEGIN
    IF EXISTS (
        SELECT 1 FROM pg_attribute
        WHERE attrelid = 'pg_constraint'::regclass
          AND attname  = 'conenforced'
          AND NOT attisdropped
    ) THEN
        EXECUTE $q$
            SELECT count(*) FROM pg_constraint
            WHERE conname IN ('fk_post_media_media_asset',
                              'fk_post_draft_media_media_asset')
              AND conenforced IS FALSE
        $q$ INTO unenforced;

        IF unenforced > 0 THEN
            RAISE EXCEPTION
                'migration 030 post-condition FAILED: % media FK(s) exist but are NOT ENFORCED',
                unenforced
                USING ERRCODE = 'integrity_constraint_violation';
        END IF;
    END IF;
END $$;
