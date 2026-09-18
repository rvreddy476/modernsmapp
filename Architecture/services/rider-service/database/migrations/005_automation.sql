-- 005_automation.sql — launch-safety automation: subscription checkout through
-- payments-service, government-verified captain approval, rule-based refunds.
--
-- Re-runnable DDL (IF NOT EXISTS / DROP CONSTRAINT IF EXISTS). Founder rule:
-- nothing a captain or customer does waits for a human, except a manually
-- uploaded document, which keeps the admin review path.

-- 1. Subscriptions: a checkout row waits for the signed capture ------------
-- 'pending_payment' is a subscription row with an open payments intent. Added
-- here and used only by later statements/transactions (Postgres 12+ rule).
ALTER TYPE rider_subscription_status ADD VALUE IF NOT EXISTS 'pending_payment';

ALTER TABLE rider_partner_subscriptions ADD COLUMN IF NOT EXISTS intent_id              UUID;
ALTER TABLE rider_partner_subscriptions ADD COLUMN IF NOT EXISTS intent_method          TEXT;
ALTER TABLE rider_partner_subscriptions ADD COLUMN IF NOT EXISTS amount_paise           BIGINT NOT NULL DEFAULT 0 CHECK (amount_paise >= 0);
-- payment_status: NULL for legacy (proof / wallet) rows; pending | confirming
-- | paid | failed for checkout rows. Only the consumer writes paid / failed.
ALTER TABLE rider_partner_subscriptions ADD COLUMN IF NOT EXISTS payment_status         TEXT;
ALTER TABLE rider_partner_subscriptions ADD COLUMN IF NOT EXISTS provider_reference     TEXT;
ALTER TABLE rider_partner_subscriptions ADD COLUMN IF NOT EXISTS payment_failure_reason TEXT;
ALTER TABLE rider_partner_subscriptions ADD COLUMN IF NOT EXISTS paid_at                TIMESTAMPTZ;
-- renews_subscription_id names the active row a renewal extends from.
ALTER TABLE rider_partner_subscriptions ADD COLUMN IF NOT EXISTS renews_subscription_id UUID;
ALTER TABLE rider_partner_subscriptions DROP CONSTRAINT IF EXISTS rider_partner_subscriptions_payment_status_check;
ALTER TABLE rider_partner_subscriptions ADD CONSTRAINT rider_partner_subscriptions_payment_status_check
    CHECK (payment_status IS NULL OR payment_status IN ('pending','confirming','paid','failed'));
CREATE INDEX IF NOT EXISTS idx_rider_partner_subscriptions_intent
    ON rider_partner_subscriptions(intent_id) WHERE intent_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_rider_partner_subscriptions_partner_created
    ON rider_partner_subscriptions(partner_id, created_at DESC);

-- The free trial is once per partner, ever: the stamp is written in the same
-- transaction as the trial row, guarded by WHERE trial_used_at IS NULL.
ALTER TABLE rider_partners ADD COLUMN IF NOT EXISTS trial_used_at TIMESTAMPTZ;

-- 2. Documents: where they came from and who verified them ------------------
-- source: 'digilocker' (fetched by rider-service from the partner's DigiLocker
-- after the Aadhaar callback; verified automatically) or 'upload' (the
-- partner's own file; the one human fallback). verified_by_actor is 'auto' or
-- the admin's user id. media_id is the media-service id of the file (the
-- selfie), photo_media_id the DigiLocker document's photo (the DL photo the
-- selfie is compared with).
ALTER TABLE rider_partner_documents ADD COLUMN IF NOT EXISTS source            TEXT NOT NULL DEFAULT 'upload';
ALTER TABLE rider_partner_documents ADD COLUMN IF NOT EXISTS verified_by_actor TEXT;
ALTER TABLE rider_partner_documents ADD COLUMN IF NOT EXISTS media_id          UUID;
ALTER TABLE rider_partner_documents ADD COLUMN IF NOT EXISTS photo_media_id    UUID;
ALTER TABLE rider_partner_documents ADD COLUMN IF NOT EXISTS auto_check_detail TEXT;
ALTER TABLE rider_partner_documents ADD COLUMN IF NOT EXISTS digilocker_ref    TEXT;
ALTER TABLE rider_partner_documents DROP CONSTRAINT IF EXISTS rider_partner_documents_source_check;
ALTER TABLE rider_partner_documents ADD CONSTRAINT rider_partner_documents_source_check
    CHECK (source IN ('digilocker','upload'));
CREATE INDEX IF NOT EXISTS idx_rider_partner_documents_partner_type
    ON rider_partner_documents(partner_id, document_type, created_at DESC);

ALTER TABLE rider_vehicle_documents ADD COLUMN IF NOT EXISTS source            TEXT NOT NULL DEFAULT 'upload';
ALTER TABLE rider_vehicle_documents ADD COLUMN IF NOT EXISTS verified_by_actor TEXT;
ALTER TABLE rider_vehicle_documents ADD COLUMN IF NOT EXISTS verified_at       TIMESTAMPTZ;
ALTER TABLE rider_vehicle_documents ADD COLUMN IF NOT EXISTS digilocker_ref    TEXT;
ALTER TABLE rider_vehicle_documents DROP CONSTRAINT IF EXISTS rider_vehicle_documents_source_check;
ALTER TABLE rider_vehicle_documents ADD CONSTRAINT rider_vehicle_documents_source_check
    CHECK (source IN ('digilocker','upload'));
CREATE INDEX IF NOT EXISTS idx_rider_vehicle_documents_vehicle_type
    ON rider_vehicle_documents(vehicle_id, document_type, created_at DESC);

ALTER TABLE rider_vehicles ADD COLUMN IF NOT EXISTS verified_by_actor TEXT;
ALTER TABLE rider_vehicles ADD COLUMN IF NOT EXISTS verified_at       TIMESTAMPTZ;

-- 3. Refunds by rule ----------------------------------------------------------
-- A refund can now name an outstanding fee paid directly (no ride payment
-- row) and carries the rule that filed it. requested_by is the fixed system
-- actor for automatic refunds.
ALTER TABLE rider_ride_refunds ALTER COLUMN payment_id DROP NOT NULL;
ALTER TABLE rider_ride_refunds ADD COLUMN IF NOT EXISTS outstanding_id UUID REFERENCES rider_customer_outstanding(id);
ALTER TABLE rider_ride_refunds ADD COLUMN IF NOT EXISTS rule_code      TEXT NOT NULL DEFAULT 'discretionary';
ALTER TABLE rider_ride_refunds DROP CONSTRAINT IF EXISTS rider_ride_refunds_rule_code_check;
ALTER TABLE rider_ride_refunds ADD CONSTRAINT rider_ride_refunds_rule_code_check
    CHECK (rule_code IN ('discretionary','captain_cancel','duplicate_capture','cancellation_fee'));
ALTER TABLE rider_ride_refunds DROP CONSTRAINT IF EXISTS rider_ride_refunds_target_check;
ALTER TABLE rider_ride_refunds ADD CONSTRAINT rider_ride_refunds_target_check
    CHECK (payment_id IS NOT NULL OR outstanding_id IS NOT NULL);
CREATE INDEX IF NOT EXISTS idx_rider_ride_refunds_outstanding
    ON rider_ride_refunds(outstanding_id) WHERE outstanding_id IS NOT NULL;
-- One automatic refund per rule and target: a re-delivered event or a
-- re-evaluation cannot file a second one.
CREATE UNIQUE INDEX IF NOT EXISTS ux_rider_ride_refunds_rule_payment
    ON rider_ride_refunds(rule_code, payment_id, intent_id)
    WHERE rule_code <> 'discretionary' AND payment_id IS NOT NULL;
CREATE UNIQUE INDEX IF NOT EXISTS ux_rider_ride_refunds_rule_outstanding
    ON rider_ride_refunds(rule_code, outstanding_id)
    WHERE rule_code <> 'discretionary' AND outstanding_id IS NOT NULL;

-- A directly paid cancellation fee can be refunded by rule.
ALTER TABLE rider_customer_outstanding DROP CONSTRAINT IF EXISTS rider_customer_outstanding_status_check;
ALTER TABLE rider_customer_outstanding ADD CONSTRAINT rider_customer_outstanding_status_check
    CHECK (status IN ('pending','settled','waived','refunded'));
ALTER TABLE rider_customer_outstanding ADD COLUMN IF NOT EXISTS refunded_at TIMESTAMPTZ;

-- Reconciliation rows can now name a subscription (a subscription capture
-- that mismatched) instead of a ride.
ALTER TABLE rider_payment_reconciliation ALTER COLUMN ride_id DROP NOT NULL;
ALTER TABLE rider_payment_reconciliation ADD COLUMN IF NOT EXISTS subscription_id UUID;
ALTER TABLE rider_payment_reconciliation DROP CONSTRAINT IF EXISTS rider_payment_reconciliation_target_check;
ALTER TABLE rider_payment_reconciliation ADD CONSTRAINT rider_payment_reconciliation_target_check
    CHECK (ride_id IS NOT NULL OR subscription_id IS NOT NULL);
