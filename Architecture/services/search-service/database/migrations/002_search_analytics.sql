-- Search analytics tables. Powers future CTR-based re-ranking and
-- query-trend dashboards. Writes are best-effort (the search request
-- never fails on insert error), so these tables are append-only and
-- index for read-time aggregation.

CREATE TABLE IF NOT EXISTS search_queries (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    viewer_id     UUID,
    query         TEXT NOT NULL,
    types         TEXT[] NOT NULL,
    result_counts JSONB NOT NULL,   -- {"posts": 23, "users": 4, ...}
    occurred_at   TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_search_queries_occurred_at ON search_queries(occurred_at DESC);
CREATE INDEX IF NOT EXISTS idx_search_queries_viewer
    ON search_queries(viewer_id, occurred_at DESC) WHERE viewer_id IS NOT NULL;

-- Click-through tracking. Joins back to search_queries via query_id so
-- ranking analytics can correlate impressions with clicks per position.
CREATE TABLE IF NOT EXISTS search_clicks (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    query_id    UUID NOT NULL,
    viewer_id   UUID,
    entity_type TEXT NOT NULL,
    entity_id   TEXT NOT NULL,
    position    INT  NOT NULL,
    occurred_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_search_clicks_query ON search_clicks(query_id);
CREATE INDEX IF NOT EXISTS idx_search_clicks_occurred_at ON search_clicks(occurred_at DESC);
