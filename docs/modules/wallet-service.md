# Module: wallet-service

_Auto-extracted from source (DDL + route registrations). Verbatim — the source of truth is the code._

## HTTP routes
```
GET /balance
GET /balance/:user_id
GET /kyc
GET /recipients
GET /top-up/:id
GET /transactions
GET /transactions/:id
POST /debit
POST /kyc/aadhaar/callback
POST /kyc/aadhaar/start
POST /kyc/pan
POST /refund
POST /send
POST /top-up
POST /top-up/:id/confirm
GROUP /v1/wallet
GROUP /v1/wallet/internal
```

## Database schema (CREATE TABLE — full column DDL)
```sql
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

CREATE TABLE IF NOT EXISTS wallet.idempotency (
    key            TEXT PRIMARY KEY,
    user_id        UUID NOT NULL,
    operation      TEXT NOT NULL,
    transaction_id UUID,
    response_body  JSONB,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    expires_at     TIMESTAMPTZ NOT NULL DEFAULT (now() + INTERVAL '24 hours')
);

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

```

## API types (request/response Go structs with JSON tags)
```go
type balanceResponse struct {
	AvailablePaise    int64  `json:"available_paise"`
	PendingInPaise    int64  `json:"pending_in_paise"`
	PendingOutPaise   int64  `json:"pending_out_paise"`
	KYCTier           string `json:"kyc_tier"`
	MonthlyLimitPaise int64  `json:"monthly_limit_paise"`
	IsFrozen          bool   `json:"is_frozen"`
}

type internalDebitRequest struct {
	UserID          string `json:"user_id"`
	AmountPaise     int64  `json:"amount_paise"`
	MerchantService string `json:"merchant_service"`
	MerchantRef     string `json:"merchant_ref"`
	IdempotencyKey  string `json:"idempotency_key"`
}

type internalRefundRequest struct {
	OriginalTransactionID string `json:"original_transaction_id"`
	AmountPaise           int64  `json:"amount_paise"`
	Reason                string `json:"reason"`
}

type aadhaarCallbackRequest struct {
	Code  string `json:"code"`
	State string `json:"state"`
}

type panRequest struct {
	PANNumber string `json:"pan_number"`
}

type sendRequest struct {
	RecipientUserID *string `json:"recipient_user_id,omitempty"`
	RecipientPhone  string  `json:"recipient_phone,omitempty"`
	AmountPaise     int64   `json:"amount_paise"`
	Label           string  `json:"label,omitempty"`
	IdempotencyKey  string  `json:"idempotency_key"`
}

type topUpRequest struct {
	AmountPaise    int64  `json:"amount_paise"`
	IdempotencyKey string `json:"idempotency_key"`
}

type confirmTopUpRequest struct {
	UPITxnRef string `json:"upi_txn_ref"`
}
```
