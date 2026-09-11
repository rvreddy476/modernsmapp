-- 007: canonical content type on every money surface, the content-type
-- quarantine, and the persisted state the daily rollup runs from.
-- Plan Phase 1D and 1E; audit M-16, M-10.
--
-- shared/postclassify.CanonicalMonetizationType is now the one mapping
-- every money-adjacent caller uses: short-form is 'flick', long-form is
-- 'long_video', anything else is not a monetizable kind. The stored
-- rows have to agree with the code that reads them, so the legacy
-- synonyms are remapped in place. The 17 'reel' views the audit found
-- (M-21) are the whole population of non-canonical video rows in the
-- dev database; they earned nothing under the old labels because the
-- rate lookup compares literals, and they remain on frozen days after
-- this, so nothing already settled changes.

UPDATE analytics.content_ownership
SET content_type = 'flick', projected_at = NOW()
WHERE content_type IN ('reel', 'short');

UPDATE analytics.content_ownership
SET content_type = 'long_video', projected_at = NOW()
WHERE content_type = 'video';

UPDATE analytics.content_hourly_agg
SET content_type = 'flick', updated_at = NOW()
WHERE content_type IN ('reel', 'short');

UPDATE analytics.content_hourly_agg
SET content_type = 'long_video', updated_at = NOW()
WHERE content_type = 'video';

UPDATE analytics.content_daily_summary
SET content_type = 'flick', updated_at = NOW()
WHERE content_type IN ('reel', 'short');

UPDATE analytics.content_daily_summary
SET content_type = 'long_video', updated_at = NOW()
WHERE content_type = 'video';

-- The quarantine. A PostCreated (or a reclassification) whose kind is
-- not monetizable is projected as content_type 'unknown' — never 'post',
-- which looked like a kind and was not one — and the raw value is kept
-- here so the exclusion can be audited. Existing non-canonical
-- ownership rows are registered now; their stored label is left as it
-- is, because model.IsDisplayView already treats every non-video label
-- as unlabelled and no rate resolves for any of them.
CREATE TABLE IF NOT EXISTS analytics.content_type_exclusions (
    content_id       UUID PRIMARY KEY,
    creator_id       UUID NOT NULL,
    raw_content_type TEXT NOT NULL,
    reason           TEXT NOT NULL,
    source           TEXT NOT NULL,
    first_seen_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    last_seen_at     TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_content_type_exclusions_creator
    ON analytics.content_type_exclusions (creator_id, first_seen_at DESC);

INSERT INTO analytics.content_type_exclusions
    (content_id, creator_id, raw_content_type, reason, source)
SELECT content_id, creator_id, content_type, 'not a monetizable kind', 'migration_007'
FROM analytics.content_ownership
WHERE content_type NOT IN ('flick', 'long_video')
ON CONFLICT (content_id) DO NOTHING;

-- The daily rollup's persisted state (Phase 1E). rollup_progress is
-- one row per UTC day the rollup has visited: 'open' while inside the
-- 48-hour reprocessing window, 'frozen' once outside it. A frozen day
-- is never rewritten except through the forced operator path.
CREATE TABLE IF NOT EXISTS analytics.rollup_progress (
    day          DATE PRIMARY KEY,
    status       TEXT NOT NULL CHECK (status IN ('open', 'frozen')),
    last_run_at  TIMESTAMPTZ,
    completed_at TIMESTAMPTZ,
    error        TEXT,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Small key/value settings the aggregation workers persist. The daily
-- rollup watermark is the earliest day the catch-up walk starts from.
CREATE TABLE IF NOT EXISTS analytics.aggregation_settings (
    key        TEXT PRIMARY KEY,
    value      TEXT NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Seed the watermark to today - 2: the first start under the new rules
-- rolls up the two days inside the window and nothing older. Without
-- this seed the first pass would walk every day since the beginning and
-- rewrite settled history under rules it was not settled by.
INSERT INTO analytics.aggregation_settings (key, value)
VALUES ('daily_rollup_watermark', (CURRENT_DATE - 2)::text)
ON CONFLICT (key) DO NOTHING;
