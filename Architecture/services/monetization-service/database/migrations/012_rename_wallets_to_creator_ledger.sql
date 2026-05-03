-- 012_rename_wallets_to_creator_ledger.sql
--
-- Rename the existing `wallets` table (creator-earnings ledger) to
-- `creator_ledger` so it is no longer confused with the upcoming
-- consumer wallet (which lives in a separate wallet-service).
--
-- Per Phase 2 §D4 (PHASE_2_DECISIONS.md), today's `wallets` table holds
-- creator earnings (lifetime_earnings, pending_payout, transaction
-- types earning|payout|refund|adjustment|subscription_payment) and is
-- NOT a consumer spendable balance. Renaming it removes a real
-- product/accounting bug where the food UI was reading this table and
-- displaying creator-earnings as a consumer wallet balance.
--
-- This migration is idempotent: running it twice is a no-op.
--
-- Backwards-compat: a read-only view named `wallets` is created over
-- `creator_ledger` so any forgotten caller (or already-deployed older
-- service binary) keeps working until 2026-10-30. After that the view
-- can be dropped in a follow-up migration.

-- 1. Idempotent rename of the bare-schema table.
DO $$
BEGIN
    IF EXISTS (
        SELECT 1
        FROM   information_schema.tables
        WHERE  table_schema = current_schema()
        AND    table_name   = 'wallets'
        AND    table_type   = 'BASE TABLE'
    ) THEN
        ALTER TABLE wallets RENAME TO creator_ledger;
    END IF;
END $$;

-- 2. Idempotent rename if the table happened to live under an explicit
--    `monetization` schema (some environments may have created it there).
DO $$
BEGIN
    IF EXISTS (
        SELECT 1
        FROM   information_schema.tables
        WHERE  table_schema = 'monetization'
        AND    table_name   = 'wallets'
        AND    table_type   = 'BASE TABLE'
    ) THEN
        ALTER TABLE monetization.wallets RENAME TO creator_ledger;
    END IF;
END $$;

-- 3. Backwards-compat view. CREATE OR REPLACE makes this idempotent.
--    The view is read-only by default (PostgreSQL does not auto-create
--    INSTEAD OF triggers) which is exactly what we want — old readers
--    keep working, but writes must go through the new table name and
--    the Go service code (which has been updated).
CREATE OR REPLACE VIEW wallets AS
    SELECT * FROM creator_ledger;

-- Mirror the view in the `monetization` schema if that schema exists,
-- so any caller that did qualify keeps working too.
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM information_schema.schemata WHERE schema_name = 'monetization') THEN
        EXECUTE 'CREATE OR REPLACE VIEW monetization.wallets AS SELECT * FROM creator_ledger';
    END IF;
END $$;

-- 4. Document the intent on both objects.
COMMENT ON TABLE creator_ledger IS
    'Creator earnings ledger. Renamed from wallets on 2026-04-30 (Phase 2 §D4). '
    'NOT a consumer wallet — see wallet-service for that.';

COMMENT ON VIEW wallets IS
    'DEPRECATED 2026-04-30. Read-only alias for creator_ledger. '
    'Will be dropped after 2026-10-30. Update callers to use creator_ledger.';
