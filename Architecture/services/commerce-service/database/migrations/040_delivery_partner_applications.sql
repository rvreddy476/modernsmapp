-- Delivery workers are NOT sellers. No merchant approval or role is copied.
-- No coverage, payout or live dispatch is enabled by this migration.
CREATE TABLE IF NOT EXISTS commerce_delivery_profiles (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID NOT NULL UNIQUE,
    full_name TEXT NOT NULL CHECK (length(full_name) BETWEEN 2 AND 120),
    vehicle_type TEXT NOT NULL CHECK (vehicle_type IN ('bicycle','motorbike','car')),
    status TEXT NOT NULL DEFAULT 'draft' CHECK (status IN ('draft','submitted','approved','changes_requested','suspended')),
    review_note TEXT NOT NULL DEFAULT '',
    revision BIGINT NOT NULL DEFAULT 1,
    submitted_at TIMESTAMPTZ,
    reviewed_at TIMESTAMPTZ,
    reviewed_by UUID,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE TABLE IF NOT EXISTS commerce_delivery_documents (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    profile_id UUID NOT NULL REFERENCES commerce_delivery_profiles(id),
    kind TEXT NOT NULL CHECK (kind IN ('selfie','identity','driving_licence','vehicle_registration','bank_proof','introduction')),
    media_id UUID NOT NULL,
    status TEXT NOT NULL DEFAULT 'submitted' CHECK (status IN ('submitted','approved','changes_requested')),
    review_note TEXT NOT NULL DEFAULT '',
    reviewed_by UUID,
    reviewed_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE(profile_id,kind)
);
CREATE TABLE IF NOT EXISTS commerce_delivery_review_audit (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    profile_id UUID NOT NULL REFERENCES commerce_delivery_profiles(id),
    document_id UUID,
    actor_id UUID NOT NULL,
    decision TEXT NOT NULL,
    note TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS commerce_delivery_review_queue ON commerce_delivery_profiles(status,updated_at,id);
CREATE OR REPLACE FUNCTION commerce_delivery_audit_append_only() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    RAISE EXCEPTION 'commerce delivery audit is append-only' USING ERRCODE='insufficient_privilege';
END; $$;
DROP TRIGGER IF EXISTS commerce_delivery_audit_append_only ON commerce_delivery_review_audit;
CREATE TRIGGER commerce_delivery_audit_append_only BEFORE UPDATE OR DELETE ON commerce_delivery_review_audit
    FOR EACH ROW EXECUTE FUNCTION commerce_delivery_audit_append_only();
