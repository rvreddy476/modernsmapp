# Module: notification-service

_Auto-extracted from source (DDL + route registrations). Verbatim — the source of truth is the code._

## HTTP routes
```
DELETE /:bucket/:ts
DELETE /devices/:id
GET /digests
GET /preferences
GET /preferences/detailed
GET /stream
GET /unread-count
GET /v1/realtime/sse
PATCH /preferences
PATCH /read-all
POST /bundle
POST /devices
POST /read
POST /v1/read-marker
POST /v1/unread/bulk
PUT /preferences/detailed
GROUP /v1/notifications
```

## Database schema (CREATE TABLE — full column DDL)
```sql
CREATE TABLE IF NOT EXISTS notify_meta.event_dedup (
    event_id UUID PRIMARY KEY,
    processed_at TIMESTAMPTZ NOT NULL
);

CREATE TABLE IF NOT EXISTS notification_preferences (
    user_id          UUID PRIMARY KEY,
    email_enabled    BOOLEAN NOT NULL DEFAULT TRUE,
    push_enabled     BOOLEAN NOT NULL DEFAULT TRUE,
    sms_enabled      BOOLEAN NOT NULL DEFAULT FALSE,
    quiet_hours_start TIME,
    quiet_hours_end   TIME,
    muted_types      JSONB NOT NULL DEFAULT '[]',
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS user_devices (
    id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id    UUID NOT NULL,
    platform   TEXT NOT NULL CHECK (platform IN ('ios', 'android', 'web')),
    push_token TEXT NOT NULL,
    is_active  BOOLEAN NOT NULL DEFAULT TRUE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE(user_id, platform, push_token)
);

CREATE TABLE IF NOT EXISTS notification_digests (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id     UUID NOT NULL,
    period_type TEXT NOT NULL CHECK (period_type IN ('weekly','monthly')),
    period_start DATE NOT NULL,
    content     JSONB NOT NULL,
    sent_at     TIMESTAMPTZ,
    UNIQUE (user_id, period_type, period_start)
);

CREATE TABLE IF NOT EXISTS notification_bundles (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id         UUID NOT NULL,
    bundle_type     TEXT NOT NULL,
    count           INT NOT NULL DEFAULT 0,
    actor_ids       UUID[] NOT NULL DEFAULT '{}',
    ref_id          UUID,
    last_updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    sent_at         TIMESTAMPTZ
);

CREATE TABLE IF NOT EXISTS notification_preferences (
    user_id             TEXT PRIMARY KEY,
    push_enabled        BOOLEAN NOT NULL DEFAULT TRUE,
    email_enabled       BOOLEAN NOT NULL DEFAULT FALSE,
    quiet_hours_enabled BOOLEAN NOT NULL DEFAULT FALSE,
    quiet_hours_start   TIME,
    quiet_hours_end     TIME,
    quiet_hours_tz      VARCHAR(50),
    push_likes          BOOLEAN NOT NULL DEFAULT FALSE,
    push_super_likes    BOOLEAN NOT NULL DEFAULT TRUE,
    push_comments       BOOLEAN NOT NULL DEFAULT TRUE,
    push_replies        BOOLEAN NOT NULL DEFAULT TRUE,
    push_mentions       BOOLEAN NOT NULL DEFAULT TRUE,
    push_follows        BOOLEAN NOT NULL DEFAULT TRUE,
    push_friend_requests BOOLEAN NOT NULL DEFAULT TRUE,
    push_group_posts    BOOLEAN NOT NULL DEFAULT TRUE,
    push_group_mentions BOOLEAN NOT NULL DEFAULT TRUE,
    push_channel_updates BOOLEAN NOT NULL DEFAULT TRUE,
    push_channel_urgent BOOLEAN NOT NULL DEFAULT TRUE,
    push_community_posts BOOLEAN NOT NULL DEFAULT FALSE,
    push_community_mentions BOOLEAN NOT NULL DEFAULT TRUE,
    push_event_reminders BOOLEAN NOT NULL DEFAULT TRUE,
    push_system         BOOLEAN NOT NULL DEFAULT TRUE,
    email_digest        VARCHAR(10) NOT NULL DEFAULT 'weekly',
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

```

## API types (request/response Go structs with JSON tags)
```go
type MarkReadRequest struct {
	Bucket int    `json:"bucket" binding:"required"`
	TS     string `json:"ts" binding:"required"`
}

type UpdatePreferencesRequest struct {
	EmailEnabled    *bool            `json:"email_enabled"`
	PushEnabled     *bool            `json:"push_enabled"`
	SMSEnabled      *bool            `json:"sms_enabled"`
	QuietHoursStart *string          `json:"quiet_hours_start"`
	QuietHoursEnd   *string          `json:"quiet_hours_end"`
	MutedTypes      *json.RawMessage `json:"muted_types"`
}

type RegisterDeviceRequest struct {
	Platform  string `json:"platform" binding:"required,oneof=ios android web"`
	PushToken string `json:"push_token" binding:"required"`
}

type BundleNotificationRequest struct {
	UserID     string  `json:"user_id" binding:"required"`
	ActorID    string  `json:"actor_id" binding:"required"`
	BundleType string  `json:"bundle_type" binding:"required"`
	RefID      *string `json:"ref_id"`
}

type UpdateNotifPreferencesRequest struct {
	PushEnabled         *bool   `json:"push_enabled"`
	EmailEnabled        *bool   `json:"email_enabled"`
	QuietHoursEnabled   *bool   `json:"quiet_hours_enabled"`
	QuietHoursStart     *string `json:"quiet_hours_start"`
	QuietHoursEnd       *string `json:"quiet_hours_end"`
	QuietHoursTZ        *string `json:"quiet_hours_tz"`
	PushLikes           *bool   `json:"push_likes"`
	PushSuperLikes      *bool   `json:"push_super_likes"`
	PushComments        *bool   `json:"push_comments"`
	PushReplies         *bool   `json:"push_replies"`
	PushMentions        *bool   `json:"push_mentions"`
	PushFollows         *bool   `json:"push_follows"`
	PushFriendRequests  *bool   `json:"push_friend_requests"`
	PushGroupPosts      *bool   `json:"push_group_posts"`
	PushGroupMentions   *bool   `json:"push_group_mentions"`
	PushChannelUpdates  *bool   `json:"push_channel_updates"`
	PushChannelUrgent   *bool   `json:"push_channel_urgent"`
	PushCommunityPosts  *bool   `json:"push_community_posts"`
	PushCommunityMentions *bool `json:"push_community_mentions"`
	PushEventReminders  *bool   `json:"push_event_reminders"`
	PushSystem          *bool   `json:"push_system"`
	EmailDigest         *string `json:"email_digest"`
}
```
