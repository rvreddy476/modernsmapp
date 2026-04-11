-- Add missing media & social fields to business_pages
ALTER TABLE business_pages ADD COLUMN IF NOT EXISTS follower_count  INTEGER NOT NULL DEFAULT 0;
ALTER TABLE business_pages ADD COLUMN IF NOT EXISTS cover_media_id  TEXT    NOT NULL DEFAULT '';
ALTER TABLE business_pages ADD COLUMN IF NOT EXISTS avatar_media_id TEXT    NOT NULL DEFAULT '';
ALTER TABLE business_pages ADD COLUMN IF NOT EXISTS website         TEXT    NOT NULL DEFAULT '';

-- Page followers
CREATE TABLE IF NOT EXISTS page_followers (
    page_id    UUID NOT NULL REFERENCES business_pages(id) ON DELETE CASCADE,
    user_id    UUID NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (page_id, user_id)
);
CREATE INDEX IF NOT EXISTS idx_page_followers_user ON page_followers (user_id);
CREATE INDEX IF NOT EXISTS idx_business_pages_category ON business_pages (category);
