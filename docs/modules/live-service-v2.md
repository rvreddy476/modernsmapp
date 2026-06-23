# Module: live-service-v2

_Auto-extracted from source (DDL + route registrations). Verbatim — the source of truth is the code._

## HTTP routes
```
DELETE /streams/:id/chat/mute/:userId
DELETE /streams/:id/chat/pin
DELETE /streams/:id/chat/word-filters/:word
GET /streams
GET /streams/:id
GET /streams/:id/chat
GET /streams/:id/chat/mutes
GET /streams/:id/chat/pinned
GET /streams/:id/chat/word-filters
GET /streams/:id/viewer-token
POST /streams
POST /streams/:id/chat
POST /streams/:id/chat/mute
POST /streams/:id/chat/pin
POST /streams/:id/chat/word-filters
POST /streams/:id/end
POST /streams/:id/start
POST /v1/livestream/egress/webhook
GROUP /v1/livestream
```

## Database schema (CREATE TABLE — full column DDL)
```sql
CREATE TABLE IF NOT EXISTS live_streams (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    creator_user_id UUID NOT NULL,
    livekit_room    TEXT NOT NULL UNIQUE,
    title           TEXT NOT NULL,
    description     TEXT NOT NULL DEFAULT '',
    cover_media_id  UUID,
    status          TEXT NOT NULL DEFAULT 'scheduled'
                       CHECK (status IN ('scheduled','live','ended','failed')),
    -- privacy: public, followers-only, paid (paid hooks into commerce later)
    visibility      TEXT NOT NULL DEFAULT 'public'
                       CHECK (visibility IN ('public','followers','paid')),
    scheduled_at    TIMESTAMPTZ,
    started_at      TIMESTAMPTZ,
    ended_at        TIMESTAMPTZ,
    viewer_peak     INT NOT NULL DEFAULT 0,
    -- VOD pointer once Egress finishes
    recording_url   TEXT,
    recording_duration_seconds INT,
    -- LiveKit Egress job ID we get back when StartEgress fires.
    -- Stored so EndStream can call StopEgress idempotently.
    egress_id       TEXT,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS live_viewer_events (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    stream_id    UUID NOT NULL REFERENCES live_streams(id) ON DELETE CASCADE,
    user_id      UUID NOT NULL,
    event_type   TEXT NOT NULL CHECK (event_type IN ('join','leave')),
    occurred_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS live_chat_messages (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    stream_id   UUID NOT NULL REFERENCES live_streams(id) ON DELETE CASCADE,
    user_id     UUID NOT NULL,
    text        TEXT NOT NULL CHECK (char_length(text) BETWEEN 1 AND 500),
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS live_chat_mutes (
    stream_id   UUID NOT NULL REFERENCES live_streams(id) ON DELETE CASCADE,
    user_id     UUID NOT NULL,
    muted_by    UUID NOT NULL,
    muted_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (stream_id, user_id)
);

CREATE TABLE IF NOT EXISTS live_chat_word_filters (
    stream_id   UUID NOT NULL REFERENCES live_streams(id) ON DELETE CASCADE,
    word        TEXT NOT NULL CHECK (char_length(word) BETWEEN 1 AND 100),
    added_by    UUID NOT NULL,
    added_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (stream_id, word)
);

CREATE TABLE IF NOT EXISTS live_streams (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    creator_user_id UUID NOT NULL,
    livekit_room    TEXT NOT NULL UNIQUE,
    title           TEXT NOT NULL,
    description     TEXT NOT NULL DEFAULT '',
    cover_media_id  UUID,
    status          TEXT NOT NULL DEFAULT 'scheduled'
                       CHECK (status IN ('scheduled','live','ended','failed')),
    visibility      TEXT NOT NULL DEFAULT 'public'
                       CHECK (visibility IN ('public','followers','paid')),
    scheduled_at    TIMESTAMPTZ,
    started_at      TIMESTAMPTZ,
    ended_at        TIMESTAMPTZ,
    viewer_peak     INT NOT NULL DEFAULT 0,
    recording_url   TEXT,
    recording_duration_seconds INT,
    egress_id       TEXT,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS live_viewer_events (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    stream_id    UUID NOT NULL REFERENCES live_streams(id) ON DELETE CASCADE,
    user_id      UUID NOT NULL,
    event_type   TEXT NOT NULL CHECK (event_type IN ('join','leave')),
    occurred_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS live_chat_mutes (
    stream_id   UUID NOT NULL REFERENCES live_streams(id) ON DELETE CASCADE,
    user_id     UUID NOT NULL,
    muted_by    UUID NOT NULL,
    muted_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (stream_id, user_id)
);

CREATE TABLE IF NOT EXISTS live_chat_word_filters (
    stream_id   UUID NOT NULL REFERENCES live_streams(id) ON DELETE CASCADE,
    word        TEXT NOT NULL CHECK (char_length(word) BETWEEN 1 AND 100),
    added_by    UUID NOT NULL,
    added_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (stream_id, word)
);

```

## API types (request/response Go structs with JSON tags)
```go
type createStreamRequest struct {
	Title        string     `json:"title" binding:"required"`
	Description  string     `json:"description"`
	Visibility   string     `json:"visibility"`
	CoverMediaID *uuid.UUID `json:"cover_media_id"`
	ScheduledAt  *time.Time `json:"scheduled_at"`
}

type egressWebhookPayload struct {
	Event      string `json:"event"`
	EgressInfo *struct {
		EgressID string `json:"egress_id"`
		RoomName string `json:"room_name"`
		File     *struct {
			Location string `json:"location"`
			Duration int64  `json:"duration"` // nanoseconds per LiveKit spec
		} `json:"file"`
	} `json:"egress_info"`
	Room *struct {
		Name string `json:"name"`
	} `json:"room"`
	Participant *struct {
		Identity string `json:"identity"`
	} `json:"participant"`
}

type sendChatRequest struct {
	Text string `json:"text" binding:"required"`
}

type muteUserRequest struct {
	UserID uuid.UUID `json:"user_id" binding:"required"`
}

type wordFilterRequest struct {
	Word string `json:"word" binding:"required"`
}

type pinMessageRequest struct {
	MessageID uuid.UUID `json:"message_id" binding:"required"`
}
```
