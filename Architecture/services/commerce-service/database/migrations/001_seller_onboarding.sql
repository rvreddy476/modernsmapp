-- ============================================================
-- Migration 001: Seller onboarding wizard + approval workflow
-- ============================================================

-- Extend sellers table with onboarding + approval fields
ALTER TABLE sellers ADD COLUMN IF NOT EXISTS business_page_id    UUID;
ALTER TABLE sellers ADD COLUMN IF NOT EXISTS owner_name          TEXT;
ALTER TABLE sellers ADD COLUMN IF NOT EXISTS business_type       TEXT NOT NULL DEFAULT 'individual'
    CHECK (business_type IN ('individual','retailer','wholesaler','manufacturer','brand','home_business'));
ALTER TABLE sellers ADD COLUMN IF NOT EXISTS tagline             TEXT;
ALTER TABLE sellers ADD COLUMN IF NOT EXISTS social_links_json   JSONB;
ALTER TABLE sellers ADD COLUMN IF NOT EXISTS onboarding_step     INT NOT NULL DEFAULT 1;
ALTER TABLE sellers ADD COLUMN IF NOT EXISTS status              TEXT NOT NULL DEFAULT 'draft'
    CHECK (status IN ('draft','submitted','under_review','changes_required','approved','rejected','suspended','disabled'));
ALTER TABLE sellers ADD COLUMN IF NOT EXISTS submitted_at        TIMESTAMPTZ;
ALTER TABLE sellers ADD COLUMN IF NOT EXISTS approved_at         TIMESTAMPTZ;
ALTER TABLE sellers ADD COLUMN IF NOT EXISTS rejected_at         TIMESTAMPTZ;
ALTER TABLE sellers ADD COLUMN IF NOT EXISTS suspension_reason   TEXT;
ALTER TABLE sellers ADD COLUMN IF NOT EXISTS rejection_reason    TEXT;
ALTER TABLE sellers ADD COLUMN IF NOT EXISTS changes_requested   TEXT;

CREATE INDEX IF NOT EXISTS idx_sellers_business_page ON sellers(business_page_id) WHERE business_page_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_sellers_status_queue  ON sellers(status) WHERE status IN ('submitted','under_review','changes_required');

-- KYC / verification documents
CREATE TABLE IF NOT EXISTS seller_documents (
    id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    seller_id           UUID NOT NULL REFERENCES sellers(id) ON DELETE CASCADE,
    document_type       TEXT NOT NULL
                            CHECK (document_type IN ('gst_certificate','pan_card','aadhaar','passport',
                                                     'business_registration','address_proof','cancelled_cheque','other')),
    document_number     TEXT,
    media_id            UUID NOT NULL,          -- uploaded via media-service
    verification_status TEXT NOT NULL DEFAULT 'pending'
                            CHECK (verification_status IN ('pending','verified','rejected','needs_correction')),
    remarks             TEXT,
    uploaded_at         TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    reviewed_at         TIMESTAMPTZ,
    reviewed_by         UUID,
    UNIQUE (seller_id, document_type)
);
CREATE INDEX IF NOT EXISTS idx_seller_docs_seller ON seller_documents(seller_id);
CREATE INDEX IF NOT EXISTS idx_seller_docs_status ON seller_documents(verification_status) WHERE verification_status = 'pending';

-- Fulfillment / delivery settings
CREATE TABLE IF NOT EXISTS seller_fulfillment_settings (
    id                   UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    seller_id            UUID NOT NULL UNIQUE REFERENCES sellers(id) ON DELETE CASCADE,
    pickup_address_id    UUID REFERENCES seller_addresses(id),
    warehouse_address_id UUID REFERENCES seller_addresses(id),
    delivery_modes       TEXT[] NOT NULL DEFAULT '{"platform"}',
    shipping_regions_json JSONB,
    dispatch_sla_hours   INT NOT NULL DEFAULT 48,
    return_supported     BOOLEAN NOT NULL DEFAULT TRUE,
    return_window_days   INT NOT NULL DEFAULT 7,
    cod_enabled          BOOLEAN NOT NULL DEFAULT FALSE,
    updated_at           TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Admin onboarding review audit trail
CREATE TABLE IF NOT EXISTS seller_onboarding_reviews (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    seller_id       UUID NOT NULL REFERENCES sellers(id) ON DELETE CASCADE,
    action          TEXT NOT NULL
                        CHECK (action IN ('approve','reject','request_changes','suspend','unsuspend','reopen')),
    notes           TEXT,
    actor_user_id   UUID NOT NULL,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_onboarding_reviews_seller ON seller_onboarding_reviews(seller_id, created_at DESC);

-- Product moderation audit trail
CREATE TABLE IF NOT EXISTS product_moderation_log (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    product_id      UUID NOT NULL REFERENCES products(id) ON DELETE CASCADE,
    action          TEXT NOT NULL
                        CHECK (action IN ('approve','reject','request_changes','suspend','archive')),
    reason          TEXT,
    actor_user_id   UUID NOT NULL,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_product_mod_log_product ON product_moderation_log(product_id, created_at DESC);

-- Extend products approval_status to match full workflow
-- (existing values: draft,pending,approved,rejected — add submitted,live,hidden,archived)
ALTER TABLE products DROP CONSTRAINT IF EXISTS products_approval_status_check;
ALTER TABLE products ADD CONSTRAINT products_approval_status_check
    CHECK (approval_status IN ('draft','submitted','under_review','approved','rejected','live','hidden','archived'));
