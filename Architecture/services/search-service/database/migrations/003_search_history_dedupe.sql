-- T-15 (2026-10-14): search history is recorded on every signed-in search,
-- so it needs a dedupe key. `query_key` is the normalised form the service
-- computes (trimmed, whitespace collapsed, lower-cased); `query` keeps what
-- the person typed for display. One row per (user, query_key): searching
-- the same thing again moves the existing row to the top instead of adding
-- a duplicate.

ALTER TABLE search_history ADD COLUMN IF NOT EXISTS query_key TEXT;

UPDATE search_history
   SET query_key = lower(regexp_replace(btrim(query), '\s+', ' ', 'g'))
 WHERE query_key IS NULL;

-- Collapse any duplicates that already exist, keeping the newest.
DELETE FROM search_history h
 USING search_history newer
 WHERE h.user_id = newer.user_id
   AND h.query_key = newer.query_key
   AND (h.searched_at, h.id) < (newer.searched_at, newer.id);

ALTER TABLE search_history ALTER COLUMN query_key SET NOT NULL;

CREATE UNIQUE INDEX IF NOT EXISTS uq_search_history_user_key
    ON search_history (user_id, query_key);

-- The popular-searches read aggregates the last 7 days across users.
CREATE INDEX IF NOT EXISTS idx_search_history_searched_at
    ON search_history (searched_at DESC);
