-- 018: a correctable ledger, part one — the types are right and a settled
-- amount can be adjusted or reversed without ever being rewritten.
--
-- Part A: creator_ledger money columns are BIGINT paise, on every install.
-- -----------------------------------------------------------------------
-- setup.sql declared balance / lifetime_earnings / pending_payout as
-- DECIMAL(12,2). Migration 005 converts them to BIGINT only when `wallets`
-- is still a base table, and since 012 setup.sql creates `creator_ledger`
-- directly with `wallets` as a VIEW — so on every install made since then
-- (the dev stack's live `app` database included, checked 2026-09-11) the
-- columns are still NUMERIC, and the Go code has been writing int64 PAISE
-- into them. The values are paise already: `282900.00` is Rs 2,829.00.
--
-- Therefore the conversion here is a plain cast, NOT `* 100`. The plan's
-- draft said `USING (col*100)::BIGINT` on the assumption that a numeric
-- column held rupees; on these installs that would inflate every balance
-- a hundredfold. The guard below refuses to run if any value carries a
-- fractional part, because that would mean a writer this migration does
-- not know about, and a human has to look before anything is cast.
--
-- The `wallets` compatibility view selects * from creator_ledger, and
-- Postgres will not change the type of a column a view depends on, so the
-- view is dropped for the duration of the ALTER and recreated as setup.sql
-- and 012 define it. Both are idempotent, so a re-run is a no-op.
DO $$
DECLARE
    col  TEXT;
    bad  BIGINT;
    todo TEXT[] := ARRAY[]::TEXT[];
BEGIN
    SELECT array_agg(column_name::TEXT) INTO todo
    FROM information_schema.columns
    WHERE table_schema = current_schema()
      AND table_name   = 'creator_ledger'
      AND column_name  IN ('balance', 'lifetime_earnings', 'pending_payout')
      AND data_type    = 'numeric';

    IF todo IS NULL OR cardinality(todo) = 0 THEN
        RAISE NOTICE '018: creator_ledger money columns are already BIGINT; nothing to convert';
        RETURN;
    END IF;

    -- Refuse to guess. Every value the service ever wrote is an integer
    -- number of paise; a fractional value means something else wrote here.
    EXECUTE format(
        'SELECT count(*) FROM creator_ledger WHERE %s',
        (SELECT string_agg(format('%I <> trunc(%I)', c, c), ' OR ') FROM unnest(todo) AS c)
    ) INTO bad;
    IF bad > 0 THEN
        RAISE EXCEPTION '018: % creator_ledger row(s) carry fractional money values; refusing to cast NUMERIC to BIGINT paise until a human has looked', bad;
    END IF;

    DROP VIEW IF EXISTS wallets;
    IF EXISTS (SELECT 1 FROM information_schema.schemata WHERE schema_name = 'monetization') THEN
        EXECUTE 'DROP VIEW IF EXISTS monetization.wallets';
    END IF;

    FOREACH col IN ARRAY todo LOOP
        EXECUTE format('ALTER TABLE creator_ledger ALTER COLUMN %I DROP DEFAULT', col);
        EXECUTE format('ALTER TABLE creator_ledger ALTER COLUMN %I TYPE BIGINT USING (%I)::BIGINT', col, col);
        EXECUTE format('ALTER TABLE creator_ledger ALTER COLUMN %I SET DEFAULT 0', col);
        RAISE NOTICE '018: creator_ledger.% converted NUMERIC -> BIGINT (values were already paise; no scaling applied)', col;
    END LOOP;

    EXECUTE 'CREATE OR REPLACE VIEW wallets AS SELECT * FROM creator_ledger';
    IF EXISTS (SELECT 1 FROM information_schema.schemata WHERE schema_name = 'monetization') THEN
        EXECUTE 'CREATE OR REPLACE VIEW monetization.wallets AS SELECT * FROM creator_ledger';
    END IF;
    COMMENT ON VIEW wallets IS
        'DEPRECATED 2026-04-30. Read-only alias for creator_ledger. '
        'Will be dropped after 2026-10-30. Update callers to use creator_ledger.';
END $$;

-- Part B: adjustments and reversals.
-- ----------------------------------
-- An adjustment is a signed wallet movement identified by its CAUSE, so a
-- retried correction cannot land twice. The key is 'adj:<kind>:<ref>'
-- (service.AdjustmentCause). Partial unique index: every other transaction
-- row keeps a NULL key and is unaffected.
ALTER TABLE transactions
    ADD COLUMN IF NOT EXISTS idempotency_key TEXT;
CREATE UNIQUE INDEX IF NOT EXISTS uq_transactions_idempotency_key
    ON transactions (idempotency_key)
    WHERE idempotency_key IS NOT NULL;

-- A reversed earning keeps its row, its UNIQUE (creator, day, type, region)
-- slot and its money columns exactly as they were settled; only the status
-- flips and the three columns below say when, why, and which adjustment
-- gave the money back (NULL when the row had never been credited, in which
-- case no money moved because the period claim filters on status =
-- 'settled').
ALTER TABLE creator_fund_earnings
    ADD COLUMN IF NOT EXISTS reversed_at TIMESTAMPTZ;
ALTER TABLE creator_fund_earnings
    ADD COLUMN IF NOT EXISTS reversal_reason TEXT;
ALTER TABLE creator_fund_earnings
    ADD COLUMN IF NOT EXISTS reversal_transaction_id UUID;

-- On the statement: reversed_paise is a MEMO of the net of reversed rows
-- whose day falls in the period (they are no longer on the fund line, so
-- the arithmetic identities still hold without them); adjustments_paise is
-- the signed wallet movement from adjustments that LANDED in the period,
-- whichever period the corrected day belonged to.
ALTER TABLE creator_fund_period_settlements
    ADD COLUMN IF NOT EXISTS reversed_paise BIGINT NOT NULL DEFAULT 0;
ALTER TABLE creator_fund_period_settlements
    ADD COLUMN IF NOT EXISTS fund_reversed_rows BIGINT NOT NULL DEFAULT 0;
ALTER TABLE creator_fund_period_settlements
    ADD COLUMN IF NOT EXISTS adjustments_paise BIGINT NOT NULL DEFAULT 0;
ALTER TABLE creator_fund_period_settlements
    ADD COLUMN IF NOT EXISTS adjustments_count BIGINT NOT NULL DEFAULT 0;
