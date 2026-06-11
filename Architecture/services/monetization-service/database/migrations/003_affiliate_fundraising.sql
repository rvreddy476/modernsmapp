-- Creator Affiliate Storefronts
CREATE TABLE IF NOT EXISTS affiliate_links (
    id               UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    creator_id       UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    -- listing_id points at marketplace.listings in commerce_db — a different
    -- database, so no FK here (same pattern as creator_tiers migration 012).
    listing_id       UUID NOT NULL,
    commission_pct   REAL NOT NULL DEFAULT 5.0,
    commission_flat  NUMERIC(8,2),
    link_code        TEXT NOT NULL UNIQUE,
    click_count      BIGINT NOT NULL DEFAULT 0,
    conversion_count BIGINT NOT NULL DEFAULT 0,
    total_earned     NUMERIC(12,2) NOT NULL DEFAULT 0,
    is_active        BOOLEAN NOT NULL DEFAULT TRUE,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_affiliate_creator ON affiliate_links(creator_id) WHERE is_active = TRUE;
CREATE INDEX IF NOT EXISTS idx_affiliate_listing ON affiliate_links(listing_id);
CREATE INDEX IF NOT EXISTS idx_affiliate_code ON affiliate_links(link_code);

CREATE TABLE IF NOT EXISTS affiliate_conversions (
    id             UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    affiliate_id   UUID NOT NULL REFERENCES affiliate_links(id),
    order_id       UUID NOT NULL,
    buyer_id       UUID NOT NULL,
    commission_amt NUMERIC(8,2) NOT NULL,
    status         TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending','confirmed','paid','reversed')),
    created_at     TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_affiliate_conv_link ON affiliate_conversions(affiliate_id, created_at DESC);

-- Fundraising & Social Giving
CREATE TABLE IF NOT EXISTS fundraisers (
    id             UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    creator_id     UUID NOT NULL REFERENCES users(id),
    type           TEXT NOT NULL CHECK (type IN ('personal','community','ngo','emergency')),
    title          TEXT NOT NULL,
    description    TEXT NOT NULL,
    cover_media_id UUID,
    goal_amount    NUMERIC(12,2) NOT NULL,
    raised_amount  NUMERIC(12,2) NOT NULL DEFAULT 0,
    donor_count    INT NOT NULL DEFAULT 0,
    currency       VARCHAR(3) NOT NULL DEFAULT 'INR',
    status         TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active','paused','completed','cancelled')),
    ngo_id         UUID,
    gst_exempt     BOOLEAN NOT NULL DEFAULT FALSE,
    ends_at        TIMESTAMPTZ,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_fundraisers_creator ON fundraisers(creator_id, status);
CREATE INDEX IF NOT EXISTS idx_fundraisers_active ON fundraisers(status, created_at DESC) WHERE status = 'active';

CREATE TABLE IF NOT EXISTS donations (
    id                UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    fundraiser_id     UUID NOT NULL REFERENCES fundraisers(id),
    donor_id          UUID NOT NULL REFERENCES users(id),
    amount            NUMERIC(12,2) NOT NULL,
    currency          VARCHAR(3) NOT NULL DEFAULT 'INR',
    payment_intent_id UUID NOT NULL,
    is_anonymous      BOOLEAN NOT NULL DEFAULT FALSE,
    message           TEXT,
    receipt_url       TEXT,
    created_at        TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_donations_fundraiser ON donations(fundraiser_id, created_at DESC);
