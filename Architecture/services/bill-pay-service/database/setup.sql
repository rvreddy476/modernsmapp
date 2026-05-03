-- bill-pay-service schema. Idempotent — every CREATE uses IF NOT EXISTS so the
-- bootstrap can be re-run on every cold start without dropping data.
--
-- BBPS MODEL (Phase 2 D2): Setu is the BBPS aggregator (bill network rail);
-- AtPost is the consumer-facing biller (UX, customer accounts, reminders,
-- scheduled payments). All actual bill fetches and payment submissions go
-- through Setu. Settlement / reconciliation is via Setu's daily files.
--
-- DPDP ACT: Customer account identifiers (electricity consumer numbers,
-- mobile numbers for recharge, vehicle numbers for FASTag) ARE stored —
-- they are required for bill-pay. They are NEVER logged in plain text.

CREATE SCHEMA IF NOT EXISTS billpay;

-- Static catalog: bill categories. Seeded at bootstrap.
CREATE TABLE IF NOT EXISTS billpay.categories (
    id              TEXT PRIMARY KEY,
    name            TEXT NOT NULL,
    icon            TEXT NOT NULL,
    sort_order      INT NOT NULL DEFAULT 0,
    is_active       BOOLEAN NOT NULL DEFAULT true
);

-- Providers per category. Seeded from Setu's biller list at startup; refreshed nightly.
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

CREATE INDEX IF NOT EXISTS idx_billpay_providers_category ON billpay.providers(category_id) WHERE is_active = true;

-- User's saved bill accounts.
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

CREATE INDEX IF NOT EXISTS idx_billpay_accounts_user ON billpay.accounts(user_id) WHERE deleted_at IS NULL;

-- Bill fetches (cached snapshot of latest bill from Setu)
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

CREATE INDEX IF NOT EXISTS idx_billpay_bills_account ON billpay.bills(account_id, fetched_at DESC);
CREATE INDEX IF NOT EXISTS idx_billpay_bills_due ON billpay.bills(bill_due_date) WHERE status = 'fetched';

-- Payments (the actual transactions)
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

CREATE INDEX IF NOT EXISTS idx_billpay_payments_user ON billpay.payments(user_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_billpay_payments_status ON billpay.payments(status) WHERE status IN ('initiated','submitted');
CREATE INDEX IF NOT EXISTS idx_billpay_payments_setu_ref ON billpay.payments(setu_payment_ref) WHERE setu_payment_ref IS NOT NULL;

-- Idempotency (24h replay window)
CREATE TABLE IF NOT EXISTS billpay.idempotency (
    key                TEXT PRIMARY KEY,
    user_id            UUID NOT NULL,
    payment_id         UUID,
    response_body      JSONB,
    created_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
    expires_at         TIMESTAMPTZ NOT NULL DEFAULT (now() + INTERVAL '24 hours')
);

CREATE INDEX IF NOT EXISTS idx_billpay_idemp_expires ON billpay.idempotency(expires_at);

-- Reminders (configurable per account)
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

CREATE INDEX IF NOT EXISTS idx_billpay_reminders_user ON billpay.reminders(user_id) WHERE is_active = true;

-- Scheduled payments (recurring or one-off future)
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

CREATE INDEX IF NOT EXISTS idx_billpay_scheduled_due ON billpay.scheduled_payments(next_run_date) WHERE is_active = true;

-- Mobile recharge plans (cached from Setu)
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

CREATE INDEX IF NOT EXISTS idx_mobile_plans_op_circle ON billpay.mobile_plans(operator, circle, plan_amount_paise);

-- Seed categories. ON CONFLICT keeps this idempotent across restarts.
INSERT INTO billpay.categories (id, name, icon, sort_order) VALUES
    ('mobile_postpaid', 'Mobile Postpaid', 'phone_iphone', 1),
    ('mobile_prepaid', 'Mobile Prepaid', 'phone_android', 2),
    ('dth', 'DTH', 'live_tv', 3),
    ('electricity', 'Electricity', 'bolt', 4),
    ('gas', 'Gas', 'local_fire_department', 5),
    ('water', 'Water', 'water_drop', 6),
    ('broadband', 'Broadband', 'router', 7),
    ('fastag', 'FASTag', 'directions_car', 8),
    ('insurance', 'Insurance', 'shield', 9)
ON CONFLICT (id) DO UPDATE SET
    name = EXCLUDED.name,
    icon = EXCLUDED.icon,
    sort_order = EXCLUDED.sort_order;
