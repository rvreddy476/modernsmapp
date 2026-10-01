package http

// End-to-end proofs for live rooms over a real websocket, a real (in-
// process) Redis pub/sub and an httptest stand-in for live-service-v2's
// internal viewer route. Each "no frame reached the socket" claim is proved
// by publishing to the room and then to the user's personal channel: Redis
// delivers in order on one subscriber connection, so if the next frame the
// socket reads is the personal marker, the room frame was never relayed.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/google/uuid"
	"github.com/gorilla/websocket"
	"github.com/redis/go-redis/v9"
)

const liveTestInternalKey = "live-test-internal-key"

// fakeLiveService is live-service-v2's internal viewer route.
type fakeLiveService struct {
	mu      sync.Mutex
	allowed map[string]bool // user|stream -> allowed
	broken  bool            // answer 500 for everything
	calls   []string
}

func (f *fakeLiveService) set(user uuid.UUID, stream string, allowed bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.allowed[user.String()+"|"+stream] = allowed
}

func (f *fakeLiveService) callCount() int {
	return len(f.snapshot())
}

func (f *fakeLiveService) snapshot() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.calls...)
}

func (f *fakeLiveService) setBroken(broken bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.broken = broken
}

func (f *fakeLiveService) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("X-Internal-Service-Key") != liveTestInternalKey {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"data":{"allowed":true}}`)) // a 401 must never read as yes
		return
	}
	rest, ok := strings.CutPrefix(r.URL.Path, "/v1/livestream/internal/streams/")
	stream, ok2 := strings.CutSuffix(rest, "/viewer")
	if r.Method != http.MethodGet || !ok || !ok2 {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	user := r.URL.Query().Get("user_id")
	f.mu.Lock()
	f.calls = append(f.calls, user+"|"+stream)
	allowed, broken := f.allowed[user+"|"+stream], f.broken
	f.mu.Unlock()
	if broken {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"data":{"allowed":true}}`))
		return
	}
	_, _ = fmt.Fprintf(w, `{"data":{"allowed":%t}}`, allowed)
}

type liveHarness struct {
	t      *testing.T
	rdb    *redis.Client
	live   *fakeLiveService
	wsURL  string
	secret string
}

func newLiveHarness(t *testing.T, mutate func(*ServerOptions)) *liveHarness {
	t.Helper()
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })

	live := &fakeLiveService{allowed: map[string]bool{}}
	liveSrv := httptest.NewServer(live)
	t.Cleanup(liveSrv.Close)

	secret := "live-rooms-ws-secret"
	opts := ServerOptions{
		JWTSecret:         secret,
		EnableScopedRooms: false, // must not be needed
		EnableLiveRooms:   true,
		LiveViewer:        NewHTTPLiveViewAuthorizer(liveSrv.URL, liveTestInternalKey, liveSrv.Client()),
	}
	if mutate != nil {
		mutate(&opts)
	}
	srv := httptest.NewServer(NewServer(rdb, nil, opts).Routes())
	t.Cleanup(srv.Close)
	return &liveHarness{t: t, rdb: rdb, live: live, wsURL: "ws" + strings.TrimPrefix(srv.URL, "http") + "/v1/ws/connect", secret: secret}
}

func (h *liveHarness) dial(user uuid.UUID) *websocket.Conn {
	h.t.Helper()
	token := signJWT(h.t, map[string]any{"alg": "HS256"}, map[string]any{
		"sub": user.String(), "exp": time.Now().Add(time.Hour).Unix(),
	}, h.secret)
	header := http.Header{}
	header.Set("Authorization", "Bearer "+token)
	conn, _, err := websocket.DefaultDialer.Dial(h.wsURL, header)
	if err != nil {
		h.t.Fatal(err)
	}
	h.t.Cleanup(func() { _ = conn.Close() })
	// The personal channel is subscribed before the read loop starts; wait
	// for it so the marker publish below cannot race the connect.
	h.waitSubscribers("chat:"+user.String(), 1)
	return conn
}

func (h *liveHarness) send(conn *websocket.Conn, frame map[string]any) {
	h.t.Helper()
	raw, _ := json.Marshal(frame)
	if err := conn.WriteMessage(websocket.TextMessage, raw); err != nil {
		h.t.Fatal(err)
	}
}

func (h *liveHarness) waitSubscribers(channel string, want int64) {
	h.t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for {
		got, err := h.rdb.PubSubNumSub(context.Background(), channel).Result()
		if err != nil {
			h.t.Fatal(err)
		}
		if got[channel] == want {
			return
		}
		if time.Now().After(deadline) {
			h.t.Fatalf("%s has %d subscribers, want %d", channel, got[channel], want)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func (h *liveHarness) publish(channel, payload string) {
	h.t.Helper()
	if err := h.rdb.Publish(context.Background(), channel, payload).Err(); err != nil {
		h.t.Fatal(err)
	}
}

func readFrame(t *testing.T, conn *websocket.Conn) string {
	t.Helper()
	_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	_, raw, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("no frame: %v", err)
	}
	return string(raw)
}

func readJSONFrame(t *testing.T, conn *websocket.Conn) map[string]any {
	t.Helper()
	raw := readFrame(t, conn)
	var m map[string]any
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		t.Fatalf("frame %q: %v", raw, err)
	}
	return m
}

// assertNoRoomFrame publishes roomPayload to the live room and then a
// marker to the user's personal channel; the next frame must be the marker.
func (h *liveHarness) assertNoRoomFrame(conn *websocket.Conn, user uuid.UUID, stream, roomPayload string) {
	h.t.Helper()
	h.publish("live:stream:"+stream, roomPayload)
	marker := fmt.Sprintf(`{"type":"message","payload":{"marker":"%s"}}`, uuid.NewString())
	h.publish("chat:"+user.String(), marker)
	if got := readFrame(h.t, conn); got != marker {
		h.t.Fatalf("a frame from a room the user may not watch reached the socket: %s", got)
	}
}

func expectRefusal(t *testing.T, conn *websocket.Conn, stream string) {
	t.Helper()
	m := readJSONFrame(t, conn)
	if m["type"] != "error" || m["code"] != "LIVE_ROOM_REFUSED" || m["stream_id"] != stream {
		t.Fatalf("expected the LIVE_ROOM_REFUSED error frame for %s, got %v", stream, m)
	}
}

func TestLiveRoomAllowedViewerReceivesRoomFrames(t *testing.T) {
	h := newLiveHarness(t, nil)
	viewer, stream := uuid.New(), uuid.NewString()
	h.live.set(viewer, stream, true)

	conn := h.dial(viewer)
	h.send(conn, map[string]any{"type": "subscribe_live_stream", "stream_id": stream})
	h.waitSubscribers("live:stream:"+stream, 1)

	for _, payload := range []string{
		`{"type":"chat.message","payload":{"stream_id":"` + stream + `","text":"hello"}}`,
		`{"type":"viewer.count","payload":{"viewer_count":3}}`,
		`{"type":"status.changed","payload":{"status":"live"}}`,
	} {
		h.publish("live:stream:"+stream, payload)
		if got := readFrame(t, conn); got != payload {
			t.Fatalf("allowed viewer got %q, want %q", got, payload)
		}
	}
	if calls := h.live.snapshot(); len(calls) != 1 || calls[0] != viewer.String()+"|"+stream {
		t.Fatalf("authority asked %v, want exactly the connection's user for that stream", calls)
	}

	// Unsubscribe leaves the room.
	h.send(conn, map[string]any{"type": "unsubscribe_live_stream", "stream_id": stream})
	h.waitSubscribers("live:stream:"+stream, 0)
	h.assertNoRoomFrame(conn, viewer, stream, `{"type":"chat.message","payload":{"text":"after-unsubscribe"}}`)
}

// The host blocked this viewer: live-service-v2 says allowed=false. The
// socket gets the error frame, is not subscribed, and a message published
// to the room (which a permitted viewer DOES receive) never reaches it.
func TestLiveRoomViewerBlockedByHostIsRefusedAndReceivesNothing(t *testing.T) {
	h := newLiveHarness(t, nil)
	permitted, blocked, stream := uuid.New(), uuid.New(), uuid.NewString()
	h.live.set(permitted, stream, true)
	h.live.set(blocked, stream, false)

	okConn := h.dial(permitted)
	h.send(okConn, map[string]any{"type": "subscribe_live_stream", "stream_id": stream})
	h.waitSubscribers("live:stream:"+stream, 1)

	badConn := h.dial(blocked)
	h.send(badConn, map[string]any{"type": "subscribe_live_stream", "stream_id": stream})
	expectRefusal(t, badConn, stream)
	h.waitSubscribers("live:stream:"+stream, 1) // still only the permitted viewer

	payload := `{"type":"chat.message","payload":{"text":"secret-room-chat"}}`
	h.assertNoRoomFrame(badConn, blocked, stream, payload)
	if got := readFrame(t, okConn); got != payload {
		t.Fatalf("the permitted viewer did not get the room frame (so the negative proves nothing): %q", got)
	}
}

func TestLiveRoomUpstreamErrorRefuses(t *testing.T) {
	h := newLiveHarness(t, nil)
	viewer, stream := uuid.New(), uuid.NewString()
	h.live.set(viewer, stream, true) // would be allowed...
	h.live.setBroken(true)           // ...but live-service-v2 answers 500

	conn := h.dial(viewer)
	h.send(conn, map[string]any{"type": "subscribe_live_stream", "stream_id": stream})
	expectRefusal(t, conn, stream)
	h.assertNoRoomFrame(conn, viewer, stream, `{"type":"chat.message","payload":{"text":"x"}}`)
	h.waitSubscribers("live:stream:"+stream, 0)
}

func TestLiveRoomWrongInternalKeyRefuses(t *testing.T) {
	var liveURL string
	h := newLiveHarness(t, func(o *ServerOptions) {
		// Same fake, wrong key: the fake answers 401 with a "yes" body.
		liveURL = o.LiveViewer.(*HTTPLiveViewAuthorizer).baseURL
		o.LiveViewer = NewHTTPLiveViewAuthorizer(liveURL, "wrong-key", nil)
	})
	viewer, stream := uuid.New(), uuid.NewString()
	h.live.set(viewer, stream, true)
	conn := h.dial(viewer)
	h.send(conn, map[string]any{"type": "subscribe_live_stream", "stream_id": stream})
	expectRefusal(t, conn, stream)
	h.assertNoRoomFrame(conn, viewer, stream, `{"type":"chat.message","payload":{"text":"x"}}`)
}

// A host who blocks a viewer mid-stream: the next re-check (every 30s in
// production; 50ms here) evicts the seat and the client is told.
func TestLiveRoomRecheckEvictsAViewerBlockedMidStream(t *testing.T) {
	h := newLiveHarness(t, func(o *ServerOptions) { o.LiveRoomRecheckInterval = 50 * time.Millisecond })
	viewer, stream := uuid.New(), uuid.NewString()
	h.live.set(viewer, stream, true)

	conn := h.dial(viewer)
	h.send(conn, map[string]any{"type": "subscribe_live_stream", "stream_id": stream})
	h.waitSubscribers("live:stream:"+stream, 1)

	h.live.set(viewer, stream, false)
	m := readJSONFrame(t, conn)
	if m["type"] != "subscription_revoked" || m["stream_id"] != stream {
		t.Fatalf("expected subscription_revoked for %s, got %v", stream, m)
	}
	h.waitSubscribers("live:stream:"+stream, 0)
	h.assertNoRoomFrame(conn, viewer, stream, `{"type":"chat.message","payload":{"text":"after-block"}}`)
}

func TestLiveRoomRecheckEvictsWhenAuthorityFails(t *testing.T) {
	h := newLiveHarness(t, func(o *ServerOptions) { o.LiveRoomRecheckInterval = 50 * time.Millisecond })
	viewer, stream := uuid.New(), uuid.NewString()
	h.live.set(viewer, stream, true)
	conn := h.dial(viewer)
	h.send(conn, map[string]any{"type": "subscribe_live_stream", "stream_id": stream})
	h.waitSubscribers("live:stream:"+stream, 1)

	h.live.setBroken(true)
	if m := readJSONFrame(t, conn); m["type"] != "subscription_revoked" {
		t.Fatalf("an undecided re-check kept the seat: %v", m)
	}
	h.waitSubscribers("live:stream:"+stream, 0)
}

// Grants never survive the socket: a reconnect re-asks, and a block that
// landed while the viewer was away is enforced on the new connection.
func TestLiveRoomReconnectReChecks(t *testing.T) {
	h := newLiveHarness(t, nil)
	viewer, stream := uuid.New(), uuid.NewString()
	h.live.set(viewer, stream, true)

	first := h.dial(viewer)
	h.send(first, map[string]any{"type": "subscribe_live_stream", "stream_id": stream})
	h.waitSubscribers("live:stream:"+stream, 1)
	_ = first.Close()
	h.waitSubscribers("live:stream:"+stream, 0)

	// Still allowed: the new socket asks again (no cross-connection cache).
	second := h.dial(viewer)
	h.send(second, map[string]any{"type": "subscribe_live_stream", "stream_id": stream})
	h.waitSubscribers("live:stream:"+stream, 1)
	if n := h.live.callCount(); n != 2 {
		t.Fatalf("reconnect reused the previous socket's yes (%d authority calls)", n)
	}
	_ = second.Close()
	h.waitSubscribers("live:stream:"+stream, 0)

	// Blocked while away, well inside 30s of the last yes: refused.
	h.live.set(viewer, stream, false)
	third := h.dial(viewer)
	h.send(third, map[string]any{"type": "subscribe_live_stream", "stream_id": stream})
	expectRefusal(t, third, stream)
	h.assertNoRoomFrame(third, viewer, stream, `{"type":"chat.message","payload":{"text":"x"}}`)
}

// unsubscribe forgets the grant: a re-subscribe inside 30s re-asks.
func TestLiveRoomUnsubscribeForgetsTheAllow(t *testing.T) {
	h := newLiveHarness(t, nil)
	viewer, stream := uuid.New(), uuid.NewString()
	h.live.set(viewer, stream, true)
	conn := h.dial(viewer)
	h.send(conn, map[string]any{"type": "subscribe_live_stream", "stream_id": stream})
	h.waitSubscribers("live:stream:"+stream, 1)
	h.send(conn, map[string]any{"type": "unsubscribe_live_stream", "stream_id": stream})
	h.waitSubscribers("live:stream:"+stream, 0)

	h.live.set(viewer, stream, false)
	h.send(conn, map[string]any{"type": "subscribe_live_stream", "stream_id": stream})
	expectRefusal(t, conn, stream)
	h.assertNoRoomFrame(conn, viewer, stream, `{"type":"chat.message","payload":{"text":"x"}}`)
}

// Live rooms off: the beta gate refuses the frame, live-service-v2 is not
// asked, and the client still gets the refusal frame.
func TestLiveRoomDisabledRefusesWithoutAsking(t *testing.T) {
	h := newLiveHarness(t, func(o *ServerOptions) { o.EnableLiveRooms = false })
	viewer, stream := uuid.New(), uuid.NewString()
	h.live.set(viewer, stream, true)
	conn := h.dial(viewer)
	h.send(conn, map[string]any{"type": "subscribe_live_stream", "stream_id": stream})
	expectRefusal(t, conn, stream)
	h.assertNoRoomFrame(conn, viewer, stream, `{"type":"chat.message","payload":{"text":"x"}}`)
	if n := h.live.callCount(); n != 0 {
		t.Fatalf("live-service-v2 was asked %d times with live rooms off", n)
	}
}

// Enabling live rooms opens nothing else: every other client-selected room
// is still refused by the scoped-room gate (EnableScopedRooms stays false).
func TestLiveRoomFlagLeavesOtherScopedRoomsGated(t *testing.T) {
	h := newLiveHarness(t, nil)
	viewer := uuid.New()
	conn := h.dial(viewer)
	id := uuid.NewString()

	// A listener that would see group_post_typing relayed into a room the
	// client chose.
	typingSub := h.rdb.Subscribe(context.Background(), "group_post:"+id)
	defer typingSub.Close()
	if _, err := typingSub.Receive(context.Background()); err != nil {
		t.Fatal(err)
	}

	for _, frame := range []map[string]any{
		{"type": "subscribe_post", "post_id": id},
		{"type": "subscribe_call", "call_id": id},
		{"type": "subscribe_update", "update_id": id},
		{"type": "subscribe_group_post", "post_id": id},
		{"type": "group_post_typing", "post_id": id},
		{"type": "call_join", "call_id": id},
	} {
		h.send(conn, frame)
	}
	// Barrier: the read loop handles frames in order, so once this refused
	// live subscribe is answered every frame above has been handled.
	barrier := uuid.NewString()
	h.send(conn, map[string]any{"type": "subscribe_live_stream", "stream_id": barrier})
	expectRefusal(t, conn, barrier)

	for _, channel := range []string{"post:" + id, "call:" + id, "update:" + id} {
		h.waitSubscribers(channel, 0)
	}
	h.waitSubscribers("group_post:"+id, 1) // only the test's own listener

	for _, channel := range []string{"post:" + id, "call:" + id, "update:" + id, "group_post:" + id} {
		h.publish(channel, `{"type":"leak","payload":{"room":"`+channel+`"}}`)
	}
	marker := `{"type":"message","payload":{"marker":"other-rooms"}}`
	h.publish("chat:"+viewer.String(), marker)
	if got := readFrame(t, conn); got != marker {
		t.Fatalf("a scoped room opened on the live-rooms flag: %s", got)
	}

	// The first thing on group_post:<id> must be the test's own leak publish.
	select {
	case first := <-typingSub.Channel():
		if !strings.Contains(first.Payload, `"leak"`) {
			t.Fatalf("group_post_typing was relayed on the live-rooms flag: %s", first.Payload)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("the listener never saw the test's own publish")
	}
	if calls := h.live.snapshot(); len(calls) != 1 || calls[0] != viewer.String()+"|"+barrier {
		t.Fatalf("live-service-v2 was asked about a non-live room: %v", calls)
	}
}
