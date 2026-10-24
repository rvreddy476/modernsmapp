-- 074: saved collections and followed hashtags (Social gaps, 2026-10-24).
--
-- A. Saved collections. saved_items already carried a free-text
--    collection_name (default 'All Saved'), but nothing could create,
--    rename or delete a collection, and any string a client sent became one.
--    Collections are now rows: at most 50 per user (enforced by the store
--    under a per-user advisory lock), names 1-40 characters (the service),
--    unique per user case-insensitively (the index below). An item sits in
--    exactly one collection; collection_id NULL is the default collection
--    ("All Saved"). Deleting a collection moves its items back to the
--    default (the store does it explicitly; ON DELETE SET NULL is the
--    backstop). collection_name stays in step with collection_id so the
--    pre-collections readers (GET /v1/saved?collection=<name>) keep working.
--
-- B. Followed hashtags. One row per (user, tag); the tag is stored in the
--    form posts.hashtags holds (lowercased by the service exactly as a
--    post's tags are, no leading #). The CHECK does not re-lowercase: Go's
--    and Postgres's Unicode case maps differ for a few letters, and the
--    service's form is the one posts.hashtags matches.
--
-- Expand-only: new tables, one new nullable column, a backfill that only
-- touches rows whose collection_id is still NULL.

-- ── A. Saved collections ────────────────────────────────────────────────
CREATE TABLE IF NOT EXISTS saved_collections (
    id         UUID PRIMARY KEY,
    user_id    UUID NOT NULL,
    -- 200 is the backstop for names backfilled from the free-text column;
    -- the service holds new and renamed collections to 1-40 characters.
    name       TEXT NOT NULL CHECK (btrim(name) <> '' AND char_length(name) <= 200),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE UNIQUE INDEX IF NOT EXISTS uq_saved_collections_user_name
    ON saved_collections (user_id, lower(name));
CREATE INDEX IF NOT EXISTS idx_saved_collections_user
    ON saved_collections (user_id, created_at, id);

ALTER TABLE saved_items
    ADD COLUMN IF NOT EXISTS collection_id UUID REFERENCES saved_collections(id) ON DELETE SET NULL;
CREATE INDEX IF NOT EXISTS idx_saved_items_user_collection
    ON saved_items (user_id, collection_id, created_at DESC);

-- Backfill: every named, non-default collection_name becomes a collection
-- (case variants of one name are one collection), and its items point at it.
INSERT INTO saved_collections (id, user_id, name)
SELECT gen_random_uuid(), user_id, MIN(left(btrim(collection_name), 200))
  FROM saved_items
 WHERE collection_id IS NULL
   AND lower(btrim(collection_name)) NOT IN ('', 'all saved', 'default')
 GROUP BY user_id, lower(left(btrim(collection_name), 200))
ON CONFLICT DO NOTHING;

UPDATE saved_items si
   SET collection_id = c.id,
       collection_name = c.name
  FROM saved_collections c
 WHERE si.collection_id IS NULL
   AND c.user_id = si.user_id
   AND lower(c.name) = lower(left(btrim(si.collection_name), 200))
   AND lower(btrim(si.collection_name)) NOT IN ('', 'all saved', 'default');

-- The default collection has one spelling.
UPDATE saved_items
   SET collection_name = 'All Saved'
 WHERE collection_id IS NULL
   AND collection_name <> 'All Saved';

ALTER TABLE saved_items ALTER COLUMN collection_name SET DEFAULT 'All Saved';

-- ── B. Followed hashtags ────────────────────────────────────────────────
CREATE TABLE IF NOT EXISTS hashtag_follows (
    user_id    UUID NOT NULL,
    tag        TEXT NOT NULL CHECK (tag <> '' AND left(tag, 1) <> '#' AND char_length(tag) <= 50),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (user_id, tag)
);
CREATE INDEX IF NOT EXISTS idx_hashtag_follows_user_created
    ON hashtag_follows (user_id, created_at DESC, tag);
CREATE INDEX IF NOT EXISTS idx_hashtag_follows_tag
    ON hashtag_follows (tag);
