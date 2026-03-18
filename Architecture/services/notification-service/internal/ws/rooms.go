package ws

import (
	"context"
	"encoding/json"
	"log/slog"
	"sync"

	"github.com/redis/go-redis/v9"
)

// RoomManager handles WebSocket room subscriptions.
type RoomManager struct {
	mu         sync.RWMutex
	rooms      map[string]map[string]chan []byte // room -> connID -> send channel
	rdb        *redis.Client
	instanceID string
}

// NewRoomManager creates a new RoomManager.
func NewRoomManager(rdb *redis.Client, instanceID string) *RoomManager {
	return &RoomManager{
		rooms:      make(map[string]map[string]chan []byte),
		rdb:        rdb,
		instanceID: instanceID,
	}
}

// Subscribe adds a connection to a room. Max 5 rooms per connection (excluding notifications).
func (rm *RoomManager) Subscribe(connID, room string, sendCh chan []byte) {
	rm.mu.Lock()
	defer rm.mu.Unlock()
	if rm.rooms[room] == nil {
		rm.rooms[room] = make(map[string]chan []byte)
	}
	rm.rooms[room][connID] = sendCh

	// Track in Redis for cross-instance routing
	rm.rdb.SAdd(context.Background(), "room_subscribers:"+room, rm.instanceID+":"+connID)
	slog.Debug("room subscribe", "room", room, "conn", connID)
}

// Unsubscribe removes a connection from a room.
func (rm *RoomManager) Unsubscribe(connID, room string) {
	rm.mu.Lock()
	defer rm.mu.Unlock()
	if conns, ok := rm.rooms[room]; ok {
		delete(conns, connID)
		if len(conns) == 0 {
			delete(rm.rooms, room)
		}
	}
	rm.rdb.SRem(context.Background(), "room_subscribers:"+room, rm.instanceID+":"+connID)
}

// UnsubscribeAll removes a connection from all rooms.
func (rm *RoomManager) UnsubscribeAll(connID string) {
	rm.mu.Lock()
	defer rm.mu.Unlock()
	for room, conns := range rm.rooms {
		if _, ok := conns[connID]; ok {
			delete(conns, connID)
			rm.rdb.SRem(context.Background(), "room_subscribers:"+room, rm.instanceID+":"+connID)
			if len(conns) == 0 {
				delete(rm.rooms, room)
			}
		}
	}
}

// BroadcastToRoom sends a message to all local subscribers of a room.
func (rm *RoomManager) BroadcastToRoom(room string, msg []byte) {
	rm.mu.RLock()
	defer rm.mu.RUnlock()
	if conns, ok := rm.rooms[room]; ok {
		for connID, ch := range conns {
			select {
			case ch <- msg:
			default:
				slog.Warn("room broadcast: channel full, dropping message", "room", room, "conn", connID)
			}
		}
	}
}

// PublishToRoom publishes an event to a room via Redis pub/sub (for cross-instance).
func (rm *RoomManager) PublishToRoom(ctx context.Context, room string, event RoomEvent) {
	data, err := json.Marshal(event)
	if err != nil {
		slog.Error("failed to marshal room event", "room", room, "error", err)
		return
	}
	rm.rdb.Publish(ctx, "ws:room:"+room, string(data))
}

// StartRedisSubscriber listens for cross-instance room events.
func (rm *RoomManager) StartRedisSubscriber(ctx context.Context) {
	pubsub := rm.rdb.PSubscribe(ctx, "ws:room:*")
	defer pubsub.Close()

	ch := pubsub.Channel()
	for {
		select {
		case <-ctx.Done():
			return
		case msg, ok := <-ch:
			if !ok {
				return
			}
			// Extract room from channel name: "ws:room:{room}" -> "{room}"
			room := msg.Channel[len("ws:room:"):]
			rm.BroadcastToRoom(room, []byte(msg.Payload))
		}
	}
}

// RoomCount returns the number of local subscribers in a room.
func (rm *RoomManager) RoomCount(room string) int {
	rm.mu.RLock()
	defer rm.mu.RUnlock()
	return len(rm.rooms[room])
}

// Room naming convention (from spec):
// notifications:{user_id}          — personal (always subscribed)
// feed:home:{user_id}              — home feed delta
// feed:following:{user_id}         — following feed delta
// post:{post_id}                   — post thread
// group:{group_id}                 — group feed
// group:{group_id}:channel:{id}    — group channel
// channel:{channel_id}             — broadcast channel
// community:{id}:space:{id}        — community space
// chat:{conversation_id}           — messenger
// event:{event_id}                 — live RSVP
// poll:{post_id}                   — live poll votes
