# Module: analytics-service

_Auto-extracted from source (DDL + route registrations). Verbatim — the source of truth is the code._

## HTTP routes
```
GET /content
GET /content/:contentId
GET /content/:contentId/demographics
GET /content/:contentId/retention
GET /content/:contentId/trend
GET /content/:contentId/views
GET /creator/me
GET /creator/:userId
GET /overview
GET /trend
POST /events
GROUP /dashboard
GROUP /v1/analytics
```

## Database schema (CREATE TABLE — full column DDL)
```sql
CREATE TABLE IF NOT EXISTS analytics.events_raw (
    id UUID,
    user_id UUID,
    session_id UUID,
    type TEXT NOT NULL,
    payload JSONB,
    ts TIMESTAMPTZ NOT NULL,
    received_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
) PARTITION BY RANGE (ts);

CREATE TABLE IF NOT EXISTS analytics.events_raw_2024_01 PARTITION OF analytics.events_raw
    FOR VALUES FROM ('2024-01-01') TO ('2024-02-01');

CREATE TABLE IF NOT EXISTS analytics.events_raw_default PARTITION OF analytics.events_raw DEFAULT;

CREATE INDEX IF NOT EXISTS idx_events_type_ts ON analytics.events_raw (type, ts DESC);

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

CREATE TABLE IF NOT EXISTS user_streaks (
    user_id       UUID PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    type          TEXT NOT NULL CHECK (type IN ('daily_post','daily_login','creator_upload')),
    current_count INT NOT NULL DEFAULT 0,
    longest_count INT NOT NULL DEFAULT 0,
    last_action_at DATE NOT NULL DEFAULT CURRENT_DATE,
    started_at    DATE NOT NULL DEFAULT CURRENT_DATE
);

CREATE TABLE IF NOT EXISTS user_badges (
    id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id    UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    type       TEXT NOT NULL CHECK (type IN ('verified','creator','top_contributor','helpful_member','streak_30','streak_100','early_adopter','expert')),
    awarded_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    expires_at TIMESTAMPTZ,
    is_visible BOOLEAN NOT NULL DEFAULT TRUE,
    UNIQUE (user_id, type)
);

CREATE TABLE IF NOT EXISTS missions (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    title       TEXT NOT NULL,
    description TEXT NOT NULL,
    type        TEXT NOT NULL CHECK (type IN ('post_count','spark_count','follower_gain','community_join')),
    target      INT NOT NULL,
    reward_type TEXT NOT NULL CHECK (reward_type IN ('badge','points','feature_unlock')),
    reward_data JSONB,
    starts_at   TIMESTAMPTZ NOT NULL,
    ends_at     TIMESTAMPTZ NOT NULL,
    is_active   BOOLEAN NOT NULL DEFAULT TRUE
);

CREATE TABLE IF NOT EXISTS mission_progress (
    mission_id   UUID NOT NULL REFERENCES missions(id) ON DELETE CASCADE,
    user_id      UUID NOT NULL,
    progress     INT NOT NULL DEFAULT 0,
    completed    BOOLEAN NOT NULL DEFAULT FALSE,
    completed_at TIMESTAMPTZ,
    PRIMARY KEY (mission_id, user_id)
);

CREATE TABLE IF NOT EXISTS loyalty_points (
    user_id         UUID PRIMARY KEY REFERENCES users(id),
    balance         BIGINT NOT NULL DEFAULT 0,
    lifetime_earned BIGINT NOT NULL DEFAULT 0,
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS point_transactions (
    id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id    UUID NOT NULL REFERENCES users(id),
    amount     INT NOT NULL,
    type       TEXT NOT NULL CHECK (type IN ('post_reward','streak_bonus','mission_reward','commerce_spend','referral')),
    ref_id     UUID,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

```

## API types (request/response Go structs with JSON tags)
```go
type IngestRequest struct {
	Events []service.EventDTO `json:"events" binding:"required"`
}
```
