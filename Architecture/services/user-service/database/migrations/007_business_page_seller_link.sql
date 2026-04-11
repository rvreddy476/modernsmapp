-- Migration 007: Link business_pages to commerce seller + add status
ALTER TABLE business_pages ADD COLUMN IF NOT EXISTS status    TEXT NOT NULL DEFAULT 'active'
    CHECK (status IN ('draft','active','suspended'));
ALTER TABLE business_pages ADD COLUMN IF NOT EXISTS seller_id UUID;  -- back-ref to commerce-service seller (set after onboarding)

CREATE INDEX IF NOT EXISTS idx_business_pages_seller ON business_pages(seller_id) WHERE seller_id IS NOT NULL;
