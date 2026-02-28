-- Video analytics indexes on events_raw for content-based queries
CREATE INDEX IF NOT EXISTS idx_events_raw_content
    ON analytics.events_raw ((payload->>'content_id'), ts DESC)
    WHERE type IN ('play_start','milestone','play_end','watch_heartbeat','impression');

CREATE INDEX IF NOT EXISTS idx_events_raw_session
    ON analytics.events_raw ((payload->>'session_id'), ts DESC)
    WHERE type IN ('play_start','milestone','play_end','watch_heartbeat');

-- Hourly aggregation table (partitioned by month)
CREATE TABLE IF NOT EXISTS analytics.content_hourly_agg (
    content_id          UUID NOT NULL,
    hour_bucket         TIMESTAMPTZ NOT NULL,
    creator_id          UUID NOT NULL,
    content_type        TEXT NOT NULL,
    impressions         BIGINT NOT NULL DEFAULT 0,
    plays               BIGINT NOT NULL DEFAULT 0,
    views_display       BIGINT NOT NULL DEFAULT 0,
    views_1s            BIGINT NOT NULL DEFAULT 0,
    views_3s            BIGINT NOT NULL DEFAULT 0,
    views_10s           BIGINT NOT NULL DEFAULT 0,
    views_30s           BIGINT NOT NULL DEFAULT 0,
    views_60s           BIGINT NOT NULL DEFAULT 0,
    unique_viewers      BIGINT NOT NULL DEFAULT 0,
    repeat_viewers      BIGINT NOT NULL DEFAULT 0,
    watch_time_total_ms BIGINT NOT NULL DEFAULT 0,
    avg_watch_time_ms   DOUBLE PRECISION NOT NULL DEFAULT 0,
    avg_percent_viewed  DOUBLE PRECISION NOT NULL DEFAULT 0,
    completion_rate     DOUBLE PRECISION NOT NULL DEFAULT 0,
    rewatch_rate        DOUBLE PRECISION NOT NULL DEFAULT 0,
    skip_rate           DOUBLE PRECISION NOT NULL DEFAULT 0,
    early_swipe_rate    DOUBLE PRECISION NOT NULL DEFAULT 0,
    likes               BIGINT NOT NULL DEFAULT 0,
    comments            BIGINT NOT NULL DEFAULT 0,
    shares              BIGINT NOT NULL DEFAULT 0,
    saves               BIGINT NOT NULL DEFAULT 0,
    follows_from_content BIGINT NOT NULL DEFAULT 0,
    not_interested      BIGINT NOT NULL DEFAULT 0,
    reports             BIGINT NOT NULL DEFAULT 0,
    blocks              BIGINT NOT NULL DEFAULT 0,
    view_score_total    DOUBLE PRECISION NOT NULL DEFAULT 0,
    vqs_avg             DOUBLE PRECISION NOT NULL DEFAULT 0,
    content_quality_score DOUBLE PRECISION NOT NULL DEFAULT 0,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (content_id, hour_bucket)
) PARTITION BY RANGE (hour_bucket);

CREATE TABLE IF NOT EXISTS analytics.content_hourly_agg_default
    PARTITION OF analytics.content_hourly_agg DEFAULT;

CREATE INDEX IF NOT EXISTS idx_hourly_agg_creator
    ON analytics.content_hourly_agg (creator_id, hour_bucket DESC);

CREATE INDEX IF NOT EXISTS idx_hourly_agg_cqs
    ON analytics.content_hourly_agg (content_quality_score DESC, hour_bucket DESC);

-- Daily summary table (rolled up from hourly)
CREATE TABLE IF NOT EXISTS analytics.content_daily_summary (
    content_id          UUID NOT NULL,
    day_bucket          DATE NOT NULL,
    creator_id          UUID NOT NULL,
    content_type        TEXT NOT NULL,
    impressions         BIGINT NOT NULL DEFAULT 0,
    plays               BIGINT NOT NULL DEFAULT 0,
    views_display       BIGINT NOT NULL DEFAULT 0,
    unique_viewers      BIGINT NOT NULL DEFAULT 0,
    watch_time_total_ms BIGINT NOT NULL DEFAULT 0,
    avg_watch_time_ms   DOUBLE PRECISION NOT NULL DEFAULT 0,
    avg_percent_viewed  DOUBLE PRECISION NOT NULL DEFAULT 0,
    completion_rate     DOUBLE PRECISION NOT NULL DEFAULT 0,
    likes               BIGINT NOT NULL DEFAULT 0,
    comments            BIGINT NOT NULL DEFAULT 0,
    shares              BIGINT NOT NULL DEFAULT 0,
    saves               BIGINT NOT NULL DEFAULT 0,
    view_score_total    DOUBLE PRECISION NOT NULL DEFAULT 0,
    content_quality_score DOUBLE PRECISION NOT NULL DEFAULT 0,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (content_id, day_bucket)
);

CREATE INDEX IF NOT EXISTS idx_daily_summary_creator
    ON analytics.content_daily_summary (creator_id, day_bucket DESC);
