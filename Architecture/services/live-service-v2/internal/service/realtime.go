package service

// Real-time room events (1 Oct 2026). Everything that happens in a stream is
// published as one JSON object to the Redis channel live:stream:{id}, which
// the ws-gateway relays to sockets it admitted through MayWatch:
//
//	{"type":"<type>","stream_id":"<uuid>","at":"<RFC3339>", <payload fields>..., "payload":{...}}
//
// The payload's fields are at the top level (what the web reads) and
// repeated under "payload". chat.message carries exactly the row GET /chat
// returns; chat.removed {message_id, by_role}; status.changed {status,
// ended_reason, status_changed_at, started_at, ended_at, viewer_count,
// viewer_peak}; viewer.count {viewer_count, viewer_peak}; moderation.*
// carry user_id, or user_ids for moderation.moderators.
//
// Types: status.changed, chat.message, chat.removed, viewer.count, and
// moderation.{mute,unmute,word_filter_added,word_filter_removed,pin,unpin,
// ban,unban,moderators}. Publishing is best-effort: the database is the
// truth and every client can re-read it over HTTP.

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

// Room event types.
const (
	EventStatusChanged               = "status.changed"
	EventChatMessage                 = "chat.message"
	EventChatRemoved                 = "chat.removed"
	EventViewerCount                 = "viewer.count"
	EventModerationMute              = "moderation.mute"
	EventModerationUnmute            = "moderation.unmute"
	EventModerationWordFilterAdded   = "moderation.word_filter_added"
	EventModerationWordFilterRemoved = "moderation.word_filter_removed"
	EventModerationPin               = "moderation.pin"
	EventModerationUnpin             = "moderation.unpin"
	EventModerationBan               = "moderation.ban"
	EventModerationUnban             = "moderation.unban"
	EventModerationModerators        = "moderation.moderators"
)

// RoomChannel is the pub/sub channel of a stream (the ws-gateway's
// subscribe_live_stream room).
func RoomChannel(streamID uuid.UUID) string {
	return fmt.Sprintf("live:stream:%s", streamID.String())
}

// RoomEvents publishes room events and throttles repeated ones.
type RoomEvents interface {
	Publish(ctx context.Context, streamID uuid.UUID, body []byte) error
	// Allow returns true at most once per window for key (across replicas).
	Allow(ctx context.Context, key string, window time.Duration) bool
}

// roomEventBody builds the wire object: the payload's fields at the top
// level next to type, stream_id and at, and the same payload again under
// "payload" for readers of the earlier envelope.
func roomEventBody(streamID uuid.UUID, evtType string, payload any, at time.Time) ([]byte, error) {
	raw, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	obj := map[string]any{}
	if len(raw) > 0 && raw[0] == '{' {
		if err := json.Unmarshal(raw, &obj); err != nil {
			return nil, err
		}
	}
	obj["type"] = evtType
	obj["stream_id"] = streamID.String()
	obj["at"] = at
	obj["payload"] = json.RawMessage(raw)
	return json.Marshal(obj)
}

func (s *Service) publish(ctx context.Context, streamID uuid.UUID, evtType string, payload any) {
	if s.rt == nil {
		return
	}
	body, err := roomEventBody(streamID, evtType, payload, time.Now().UTC())
	if err != nil {
		return
	}
	_ = s.rt.Publish(ctx, streamID, body)
}

// RedisRoomEvents is RoomEvents over Redis pub/sub and SET NX PX.
type RedisRoomEvents struct{ rdb *redis.Client }

// NewRedisRoomEvents wraps a client.
func NewRedisRoomEvents(rdb *redis.Client) *RedisRoomEvents { return &RedisRoomEvents{rdb: rdb} }

// Publish sends body on the stream's channel.
func (r *RedisRoomEvents) Publish(ctx context.Context, streamID uuid.UUID, body []byte) error {
	return r.rdb.Publish(ctx, RoomChannel(streamID), string(body)).Err()
}

// Allow takes a short-lived lock; whoever sets it may publish. A Redis
// error allows (an extra count frame is harmless).
func (r *RedisRoomEvents) Allow(ctx context.Context, key string, window time.Duration) bool {
	ok, err := r.rdb.SetNX(ctx, key, "1", window).Result()
	if err != nil {
		return true
	}
	return ok
}
