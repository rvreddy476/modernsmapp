-- Migration 043: channel search (page-scoped search on the Tube app, 2026-09-05).
--
-- GET /v1/channels/search?q= matches `handle LIKE 'q%'` and
-- `lower(name) LIKE '%q%'`. The database collation is en_US.utf8, so the
-- plain btree on handle (idx_channels_handle) cannot serve a LIKE prefix;
-- text_pattern_ops can. The name substring match is served by a trigram GIN
-- index when pg_trgm is available (it is on the official image; creating the
-- extension needs superuser, which the dev stack has and production may
-- not) — otherwise lower(name) falls back to a scan, which is fine at the
-- channel counts a single-node deployment sees.
CREATE INDEX IF NOT EXISTS idx_channels_handle_pattern
    ON channels (handle text_pattern_ops);

CREATE INDEX IF NOT EXISTS idx_channels_name_lower
    ON channels (lower(name) text_pattern_ops);

DO $$
BEGIN
    BEGIN
        CREATE EXTENSION IF NOT EXISTS pg_trgm;
    EXCEPTION WHEN OTHERS THEN
        RAISE NOTICE 'pg_trgm not installable here (%); channel name search stays on lower(name)', SQLERRM;
    END;
    IF EXISTS (SELECT 1 FROM pg_extension WHERE extname = 'pg_trgm') THEN
        CREATE INDEX IF NOT EXISTS idx_channels_name_trgm
            ON channels USING gin (lower(name) gin_trgm_ops);
    END IF;
END $$;
