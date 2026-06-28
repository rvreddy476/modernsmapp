# Module: payments-service

_Auto-extracted from source (DDL + route registrations). Verbatim — the source of truth is the code._

## HTTP routes
```
GET /intents
GET /intents/:id
PATCH /intents/:id/status
POST /holds/:intentId/release
POST /intents
POST /intents/:id/refund
POST /intents/:id/verify
POST /v1/payments/webhook
GROUP /v1/payments
```

## Database schema (CREATE TABLE — full column DDL)
```sql
CREATE TABLE IF NOT EXISTS payments.payment_intents (
    id               UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    payer_id         UUID NOT NULL,
    payee_id         UUID NOT NULL,
    reference_type   TEXT NOT NULL,
    reference_id     UUID NOT NULL,
    amount           NUMERIC(12,2) NOT NULL,
    currency         TEXT NOT NULL DEFAULT 'INR',
    method           TEXT NOT NULL CHECK (method IN ('upi','card','wallet','cod','escrow')),
    status           TEXT NOT NULL DEFAULT 'pending'
                       CHECK (status IN ('pending','processing','succeeded','failed','refunded','partially_refunded','disputed')),
    provider_ref     TEXT,
    upi_intent_url   TEXT,
    metadata         JSONB DEFAULT '{}',
    idempotency_key  TEXT NOT NULL UNIQUE,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS payments.payment_audit_log (
    id          BIGSERIAL PRIMARY KEY,
    intent_id   UUID NOT NULL REFERENCES payments.payment_intents(id),
    event       TEXT NOT NULL,
    old_status  TEXT,
    new_status  TEXT,
    actor_id    UUID,
    metadata    JSONB,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS payments.outbox_events (
    id            BIGSERIAL PRIMARY KEY,
    event_type    TEXT NOT NULL,
    partition_key TEXT NOT NULL DEFAULT '',
    payload       JSONB NOT NULL,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    published_at  TIMESTAMPTZ
);

CREATE TABLE IF NOT EXISTS payments.webhook_events (
    event_id     TEXT PRIMARY KEY,
    event_type   TEXT NOT NULL,
    provider_ref TEXT,
    received_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS payments.refunds_applied (
    refund_provider_ref TEXT PRIMARY KEY,
    intent_id           UUID NOT NULL,
    amount_minor        BIGINT NOT NULL CHECK (amount_minor > 0),
    applied_at          TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS payments.payment_intents (
    id               UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    payer_id         UUID NOT NULL,
    payee_id         UUID NOT NULL,
    reference_type   TEXT NOT NULL,
    reference_id     UUID NOT NULL,
    amount           NUMERIC(12,2) NOT NULL,
    currency         TEXT NOT NULL DEFAULT 'INR',
    method           TEXT NOT NULL CHECK (method IN ('upi','card','wallet','cod','escrow')),
    status           TEXT NOT NULL DEFAULT 'pending'
                       CHECK (status IN ('pending','processing','succeeded','failed','refunded','disputed')),
    provider_ref     TEXT,
    upi_intent_url   TEXT,
    metadata         JSONB DEFAULT '{}',
    idempotency_key  TEXT NOT NULL UNIQUE,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS payments.payment_audit_log (
    id          BIGSERIAL PRIMARY KEY,
    intent_id   UUID NOT NULL REFERENCES payments.payment_intents(id),
    event       TEXT NOT NULL,
    old_status  TEXT,
    new_status  TEXT,
    actor_id    UUID,
    metadata    JSONB,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS payments.outbox_events (
    id            BIGSERIAL PRIMARY KEY,
    event_type    TEXT NOT NULL,
    partition_key TEXT NOT NULL DEFAULT '',
    payload       JSONB NOT NULL,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    published_at  TIMESTAMPTZ
);

CREATE TABLE IF NOT EXISTS payments.payment_holds (
    id                UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    payment_intent_id UUID NOT NULL REFERENCES payments.payment_intents(id) ON DELETE CASCADE,
    hold_amount       BIGINT NOT NULL CHECK (hold_amount > 0),
    currency          TEXT NOT NULL DEFAULT 'INR',
    release_condition TEXT NOT NULL
                      CHECK (release_condition IN ('order_delivered', 'dispute_resolved', 'manual')),
    release_after     TIMESTAMPTZ,
    released_at       TIMESTAMPTZ,
    released_by       TEXT,
    created_at        TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS payments.webhook_events (
    event_id      TEXT PRIMARY KEY,
    event_type    TEXT NOT NULL,
    provider_ref  TEXT,
    received_at   TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS payments.refunds_applied (
    refund_provider_ref TEXT PRIMARY KEY,
    intent_id           UUID NOT NULL,
    amount_minor        BIGINT NOT NULL CHECK (amount_minor > 0),
    applied_at          TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

```
