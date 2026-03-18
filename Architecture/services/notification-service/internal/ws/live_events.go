package ws

import (
	"context"
	"encoding/json"
	"log/slog"
	"time"

	"github.com/redis/go-redis/v9"
)

// PollVoteUpdate is broadcast to poll:{post_id} room.
type PollVoteUpdate struct {
	OptionIndex int   `json:"option_index"`
	NewCounts   []int `json:"new_counts"`
	TotalVotes  int   `json:"total_votes"`
}

// RSVPUpdate is broadcast to event:{event_id} room.
type RSVPUpdate struct {
	GoingCount  int    `json:"going_count"`
	MaybeCount  int    `json:"maybe_count"`
	NewAttendee string `json:"new_attendee_preview,omitempty"`
}

// ThrottledBroadcast batches high-frequency events.
// For polls with >10 votes/sec, batch every 2 seconds.
func ThrottledBroadcast(ctx context.Context, rdb *redis.Client, rm *RoomManager, room string, data interface{}) {
	throttleKey := "broadcast_throttle:" + room
	count, _ := rdb.Incr(ctx, throttleKey).Result()
	if count == 1 {
		rdb.Expire(ctx, throttleKey, 2*time.Second)
	}

	// If under threshold (10 events in 2s), broadcast immediately
	if count <= 10 {
		rm.PublishToRoom(ctx, room, RoomEvent{
			Type:  "room_event",
			Room:  room,
			Event: "live_update",
			Data:  data,
		})
		return
	}

	// If over threshold, store latest state in Redis and let a delayed publish handle it
	latest, err := json.Marshal(data)
	if err != nil {
		slog.Error("failed to marshal broadcast data", "room", room, "error", err)
		return
	}
	rdb.Set(ctx, "broadcast_latest:"+room, string(latest), 3*time.Second)
}
