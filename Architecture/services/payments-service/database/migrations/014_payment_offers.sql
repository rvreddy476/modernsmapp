-- payments-service migration 014 — bank offers (Razorpay Offers)
--
-- Founder, 1 Oct 2026: BANK OFFERS = "Yes, via Razorpay Offers", and he
-- knowingly authorised changing payment matching for it — ONLY so far as a
-- capture LOWER than the intent is accepted as a full payment when it was made
-- through an offer in THIS registry, active, within its computed cap
-- (internal/gateway/offermatch.go). Everything else matches exactly as before.
--
--   1. payments.payment_offers — the registry. An offer is created in the
--      Razorpay dashboard first (id `offer_…`), then registered here by an
--      operator through the admin family. application, provider and
--      provider_offer_id never change; there is no delete (deactivate is
--      active=false).
--   2. payments.payment_offer_changes — APPEND-ONLY: one full snapshot per
--      create and per edit, with who and with which credential. The matching
--      rule reads the snapshot in force when the customer paid, so "active at
--      capture time" and "its computed cap" mean the terms of that moment, not
--      of whenever a late webhook happens to arrive.
--   3. payment_intents: captured_minor / offer_id / offer_discount_minor, set
--      together only when an offer capture is accepted
--      (captured + discount = amount_minor), and refunded_captured_minor, the
--      money actually returned on an offer payment (refunded_amount_minor stays
--      in ORDER VALUE, as commerce requests and reads it).
--   4. refund_commands.provider_amount_minor — the money a refund of an offer
--      payment sends to the provider: floor(X × captured / intent) for a
--      partial X, the whole remaining captured money for the refund that
--      completes the order value, and never more than is left of the capture;
--      provider_refunds_applied.order_value_minor — the order value a provider
--      refund of an offer payment was credited as.
--
-- Every new column is NULL for a payment no offer touched, and nothing written
-- for such a payment changes (the golden in
-- internal/service/offers_golden_integration_test.go pins it).
--
-- EXPAND ONLY and re-runnable: nullable ADD COLUMN IF NOT EXISTS (no rewrite),
-- constraints added NOT VALID and validated separately (SHARE UPDATE EXCLUSIVE,
-- which does not block writes), every object guarded.

-- ─── 1. The registry ─────────────────────────────────────────────────

CREATE TABLE IF NOT EXISTS payments.payment_offers (
    id                 UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    application        TEXT        NOT NULL
                       CONSTRAINT fk_payment_offers_application REFERENCES payments.applications(key),
    provider           TEXT        NOT NULL DEFAULT 'razorpay',
    provider_offer_id  TEXT        NOT NULL,
    title              TEXT        NOT NULL,
    description        TEXT        NOT NULL DEFAULT '',
    payment_method     TEXT        NOT NULL,
    discount_type      TEXT        NOT NULL,
    -- Basis points for a percentage offer (1000 = 10%), paise for a flat one.
    discount_value     BIGINT      NOT NULL,
    max_discount_minor BIGINT,
    min_amount_minor   BIGINT      NOT NULL DEFAULT 0,
    funded_by          TEXT        NOT NULL,
    starts_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    ends_at            TIMESTAMPTZ,
    active             BOOLEAN     NOT NULL DEFAULT TRUE,
    created_by         TEXT        NOT NULL,
    created_at         TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at         TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT uq_payment_offers_provider_offer UNIQUE (provider, provider_offer_id),
    CONSTRAINT chk_payment_offers_provider CHECK (provider IN ('razorpay')),
    CONSTRAINT chk_payment_offers_provider_offer_id CHECK (provider_offer_id ~ '^offer_[A-Za-z0-9]{6,40}$'),
    CONSTRAINT chk_payment_offers_title CHECK (length(btrim(title)) BETWEEN 1 AND 120),
    CONSTRAINT chk_payment_offers_description CHECK (length(description) <= 1000),
    CONSTRAINT chk_payment_offers_method CHECK (payment_method IN ('card','upi','any')),
    CONSTRAINT chk_payment_offers_discount_type CHECK (discount_type IN ('percentage','flat')),
    CONSTRAINT chk_payment_offers_discount_value CHECK (
        discount_value > 0 AND (discount_type <> 'percentage' OR discount_value <= 10000)),
    CONSTRAINT chk_payment_offers_max_discount CHECK (max_discount_minor IS NULL OR max_discount_minor > 0),
    CONSTRAINT chk_payment_offers_min_amount CHECK (min_amount_minor >= 0),
    CONSTRAINT chk_payment_offers_funded_by CHECK (funded_by IN ('bank','merchant')),
    CONSTRAINT chk_payment_offers_window CHECK (ends_at IS NULL OR ends_at > starts_at),
    CONSTRAINT chk_payment_offers_created_by CHECK (length(btrim(created_by)) > 0)
);

CREATE INDEX IF NOT EXISTS idx_payment_offers_application_active
    ON payments.payment_offers (application, provider) WHERE active;

-- The identity of an offer never changes, and an offer is never deleted: a
-- capture may already have been accepted against it.
CREATE OR REPLACE FUNCTION payments.payment_offers_guard() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF TG_OP = 'DELETE' THEN
        RAISE EXCEPTION 'payments.payment_offers rows are never deleted; deactivate instead (active = false)';
    END IF;
    IF NEW.id <> OLD.id OR NEW.application <> OLD.application OR NEW.provider <> OLD.provider
       OR NEW.provider_offer_id <> OLD.provider_offer_id OR NEW.created_by <> OLD.created_by
       OR NEW.created_at <> OLD.created_at THEN
        RAISE EXCEPTION 'payments.payment_offers: id, application, provider, provider_offer_id and creation are immutable';
    END IF;
    RETURN NEW;
END $$;

DROP TRIGGER IF EXISTS trg_payment_offers_guard ON payments.payment_offers;
CREATE TRIGGER trg_payment_offers_guard
    BEFORE UPDATE OR DELETE ON payments.payment_offers
    FOR EACH ROW EXECUTE FUNCTION payments.payment_offers_guard();

-- ─── 2. The append-only change log ───────────────────────────────────

CREATE TABLE IF NOT EXISTS payments.payment_offer_changes (
    id                 BIGSERIAL   PRIMARY KEY,
    offer_id           UUID        NOT NULL REFERENCES payments.payment_offers(id),
    action             TEXT        NOT NULL CHECK (action IN ('created','updated')),
    operator_id        TEXT        NOT NULL,
    credential         TEXT        NOT NULL,
    -- The offer's terms AFTER this change, in full.
    title              TEXT        NOT NULL,
    description        TEXT        NOT NULL,
    payment_method     TEXT        NOT NULL,
    discount_type      TEXT        NOT NULL,
    discount_value     BIGINT      NOT NULL,
    max_discount_minor BIGINT,
    min_amount_minor   BIGINT      NOT NULL,
    funded_by          TEXT        NOT NULL,
    starts_at          TIMESTAMPTZ NOT NULL,
    ends_at            TIMESTAMPTZ,
    active             BOOLEAN     NOT NULL,
    changed_at         TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_payment_offer_changes_offer_time
    ON payments.payment_offer_changes (offer_id, changed_at DESC, id DESC);

CREATE OR REPLACE FUNCTION payments.payment_offer_changes_append_only() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    RAISE EXCEPTION 'payments.payment_offer_changes is append-only';
END $$;

DROP TRIGGER IF EXISTS trg_payment_offer_changes_append_only ON payments.payment_offer_changes;
CREATE TRIGGER trg_payment_offer_changes_append_only
    BEFORE UPDATE OR DELETE ON payments.payment_offer_changes
    FOR EACH ROW EXECUTE FUNCTION payments.payment_offer_changes_append_only();

DROP TRIGGER IF EXISTS trg_payment_offer_changes_no_truncate ON payments.payment_offer_changes;
CREATE TRIGGER trg_payment_offer_changes_no_truncate
    BEFORE TRUNCATE ON payments.payment_offer_changes
    FOR EACH STATEMENT EXECUTE FUNCTION payments.payment_offer_changes_append_only();

-- ─── 3. Intents ──────────────────────────────────────────────────────

ALTER TABLE payments.payment_intents ADD COLUMN IF NOT EXISTS captured_minor          BIGINT;
ALTER TABLE payments.payment_intents ADD COLUMN IF NOT EXISTS offer_id                UUID;
ALTER TABLE payments.payment_intents ADD COLUMN IF NOT EXISTS offer_discount_minor    BIGINT;
ALTER TABLE payments.payment_intents ADD COLUMN IF NOT EXISTS refunded_captured_minor BIGINT;

-- ─── 4. Refunds ──────────────────────────────────────────────────────

ALTER TABLE payments.refund_commands          ADD COLUMN IF NOT EXISTS provider_amount_minor BIGINT;
ALTER TABLE payments.provider_refunds_applied ADD COLUMN IF NOT EXISTS order_value_minor     BIGINT;

-- ─── Constraints (NOT VALID, then validated) ─────────────────────────

DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'fk_payment_intents_offer') THEN
        ALTER TABLE payments.payment_intents
            ADD CONSTRAINT fk_payment_intents_offer FOREIGN KEY (offer_id)
            REFERENCES payments.payment_offers(id) NOT VALID;
    END IF;
    -- All three together, or none; and the capture plus the discount is the
    -- order value exactly.
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'chk_payment_intents_offer_capture') THEN
        ALTER TABLE payments.payment_intents
            ADD CONSTRAINT chk_payment_intents_offer_capture CHECK (
                (captured_minor IS NULL AND offer_id IS NULL AND offer_discount_minor IS NULL)
                OR (captured_minor > 0 AND offer_id IS NOT NULL AND offer_discount_minor > 0
                    AND captured_minor + offer_discount_minor = amount_minor)) NOT VALID;
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'chk_payment_intents_refunded_captured') THEN
        ALTER TABLE payments.payment_intents
            ADD CONSTRAINT chk_payment_intents_refunded_captured CHECK (
                refunded_captured_minor IS NULL
                OR (captured_minor IS NOT NULL AND refunded_captured_minor BETWEEN 0 AND captured_minor)) NOT VALID;
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'chk_refund_commands_provider_amount') THEN
        ALTER TABLE payments.refund_commands
            ADD CONSTRAINT chk_refund_commands_provider_amount CHECK (
                provider_amount_minor IS NULL OR provider_amount_minor > 0) NOT VALID;
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'chk_provider_refunds_applied_order_value') THEN
        ALTER TABLE payments.provider_refunds_applied
            ADD CONSTRAINT chk_provider_refunds_applied_order_value CHECK (
                order_value_minor IS NULL OR order_value_minor > 0) NOT VALID;
    END IF;
END $$;

ALTER TABLE payments.payment_intents          VALIDATE CONSTRAINT fk_payment_intents_offer;
ALTER TABLE payments.payment_intents          VALIDATE CONSTRAINT chk_payment_intents_offer_capture;
ALTER TABLE payments.payment_intents          VALIDATE CONSTRAINT chk_payment_intents_refunded_captured;
ALTER TABLE payments.refund_commands          VALIDATE CONSTRAINT chk_refund_commands_provider_amount;
ALTER TABLE payments.provider_refunds_applied VALIDATE CONSTRAINT chk_provider_refunds_applied_order_value;
