# Module: feed-service

_Auto-extracted from source (DDL + route registrations). Verbatim — the source of truth is the code._

## HTTP routes
```
DELETE /hide/:postId
DELETE /mute
GET /debug
GET /delta
GET /flicks
GET /home
GET /internal/debug
GET /muted
GET /reels
GET /videos
GET /watch
POST /hide/:postId
POST /mute
POST /preference
POST /signal
GROUP /v1/feed
```

## Database schema (CREATE TABLE — full column DDL)
```sql
CREATE TABLE IF NOT EXISTS celeb_authors (
    author_id UUID PRIMARY KEY,
    is_celeb BOOLEAN NOT NULL DEFAULT FALSE,
    updated_at TIMESTAMPTZ NOT NULL
);

CREATE TABLE IF NOT EXISTS event_dedup (
    event_id UUID PRIMARY KEY,
    processed_at TIMESTAMPTZ NOT NULL
);

CREATE TABLE IF NOT EXISTS user_interactions (
    viewer_id       UUID NOT NULL,
    author_id       UUID NOT NULL,
    like_rate       FLOAT NOT NULL DEFAULT 0.0,
    comment_rate    FLOAT NOT NULL DEFAULT 0.0,
    share_rate      FLOAT NOT NULL DEFAULT 0.0,
    total_score     FLOAT NOT NULL DEFAULT 0.0,
    author_penalty  FLOAT NOT NULL DEFAULT 0.0,
    author_boost    FLOAT NOT NULL DEFAULT 0.0,
    interaction_count INTEGER NOT NULL DEFAULT 0,
    last_interaction TIMESTAMPTZ,
    computed_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (viewer_id, author_id)
);

CREATE TABLE IF NOT EXISTS viewer_media_prefs (
    user_id         UUID PRIMARY KEY,
    video_p95_dwell FLOAT DEFAULT 0,
    image_p95_dwell FLOAT DEFAULT 0,
    text_p95_dwell  FLOAT DEFAULT 0,
    preferred_type  TEXT DEFAULT 'text',
    computed_at     TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS post_impressions (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id         UUID NOT NULL,
    post_id         UUID NOT NULL,
    media_type      TEXT,
    dwell_seconds   FLOAT NOT NULL DEFAULT 0,
    action          TEXT,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS user_preferences (
    user_id    UUID PRIMARY KEY,
    feed_mode  TEXT NOT NULL DEFAULT 'chronological',
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS celeb_authors (
    author_id UUID PRIMARY KEY,
    is_celeb BOOLEAN NOT NULL DEFAULT FALSE,
    updated_at TIMESTAMPTZ NOT NULL
);

CREATE TABLE IF NOT EXISTS event_dedup (
    event_id UUID PRIMARY KEY,
    processed_at TIMESTAMPTZ NOT NULL
);

CREATE TABLE IF NOT EXISTS user_interactions (
    viewer_id       UUID NOT NULL,
    author_id       UUID NOT NULL,
    like_rate       FLOAT NOT NULL DEFAULT 0.0,
    comment_rate    FLOAT NOT NULL DEFAULT 0.0,
    share_rate      FLOAT NOT NULL DEFAULT 0.0,
    total_score     FLOAT NOT NULL DEFAULT 0.0,
    author_penalty  FLOAT NOT NULL DEFAULT 0.0,
    author_boost    FLOAT NOT NULL DEFAULT 0.0,
    interaction_count INTEGER NOT NULL DEFAULT 0,
    last_interaction TIMESTAMPTZ,
    computed_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (viewer_id, author_id)
);

CREATE TABLE IF NOT EXISTS viewer_media_prefs (
    user_id         UUID PRIMARY KEY,
    video_p95_dwell FLOAT DEFAULT 0,
    image_p95_dwell FLOAT DEFAULT 0,
    text_p95_dwell  FLOAT DEFAULT 0,
    preferred_type  TEXT DEFAULT 'text',
    computed_at     TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS post_impressions (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id         UUID NOT NULL,
    post_id         UUID NOT NULL,
    media_type      TEXT,
    dwell_seconds   FLOAT NOT NULL DEFAULT 0,
    action          TEXT,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS user_preferences (
    user_id    UUID PRIMARY KEY,
    feed_mode  TEXT NOT NULL DEFAULT 'chronological',
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS feed_hides (
    user_id   UUID NOT NULL,
    post_id   UUID NOT NULL,
    hidden_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (user_id, post_id)
);

CREATE TABLE IF NOT EXISTS feed_mutes (
    user_id     UUID NOT NULL,
    target_type TEXT NOT NULL CHECK (target_type IN ('user','topic','hashtag')),
    target_id   TEXT NOT NULL,
    muted_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    expires_at  TIMESTAMPTZ,
    PRIMARY KEY (user_id, target_type, target_id)
);

```

## API types (request/response Go structs with JSON tags)
```go
type DeltaResponse struct {
	NewCount     int    `json:"new_count"`
	NewestAnchor string `json:"newest_anchor,omitempty"`
	HasMore      bool   `json:"has_more"`
}

type preferenceRequest struct {
	FeedMode string `json:"feed_mode" binding:"required"`
}

type signalRequest struct {
	PostID string `json:"post_id" binding:"required"`
	Signal string `json:"signal" binding:"required"`
}
```
