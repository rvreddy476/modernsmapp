-- wallet-service schema. Idempotent — every CREATE uses IF NOT EXISTS so the
-- bootstrap can be re-run on every cold start without dropping data.
--
-- BC-of-PPI MODEL: AtPost is a Business Correspondent of a partner bank that
-- holds the PPI license. The partner bank is the source of truth for funds.
-- Tables in this schema are a *mirror* + *audit log* used for UX speed and
-- analytics; nightly reconciliation against the partner bank's settlement
-- file is authoritative — see cmd/reconciler.
--
-- DPDP ACT: No raw Aadhaar number is stored. KYC documents are referenced via
-- partner-supplied opaque assertion ids (digilocker_ref). PAN is masked to
-- last 4 digits before persistence.

CREATE SCHEMA IF NOT EXISTS wallet;

-- Consumer balance mirror (truth = partner bank PPI)
CREATE TABLE IF NOT EXISTS wallet.balances (
    user_id             UUID PRIMARY KEY,
    bank_account_ref    TEXT NOT NULL,
    available_paise     BIGINT NOT NULL DEFAULT 0,
    pending_in_paise    BIGINT NOT NULL DEFAULT 0,
    pending_out_paise   BIGINT NOT NULL DEFAULT 0,
    kyc_tier            TEXT NOT NULL DEFAULT 'minimal' CHECK (kyc_tier IN ('minimal','full','enhanced')),
    monthly_limit_paise BIGINT NOT NULL DEFAULT 1000000,
    is_frozen           BOOLEAN NOT NULL DEFAULT false,
    frozen_reason       TEXT,
    last_synced_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS wallet.transactions (
    id                   UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id              UUID NOT NULL,
    type                 TEXT NOT NULL CHECK (type IN ('top_up','send','receive','merchant_pay','refund','adjustment','reversal')),
    direction            TEXT NOT NULL CHECK (direction IN ('credit','debit')),
    amount_paise         BIGINT NOT NULL CHECK (amount_paise > 0),
    counterparty_user_id UUID,
    counterparty_phone   TEXT,
    counterparty_label   TEXT,
    merchant_service     TEXT,
    merchant_ref         TEXT,
    status               TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending','succeeded','failed','reversed','pending_invite')),
    bank_txn_ref         TEXT,
    upi_txn_ref          TEXT,
    failure_reason       TEXT,
    metadata             JSONB NOT NULL DEFAULT '{}',
    idempotency_key      TEXT,
    created_at           TIMESTAMPTZ NOT NULL DEFAULT now(),
    settled_at           TIMESTAMPTZ
);

CREATE INDEX IF NOT EXISTS idx_wallet_txn_user_created ON wallet.transactions(user_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_wallet_txn_idempotency ON wallet.transactions(idempotency_key) WHERE idempotency_key IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_wallet_txn_bank_ref ON wallet.transactions(bank_txn_ref) WHERE bank_txn_ref IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_wallet_txn_status ON wallet.transactions(status);
CREATE INDEX IF NOT EXISTS idx_wallet_txn_merchant ON wallet.transactions(merchant_service, merchant_ref) WHERE merchant_service IS NOT NULL;

CREATE TABLE IF NOT EXISTS wallet.idempotency (
    key            TEXT PRIMARY KEY,
    user_id        UUID NOT NULL,
    operation      TEXT NOT NULL,
    transaction_id UUID,
    response_body  JSONB,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    expires_at     TIMESTAMPTZ NOT NULL DEFAULT (now() + INTERVAL '24 hours')
);

CREATE INDEX IF NOT EXISTS idx_wallet_idemp_expires ON wallet.idempotency(expires_at);
CREATE INDEX IF NOT EXISTS idx_wallet_idemp_user ON wallet.idempotency(user_id);

CREATE TABLE IF NOT EXISTS wallet.kyc_records (
    user_id           UUID PRIMARY KEY,
    tier              TEXT NOT NULL DEFAULT 'minimal',
    aadhaar_status    TEXT,
    digilocker_ref    TEXT,
    pan_status        TEXT,
    pan_masked        TEXT,
    address_proof_ref TEXT,
    submitted_at      TIMESTAMPTZ,
    verified_at       TIMESTAMPTZ,
    rejection_reason  TEXT
);

CREATE TABLE IF NOT EXISTS wallet.recipients (
    user_id           UUID NOT NULL,
    recipient_user_id UUID,
    recipient_phone   TEXT,
    label             TEXT,
    last_sent_at      TIMESTAMPTZ,
    send_count        INT NOT NULL DEFAULT 0
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_wallet_recipients_identity
    ON wallet.recipients (user_id, (COALESCE(recipient_user_id::text, recipient_phone)));
CREATE INDEX IF NOT EXISTS idx_wallet_recipients_user ON wallet.recipients(user_id, send_count DESC);

CREATE TABLE IF NOT EXISTS wallet.partner_bank_settlements (
    id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    settlement_date     DATE NOT NULL,
    settlement_file_ref TEXT,
    total_paise         BIGINT NOT NULL,
    transaction_count   INT NOT NULL,
    status              TEXT NOT NULL DEFAULT 'pending',
    discrepancies       JSONB,
    reconciled_at       TIMESTAMPTZ,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_wallet_settlement_date ON wallet.partner_bank_settlements(settlement_date DESC);
