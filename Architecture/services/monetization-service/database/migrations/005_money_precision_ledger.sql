-- Convert all DECIMAL money columns to BIGINT (paise/cents minor units)
ALTER TABLE wallets ALTER COLUMN balance TYPE BIGINT USING (balance * 100)::BIGINT;
ALTER TABLE wallets ALTER COLUMN lifetime_earnings TYPE BIGINT USING (lifetime_earnings * 100)::BIGINT;
ALTER TABLE wallets ALTER COLUMN pending_payout TYPE BIGINT USING (pending_payout * 100)::BIGINT;

ALTER TABLE transactions ALTER COLUMN amount TYPE BIGINT USING (amount * 100)::BIGINT;

ALTER TABLE creator_tiers ALTER COLUMN price TYPE BIGINT USING (price * 100)::BIGINT;

ALTER TABLE subscriptions ALTER COLUMN price TYPE BIGINT USING (price * 100)::BIGINT;

ALTER TABLE payout_requests ALTER COLUMN amount TYPE BIGINT USING (amount * 100)::BIGINT;

-- Accounts for double-entry ledger
CREATE TABLE IF NOT EXISTS accounts (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    owner_id UUID NOT NULL,
    account_type TEXT NOT NULL CHECK (account_type IN ('user_wallet','platform_revenue','platform_gst','platform_tds','escrow','payout_hold')),
    balance_paise BIGINT NOT NULL DEFAULT 0,
    currency TEXT NOT NULL DEFAULT 'INR',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE(owner_id, account_type)
);

-- Immutable ledger entries (append-only)
CREATE TABLE IF NOT EXISTS ledger_entries (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    debit_account_id UUID NOT NULL REFERENCES accounts(id),
    credit_account_id UUID NOT NULL REFERENCES accounts(id),
    amount_paise BIGINT NOT NULL CHECK (amount_paise > 0),
    currency TEXT NOT NULL DEFAULT 'INR',
    reference_type TEXT NOT NULL,
    reference_id UUID,
    idempotency_key TEXT UNIQUE,
    description TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_le_debit ON ledger_entries(debit_account_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_le_credit ON ledger_entries(credit_account_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_le_ref ON ledger_entries(reference_type, reference_id);

-- Balance snapshots for periodic rebuild verification
CREATE TABLE IF NOT EXISTS balance_snapshots (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    account_id UUID NOT NULL REFERENCES accounts(id),
    balance_paise BIGINT NOT NULL,
    snapshot_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
