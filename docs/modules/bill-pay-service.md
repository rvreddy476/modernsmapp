# Module: bill-pay-service

_Auto-extracted from source (DDL + route registrations). Verbatim — the source of truth is the code._

## HTTP routes
```
DELETE /accounts/:id
DELETE /reminders/:id
DELETE /scheduled/:id
GET /accounts
GET /accounts/:id/bill
GET /categories
GET /payments
GET /payments/:id
GET /providers
GET /providers/:id
GET /recharge/operator-circle
GET /recharge/plans
GET /reminders
GET /scheduled
PATCH /accounts/:id
PATCH /scheduled/:id
POST /accounts
POST /pay
POST /recharge/mobile
POST /reminders
POST /scheduled
POST /setu-webhook
GROUP /v1/billpay
GROUP /v1/billpay/internal
```

## Database schema (CREATE TABLE — full column DDL)
```sql
CREATE TABLE IF NOT EXISTS billpay.categories (
    id              TEXT PRIMARY KEY,
    name            TEXT NOT NULL,
    icon            TEXT NOT NULL,
    sort_order      INT NOT NULL DEFAULT 0,
    is_active       BOOLEAN NOT NULL DEFAULT true
);

CREATE TABLE IF NOT EXISTS billpay.providers (
    id                   UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    setu_biller_id       TEXT NOT NULL UNIQUE,
    category_id          TEXT NOT NULL REFERENCES billpay.categories(id),
    name                 TEXT NOT NULL,
    short_name           TEXT,
    logo_url             TEXT,
    states               TEXT[] NOT NULL DEFAULT '{}',
    customer_params      JSONB NOT NULL DEFAULT '[]',
    bill_fetch_supported BOOLEAN NOT NULL DEFAULT true,
    is_active            BOOLEAN NOT NULL DEFAULT true,
    last_synced_at       TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS billpay.accounts (
    id                 UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id            UUID NOT NULL,
    provider_id        UUID NOT NULL REFERENCES billpay.providers(id),
    identifier         TEXT NOT NULL,
    extra_params       JSONB NOT NULL DEFAULT '{}',
    label              TEXT NOT NULL,
    is_default         BOOLEAN NOT NULL DEFAULT false,
    autopay_enabled    BOOLEAN NOT NULL DEFAULT false,
    created_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
    deleted_at         TIMESTAMPTZ,
    UNIQUE (user_id, provider_id, identifier)
);

CREATE TABLE IF NOT EXISTS billpay.bills (
    id                 UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    account_id         UUID NOT NULL REFERENCES billpay.accounts(id) ON DELETE CASCADE,
    bill_amount_paise  BIGINT NOT NULL,
    bill_period_start  DATE,
    bill_period_end    DATE,
    bill_due_date      DATE,
    bill_number        TEXT,
    customer_name      TEXT,
    setu_bill_ref      TEXT,
    status             TEXT NOT NULL DEFAULT 'fetched' CHECK (status IN ('fetched','paid','expired')),
    fetched_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
    paid_at            TIMESTAMPTZ,
    payment_id         UUID
);

CREATE TABLE IF NOT EXISTS billpay.payments (
    id                 UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id            UUID NOT NULL,
    account_id         UUID REFERENCES billpay.accounts(id),
    provider_id        UUID NOT NULL REFERENCES billpay.providers(id),
    amount_paise       BIGINT NOT NULL CHECK (amount_paise > 0),
    fee_paise          BIGINT NOT NULL DEFAULT 0,
    payment_method     TEXT NOT NULL CHECK (payment_method IN ('wallet','upi','card')),
    wallet_txn_id      UUID,
    upi_txn_ref        TEXT,
    setu_payment_ref   TEXT,
    status             TEXT NOT NULL DEFAULT 'initiated' CHECK (status IN ('initiated','submitted','succeeded','failed','refunded')),
    failure_reason     TEXT,
    receipt_number     TEXT,
    bill_id            UUID REFERENCES billpay.bills(id),
    idempotency_key    TEXT UNIQUE,
    created_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
    settled_at         TIMESTAMPTZ
);

CREATE TABLE IF NOT EXISTS billpay.idempotency (
    key                TEXT PRIMARY KEY,
    user_id            UUID NOT NULL,
    payment_id         UUID,
    response_body      JSONB,
    created_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
    expires_at         TIMESTAMPTZ NOT NULL DEFAULT (now() + INTERVAL '24 hours')
);

CREATE TABLE IF NOT EXISTS billpay.reminders (
    id                 UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    account_id         UUID NOT NULL REFERENCES billpay.accounts(id) ON DELETE CASCADE,
    user_id            UUID NOT NULL,
    days_before_due    INT NOT NULL DEFAULT 3,
    channels           TEXT[] NOT NULL DEFAULT '{push}',
    is_active          BOOLEAN NOT NULL DEFAULT true,
    last_sent_at       TIMESTAMPTZ,
    created_at         TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS billpay.scheduled_payments (
    id                 UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id            UUID NOT NULL,
    account_id         UUID NOT NULL REFERENCES billpay.accounts(id) ON DELETE CASCADE,
    amount_paise       BIGINT,
    payment_method     TEXT NOT NULL,
    schedule_kind      TEXT NOT NULL CHECK (schedule_kind IN ('one_off','monthly')),
    next_run_date      DATE NOT NULL,
    last_run_at        TIMESTAMPTZ,
    is_active          BOOLEAN NOT NULL DEFAULT true,
    created_at         TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS billpay.mobile_plans (
    id                 UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    operator           TEXT NOT NULL,
    circle             TEXT NOT NULL,
    plan_amount_paise  BIGINT NOT NULL,
    validity_days      INT,
    data_gb_per_day    NUMERIC(5,2),
    talktime_paise     BIGINT,
    sms_count_per_day  INT,
    description        TEXT,
    category           TEXT,
    is_active          BOOLEAN NOT NULL DEFAULT true,
    last_synced_at     TIMESTAMPTZ NOT NULL DEFAULT now()
);

```

## API types (request/response Go structs with JSON tags)
```go
type payRequestWire struct {
	AccountID      string            `json:"account_id,omitempty"`
	ProviderID     string            `json:"provider_id"`
	Identifier     string            `json:"identifier"`
	AmountPaise    int64             `json:"amount_paise"`
	PaymentMethod  string            `json:"payment_method"`
	IdempotencyKey string            `json:"idempotency_key"`
	BillID         string            `json:"bill_id,omitempty"`
	ExtraParams    map[string]string `json:"extra_params,omitempty"`
}

type rechargeMobileWire struct {
	Phone          string `json:"phone"`
	Operator       string `json:"operator"`
	Circle         string `json:"circle"`
	AmountPaise    int64  `json:"amount_paise"`
	PlanID         string `json:"plan_id,omitempty"`
	PaymentMethod  string `json:"payment_method"`
	IdempotencyKey string `json:"idempotency_key"`
}
```
