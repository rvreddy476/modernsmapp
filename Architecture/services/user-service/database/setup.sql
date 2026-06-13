-- Database setup for Architecture/user-service
-- Consolidates initial schema and extended profile fields.

CREATE TABLE IF NOT EXISTS users (
    id UUID PRIMARY KEY,
    username TEXT UNIQUE,
    display_name TEXT NOT NULL,
    first_name TEXT,
    last_name TEXT,
    bio TEXT DEFAULT '',
    dob DATE,
    gender TEXT,
    avatar_media_id UUID,
    cover_media_id UUID,
    category TEXT DEFAULT 'personal',
    profession TEXT DEFAULT '',
    website TEXT DEFAULT '',
    location TEXT DEFAULT '',
    badge_flags INT DEFAULT 0,
    is_verified BOOLEAN DEFAULT FALSE,
    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_users_username ON users(username) WHERE username IS NOT NULL;

CREATE TABLE IF NOT EXISTS user_links (
    user_id UUID NOT NULL REFERENCES users(id),
    platform TEXT NOT NULL,
    url TEXT NOT NULL,
    display_label TEXT DEFAULT '',
    sort_order INT DEFAULT 0,
    PRIMARY KEY (user_id, platform)
);

CREATE TABLE IF NOT EXISTS user_about (
    user_id UUID NOT NULL REFERENCES users(id),
    section TEXT NOT NULL,
    item_id UUID NOT NULL DEFAULT gen_random_uuid(),
    data JSONB NOT NULL,
    visibility TEXT NOT NULL DEFAULT 'public',
    sort_order INT DEFAULT 0,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (user_id, section, item_id)
);

CREATE INDEX IF NOT EXISTS idx_user_about_section ON user_about(user_id, section);

CREATE TABLE IF NOT EXISTS user_settings (
    user_id UUID PRIMARY KEY REFERENCES users(id),
    account_visibility TEXT DEFAULT 'public',
    allow_messages_from TEXT DEFAULT 'everyone',
    allow_comments_from TEXT DEFAULT 'everyone',
    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL
);

-- ============================================================================
-- Follow-Only Public Pages (business_pages evolved in place).
-- BootstrapSchema runs setup.sql ONLY (not database/migrations/*), so the full
-- business_pages family + the Follow-Only Pages additions live here so a fresh
-- boot has everything. All idempotent — safe on a DB that already ran the
-- migration files.
-- ============================================================================

-- Base business_pages table (mirror of migration 001 + 006 + 007).
CREATE TABLE IF NOT EXISTS business_pages (
    id             UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id        UUID NOT NULL REFERENCES users(id),
    page_handle    TEXT NOT NULL UNIQUE,
    page_name      TEXT NOT NULL,
    category       TEXT NOT NULL,
    description    TEXT NOT NULL DEFAULT '',
    address        TEXT NOT NULL DEFAULT '',
    lat            DOUBLE PRECISION,
    lng            DOUBLE PRECISION,
    business_hours JSONB,
    phone          TEXT NOT NULL DEFAULT '',
    whatsapp       TEXT NOT NULL DEFAULT '',
    business_email TEXT NOT NULL DEFAULT '',
    services       JSONB,
    price_range    TEXT NOT NULL DEFAULT '',
    booking_url    TEXT NOT NULL DEFAULT '',
    menu_urls      JSONB,
    is_verified    BOOLEAN NOT NULL DEFAULT FALSE,
    avg_rating     DOUBLE PRECISION NOT NULL DEFAULT 0,
    review_count   INTEGER NOT NULL DEFAULT 0,
    faq            JSONB,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_business_pages_user ON business_pages (user_id);
CREATE INDEX IF NOT EXISTS idx_business_pages_handle ON business_pages (page_handle);
CREATE INDEX IF NOT EXISTS idx_business_pages_category ON business_pages (category);

CREATE TABLE IF NOT EXISTS business_reviews (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    page_id     UUID NOT NULL REFERENCES business_pages(id) ON DELETE CASCADE,
    reviewer_id UUID NOT NULL,
    rating      INTEGER NOT NULL CHECK (rating BETWEEN 1 AND 5),
    review_text TEXT NOT NULL DEFAULT '',
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE(page_id, reviewer_id)
);
CREATE INDEX IF NOT EXISTS idx_business_reviews_page ON business_reviews (page_id, created_at DESC);

-- Legacy columns (migration 006 + 007) — idempotent for fresh installs.
ALTER TABLE business_pages ADD COLUMN IF NOT EXISTS follower_count  INTEGER NOT NULL DEFAULT 0;
ALTER TABLE business_pages ADD COLUMN IF NOT EXISTS cover_media_id  TEXT    NOT NULL DEFAULT '';
ALTER TABLE business_pages ADD COLUMN IF NOT EXISTS avatar_media_id TEXT    NOT NULL DEFAULT '';
ALTER TABLE business_pages ADD COLUMN IF NOT EXISTS website         TEXT    NOT NULL DEFAULT '';
ALTER TABLE business_pages ADD COLUMN IF NOT EXISTS status          TEXT    NOT NULL DEFAULT 'draft';
ALTER TABLE business_pages ADD COLUMN IF NOT EXISTS seller_id       UUID;
CREATE INDEX IF NOT EXISTS idx_business_pages_seller ON business_pages(seller_id) WHERE seller_id IS NOT NULL;

-- Follow-Only Pages: page_type + 6-state lifecycle columns.
ALTER TABLE business_pages ADD COLUMN IF NOT EXISTS page_type           VARCHAR(50);
ALTER TABLE business_pages ADD COLUMN IF NOT EXISTS verification_status VARCHAR(40) NOT NULL DEFAULT 'not_submitted';
ALTER TABLE business_pages ADD COLUMN IF NOT EXISTS submitted_at        TIMESTAMPTZ;
ALTER TABLE business_pages ADD COLUMN IF NOT EXISTS approved_at         TIMESTAMPTZ;
ALTER TABLE business_pages ADD COLUMN IF NOT EXISTS approved_by_user_id UUID;
ALTER TABLE business_pages ADD COLUMN IF NOT EXISTS rejected_at         TIMESTAMPTZ;
ALTER TABLE business_pages ADD COLUMN IF NOT EXISTS rejection_reason    TEXT;
ALTER TABLE business_pages ADD COLUMN IF NOT EXISTS suspended_at        TIMESTAMPTZ;
ALTER TABLE business_pages ADD COLUMN IF NOT EXISTS disabled_at         TIMESTAMPTZ;

-- The old 3-value status CHECK (migration 007) blocks the new vocabulary —
-- drop it; status is validated in the Go service layer instead.
ALTER TABLE business_pages DROP CONSTRAINT IF EXISTS business_pages_status_check;

-- Migrate legacy 'active' → spec 'approved' (already-public pages stay followable).
UPDATE business_pages SET status = 'approved' WHERE status = 'active';
UPDATE business_pages SET page_type = 'business' WHERE page_type IS NULL;
UPDATE business_pages SET verification_status = 'verified' WHERE status = 'approved' AND verification_status = 'not_submitted';

CREATE INDEX IF NOT EXISTS idx_business_pages_status ON business_pages (status);
CREATE INDEX IF NOT EXISTS idx_business_pages_page_type ON business_pages (page_type);

-- page_followers (migration 006) + soft-delete revival support.
CREATE TABLE IF NOT EXISTS page_followers (
    page_id    UUID NOT NULL REFERENCES business_pages(id) ON DELETE CASCADE,
    user_id    UUID NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (page_id, user_id)
);
CREATE INDEX IF NOT EXISTS idx_page_followers_user ON page_followers (user_id);
ALTER TABLE page_followers ADD COLUMN IF NOT EXISTS deleted_at TIMESTAMPTZ;
CREATE UNIQUE INDEX IF NOT EXISTS uq_page_followers_active
    ON page_followers(page_id, user_id) WHERE deleted_at IS NULL;
CREATE INDEX IF NOT EXISTS idx_page_followers_user_active
    ON page_followers(user_id) WHERE deleted_at IS NULL;

-- Per-page roles (owner/admin/editor/viewer). Exactly one active owner per page.
CREATE TABLE IF NOT EXISTS page_roles (
    id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    page_id    UUID NOT NULL REFERENCES business_pages(id) ON DELETE CASCADE,
    user_id    UUID NOT NULL,
    role       VARCHAR(40) NOT NULL CHECK (role IN ('owner','admin','editor','viewer')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    deleted_at TIMESTAMPTZ
);
CREATE UNIQUE INDEX IF NOT EXISTS uq_page_roles_active
    ON page_roles(page_id, user_id) WHERE deleted_at IS NULL;
-- Backfill owner rows for pre-existing pages.
INSERT INTO page_roles (page_id, user_id, role)
    SELECT id, user_id, 'owner' FROM business_pages
    ON CONFLICT DO NOTHING;

-- Page verification documents (page-scoped, distinct from seller KYC docs).
CREATE TABLE IF NOT EXISTS page_verification_documents (
    id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    page_id             UUID NOT NULL REFERENCES business_pages(id) ON DELETE CASCADE,
    document_type       VARCHAR(80) NOT NULL,
    document_url        TEXT NOT NULL,
    status              VARCHAR(40) NOT NULL DEFAULT 'pending'
                            CHECK (status IN ('pending','approved','rejected')),
    reviewed_by_user_id UUID,
    reviewed_at         TIMESTAMPTZ,
    rejection_reason    TEXT,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_pvd_page_id ON page_verification_documents(page_id);
