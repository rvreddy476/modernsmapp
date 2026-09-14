-- payments-service migration 010 — every payment belongs to one APPLICATION
--
-- An application is a PRODUCT: mstore, feast, mopedu, dating, vtube, momentum.
-- Feast Kitchen and Feast Rider payments belong to `feast`. The installed app a
-- payment came through is a separate, optional `channel` (momentum_android,
-- feast_rider_android, web, …), never the application.
--
--   1. payments.applications — the registry. payments-service owns it. A key is
--      permanent (^[a-z][a-z0-9_]{1,31}$); status, merchant name, enabled methods
--      and settings are what an operator changes through
--      PUT /v1/payments/internal/applications/:key, audited in
--      payments.application_audit_log.
--   2. application_id on payment_intents (plus an optional channel) and on every
--      row derived from an intent: refund_commands, refund_required,
--      provider_refunds_applied, the legacy refunds_applied, payment_holds.
--      The code copies it from the intent at insert.
--   3. A backfill from what each intent already says about its owner:
--      owner_domain commerce-service → mstore, food-service → feast, then
--      reference_type order → mstore, food_order → feast. Child rows copy their
--      intent's. Anything that maps to nothing stays NULL and is COUNTED in
--      payments.application_backfill_report.
--
-- EXPAND ONLY. The columns stay NULLABLE here, on purpose. This migration runs
-- when the first new replica boots, while old replicas are still serving; an old
-- replica inserts intents and refund commands without application_id, and a NOT
-- NULL added now would fail those writes (checkout and refunds) for the length of
-- the rollout. New code refuses to write a row without an application instead.
-- The NOT NULL is gated/998_application_id_not_null.sql, run once the old
-- replicas are gone: it re-runs the backfill (catching rows old replicas wrote
-- after this ran) and refuses if anything is still unmapped.
--
-- Re-runnable: every step is guarded, and payments.backfill_application_ids()
-- only touches rows that are still NULL.

-- ─── 1. The registry ─────────────────────────────────────────────────

CREATE TABLE IF NOT EXISTS payments.applications (
    key                   TEXT        PRIMARY KEY,
    display_name          TEXT        NOT NULL,
    status                TEXT        NOT NULL DEFAULT 'active',
    -- The name the provider's checkout sheet shows the customer.
    merchant_display_name TEXT        NOT NULL,
    -- Methods this application accepts. Each must also be in the launch
    -- vocabulary (shared/paymentmethod); the API refuses anything else.
    enabled_methods       TEXT[]      NOT NULL DEFAULT '{}',
    settings              JSONB       NOT NULL DEFAULT '{}'::jsonb,
    created_at            TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at            TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT chk_applications_key CHECK (key ~ '^[a-z][a-z0-9_]{1,31}$'),
    CONSTRAINT chk_applications_status CHECK (status IN ('active','disabled')),
    CONSTRAINT chk_applications_display_name CHECK (length(btrim(display_name)) BETWEEN 1 AND 100),
    CONSTRAINT chk_applications_merchant_name CHECK (length(btrim(merchant_display_name)) BETWEEN 1 AND 100),
    CONSTRAINT chk_applications_methods CHECK (
        cardinality(enabled_methods) = 0
        OR array_to_string(enabled_methods, ',') ~ '^[a-z][a-z_]{1,31}(,[a-z][a-z_]{1,31})*$'),
    CONSTRAINT chk_applications_settings CHECK (jsonb_typeof(settings) = 'object')
);

-- The two products that take money today, as they are configured today: the
-- Razorpay sheet says "Momentum Merchant" (the business name on the account) and
-- both commerce and food accept upi and card (shared/paymentmethod). Nothing else
-- is seeded; a new product is added through the registry route.
INSERT INTO payments.applications (key, display_name, status, merchant_display_name, enabled_methods)
VALUES ('mstore', 'MStore', 'active', 'Momentum Merchant', ARRAY['card','upi']),
       ('feast',  'Feast',  'active', 'Momentum Merchant', ARRAY['card','upi'])
ON CONFLICT (key) DO NOTHING;

-- One row per registry write that changed something.
CREATE TABLE IF NOT EXISTS payments.application_audit_log (
    id              BIGSERIAL   PRIMARY KEY,
    application_key TEXT        NOT NULL REFERENCES payments.applications(key),
    action          TEXT        NOT NULL CHECK (action IN ('created','updated')),
    operator_id     TEXT        NOT NULL,
    credential      TEXT        NOT NULL,
    before          JSONB,
    after           JSONB       NOT NULL,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_application_audit_key
    ON payments.application_audit_log (application_key, created_at DESC);

-- ─── 2. application_id on every payment row ──────────────────────────

ALTER TABLE payments.payment_intents
    ADD COLUMN IF NOT EXISTS application_id TEXT,
    ADD COLUMN IF NOT EXISTS channel        TEXT;
ALTER TABLE payments.refund_commands          ADD COLUMN IF NOT EXISTS application_id TEXT;
ALTER TABLE payments.refund_required          ADD COLUMN IF NOT EXISTS application_id TEXT;
ALTER TABLE payments.provider_refunds_applied ADD COLUMN IF NOT EXISTS application_id TEXT;
ALTER TABLE payments.refunds_applied          ADD COLUMN IF NOT EXISTS application_id TEXT;
ALTER TABLE payments.payment_holds            ADD COLUMN IF NOT EXISTS application_id TEXT;

DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_constraint
                    WHERE conrelid = 'payments.payment_intents'::regclass
                      AND conname  = 'chk_payment_intents_channel') THEN
        -- Every existing row is NULL, so validating this is a quick scan.
        ALTER TABLE payments.payment_intents
            ADD CONSTRAINT chk_payment_intents_channel
            CHECK (channel IS NULL OR channel ~ '^[a-z][a-z0-9_]{1,31}$');
    END IF;
END$$;

-- Foreign keys to the registry, added NOT VALID and validated after the
-- backfill below. VALIDATE takes a lock that does not block writes.
DO $$
DECLARE
    t RECORD;
BEGIN
    FOR t IN
        SELECT * FROM (VALUES
            ('payment_intents',          'fk_payment_intents_application'),
            ('refund_commands',          'fk_refund_commands_application'),
            ('refund_required',          'fk_refund_required_application'),
            ('provider_refunds_applied', 'fk_provider_refunds_applied_application'),
            ('refunds_applied',          'fk_refunds_applied_application'),
            ('payment_holds',            'fk_payment_holds_application')
        ) AS v(tbl, con)
    LOOP
        IF NOT EXISTS (SELECT 1 FROM pg_constraint
                        WHERE conrelid = format('payments.%I', t.tbl)::regclass
                          AND conname  = t.con) THEN
            EXECUTE format(
                'ALTER TABLE payments.%I ADD CONSTRAINT %I FOREIGN KEY (application_id) '
                'REFERENCES payments.applications(key) NOT VALID', t.tbl, t.con);
        END IF;
    END LOOP;
END$$;

-- Per-application reads: the transactions route and the filtered lists.
CREATE INDEX IF NOT EXISTS idx_payment_intents_application_created
    ON payments.payment_intents (application_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_payment_intents_application_reference
    ON payments.payment_intents (application_id, reference_type, reference_id);
CREATE INDEX IF NOT EXISTS idx_refund_commands_application_created
    ON payments.refund_commands (application_id, created_at DESC);
-- Keeps the boot-time "unmapped rows" count cheap however large the table grows.
CREATE INDEX IF NOT EXISTS idx_payment_intents_application_unmapped
    ON payments.payment_intents (created_at) WHERE application_id IS NULL;

-- ─── 3. Backfill ─────────────────────────────────────────────────────

-- The legacy mapping, in one place: the owning service first, the reference
-- type second. NULL means "no application can be established".
CREATE OR REPLACE FUNCTION payments.legacy_application_for(owner_domain TEXT, reference_type TEXT)
RETURNS TEXT
LANGUAGE sql IMMUTABLE AS $$
    SELECT CASE owner_domain
               WHEN 'commerce-service' THEN 'mstore'
               WHEN 'food-service'     THEN 'feast'
               ELSE CASE reference_type
                        WHEN 'order'      THEN 'mstore'
                        WHEN 'food_order' THEN 'feast'
                    END
           END
$$;

-- What each backfill run did, so the unmapped count is on record rather than
-- in a NOTICE nobody saw.
CREATE TABLE IF NOT EXISTS payments.application_backfill_report (
    id         BIGSERIAL   PRIMARY KEY,
    ran_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    table_name TEXT        NOT NULL,
    backfilled BIGINT      NOT NULL,
    unmapped   BIGINT      NOT NULL
);

-- Idempotent: only NULL rows are touched, and only when the mapping names an
-- application. Returns, per table, how many rows it filled and how many are
-- still NULL, and records the same in application_backfill_report.
CREATE OR REPLACE FUNCTION payments.backfill_application_ids()
RETURNS TABLE (table_name TEXT, backfilled BIGINT, unmapped BIGINT)
LANGUAGE plpgsql AS $$
DECLARE
    child RECORD;
    n     BIGINT;
    left_ BIGINT;
BEGIN
    UPDATE payments.payment_intents i
       SET application_id = payments.legacy_application_for(i.owner_domain, i.reference_type)
     WHERE i.application_id IS NULL
       AND payments.legacy_application_for(i.owner_domain, i.reference_type) IS NOT NULL;
    GET DIAGNOSTICS n = ROW_COUNT;
    SELECT count(*) INTO left_ FROM payments.payment_intents WHERE application_id IS NULL;
    table_name := 'payment_intents'; backfilled := n; unmapped := left_;
    INSERT INTO payments.application_backfill_report (table_name, backfilled, unmapped) VALUES ('payment_intents', n, left_);
    IF left_ > 0 THEN
        RAISE WARNING 'payments 010: % payment_intents row(s) could not be mapped to an application and stay NULL', left_;
    END IF;
    RETURN NEXT;

    FOR child IN
        SELECT * FROM (VALUES
            ('refund_commands',          'intent_id'),
            ('refund_required',          'intent_id'),
            ('provider_refunds_applied', 'intent_id'),
            ('refunds_applied',          'intent_id'),
            ('payment_holds',            'payment_intent_id')
        ) AS v(tbl, fk)
    LOOP
        EXECUTE format(
            'UPDATE payments.%I c SET application_id = i.application_id
               FROM payments.payment_intents i
              WHERE c.%I = i.id AND c.application_id IS NULL AND i.application_id IS NOT NULL',
            child.tbl, child.fk);
        GET DIAGNOSTICS n = ROW_COUNT;
        EXECUTE format('SELECT count(*) FROM payments.%I WHERE application_id IS NULL', child.tbl) INTO left_;
        table_name := child.tbl; backfilled := n; unmapped := left_;
        INSERT INTO payments.application_backfill_report (table_name, backfilled, unmapped) VALUES (child.tbl, n, left_);
        IF left_ > 0 THEN
            RAISE WARNING 'payments 010: % % row(s) could not be mapped to an application and stay NULL', left_, child.tbl;
        END IF;
        RETURN NEXT;
    END LOOP;
END
$$;

SELECT * FROM payments.backfill_application_ids();

ALTER TABLE payments.payment_intents          VALIDATE CONSTRAINT fk_payment_intents_application;
ALTER TABLE payments.refund_commands          VALIDATE CONSTRAINT fk_refund_commands_application;
ALTER TABLE payments.refund_required          VALIDATE CONSTRAINT fk_refund_required_application;
ALTER TABLE payments.provider_refunds_applied VALIDATE CONSTRAINT fk_provider_refunds_applied_application;
ALTER TABLE payments.refunds_applied          VALIDATE CONSTRAINT fk_refunds_applied_application;
ALTER TABLE payments.payment_holds            VALIDATE CONSTRAINT fk_payment_holds_application;
