# Module: post-service

_Auto-extracted from source (DDL + route registrations). Verbatim — the source of truth is the code._

## HTTP routes
```
DELETE /:commentId
DELETE /:crosspostId
DELETE /:draftId
DELETE /:playlistId
DELETE /:playlistId/items/:postId
DELETE /:postId
DELETE /:postId/bookmark
DELETE /:postId/reactions
DELETE /:postId/repost
DELETE /:postId/tune
DELETE /:reelId/react
DELETE /:reelId/save
DELETE /:savedId
DELETE /:seriesId/follow
DELETE /:storyId
DELETE /v1/posts/:postId/product-tags/:tagId
DELETE /v1/videos/:videoId/progress
GET /author/:authorId
GET /bookmarks
GET /by-author/:authorId
GET /by-author/:authorId/counts
GET /collections
GET /counts
GET /:draftId
GET /feed
GET /flicks
GET /liked
GET /moderation
GET /:playlistId
GET /:playlistId/items
GET /:postId
GET /:postId/comments
GET /:postId/comments/around/:commentId
GET /:postId/poll
GET /:postId/poll/results
GET /:postId/reactions/counts
GET /:postId/reactions/me
GET /:postId/remix-token
GET /:postId/reposters
GET /:postId/repost/me
GET /:postId/tune/me
GET /posts
GET /recent
GET /:reelId/comments
GET /:reelId/counts
GET /:reelId/react/me
GET /:reelId/saved
GET /saved
GET /search
GET /:seriesId
GET /:seriesId/episodes
GET /:storyId
GET /:trackId
GET /trending
GET /v1/creators/:creatorId/playlists
GET /v1/creators/:creatorId/product-tags
GET /v1/creators/:creatorId/series
GET /v1/creators/:creatorId/video-series
GET /v1/crossposts/mine
GET /v1/events/:eventId
GET /v1/events/:eventId/rsvps
GET /v1/hashtags/search
GET /v1/hashtags/:tag/posts
GET /v1/hashtags/:tag/stream
GET /v1/hashtags/trending
GET /v1/hashtags/trending/stream
GET /v1/posts/:postId/cards
GET /v1/posts/:postId/chapters
GET /v1/posts/:postId/end-screens
GET /v1/posts/:postId/product-tags
GET /v1/posts/trending
GET /v1/reels/feed
GET /v1/reels/hashtags/trending
GET /v1/reels/moderation/flagged
GET /v1/reels/:reelId/moderation
GET /v1/reels/slug/:slug
GET /v1/users/:userId/reposts
GET /v1/videos/continue-watching
GET /:videoId
GET /videos
PATCH /:commentId
PATCH /:commentId/moderation
PATCH /:draftId
PATCH /:reportId
PATCH /:videoId/category
PATCH /:videoId/trim
POST /batch
POST /batch/counts
POST /:commentId/dislike
POST /:commentId/like
POST /:commentId/reply
POST /:draftId/publish
POST /:playlistId/items
POST /:postId/bookmark
POST /:postId/comments
POST /:postId/like
POST /:postId/poll/vote
POST /:postId/react
POST /:postId/reactions
POST /:postId/repost
POST /:postId/resubmit
POST /:postId/share
POST /:postId/tune
POST /:postId/vote
POST /:reelId/comments
POST /:reelId/react
POST /:reelId/save
POST /:reelId/share
POST /:reelId/view
POST /:seriesId/episodes
POST /:seriesId/follow
POST /:storyId/view
POST /v1/events
POST /v1/events/:eventId/rsvp
POST /v1/feedback
POST /v1/posts/internal/review-status
POST /v1/posts/internal/visibility
POST /v1/posts/:postId/cards
POST /v1/posts/:postId/chapters
POST /v1/posts/:postId/end-screens
POST /v1/posts/:postId/product-tags
POST /v1/posts/:postId/product-tags/:tagId/click
POST /v1/posts/:postId/product-tags/:tagId/impression
POST /v1/reels/feed/refresh
POST /v1/reports
POST /v1/videos/:videoId/progress
POST /:videoId/cover-frame
POST /:videoId/publish
PUT /:postId/pin
PUT /v1/posts/:postId/membership
GROUP /v1/admin/comments
GROUP /v1/admin/reports
GROUP /v1/audio/tracks
GROUP /v1/comments
GROUP /v1/playlists
GROUP /v1/posts
GROUP /v1/posts/:postId/crossposts
GROUP /v1/reels
GROUP /v1/reels/drafts
GROUP /v1/saved
GROUP /v1/series
GROUP /v1/stories
GROUP /v1/uploads
GROUP /v1/videos
GROUP /v1/video-series
```

## Database schema (CREATE TABLE — full column DDL)
```sql
CREATE TABLE IF NOT EXISTS posts (
    id UUID PRIMARY KEY,
    author_id UUID NOT NULL,
    text TEXT NOT NULL,
    visibility TEXT NOT NULL,
    content_type TEXT NOT NULL DEFAULT 'post',
    is_pinned BOOLEAN DEFAULT FALSE,
    feeling TEXT,
    activity TEXT,
    activity_detail TEXT,
    rich_text JSONB,
    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL,
    deleted_at TIMESTAMPTZ
);

CREATE TABLE IF NOT EXISTS post_media (
    post_id UUID NOT NULL,
    media_id UUID NOT NULL,
    kind TEXT NOT NULL,
    PRIMARY KEY (post_id, media_id)
);

CREATE TABLE IF NOT EXISTS polls (
    post_id UUID PRIMARY KEY REFERENCES posts(id),
    question TEXT NOT NULL,
    allows_multiple BOOLEAN DEFAULT FALSE,
    ends_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS poll_options (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    post_id UUID NOT NULL REFERENCES polls(post_id),
    label TEXT NOT NULL,
    sort_order INT DEFAULT 0
);

CREATE TABLE IF NOT EXISTS poll_votes (
    post_id UUID NOT NULL,
    option_id UUID NOT NULL,
    user_id UUID NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (post_id, user_id, option_id)
);

CREATE TABLE IF NOT EXISTS comments (
    id             UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    post_id        UUID NOT NULL REFERENCES posts(id),
    author_id      UUID NOT NULL,
    parent_id      UUID REFERENCES comments(id),
    body           TEXT NOT NULL CHECK (char_length(body) BETWEEN 1 AND 2000),
    like_count     INTEGER NOT NULL DEFAULT 0,
    reply_count    INTEGER NOT NULL DEFAULT 0,
    is_reply       BOOLEAN NOT NULL DEFAULT FALSE,
    is_deleted     BOOLEAN NOT NULL DEFAULT FALSE,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS post_engagement_counts (
    post_id         UUID PRIMARY KEY REFERENCES posts(id),
    like_count      INTEGER NOT NULL DEFAULT 0,
    comment_count   INTEGER NOT NULL DEFAULT 0,
    share_count     INTEGER NOT NULL DEFAULT 0,
    bookmark_count  INTEGER NOT NULL DEFAULT 0,
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS engagement_event_log (
    event_id      TEXT PRIMARY KEY,
    event_type    TEXT NOT NULL,
    target_id     UUID NOT NULL,
    processed_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- references 28+ columns that weren't in the original CREATE TABLE. Each
-- ADD COLUMN IF NOT EXISTS is idempotent so re-runs on existing DBs are
-- safe. Types match the Post struct in posts.go.
ALTER TABLE posts
    ADD COLUMN IF NOT EXISTS no_comments         BOOLEAN NOT NULL DEFAULT FALSE,
    ADD COLUMN IF NOT EXISTS no_likes            BOOLEAN NOT NULL DEFAULT FALSE,
    ADD COLUMN IF NOT EXISTS hashtags            TEXT[]  NOT NULL DEFAULT '{}',
    ADD COLUMN IF NOT EXISTS mentions            UUID[]  NOT NULL DEFAULT '{}',
    ADD COLUMN IF NOT EXISTS location_name       TEXT,
    ADD COLUMN IF NOT EXISTS location_lat        DOUBLE PRECISION,
    ADD COLUMN IF NOT EXISTS location_lng        DOUBLE PRECISION,
    ADD COLUMN IF NOT EXISTS post_type           TEXT    NOT NULL DEFAULT 'post',
    ADD COLUMN IF NOT EXISTS app_origin          TEXT    NOT NULL DEFAULT 'postbook',
    ADD COLUMN IF NOT EXISTS share_to_postbook   BOOLEAN NOT NULL DEFAULT TRUE,
    ADD COLUMN IF NOT EXISTS title               TEXT    NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS tags                TEXT[]  NOT NULL DEFAULT '{}',
    ADD COLUMN IF NOT EXISTS category            TEXT    NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS language            TEXT    NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS seo_title           TEXT    NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS paid_promotion      BOOLEAN NOT NULL DEFAULT FALSE,
    ADD COLUMN IF NOT EXISTS altered_content     BOOLEAN NOT NULL DEFAULT FALSE,
    ADD COLUMN IF NOT EXISTS is_made_for_kids    BOOLEAN NOT NULL DEFAULT FALSE,
    ADD COLUMN IF NOT EXISTS license             TEXT    NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS allow_embedding     BOOLEAN NOT NULL DEFAULT TRUE,
    ADD COLUMN IF NOT EXISTS publish_to_feed     BOOLEAN NOT NULL DEFAULT TRUE,
    ADD COLUMN IF NOT EXISTS remix_setting       TEXT    NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS comment_moderation  TEXT    NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS comment_access      TEXT    NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS recording_date      TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS recording_location  TEXT    NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS cover_media_id      UUID,
    ADD COLUMN IF NOT EXISTS original_audio_volume REAL  NOT NULL DEFAULT 1.0,
    ADD COLUMN IF NOT EXISTS overlay_audio_volume  REAL  NOT NULL DEFAULT 0.0,
    ADD COLUMN IF NOT EXISTS tier_required_id    UUID;
CREATE INDEX IF NOT EXISTS idx_posts_review_status
    ON posts(review_status)
    WHERE review_status != 'approved';
-- migration 018: allow the 'pending' review state for the video publish gate.
-- migration 024: allow 'needs_changes' (super-admin requested edits; creator loop).
ALTER TABLE posts DROP CONSTRAINT IF EXISTS posts_review_status_check;
ALTER TABLE posts ADD CONSTRAINT posts_review_status_check
    CHECK (review_status IN ('approved', 'flagged', 'rejected', 'pending', 'needs_changes'));

CREATE TABLE IF NOT EXISTS post_reposts (
    id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id             UUID NOT NULL,
    original_post_id    UUID NOT NULL,
    repost_type         TEXT NOT NULL CHECK (repost_type IN ('plain', 'quote')),
    quote_text          TEXT,
    visibility          TEXT NOT NULL DEFAULT 'public',
    status              TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'undone')),
    source_context_type TEXT CHECK (source_context_type IS NULL OR source_context_type IN ('feed', 'post_detail', 'profile', 'search', 'stash')),
    source_context_id   UUID,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    deleted_at          TIMESTAMPTZ
);

CREATE TABLE IF NOT EXISTS stories (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    author_id       UUID NOT NULL,
    media_url       TEXT NOT NULL,
    media_type      TEXT NOT NULL,
    caption         TEXT NOT NULL DEFAULT '',
    stickers        JSONB,
    music_track     JSONB,
    visibility      TEXT NOT NULL DEFAULT 'public',
    view_count      INTEGER NOT NULL DEFAULT 0,
    expires_at      TIMESTAMPTZ NOT NULL,
    is_highlight    BOOLEAN NOT NULL DEFAULT FALSE,
    highlight_group TEXT,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS reactions (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    target_type   TEXT NOT NULL,
    target_id     UUID NOT NULL,
    user_id       UUID NOT NULL,
    reaction_type TEXT NOT NULL,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE(target_type, target_id, user_id)
);

CREATE TABLE IF NOT EXISTS saved_items (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id         UUID NOT NULL,
    target_type     TEXT NOT NULL,
    target_id       UUID NOT NULL,
    collection_name TEXT NOT NULL DEFAULT 'default',
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE(user_id, target_type, target_id)
);

CREATE TABLE IF NOT EXISTS reel_drafts (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    author_id       UUID NOT NULL,
    media_id        UUID,

    -- Content
    title           TEXT NOT NULL DEFAULT '',
    caption         TEXT NOT NULL DEFAULT '',
    hashtags        TEXT[] DEFAULT '{}',
    tags            TEXT[] DEFAULT '{}',

    -- Distribution
    visibility      TEXT NOT NULL DEFAULT 'public'
                    CHECK (visibility IN ('public', 'followers', 'private', 'unlisted')),
    topic_id        INT,
    category        TEXT DEFAULT '',
    language        TEXT DEFAULT 'en',
    seo_title       TEXT DEFAULT '',

    -- Cross-post
    cross_post_postbook BOOLEAN DEFAULT TRUE,
    cross_post_posttube BOOLEAN DEFAULT FALSE,
    publish_to_feed     BOOLEAN DEFAULT TRUE,

    -- Compliance / Disclosure
    is_made_for_kids    BOOLEAN DEFAULT FALSE,
    paid_promotion      BOOLEAN DEFAULT FALSE,
    altered_content     BOOLEAN DEFAULT FALSE,

    -- Smart features
    auto_chapters       BOOLEAN DEFAULT TRUE,
    featured_places     BOOLEAN DEFAULT TRUE,
    auto_concepts       BOOLEAN DEFAULT TRUE,

    -- Rights / Permissions
    license             TEXT DEFAULT 'standard' CHECK (license IN ('standard', 'creative_commons')),
    allow_embedding     BOOLEAN DEFAULT TRUE,
    remix_setting       TEXT DEFAULT 'allow' CHECK (remix_setting IN ('allow', 'allow_audio_only', 'disallow')),

    -- Comments & Ratings
    likes_enabled       BOOLEAN DEFAULT TRUE,
    comments_enabled    BOOLEAN DEFAULT TRUE,
    comment_moderation  TEXT DEFAULT 'basic' CHECK (comment_moderation IN ('none', 'basic', 'strict', 'hold_all')),
    comment_access      TEXT DEFAULT 'everyone' CHECK (comment_access IN ('everyone', 'followers', 'nobody')),

    -- Recording metadata
    recording_date      DATE,
    recording_location  TEXT DEFAULT '',

    -- Audio
    audio_track_id      TEXT,
    audio_start_ms      INT DEFAULT 0,
    original_audio_volume REAL DEFAULT 1.0,
    overlay_audio_volume  REAL DEFAULT 1.0,

    -- Cover
    cover_media_id      UUID,

    -- Scheduling & Status
    schedule_at         TIMESTAMPTZ,
    status              TEXT NOT NULL DEFAULT 'draft'
                        CHECK (status IN ('draft', 'processing', 'publishing_pending', 'published', 'rejected', 'deleted')),
    moderation_status   TEXT DEFAULT 'pending'
                        CHECK (moderation_status IN ('pending', 'approved', 'flagged', 'rejected')),
    published_post_id   UUID,  -- links to posts.id after publish

    created_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS reel_hashtags (
    reel_id     UUID NOT NULL,
    hashtag     TEXT NOT NULL,
    position    INT NOT NULL DEFAULT 0,  -- position in caption for highlighting
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (reel_id, hashtag)
);

CREATE TABLE IF NOT EXISTS reel_crosspost (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    source_reel_id  UUID NOT NULL,
    target_type     TEXT NOT NULL,           -- 'feed', 'story', 'group', 'page'
    target_id       TEXT,                    -- group_id/page_id if applicable
    status          TEXT NOT NULL DEFAULT 'pending',  -- pending, published, failed
    idempotency_key TEXT,
    error_message   TEXT,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    published_at    TIMESTAMPTZ,
    UNIQUE (source_reel_id, target_type, target_id)
);

CREATE TABLE IF NOT EXISTS slug_history (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    reel_id     UUID NOT NULL,
    old_slug    TEXT NOT NULL,
    new_slug    TEXT NOT NULL,
    changed_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS moderation_reviews (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    reel_id         UUID NOT NULL,
    reviewer_type   TEXT NOT NULL,          -- 'auto', 'human'
    reviewer_id     TEXT,                   -- NULL for auto, moderator user_id for human
    decision        TEXT NOT NULL,          -- 'approved', 'rejected', 'flagged', 'pending_review'
    reason          TEXT,
    confidence      FLOAT,                 -- auto-scan confidence score (0.0-1.0)
    policy_violated TEXT,                   -- which policy was violated
    metadata        JSONB,                 -- additional scan results
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS post_outbox_events (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    event_type      TEXT NOT NULL,
    aggregate_type  TEXT NOT NULL,          -- 'reel', 'post', 'draft'
    aggregate_id    UUID NOT NULL,
    payload         JSONB NOT NULL,
    published       BOOLEAN NOT NULL DEFAULT FALSE,
    published_at    TIMESTAMPTZ,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS idempotency_keys (
    key             TEXT PRIMARY KEY,
    result_status   INT NOT NULL,           -- HTTP status code of the original response
    result_body     JSONB,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    expires_at      TIMESTAMPTZ NOT NULL DEFAULT (NOW() + INTERVAL '24 hours')
);

CREATE TABLE IF NOT EXISTS topics (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name        TEXT NOT NULL UNIQUE,
    slug        TEXT NOT NULL UNIQUE,
    parent_id   UUID REFERENCES topics(id),
    is_active   BOOLEAN NOT NULL DEFAULT TRUE,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS video_metadata (
    post_id             UUID PRIMARY KEY REFERENCES posts(id) ON DELETE CASCADE,
    duration_seconds    REAL NOT NULL DEFAULT 0,
    width               INT,
    height              INT,
    aspect_ratio        TEXT,
    orientation         TEXT NOT NULL DEFAULT 'landscape'
                        CHECK (orientation IN ('portrait', 'landscape', 'square')),
    file_size_bytes     BIGINT,
    mime_type           TEXT,
    codec_video         TEXT,
    codec_audio         TEXT,
    frame_rate          REAL,
    storage_video_url   TEXT,
    playback_url        TEXT,
    thumbnail_url       TEXT,
    trim_start_ms       INT DEFAULT 0,
    trim_end_ms         INT,
    computed_category   TEXT NOT NULL DEFAULT 'flick'
                        CHECK (computed_category IN ('flick', 'long_video')),
    final_category      TEXT NOT NULL DEFAULT 'flick'
                        CHECK (final_category IN ('flick', 'long_video')),
    upload_status       TEXT NOT NULL DEFAULT 'pending'
                        CHECK (upload_status IN ('pending', 'processing', 'ready', 'failed')),
    media_asset_id      UUID,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS crosspost_links (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    source_module   TEXT NOT NULL CHECK (source_module IN ('posttube', 'postgram')),
    source_post_id  UUID NOT NULL REFERENCES posts(id) ON DELETE CASCADE,
    target_module   TEXT NOT NULL CHECK (target_module IN ('postbook')),
    target_post_id  UUID NOT NULL REFERENCES posts(id) ON DELETE CASCADE,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    deleted_at      TIMESTAMPTZ
);

CREATE TABLE IF NOT EXISTS tunes (
    id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id    UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    post_id    UUID NOT NULL REFERENCES posts(id) ON DELETE CASCADE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (user_id, post_id)
);

CREATE TABLE IF NOT EXISTS link_previews (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    url         TEXT NOT NULL UNIQUE,
    title       TEXT,
    description TEXT,
    image_url   TEXT,
    domain      TEXT,
    fetched_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    expires_at  TIMESTAMPTZ NOT NULL DEFAULT NOW() + INTERVAL '24 hours'
);

CREATE TABLE IF NOT EXISTS article_tags (
    post_id UUID NOT NULL REFERENCES posts(id) ON DELETE CASCADE,
    tag     TEXT NOT NULL,
    PRIMARY KEY (post_id, tag)
);

CREATE TABLE IF NOT EXISTS story_interactive (
    id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    story_id   UUID NOT NULL REFERENCES stories(id) ON DELETE CASCADE,
    type       TEXT NOT NULL CHECK (type IN ('poll','quiz','countdown','question','slider')),
    question   TEXT NOT NULL,
    options    JSONB,
    correct_idx INT,
    end_time   TIMESTAMPTZ,
    position   JSONB,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS story_interactive_responses (
    id             UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    interactive_id UUID NOT NULL REFERENCES story_interactive(id) ON DELETE CASCADE,
    user_id        UUID NOT NULL,
    response       JSONB NOT NULL,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (interactive_id, user_id)
);

CREATE TABLE IF NOT EXISTS events (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    post_id         UUID REFERENCES posts(id) ON DELETE CASCADE,
    creator_id      UUID NOT NULL REFERENCES users(id),
    title           TEXT NOT NULL,
    description     TEXT NOT NULL DEFAULT '',
    starts_at       TIMESTAMPTZ NOT NULL,
    ends_at         TIMESTAMPTZ,
    location_name   TEXT,
    location_lat    DOUBLE PRECISION,
    location_lng    DOUBLE PRECISION,
    cover_media_id  UUID,
    is_ticketed     BOOLEAN NOT NULL DEFAULT FALSE,
    ticket_price    NUMERIC(10,2),
    max_attendees   INT,
    chat_conv_id    UUID,
    rsvp_count      INT NOT NULL DEFAULT 0,
    status          TEXT NOT NULL DEFAULT 'upcoming' CHECK (status IN ('upcoming','ongoing','ended','cancelled')),
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS event_rsvps (
    event_id   UUID NOT NULL REFERENCES events(id) ON DELETE CASCADE,
    user_id    UUID NOT NULL REFERENCES users(id),
    status     TEXT NOT NULL DEFAULT 'going' CHECK (status IN ('going','maybe','not_going')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (event_id, user_id)
);

CREATE TABLE IF NOT EXISTS media_rights_checks (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    post_id       UUID NOT NULL REFERENCES posts(id) ON DELETE CASCADE,
    audio_id      UUID,
    check_type    TEXT NOT NULL CHECK (check_type IN ('audio_rights','content_id','copyright')),
    status        TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending','cleared','flagged','manual_review')),
    provider      TEXT,
    provider_ref  TEXT,
    result_detail JSONB,
    checked_at    TIMESTAMPTZ,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS flick_series (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    creator_id    UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    title         TEXT NOT NULL,
    description   TEXT NOT NULL DEFAULT '',
    cover_url     TEXT,
    episode_count INT NOT NULL DEFAULT 0,
    is_complete   BOOLEAN NOT NULL DEFAULT FALSE,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS flick_series_items (
    series_id   UUID NOT NULL REFERENCES flick_series(id) ON DELETE CASCADE,
    post_id     UUID NOT NULL REFERENCES posts(id) ON DELETE CASCADE,
    episode_num INT NOT NULL,
    added_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (series_id, episode_num)
);

CREATE TABLE IF NOT EXISTS flick_series_followers (
    series_id   UUID NOT NULL REFERENCES flick_series(id) ON DELETE CASCADE,
    user_id     UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    followed_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (series_id, user_id)
);

CREATE TABLE IF NOT EXISTS video_series (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    creator_id      UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    channel_id      UUID REFERENCES channels(id),
    title           TEXT NOT NULL,
    description     TEXT NOT NULL DEFAULT '',
    cover_media_id  UUID REFERENCES media_assets(id),
    trailer_post_id UUID REFERENCES posts(id),
    episode_count   INT NOT NULL DEFAULT 0,
    is_complete     BOOLEAN NOT NULL DEFAULT FALSE,
    is_public       BOOLEAN NOT NULL DEFAULT TRUE,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS video_series_episodes (
    series_id   UUID NOT NULL REFERENCES video_series(id) ON DELETE CASCADE,
    post_id     UUID NOT NULL REFERENCES posts(id) ON DELETE CASCADE,
    episode_num INT NOT NULL,
    title       TEXT,
    added_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (series_id, episode_num)
);

CREATE TABLE IF NOT EXISTS playlists (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    creator_id  UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    channel_id  UUID REFERENCES channels(id),
    title       TEXT NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    cover_url   TEXT,
    visibility  TEXT NOT NULL DEFAULT 'public' CHECK (visibility IN ('public','unlisted','private')),
    item_count  INT NOT NULL DEFAULT 0,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS playlist_items (
    playlist_id UUID NOT NULL REFERENCES playlists(id) ON DELETE CASCADE,
    post_id     UUID NOT NULL REFERENCES posts(id) ON DELETE CASCADE,
    position    INT NOT NULL,
    added_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (playlist_id, position)
);

CREATE TABLE IF NOT EXISTS media_chapters (
    post_id       UUID NOT NULL REFERENCES posts(id) ON DELETE CASCADE,
    chapter_index INT NOT NULL,
    title         TEXT NOT NULL,
    start_ms      INT NOT NULL,
    thumbnail_url TEXT,
    source        TEXT NOT NULL DEFAULT 'manual' CHECK (source IN ('manual','ai_generated')),
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (post_id, chapter_index)
);

CREATE TABLE IF NOT EXISTS video_end_screens (
    id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    post_id    UUID NOT NULL REFERENCES posts(id) ON DELETE CASCADE,
    type       TEXT NOT NULL CHECK (type IN ('video','playlist','channel_subscribe','external_link')),
    target_id  UUID,
    target_url TEXT,
    title      TEXT,
    position   JSONB NOT NULL,
    start_ms   INT NOT NULL,
    end_ms     INT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS video_cards (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    post_id      UUID NOT NULL REFERENCES posts(id) ON DELETE CASCADE,
    type         TEXT NOT NULL CHECK (type IN ('video','playlist','poll','external_link')),
    target_id    UUID,
    target_url   TEXT,
    title        TEXT NOT NULL,
    teaser_text  TEXT,
    appear_at_ms INT NOT NULL,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS watch_progress (
    user_id         UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    post_id         UUID NOT NULL REFERENCES posts(id) ON DELETE CASCADE,
    position_ms     INT NOT NULL DEFAULT 0,
    duration_ms     INT NOT NULL,
    percent_watched REAL NOT NULL DEFAULT 0,
    completed       BOOLEAN NOT NULL DEFAULT FALSE,
    last_watched_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (user_id, post_id)
);

-- wins the CREATE TABLE race. This migration is therefore written defensively:
-- it adds the columns post-service queries by, on top of whatever shape exists.
-- The full reconciliation (single canonical audio_tracks owner) is tracked
-- separately as tech debt; this just keeps both services bootable on a shared DB.

CREATE TABLE IF NOT EXISTS audio_tracks (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    title TEXT NOT NULL,
    artist TEXT NOT NULL DEFAULT '',
    duration_ms INT NOT NULL DEFAULT 0,
    media_id UUID NOT NULL,
    original_post_id UUID,
    genre TEXT NOT NULL DEFAULT '',
    is_original BOOLEAN NOT NULL DEFAULT true,
    use_count INT NOT NULL DEFAULT 0,
    is_trending BOOLEAN NOT NULL DEFAULT false,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS post_mentions (
    id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    post_id             UUID NOT NULL,
    post_type           VARCHAR(20) NOT NULL DEFAULT 'post',
    mentioned_user_id   TEXT NOT NULL,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE(post_id, mentioned_user_id)
);

CREATE TABLE IF NOT EXISTS post_reposts (
    id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id             UUID NOT NULL,
    original_post_id    UUID NOT NULL,
    repost_type         TEXT NOT NULL CHECK (repost_type IN ('plain', 'quote')),
    quote_text          TEXT,
    visibility          TEXT NOT NULL DEFAULT 'public',
    status              TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'undone')),
    source_context_type TEXT CHECK (source_context_type IS NULL OR source_context_type IN ('feed', 'post_detail', 'profile', 'search', 'stash')),
    source_context_id   UUID,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    deleted_at          TIMESTAMPTZ
);

CREATE TABLE IF NOT EXISTS post_product_tags (
    id                UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    post_id           UUID NOT NULL,
    -- Cross-service reference into monetization.affiliate_links.id.
    affiliate_link_id UUID NOT NULL,
    -- The creator who owns this tag. Denormalised so the per-tag delete
    -- permission check doesn't have to join back to posts.
    creator_id        UUID NOT NULL,

    -- Display window inside the video. NULL start AND NULL end means
    -- "show across the whole video" — for image posts the time fields
    -- are ignored.
    time_start_ms     INT,
    time_end_ms       INT,

    -- Overlay anchor as percentages of the player viewport (0..100).
    -- 50/50 = dead centre. The mobile/web player decides exact pixel
    -- offset + collision avoidance; the server just stores intent.
    position_x        REAL,
    position_y        REAL,

    -- Display payload. Cached at tag-creation time so the player
    -- doesn't have to join through to commerce on every view. Refreshed
    -- when the underlying listing changes (best-effort, via the
    -- product-update event consumer — follow-up).
    label             TEXT NOT NULL DEFAULT '',
    image_url         TEXT NOT NULL DEFAULT '',

    -- Counters. Player increments via the impression/click endpoints;
    -- these are eventually-consistent (Redis flush worker, same shape as
    -- the engagement counter story).
    impression_count  BIGINT NOT NULL DEFAULT 0,
    click_count       BIGINT NOT NULL DEFAULT 0,

    is_active         BOOLEAN NOT NULL DEFAULT TRUE,
    created_at        TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at        TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    -- CHECK: position percentages, when set, must be 0..100.
    CONSTRAINT post_product_tags_pos_x_check
        CHECK (position_x IS NULL OR (position_x >= 0 AND position_x <= 100)),
    CONSTRAINT post_product_tags_pos_y_check
        CHECK (position_y IS NULL OR (position_y >= 0 AND position_y <= 100)),
    -- CHECK: time window, when both set, must be ordered.
    CONSTRAINT post_product_tags_time_window_check
        CHECK (time_start_ms IS NULL OR time_end_ms IS NULL OR time_end_ms >= time_start_ms)
);

CREATE TABLE IF NOT EXISTS app_feedback (
    id             UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id        UUID NOT NULL,
    feedback_type  TEXT NOT NULL DEFAULT 'other'
        CHECK (feedback_type IN ('bug','feature','performance','content','ui','other')),
    post_id        UUID,
    message        TEXT NOT NULL CHECK (char_length(message) BETWEEN 1 AND 5000),
    context        TEXT,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now()
);

```

## API types (request/response Go structs with JSON tags)
```go
type CreateAudioTrackRequest struct {
	Title          string     `json:"title" binding:"required"`
	Artist         string     `json:"artist"`
	DurationMs     int        `json:"duration_ms"`
	MediaID        string     `json:"media_id" binding:"required"`
	OriginalPostID *string    `json:"original_post_id"`
	Genre          string     `json:"genre"`
	IsOriginal     *bool      `json:"is_original"`
}

type createDraftRequest struct {
	MediaID    *string `json:"media_id"`
	Visibility string  `json:"visibility"`
	Caption    string  `json:"caption"`
}

type publishDraftRequest struct {
	ScheduleAt *string `json:"schedule_at"`
}

type submitFeedbackRequest struct {
	FeedbackType string  `json:"feedback_type"`
	PostID       *string `json:"post_id"`
	Message      string  `json:"message" binding:"required"`
	Context      string  `json:"context"`
}

type setReviewStatusRequest struct {
	PostID string `json:"post_id" binding:"required"`
	Status string `json:"status" binding:"required"` // approved | rejected
}

type setVisibilityRequest struct {
	PostID     string `json:"post_id" binding:"required"`
	Visibility string `json:"visibility" binding:"required"` // typically "public"
}

type CreatePollRequest struct {
	Question       string   `json:"question" binding:"required"`
	Options        []string `json:"options" binding:"required,min=2,max=6"`
	AllowsMultiple bool     `json:"allows_multiple"`
	DurationHours  *int     `json:"duration_hours"`
}

type CreatePostRequest struct {
	Text             string `json:"text"`
	Visibility       string `json:"visibility" binding:"required,oneof=public followers private unlisted"`
	VisibilityPolicy *struct {
		Mode       string   `json:"mode"`
		AllowLists []string `json:"allow_lists,omitempty"`
		AllowUsers []string `json:"allow_users,omitempty"`
		DenyUsers  []string `json:"deny_users,omitempty"`
	} `json:"visibility_policy,omitempty"`
	ContentType     string             `json:"content_type"`
	MediaIDs        []string           `json:"media_ids"`
	Feeling         *string            `json:"feeling"`
	Activity        *string            `json:"activity"`
	ActivityDetail  *string            `json:"activity_detail"`
	RichText        json.RawMessage    `json:"rich_text"`
	Poll            *CreatePollRequest `json:"poll"`
	NoComments      bool               `json:"no_comments"`
	NoLikes         bool               `json:"no_likes"`
	LocationName    *string            `json:"location_name"`
	LocationLat     *float64           `json:"location_lat"`
	LocationLng     *float64           `json:"location_lng"`
	PostType        string             `json:"post_type"`
	AppOrigin       string             `json:"app_origin"`
	ShareToPostbook bool               `json:"share_to_postbook"`
	// Reel metadata
	Title             string   `json:"title"`
	// M5: cap tags array to bound payload memory. 20 tags × 50 chars
	// is the upper bound for legitimate use cases (most reels carry
	// 3-5 tags); larger arrays are either spam or accidents.
	Tags              []string `json:"tags" binding:"max=20,dive,max=50"`
	Category          string   `json:"category"`
	Language          string   `json:"language"`
	SEOTitle          string   `json:"seo_title"`
	PaidPromotion     bool     `json:"paid_promotion"`
	AlteredContent    bool     `json:"altered_content"`
	IsMadeForKids     bool     `json:"is_made_for_kids"`
	License           string   `json:"license"`
	AllowEmbedding    *bool    `json:"allow_embedding"`
	PublishToFeed     *bool    `json:"publish_to_feed"`
	RemixSetting      string   `json:"remix_setting"`
	CommentModeration string   `json:"comment_moderation"`
	CommentAccess     string   `json:"comment_access"`
	RecordingDate     *string  `json:"recording_date"`
	RecordingLocation string   `json:"recording_location"`
	CoverMediaID      *string  `json:"cover_media_id"`
	OriginalAudioVol  float32  `json:"original_audio_volume"`
	OverlayAudioVol   float32  `json:"overlay_audio_volume"`
	// AudioTrackID attaches a track from /v1/audio/tracks to the post on
	// create. Used by the Flicks composer's audio browser. Optional —
	// posts without background audio leave this empty.
	AudioTrackID *string `json:"audio_track_id"`
}

type PinRequest struct {
	Pinned bool `json:"pinned"`
}

type ReactionRequest struct {
	Reaction string `json:"reaction" binding:"required"`
}

type CommentRequest struct {
	Text string `json:"text" binding:"required"`
}

type VoteRequest struct {
	OptionID string `json:"option_id" binding:"required"`
}

type ShareRequest struct {
	ShareType string `json:"share_type" binding:"required,oneof=repost quote external"`
	QuoteText string `json:"quote_text"`
}

type EditCommentRequest struct {
	Body string `json:"body" binding:"required"`
}

type BatchGetPostsRequest struct {
	IDs []string `json:"ids" binding:"required"`
}

type CreateStoryRequest struct {
	MediaURL       string  `json:"media_url" binding:"required"`
	MediaType      string  `json:"media_type" binding:"required,oneof=image video"`
	Caption        string  `json:"caption"`
	Visibility     string  `json:"visibility" binding:"required,oneof=public followers close_friends"`
	IsHighlight    bool    `json:"is_highlight"`
	HighlightGroup *string `json:"highlight_group"`
}

type ToggleReactionRequest struct {
	ReactionType string `json:"reaction_type" binding:"required"`
}

type SaveItemRequest struct {
	TargetType     string `json:"target_type" binding:"required,oneof=post video reel"`
	TargetID       string `json:"target_id" binding:"required"`
	CollectionName string `json:"collection_name"`
}

type CreateRepostRequest struct {
	Type              string  `json:"type" binding:"required,oneof=plain quote"`
	QuoteText         string  `json:"quote_text"`
	SourceContextType string  `json:"source_context_type"`
	SourceContextID   *string `json:"source_context_id"`
}

type createProductTagRequest struct {
	AffiliateLinkID uuid.UUID `json:"affiliate_link_id" binding:"required"`
	TimeStartMS     *int32    `json:"time_start_ms,omitempty"`
	TimeEndMS       *int32    `json:"time_end_ms,omitempty"`
	PositionX       *float32  `json:"position_x,omitempty"`
	PositionY       *float32  `json:"position_y,omitempty"`
	Label           string    `json:"label"`
	ImageURL        string    `json:"image_url"`
}

type reactToReelRequest struct {
	Reaction string `json:"reaction" binding:"required"`
}

type addReelCommentRequest struct {
	Text string `json:"text" binding:"required"`
}

type shareReelRequest struct {
	ShareType string `json:"share_type"`
}

type recordReelViewRequest struct {
	SessionID string `json:"session_id"`
	WatchedMs int64  `json:"watched_ms"`
	Surface   string `json:"surface"`
}

type batchReelCountsRequest struct {
	ReelIDs []string `json:"reel_ids" binding:"required"`
}

type createVideoSeriesRequest struct {
	Title         string  `json:"title" binding:"required"`
	Description   string  `json:"description"`
	ChannelID     *string `json:"channel_id"`
	CoverMediaID  *string `json:"cover_media_id"`
	TrailerPostID *string `json:"trailer_post_id"`
	IsComplete    bool    `json:"is_complete"`
	IsPublic      *bool   `json:"is_public"`
}

type addVideoSeriesEpisodeRequest struct {
	PostID     string  `json:"post_id" binding:"required"`
	EpisodeNum int     `json:"episode_num" binding:"required"`
	Title      *string `json:"title"`
}

type createPlaylistRequest struct {
	Title       string  `json:"title" binding:"required"`
	Description string  `json:"description"`
	ChannelID   *string `json:"channel_id"`
	CoverURL    *string `json:"cover_url"`
	Visibility  string  `json:"visibility"`
}

type addPlaylistItemRequest struct {
	PostID   string `json:"post_id" binding:"required"`
	Position int    `json:"position"`
}

type chapterInput struct {
	ChapterIndex int     `json:"chapter_index"`
	Title        string  `json:"title" binding:"required"`
	StartMs      int     `json:"start_ms"`
	ThumbnailURL *string `json:"thumbnail_url"`
	Source       string  `json:"source"`
}

type saveChaptersRequest struct {
	Chapters []chapterInput `json:"chapters" binding:"required"`
}

type endScreenInput struct {
	Type      string          `json:"type" binding:"required"`
	TargetID  *string         `json:"target_id"`
	TargetURL *string         `json:"target_url"`
	Title     *string         `json:"title"`
	Position  json.RawMessage `json:"position" binding:"required"`
	StartMs   int             `json:"start_ms"`
	EndMs     int             `json:"end_ms"`
}

type saveEndScreensRequest struct {
	Screens []endScreenInput `json:"screens" binding:"required"`
}

type videoCardInput struct {
	Type       string  `json:"type" binding:"required"`
	TargetID   *string `json:"target_id"`
	TargetURL  *string `json:"target_url"`
	Title      string  `json:"title" binding:"required"`
	TeaserText *string `json:"teaser_text"`
	AppearAtMs int     `json:"appear_at_ms"`
}

type saveVideoCardsRequest struct {
	Cards []videoCardInput `json:"cards" binding:"required"`
}

type saveWatchProgressRequest struct {
	PositionMs int `json:"position_ms"`
	DurationMs int `json:"duration_ms" binding:"required"`
}
```
