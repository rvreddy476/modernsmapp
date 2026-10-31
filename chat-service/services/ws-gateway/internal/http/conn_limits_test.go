package http

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/atpost/chat-shared/callauth"
	"github.com/google/uuid"
	"github.com/gorilla/websocket"
	"github.com/redis/go-redis/v9"
)

func TestFrameLimiterBurstThenRate(t *testing.T) {
	now := time.Unix(1_000_000, 0)
	l := newFrameLimiter(10, 3, now)
	for i := 0; i < 3; i++ {
		if !l.allow(now) {
			t.Fatalf("frame %d inside the burst was refused", i)
		}
	}
	if l.allow(now) {
		t.Fatal("a frame past the burst was admitted")
	}
	// 10/s refills one token every 100 ms.
	if !l.allow(now.Add(100 * time.Millisecond)) {
		t.Fatal("the bucket did not refill")
	}
	if l.allow(now.Add(100 * time.Millisecond)) {
		t.Fatal("one refill admitted two frames")
	}
	// A long idle never banks more than the burst.
	later := now.Add(time.Hour)
	admitted := 0
	for l.allow(later) {
		admitted++
	}
	if admitted != 3 {
		t.Fatalf("after idling the bucket admitted %d, want the burst of 3", admitted)
	}
}

func TestFrameLimiterDefaults(t *testing.T) {
	l := newFrameLimiter(0, 0, time.Now())
	if l.rate != defaultFrameRate || l.burst != defaultFrameBurst {
		t.Fatalf("defaults rate=%v burst=%v", l.rate, l.burst)
	}
}

func limitServer(t *testing.T, maxPerUser int) (*Server, *miniredis.Miniredis) {
	t.Helper()
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	return NewServer(rdb, quietLog(), ServerOptions{MaxConnsPerUser: maxPerUser}), mr
}

func TestAdmitUserConnectionCapsPerUser(t *testing.T) {
	s, _ := limitServer(t, 2)
	ctx := context.Background()
	now := time.Now()
	user := uuid.NewString()

	for _, c := range []string{"c1", "c2"} {
		if ok, err := s.admitUserConnection(ctx, user, c, now); err != nil || !ok {
			t.Fatalf("%s refused under the cap: ok=%v err=%v", c, ok, err)
		}
	}
	if ok, _ := s.admitUserConnection(ctx, user, "c3", now); ok {
		t.Fatal("a third socket was admitted over a cap of 2")
	}
	// Another user is unaffected.
	if ok, _ := s.admitUserConnection(ctx, uuid.NewString(), "c1", now); !ok {
		t.Fatal("the cap leaked across users")
	}
	// Closing one frees a slot.
	s.releaseUserConnection(ctx, user, "c1")
	if ok, _ := s.admitUserConnection(ctx, user, "c3", now); !ok {
		t.Fatal("a released slot was not reusable")
	}
}

// A replica that died without releasing leaves entries that expire, so the
// user is not locked out forever; a heartbeat keeps a live entry.
func TestAdmitUserConnectionExpiresStaleEntries(t *testing.T) {
	s, _ := limitServer(t, 1)
	ctx := context.Background()
	user := uuid.NewString()
	start := time.Now()
	if ok, _ := s.admitUserConnection(ctx, user, "live", start); !ok {
		t.Fatal("first socket refused")
	}
	// Heartbeat at +100 s keeps it past the original expiry.
	s.refreshUserConnection(ctx, user, "live", start.Add(100*time.Second))
	if ok, _ := s.admitUserConnection(ctx, user, "other", start.Add(130*time.Second)); ok {
		t.Fatal("a heartbeating socket's slot was handed out")
	}
	// No heartbeat past its window: the slot is free again.
	if ok, _ := s.admitUserConnection(ctx, user, "other", start.Add(400*time.Second)); !ok {
		t.Fatal("a stale entry still held the slot")
	}
	// Refresh never re-creates a released entry.
	s.releaseUserConnection(ctx, user, "other")
	s.refreshUserConnection(ctx, user, "other", start.Add(401*time.Second))
	if n := s.rdb.ZCard(ctx, wsConnsKey(user)).Val(); n != 0 {
		t.Fatalf("refresh re-created a released entry (%d entries)", n)
	}
}

func TestDefaultPerUserCap(t *testing.T) {
	s, _ := limitServer(t, 0)
	if s.opts.MaxConnsPerUser != defaultMaxConnsPerUser {
		t.Fatalf("default per-user cap = %d", s.opts.MaxConnsPerUser)
	}
}

func dialStatus(t *testing.T, h *liveHarness, user uuid.UUID) (*websocket.Conn, int) {
	t.Helper()
	token := signJWT(t, map[string]any{"alg": "HS256"}, map[string]any{
		"sub": user.String(), "exp": time.Now().Add(time.Hour).Unix(),
	}, h.secret)
	header := http.Header{}
	header.Set("Authorization", "Bearer "+token)
	conn, resp, err := websocket.DefaultDialer.Dial(h.wsURL, header)
	if err != nil {
		if errors.Is(err, websocket.ErrBadHandshake) && resp != nil {
			return nil, resp.StatusCode
		}
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return conn, http.StatusSwitchingProtocols
}

// Audit M5 end to end: the socket over the per-user cap is refused with 429
// before the upgrade; closing one lets the next in.
func TestWSPerUserConnectionCap(t *testing.T) {
	h := newLiveHarness(t, func(o *ServerOptions) { o.MaxConnsPerUser = 2 })
	user := uuid.New()
	first := h.dial(user)
	h.dial(user)
	// Two local sockets share the process's one Redis subscription.
	h.waitLocalSubscribers("chat:"+user.String(), 2)
	h.waitSubscribers("chat:"+user.String(), 1)

	if _, status := dialStatus(t, h, user); status != http.StatusTooManyRequests {
		t.Fatalf("third socket: status %d, want 429", status)
	}
	if n := h.server.hub.localSubscribers("chat:" + user.String()); n != 2 {
		t.Fatalf("refused socket still registered a subscription (%d)", n)
	}
	if n := h.rdb.PubSubNumSub(context.Background(), "chat:"+user.String()).Val()["chat:"+user.String()]; n != 1 {
		t.Fatalf("refused socket changed the Redis subscription count (%d)", n)
	}

	_ = first.Close()
	h.waitLocalSubscribers("chat:"+user.String(), 1)
	h.waitSubscribers("chat:"+user.String(), 1)
	deadline := time.Now().Add(3 * time.Second)
	for {
		if _, status := dialStatus(t, h, user); status == http.StatusSwitchingProtocols {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("a closed socket's slot was never released")
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestWSPerProcessConnectionCap(t *testing.T) {
	h := newLiveHarness(t, func(o *ServerOptions) { o.MaxConnsPerProcess = 1 })
	h.dial(uuid.New())
	if _, status := dialStatus(t, h, uuid.New()); status != http.StatusServiceUnavailable {
		t.Fatalf("socket over the process cap: status %d, want 503", status)
	}
}

// Audit M5: a flood of signalling frames past the burst is dropped before
// it reaches Redis; only the burst is relayed.
func TestWSInboundFrameRateLimitDropsFlood(t *testing.T) {
	h := newLiveHarness(t, func(o *ServerOptions) {
		o.FrameRate = 0.001 // effectively no refill during the test
		o.FrameBurst = 3
	})
	caller, callee := uuid.New(), uuid.New()
	if err := callauth.Set(context.Background(), h.rdb, caller, callee, uuid.New(), callauth.StateActive); err != nil {
		t.Fatal(err)
	}
	sub := h.rdb.Subscribe(context.Background(), "chat:"+callee.String())
	t.Cleanup(func() { _ = sub.Close() })
	if _, err := sub.Receive(context.Background()); err != nil {
		t.Fatal(err)
	}

	conn := h.dial(caller)
	for i := 0; i < 20; i++ {
		h.send(conn, map[string]any{"type": "ice_candidate", "target_user_id": callee.String(), "seq": i})
	}
	got := 0
	ch := sub.Channel()
	timeout := time.After(700 * time.Millisecond)
loop:
	for {
		select {
		case msg := <-ch:
			if strings.Contains(msg.Payload, "ice_candidate") {
				got++
			}
		case <-timeout:
			break loop
		}
	}
	if got != 3 {
		t.Fatalf("relayed %d of 20 flooded frames, want the burst of 3", got)
	}
}
