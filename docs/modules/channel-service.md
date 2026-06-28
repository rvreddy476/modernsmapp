# Module: channel-service

_Auto-extracted from source (DDL + route registrations). Verbatim — the source of truth is the code._

## HTTP routes
```
DELETE /:channelId
DELETE /:channelId/subscribe
DELETE /:channelId/updates/:updateId
DELETE /:channelId/updates/:updateId/comments/:commentId
DELETE /:channelId/updates/:updateId/echo
DELETE /:channelId/updates/:updateId/spark
DELETE /:channelId/updates/:updateId/stash
GET /:channelId
GET /:channelId/subscribers
GET /:channelId/updates
GET /:channelId/updates/:updateId/attendees
GET /:channelId/updates/:updateId/comments
GET /:channelId/updates/:updateId/comments/delta
GET /:channelId/updates/:updateId/results
GET /discover
GET /my
POST /:channelId/subscribe
POST /:channelId/updates
POST /:channelId/updates/:updateId/comments
POST /:channelId/updates/:updateId/comments/:commentId/pin
POST /:channelId/updates/:updateId/echo
POST /:channelId/updates/:updateId/rsvp
POST /:channelId/updates/:updateId/spark
POST /:channelId/updates/:updateId/stash
POST /:channelId/updates/:updateId/view
POST /:channelId/updates/:updateId/vote
PUT /:channelId
PUT /:channelId/subscribe/mute
PUT /:channelId/updates/:updateId
GROUP /v1/broadcast-channels
```

## Database schema (CREATE TABLE — full column DDL)
```sql
CREATE TABLE IF NOT EXISTS broadcast_channels (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    owner_id UUID NOT NULL,
    handle TEXT NOT NULL UNIQUE,
    name TEXT NOT NULL,
    description TEXT DEFAULT '',
    avatar_media_id UUID,
    banner_media_id UUID,
    channel_type TEXT NOT NULL DEFAULT 'public' CHECK (channel_type IN ('public','private','creator','brand','education','official','topic','paid')),
    category TEXT NOT NULL DEFAULT '',
    language TEXT NOT NULL DEFAULT '',
    comment_mode TEXT NOT NULL DEFAULT 'enabled' CHECK (comment_mode IN ('enabled','moderated','subscribers_only','disabled')),
    reaction_mode TEXT NOT NULL DEFAULT 'enabled' CHECK (reaction_mode IN ('enabled','disabled')),
    forward_allowed BOOLEAN NOT NULL DEFAULT true,
    paid_access BOOLEAN NOT NULL DEFAULT false,
    subscription_price_cents INT NOT NULL DEFAULT 0,
    post_schedule_enabled BOOLEAN NOT NULL DEFAULT true,
    subscriber_count_visible BOOLEAN NOT NULL DEFAULT true,
    allow_preview_posts INT NOT NULL DEFAULT 3,
    is_verified BOOLEAN NOT NULL DEFAULT false,
    subscriber_count BIGINT NOT NULL DEFAULT 0,
    update_count BIGINT NOT NULL DEFAULT 0,
    status TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active','suspended','archived','deleted')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    deleted_at TIMESTAMPTZ
);

CREATE TABLE IF NOT EXISTS channel_members (
    channel_id UUID NOT NULL REFERENCES broadcast_channels(id) ON DELETE CASCADE,
    user_id UUID NOT NULL,
    role TEXT NOT NULL DEFAULT 'subscriber' CHECK (role IN ('owner','admin','editor','moderator','subscriber','banned')),
    notify_on TEXT NOT NULL DEFAULT 'all' CHECK (notify_on IN ('all','highlights','none')),
    muted_until TIMESTAMPTZ,
    snoozed_until TIMESTAMPTZ,
    paid BOOLEAN NOT NULL DEFAULT false,
    subscribed_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (channel_id, user_id)
);

-- existed (CREATE TABLE IF NOT EXISTS won't add them). Idempotent ALTERs run on
-- every BootstrapSchema startup so existing DBs converge to the current schema.
-- (Missing notify_on caused subscribe/unsubscribe to 500: SQLSTATE 42703.)
ALTER TABLE channel_members ADD COLUMN IF NOT EXISTS notify_on TEXT NOT NULL DEFAULT 'all';
ALTER TABLE channel_members ADD COLUMN IF NOT EXISTS muted_until TIMESTAMPTZ;
ALTER TABLE channel_members ADD COLUMN IF NOT EXISTS snoozed_until TIMESTAMPTZ;
ALTER TABLE channel_members ADD COLUMN IF NOT EXISTS paid BOOLEAN NOT NULL DEFAULT false;
ALTER TABLE channel_members ADD COLUMN IF NOT EXISTS subscribed_at TIMESTAMPTZ NOT NULL DEFAULT NOW();

CREATE TABLE IF NOT EXISTS channel_updates (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    channel_id UUID NOT NULL REFERENCES broadcast_channels(id) ON DELETE CASCADE,
    author_id UUID NOT NULL,
    update_type TEXT NOT NULL DEFAULT 'announcement' CHECK (update_type IN ('announcement','image','video','audio','poll','event','commerce','alert','digest')),
    title TEXT,
    body TEXT NOT NULL DEFAULT '',
    media_ids UUID[] NOT NULL DEFAULT '{}',
    metadata JSONB,
    is_pinned BOOLEAN NOT NULL DEFAULT false,
    scheduled_at TIMESTAMPTZ,
    published_at TIMESTAMPTZ,
    status TEXT NOT NULL DEFAULT 'draft' CHECK (status IN ('draft','scheduled','published','deleted')),
    view_count BIGINT NOT NULL DEFAULT 0,
    reaction_count BIGINT NOT NULL DEFAULT 0,
    comment_count BIGINT NOT NULL DEFAULT 0,
    forward_count BIGINT NOT NULL DEFAULT 0,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS update_sparks (
    update_id UUID NOT NULL REFERENCES channel_updates(id) ON DELETE CASCADE,
    user_id UUID NOT NULL,
    is_supernova BOOLEAN NOT NULL DEFAULT false,
    weight INT NOT NULL DEFAULT 1,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (update_id, user_id)
);

CREATE TABLE IF NOT EXISTS update_views (
    update_id UUID NOT NULL REFERENCES channel_updates(id) ON DELETE CASCADE,
    user_id UUID NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (update_id, user_id)
);

CREATE TABLE IF NOT EXISTS update_stashes (
    update_id UUID NOT NULL REFERENCES channel_updates(id) ON DELETE CASCADE,
    user_id UUID NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (update_id, user_id)
);

CREATE TABLE IF NOT EXISTS update_echoes (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    update_id UUID NOT NULL REFERENCES channel_updates(id) ON DELETE CASCADE,
    user_id UUID NOT NULL,
    echo_type TEXT NOT NULL DEFAULT 'share',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS update_comments (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    update_id UUID NOT NULL REFERENCES channel_updates(id) ON DELETE CASCADE,
    author_id UUID NOT NULL,
    body TEXT NOT NULL,
    parent_id UUID REFERENCES update_comments(id) ON DELETE SET NULL,
    is_pinned BOOLEAN NOT NULL DEFAULT false,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS poll_votes (
    update_id UUID NOT NULL REFERENCES channel_updates(id) ON DELETE CASCADE,
    user_id UUID NOT NULL,
    option_index INT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (update_id, user_id, option_index)
);

CREATE TABLE IF NOT EXISTS event_rsvps (
    update_id UUID NOT NULL REFERENCES channel_updates(id) ON DELETE CASCADE,
    user_id UUID NOT NULL,
    status TEXT NOT NULL CHECK (status IN ('going','interested','not_going')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (update_id, user_id)
);

```

## API types (request/response Go structs with JSON tags)
```go
type SparkRequest struct {
	IsSupernova bool `json:"is_supernova"`
}

type EchoRequest struct {
	EchoType string `json:"echo_type"`
}

type AddCommentRequest struct {
	Body     string     `json:"body" binding:"required"`
	ParentID *uuid.UUID `json:"parent_id"`
}

type VoteRequest struct {
	OptionIndexes []int `json:"option_indexes" binding:"required"`
}

type RSVPRequest struct {
	Status string `json:"status" binding:"required"`
}

type CreateChannelRequest struct {
	Handle                 string     `json:"handle" binding:"required"`
	Name                   string     `json:"name" binding:"required"`
	Description            string     `json:"description"`
	AvatarMediaID          *uuid.UUID `json:"avatar_media_id"`
	BannerMediaID          *uuid.UUID `json:"banner_media_id"`
	ChannelType            string     `json:"channel_type"`
	Category               string     `json:"category"`
	Language               string     `json:"language"`
	CommentMode            string     `json:"comment_mode"`
	ReactionMode           string     `json:"reaction_mode"`
	ForwardAllowed         *bool      `json:"forward_allowed"`
	PaidAccess             bool       `json:"paid_access"`
	SubscriptionPriceCents int        `json:"subscription_price_cents"`
}

type UpdateChannelRequest struct {
	Name                   *string    `json:"name"`
	Description            *string    `json:"description"`
	AvatarMediaID          *uuid.UUID `json:"avatar_media_id"`
	BannerMediaID          *uuid.UUID `json:"banner_media_id"`
	ChannelType            *string    `json:"channel_type"`
	Category               *string    `json:"category"`
	Language               *string    `json:"language"`
	CommentMode            *string    `json:"comment_mode"`
	ReactionMode           *string    `json:"reaction_mode"`
	ForwardAllowed         *bool      `json:"forward_allowed"`
	PaidAccess             *bool      `json:"paid_access"`
	SubscriptionPriceCents *int       `json:"subscription_price_cents"`
	PostScheduleEnabled    *bool      `json:"post_schedule_enabled"`
	SubscriberCountVisible *bool      `json:"subscriber_count_visible"`
	AllowPreviewPosts      *int       `json:"allow_preview_posts"`
}

type CreateUpdateRequest struct {
	UpdateType  string          `json:"update_type"`
	Title       *string         `json:"title"`
	Body        string          `json:"body"`
	MediaIDs    []uuid.UUID     `json:"media_ids"`
	Metadata    json.RawMessage `json:"metadata"`
	ScheduledAt *time.Time      `json:"scheduled_at"`
}

type MuteRequest struct {
	MutedUntil *time.Time `json:"muted_until"`
}
```
