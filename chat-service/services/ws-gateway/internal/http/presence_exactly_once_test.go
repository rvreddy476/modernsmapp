package http

// Exactly-once presence delivery (chat-parity fix pass 2, lane WSG).
//
// On dev a peer received presence_update [true,true] on connect and
// [false,false] on disconnect: the pair held two accepted direct
// conversations, the peer list had one row per conversation, and Announce
// published once per row. These tests pin one frame per peer per transition
// even when the peer source repeats a peer.

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/atpost/chat-shared/presence"
	"github.com/google/uuid"
	"github.com/gorilla/websocket"
	"github.com/redis/go-redis/v9"
)

func TestPresenceFanoutDeliversOncePerPeerWhenPeerRepeats(t *testing.T) {
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	defer rdb.Close()
	ctx := context.Background()

	user, twice, once := uuid.New(), uuid.New(), uuid.New()
	peers := []uuid.UUID{twice, once, twice, twice}
	graph := fakeGraph{twice: {Online: true, LastSeen: true}, once: {Online: true}}
	store := presence.NewRedisStore(rdb, presence.Options{})
	if _, err := store.ConnectionOpened(ctx, user.String(), "c1", time.Now()); err != nil {
		t.Fatal(err)
	}
	f := newPresenceFanout(fakePeers{peers}, graph, rdb, quietLog())

	got := collect(t, rdb, []uuid.UUID{twice, once}, func() { f.Announce(ctx, user) })
	if len(got[twice]) != 1 || len(got[once]) != 1 {
		t.Fatalf("each peer must get exactly one frame per announce, got twice=%d once=%d", len(got[twice]), len(got[once]))
	}
}

// presenceStream relays the is_online value of every presence_update about
// subject that conn receives. (A gorilla read error is terminal, so one
// reader goroutine runs for the socket's life rather than per window.)
func presenceStream(conn *websocket.Conn, subject uuid.UUID) <-chan bool {
	out := make(chan bool, 16)
	go func() {
		defer close(out)
		for {
			_, raw, err := conn.ReadMessage()
			if err != nil {
				return
			}
			var frame struct {
				Type    string `json:"type"`
				Payload struct {
					UserID   string `json:"user_id"`
					IsOnline bool   `json:"is_online"`
				} `json:"payload"`
			}
			if json.Unmarshal(raw, &frame) == nil && frame.Type == "presence_update" && frame.Payload.UserID == subject.String() {
				out <- frame.Payload.IsOnline
			}
		}
	}()
	return out
}

// drain returns everything stream yields within window.
func drain(stream <-chan bool, window time.Duration) []bool {
	var states []bool
	timeout := time.After(window)
	for {
		select {
		case v, ok := <-stream:
			if !ok {
				return states
			}
			states = append(states, v)
		case <-timeout:
			return states
		}
	}
}

// End to end over real sockets: B is connected; A connects then closes.
// B must see exactly [true] and then exactly [false], although the peer
// source lists B twice (two shared direct conversations).
func TestPresenceUpdateReachesPeerExactlyOncePerTransition(t *testing.T) {
	a, b := uuid.New(), uuid.New()
	h := newLiveHarness(t, func(o *ServerOptions) {
		o.PresencePeers = peerSourceFunc(func(user uuid.UUID) []uuid.UUID {
			if user == a {
				return []uuid.UUID{b, b}
			}
			return []uuid.UUID{a, a}
		})
		o.PresenceVisibility = fakeGraph{a: {Online: true, LastSeen: true}, b: {Online: true, LastSeen: true}}
	})

	connB := h.dial(b)
	aboutA := presenceStream(connB, a)
	// B's own connect announces to A (offline, no socket): nothing for B.
	if got := drain(aboutA, 300*time.Millisecond); len(got) != 0 {
		t.Fatalf("B saw presence for A before A connected: %v", got)
	}

	connA := h.dial(a)
	if got := drain(aboutA, 600*time.Millisecond); len(got) != 1 || !got[0] {
		t.Fatalf("A's connect must reach B exactly once as online, got %v", got)
	}

	_ = connA.Close()
	if got := drain(aboutA, 800*time.Millisecond); len(got) != 1 || got[0] {
		t.Fatalf("A's disconnect must reach B exactly once as offline, got %v", got)
	}
}

type peerSourceFunc func(user uuid.UUID) []uuid.UUID

func (f peerSourceFunc) DMPeers(_ context.Context, user uuid.UUID, _ int) ([]uuid.UUID, error) {
	return f(user), nil
}
