-- Migration 070: cast & crew credits on long videos (2026-10-23).
--
-- A long video carries an ordered list of credits ({name, role}) and may tag
-- a VChat account on each one. The tagged person has to ACCEPT before the
-- video shows on their profile ("Appears in"), so a tag has a status:
--
--   unlinked  plain text, no account (user_id NULL) — also what a tag to an
--             account that blocked the uploader (either way), an unknown
--             account, or one over the uploader's daily limit is stored as
--   pending   tagged and waiting for that person's answer
--   accepted  the person said yes; the video is in their "Appears in"
--   declined  the person said no; the credit stays as plain text to viewers
--
-- Editing the list replaces it (service/credits.go), but a tag whose
-- (user_id, role) is still present keeps its row id and status, so an
-- unrelated edit never re-asks the person or loses their answer.
--
-- post_credit_tag_events is the uploader's tagging ledger behind the limit
-- of 20 new pending tags per 24 hours. It is a ledger rather than a count of
-- post_credits because an uploader could otherwise tag 20 people, remove the
-- tags and tag 20 more. Rows older than a day are pruned by the write that
-- reads them.
--
-- Also the three long-video facts search filters on: genres (up to 3 slugs
-- from service/genres.go), release_year and production_house.
--
-- Idempotent.

CREATE TABLE IF NOT EXISTS post_credits (
    id           UUID PRIMARY KEY,
    post_id      UUID NOT NULL REFERENCES posts(id) ON DELETE CASCADE,
    position     INT  NOT NULL,
    name         TEXT NOT NULL,
    role         TEXT NOT NULL,
    user_id      UUID,
    status       TEXT NOT NULL DEFAULT 'unlinked',
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    responded_at TIMESTAMPTZ
);

ALTER TABLE post_credits DROP CONSTRAINT IF EXISTS chk_post_credits_status;
ALTER TABLE post_credits ADD CONSTRAINT chk_post_credits_status
    CHECK (status IN ('unlinked', 'pending', 'accepted', 'declined'));

-- A row has an account exactly when it is not plain text.
ALTER TABLE post_credits DROP CONSTRAINT IF EXISTS chk_post_credits_link;
ALTER TABLE post_credits ADD CONSTRAINT chk_post_credits_link
    CHECK ((status = 'unlinked') = (user_id IS NULL));

ALTER TABLE post_credits DROP CONSTRAINT IF EXISTS chk_post_credits_position;
ALTER TABLE post_credits ADD CONSTRAINT chk_post_credits_position
    CHECK (position >= 0 AND position < 30);

ALTER TABLE post_credits DROP CONSTRAINT IF EXISTS chk_post_credits_name;
ALTER TABLE post_credits ADD CONSTRAINT chk_post_credits_name
    CHECK (char_length(name) BETWEEN 1 AND 80);

ALTER TABLE post_credits DROP CONSTRAINT IF EXISTS chk_post_credits_role;
ALTER TABLE post_credits ADD CONSTRAINT chk_post_credits_role
    CHECK (char_length(role) BETWEEN 1 AND 40);

CREATE INDEX IF NOT EXISTS idx_post_credits_post_position
    ON post_credits (post_id, position);
-- My pending requests and my "Appears in", newest first.
CREATE INDEX IF NOT EXISTS idx_post_credits_user_status
    ON post_credits (user_id, status, created_at DESC)
    WHERE user_id IS NOT NULL;

CREATE TABLE IF NOT EXISTS post_credit_tag_events (
    uploader_id UUID NOT NULL,
    credit_id   UUID NOT NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_post_credit_tag_events_uploader
    ON post_credit_tag_events (uploader_id, created_at);

ALTER TABLE posts ADD COLUMN IF NOT EXISTS genres           TEXT[] NOT NULL DEFAULT '{}';
ALTER TABLE posts ADD COLUMN IF NOT EXISTS release_year     INT;
ALTER TABLE posts ADD COLUMN IF NOT EXISTS production_house TEXT;

ALTER TABLE posts DROP CONSTRAINT IF EXISTS chk_posts_genres;
ALTER TABLE posts ADD CONSTRAINT chk_posts_genres
    CHECK (cardinality(genres) <= 3);
ALTER TABLE posts DROP CONSTRAINT IF EXISTS chk_posts_release_year;
ALTER TABLE posts ADD CONSTRAINT chk_posts_release_year
    CHECK (release_year IS NULL OR release_year BETWEEN 1900 AND 2100);
ALTER TABLE posts DROP CONSTRAINT IF EXISTS chk_posts_production_house;
ALTER TABLE posts ADD CONSTRAINT chk_posts_production_house
    CHECK (production_house IS NULL OR char_length(production_house) BETWEEN 1 AND 100);

-- Reel / long-video studio drafts keep what the Credits step set, so a saved
-- draft publishes the same credits (service/drafts.go). Plain JSON: tags are
-- only made, checked and announced when the draft publishes.
ALTER TABLE reel_drafts ADD COLUMN IF NOT EXISTS credits          JSONB;
ALTER TABLE reel_drafts ADD COLUMN IF NOT EXISTS genres           TEXT[] NOT NULL DEFAULT '{}';
ALTER TABLE reel_drafts ADD COLUMN IF NOT EXISTS release_year     INT;
ALTER TABLE reel_drafts ADD COLUMN IF NOT EXISTS production_house TEXT;
