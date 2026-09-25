-- In-group post search.
--
-- The group page's search box filters the GROUP's posts server-side, which
-- means a full-text predicate over title+body on every read. Without an index
-- that is a sequential scan of group_posts per keystroke-debounced request, so
-- the index ships in the same change as the query rather than "later".
--
-- EXPRESSION INDEX, and the expression must match the query's byte for byte or
-- the planner will not use it and nobody will notice — the endpoint stays
-- correct and merely gets slow. The single source of truth for that expression
-- is groupPostSearchVector in internal/store/group_post_search.go, and a test
-- (TestSearchVectorExpressionMatchesTheMigration) asserts this file contains
-- the same text with the `p.` alias stripped.
--
-- to_tsvector with an explicit configuration is IMMUTABLE, so it is indexable.
-- The one-argument form is not, and is the usual reason this fails.
--
-- coalesce on both columns: title and body are nullable, and `NULL || ' '` is
-- NULL, so a post with no title would index as NULL and be invisible to
-- search. That is the quiet failure this line prevents.
CREATE INDEX IF NOT EXISTS idx_group_posts_search
    ON group_posts
    USING gin(to_tsvector('english', coalesce(title, '') || ' ' || coalesce(body, '')))
    WHERE status = 'published';

-- Partial on status = 'published' for two reasons, one of them safety:
--
--   * Size. Deleted and pending posts are never searchable, so indexing them
--     is pure cost.
--
--   * It matches the query's own predicate. SearchGroupPostsV2 filters
--     p.status = 'published' exactly as the feed does, so a deleted or
--     awaiting-approval post cannot surface through search. The partial index
--     keeps the index and the query making the same statement, which is one
--     fewer place for them to drift.
--
-- The group_id side of the predicate is already served by idx_group_posts_feed
-- (group_id, status, created_at DESC) WHERE status = 'published', which the
-- planner can bitmap-AND with this one. No btree_gin extension is required,
-- and requiring one would be a deployment dependency for a search box.
