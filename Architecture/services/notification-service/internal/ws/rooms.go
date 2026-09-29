package ws

import (
	"context"
	"encoding/json"
	"log/slog"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
)

// RoomAuthorizer re-answers, for a member already in a room, the question
// asked when they joined: may this user still be in this room? It is the
// same authority the join used (for post:<id> rooms, post-service's
// GET /v1/internal/posts/:id/visibility — review status, schedule,
// visibility, private accounts, blocks). A false and an error both mean
// "no longer": an authority that cannot answer is not a reason to keep
// delivering, so the sweep fails closed exactly like the join does.
type RoomAuthorizer interface {
	MayStay(ctx context.Context, userID, room string) (bool, error)
}

// ReauthorizeInterval is how often every membership is re-checked (P-8 e,
// 2026-09-29). Before this, rooms were authorized at join only: a post that
// went private, an author who blocked the viewer, or an account that went
// private kept streaming comment and poll frames to a socket already in the
// room until it disconnected. Five minutes bounds that window the same way
// the media gate's signed-URL TTL bounds a leaked link.
const ReauthorizeInterval = 5 * time.Minute

// roomMember is one connection's seat in one room: who they are (for the
// re-check) and where their frames go.
type roomMember struct {
	userID string
	send   chan []byte
}

// RoomManager handles WebSocket room subscriptions.
type RoomManager struct {
	mu         sync.RWMutex
	rooms      map[string]map[string]roomMember // room -> connID -> member
	rdb        *redis.Client
	instanceID string
	authz      RoomAuthorizer
	// now is the clock seam for tests.
	now func() time.Time
}

// NewRoomManager creates a new RoomManager. rdb may be nil (no cross-instance
// routing; local delivery still works), which is what the tests use.
func NewRoomManager(rdb *redis.Client, instanceID string) *RoomManager {
	return &RoomManager{
		rooms:      make(map[string]map[string]roomMember),
		rdb:        rdb,
		instanceID: instanceID,
		now:        time.Now,
	}
}

// WithAuthorizer wires the re-authorization authority. Without one the
// periodic sweep is a no-op (join-time authorization only, the pre-2026-09-29
// behaviour); StartReauthorizer logs that loudly.
func (rm *RoomManager) WithAuthorizer(a RoomAuthorizer) *RoomManager {
	rm.authz = a
	return rm
}

// Subscribe adds a connection to a room with no identity attached. Kept for
// callers that predate the re-check; a member with no user id cannot be
// re-authorized and is DROPPED by the first sweep when an authorizer is
// wired, so new callers use SubscribeUser.
func (rm *RoomManager) Subscribe(connID, room string, sendCh chan []byte) {
	rm.SubscribeUser(connID, "", room, sendCh)
}

// SubscribeUser adds a connection, as userID, to a room. Max 5 rooms per
// connection (excluding notifications) is the caller's rule.
func (rm *RoomManager) SubscribeUser(connID, userID, room string, sendCh chan []byte) {
	rm.mu.Lock()
	defer rm.mu.Unlock()
	if rm.rooms[room] == nil {
		rm.rooms[room] = make(map[string]roomMember)
	}
	rm.rooms[room][connID] = roomMember{userID: userID, send: sendCh}

	// Track in Redis for cross-instance routing
	if rm.rdb != nil {
		rm.rdb.SAdd(context.Background(), "room_subscribers:"+room, rm.instanceID+":"+connID)
	}
	slog.Debug("room subscribe", "room", room, "conn", connID, "user", userID)
}

// Unsubscribe removes a connection from a room.
func (rm *RoomManager) Unsubscribe(connID, room string) {
	rm.mu.Lock()
	defer rm.mu.Unlock()
	rm.removeLocked(connID, room)
}

// removeLocked drops one seat; the caller holds the write lock.
func (rm *RoomManager) removeLocked(connID, room string) {
	if conns, ok := rm.rooms[room]; ok {
		delete(conns, connID)
		if len(conns) == 0 {
			delete(rm.rooms, room)
		}
	}
	if rm.rdb != nil {
		rm.rdb.SRem(context.Background(), "room_subscribers:"+room, rm.instanceID+":"+connID)
	}
}

// UnsubscribeAll removes a connection from all rooms.
func (rm *RoomManager) UnsubscribeAll(connID string) {
	rm.mu.Lock()
	defer rm.mu.Unlock()
	for room, conns := range rm.rooms {
		if _, ok := conns[connID]; ok {
			rm.removeLocked(connID, room)
		}
	}
}

// BroadcastToRoom sends a message to all local subscribers of a room.
func (rm *RoomManager) BroadcastToRoom(room string, msg []byte) {
	rm.mu.RLock()
	defer rm.mu.RUnlock()
	if conns, ok := rm.rooms[room]; ok {
		for connID, member := range conns {
			select {
			case member.send <- msg:
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
	if rm.rdb == nil {
		rm.BroadcastToRoom(room, data)
		return
	}
	rm.rdb.Publish(ctx, "ws:room:"+room, string(data))
}

// StartRedisSubscriber listens for cross-instance room events.
func (rm *RoomManager) StartRedisSubscriber(ctx context.Context) {
	if rm.rdb == nil {
		return
	}
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

// StartReauthorizer re-checks every membership every interval (0 =
// ReauthorizeInterval) until ctx ends. Blocking; run it in its own
// goroutine next to StartRedisSubscriber.
func (rm *RoomManager) StartReauthorizer(ctx context.Context, interval time.Duration) {
	if rm.authz == nil {
		slog.Warn("room re-authorization disabled: no RoomAuthorizer wired; rooms are authorized at join only")
		return
	}
	if interval <= 0 {
		interval = ReauthorizeInterval
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			rm.ReauthorizeOnce(ctx)
		}
	}
}

// roomSeat is a snapshot of one membership taken under the read lock, so
// the authority is asked without holding it.
type roomSeat struct {
	room   string
	connID string
	member roomMember
}

// ReauthorizeOnce runs one sweep: every seat is re-asked of the authority
// and dropped — with an `unsubscribed` frame to the socket, so the client
// knows to stop expecting the room — when the answer is no, an error, or
// cannot be asked (a seat with no user id). Returns the number dropped.
func (rm *RoomManager) ReauthorizeOnce(ctx context.Context) int {
	if rm.authz == nil {
		return 0
	}
	rm.mu.RLock()
	seats := make([]roomSeat, 0, 16)
	for room, conns := range rm.rooms {
		for connID, member := range conns {
			seats = append(seats, roomSeat{room: room, connID: connID, member: member})
		}
	}
	rm.mu.RUnlock()

	dropped := 0
	for _, seat := range seats {
		reason := rm.recheck(ctx, seat)
		if reason == "" {
			continue
		}
		rm.mu.Lock()
		// Only drop the seat we checked: a re-subscribe that replaced it
		// meanwhile is a fresh, join-authorized membership.
		current, still := rm.rooms[seat.room][seat.connID]
		if still && current.send == seat.member.send && current.userID == seat.member.userID {
			rm.removeLocked(seat.connID, seat.room)
			dropped++
		} else {
			still = false
		}
		rm.mu.Unlock()
		if !still {
			continue
		}
		slog.Info("room membership dropped by re-authorization",
			"room", seat.room, "conn", seat.connID, "user", seat.member.userID, "reason", reason)
		rm.notifyDropped(seat, reason)
	}
	return dropped
}

// recheck returns "" when the seat may stay, else the reason it may not.
func (rm *RoomManager) recheck(ctx context.Context, seat roomSeat) string {
	if seat.member.userID == "" {
		return "no_identity"
	}
	ok, err := rm.authz.MayStay(ctx, seat.member.userID, seat.room)
	if err != nil {
		return "unresolved"
	}
	if !ok {
		return "no_longer_authorized"
	}
	return ""
}

// notifyDropped tells the socket its room is gone. Non-blocking, like every
// room delivery: a full channel loses the notice, not the drop.
func (rm *RoomManager) notifyDropped(seat roomSeat, reason string) {
	frame, err := json.Marshal(RoomEvent{
		Type:  "unsubscribed",
		Room:  seat.room,
		Event: "reauthorization",
		Data:  map[string]string{"reason": reason},
	})
	if err != nil {
		return
	}
	select {
	case seat.member.send <- frame:
	default:
	}
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
