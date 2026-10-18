package http

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	nethttp "net/http"
	"net/http/httptest"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/atpost/chat-shared/presence"
	"github.com/atpost/chat-shared/roomauth"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

// --- fake clock --------------------------------------------------------

type fakeTimer struct {
	at      time.Time
	f       func()
	stopped bool
	fired   bool
	leaky   bool
}

func (t *fakeTimer) Stop() bool {
	was := !t.stopped && !t.fired
	if t.leaky {
		return false // models a real timer that already fired and waits on the lock
	}
	t.stopped = true
	return was
}

type fakeClock struct {
	mu     sync.Mutex
	now    time.Time
	timers []*fakeTimer
	leaky  bool
}

func newFakeClock() *fakeClock { return &fakeClock{now: time.Unix(1_800_000_000, 0)} }

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) AfterFunc(d time.Duration, f func()) typingTimer {
	c.mu.Lock()
	defer c.mu.Unlock()
	t := &fakeTimer{at: c.now.Add(d), f: f, leaky: c.leaky}
	c.timers = append(c.timers, t)
	return t
}

// Advance moves time forward and fires due timers in order.
func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	c.now = c.now.Add(d)
	var due []*fakeTimer
	for _, t := range c.timers {
		if !t.stopped && !t.fired && !t.at.After(c.now) {
			t.fired = true
			due = append(due, t)
		}
	}
	c.mu.Unlock()
	sort.Slice(due, func(i, j int) bool { return due[i].at.Before(due[j].at) })
	for _, t := range due {
		t.f()
	}
}

// --- harness -----------------------------------------------------------

type typingEmit struct {
	conv     string
	isTyping bool
}

type recorder struct {
	mu     sync.Mutex
	events []typingEmit
}

func (r *recorder) emit(conv string, isTyping bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, typingEmit{conv, isTyping})
}

func (r *recorder) take() []typingEmit {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := r.events
	r.events = nil
	return out
}

type frameHarness struct {
	s      *Server
	subs   *roomSubscriptions
	typing *typingTracker
	clock  *fakeClock
	rec    *recorder
	user   uuid.UUID
	conv   string
	rdb    *redis.Client
}

func newFrameHarness(t *testing.T) *frameHarness {
	t.Helper()
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	s := NewServer(rdb, slog.New(slog.NewTextHandler(io.Discard, nil)), ServerOptions{EnableConversationFrames: true})
	clock := newFakeClock()
	rec := &recorder{}
	h := &frameHarness{
		s: s, subs: newRoomSubscriptions(), clock: clock, rec: rec,
		typing: newTypingTracker(clock, typingTimeout, typingRefresh, rec.emit),
		user:   uuid.New(), conv: uuid.NewString(), rdb: rdb,
	}
	return h
}

func (h *frameHarness) subscribe(expires time.Time) {
	h.subs.set(h.conv, &roomauth.Claims{Subject: h.user.String(), ConversationID: h.conv, ExpiresAt: expires.Unix()})
}

func (h *frameHarness) frame(msgType string) bool {
	return h.s.handleConversationFrame(context.Background(), msgType,
		map[string]any{"type": msgType, "conversation_id": h.conv}, h.user, h.subs, h.typing, h.clock.Now())
}

// --- typing ------------------------------------------------------------

func TestTypingStartRefusedWhenNotSubscribed(t *testing.T) {
	h := newFrameHarness(t)
	if h.frame("typing.start") {
		t.Fatal("typing.start honoured for a room the connection is not subscribed to")
	}
	if got := h.rec.take(); len(got) != 0 {
		t.Fatalf("unsubscribed typing forwarded: %v", got)
	}
	if users, _ := h.s.pres.TypingUsers(context.Background(), h.conv, 10); len(users) != 0 {
		t.Fatalf("unsubscribed typing written to presence: %v", users)
	}
}

func TestTypingStartRefusedWhenSubscriptionExpired(t *testing.T) {
	h := newFrameHarness(t)
	h.subscribe(h.clock.Now().Add(-time.Second))
	if h.frame("typing.start") {
		t.Fatal("typing.start honoured on an expired entitlement")
	}
	if got := h.rec.take(); len(got) != 0 {
		t.Fatalf("expired-entitlement typing forwarded: %v", got)
	}
}

func TestTypingStartAllowedAndAutoStopsAfterSixSeconds(t *testing.T) {
	h := newFrameHarness(t)
	h.subscribe(h.clock.Now().Add(5 * time.Minute))

	if !h.frame("typing.start") {
		t.Fatal("subscribed typing.start refused")
	}
	if got := h.rec.take(); len(got) != 1 || !got[0].isTyping || got[0].conv != h.conv {
		t.Fatalf("first start must forward is_typing:true once, got %v", got)
	}
	// Keystroke frames inside the refresh window are not re-forwarded.
	h.clock.Advance(time.Second)
	h.frame("typing.start")
	if got := h.rec.take(); len(got) != 0 {
		t.Fatalf("repeat start inside refresh window forwarded: %v", got)
	}
	// The second start re-armed the timer: 5s after it, still typing.
	h.clock.Advance(5 * time.Second)
	if got := h.rec.take(); len(got) != 0 {
		t.Fatalf("stopped before 6s of silence: %v", got)
	}
	h.clock.Advance(time.Second)
	if got := h.rec.take(); len(got) != 1 || got[0].isTyping {
		t.Fatalf("6s of silence must forward exactly one is_typing:false, got %v", got)
	}
	// Nothing more fires.
	h.clock.Advance(time.Minute)
	if got := h.rec.take(); len(got) != 0 {
		t.Fatalf("extra frames after timeout: %v", got)
	}
}

func TestTypingRefreshReassertsAfterWindow(t *testing.T) {
	h := newFrameHarness(t)
	h.subscribe(h.clock.Now().Add(5 * time.Minute))
	h.frame("typing.start")
	h.rec.take()
	h.clock.Advance(typingRefresh)
	h.frame("typing.start")
	if got := h.rec.take(); len(got) != 1 || !got[0].isTyping {
		t.Fatalf("continuing typist must re-assert true after the refresh window, got %v", got)
	}
}

func TestTypingStopSendsFalseOnce(t *testing.T) {
	h := newFrameHarness(t)
	h.subscribe(h.clock.Now().Add(5 * time.Minute))
	h.frame("typing.start")
	h.rec.take()
	if !h.frame("typing.stop") {
		t.Fatal("subscribed typing.stop refused")
	}
	if got := h.rec.take(); len(got) != 1 || got[0].isTyping {
		t.Fatalf("stop must forward is_typing:false, got %v", got)
	}
	h.frame("typing.stop")
	h.clock.Advance(time.Minute)
	if got := h.rec.take(); len(got) != 0 {
		t.Fatalf("second stop / cancelled timer forwarded: %v", got)
	}
}

func TestTypingStopAllOnDisconnect(t *testing.T) {
	h := newFrameHarness(t)
	h.subscribe(h.clock.Now().Add(5 * time.Minute))
	h.frame("typing.start")
	h.rec.take()
	h.typing.StopAll()
	if got := h.rec.take(); len(got) != 1 || got[0].isTyping {
		t.Fatalf("disconnect must turn the indicator off, got %v", got)
	}
	h.frame("typing.start")
	if got := h.rec.take(); len(got) != 0 {
		t.Fatalf("start after disconnect forwarded: %v", got)
	}
}

func TestConversationEnterRequiresSubscription(t *testing.T) {
	h := newFrameHarness(t)
	ctx := context.Background()
	if h.frame("conversation.enter") {
		t.Fatal("conversation.enter honoured without a subscription")
	}
	if n, _ := h.s.pres.ActiveCount(ctx, h.conv); n != 0 {
		t.Fatalf("unsubscribed enter wrote presence (count %d)", n)
	}
	h.subscribe(h.clock.Now().Add(5 * time.Minute))
	if !h.frame("conversation.enter") {
		t.Fatal("subscribed conversation.enter refused")
	}
	if n, _ := h.s.pres.ActiveCount(ctx, h.conv); n != 1 {
		t.Fatalf("subscribed enter not recorded (count %d)", n)
	}
}

// The new flag opens ONLY the conversation frames — never the unchecked
// call/update/group-post rooms the scoped-rooms gate still guards.
func TestConversationFramesFlagOpensOnlyConversationFrames(t *testing.T) {
	s := &Server{opts: ServerOptions{EnableConversationFrames: true}}
	for _, f := range []string{"typing.start", "typing.stop", "conversation.enter", "conversation.heartbeat", "conversation.leave"} {
		if s.betaRoomGateRejects(f) {
			t.Errorf("conversation frame %q still refused with the flag on", f)
		}
	}
	for _, f := range []string{"subscribe_call", "call_join", "subscribe_update", "subscribe_group_post", "group_post_typing"} {
		if !s.betaRoomGateRejects(f) {
			t.Errorf("flag must not admit unchecked room frame %q", f)
		}
	}
	off := &Server{opts: ServerOptions{}}
	if !off.betaRoomGateRejects("typing.stop") {
		t.Error("typing.stop must be gated when the flag is off")
	}
}

// --- HTTP notifier + queue ---------------------------------------------

func TestHTTPTypingNotifierCallsMessageServiceWithBearer(t *testing.T) {
	type call struct {
		path, auth string
		isTyping   bool
	}
	var mu sync.Mutex
	var calls []call
	srv := httptest.NewServer(nethttp.HandlerFunc(func(w nethttp.ResponseWriter, r *nethttp.Request) {
		var body struct {
			IsTyping bool `json:"is_typing"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		mu.Lock()
		calls = append(calls, call{r.URL.Path, r.Header.Get("Authorization"), body.IsTyping})
		mu.Unlock()
		w.WriteHeader(nethttp.StatusOK)
	}))
	defer srv.Close()

	conv := uuid.New()
	q := newTypingQueue()
	q.push(typingEvent{conversationID: conv, isTyping: true})
	q.push(typingEvent{conversationID: conv, isTyping: false})
	q.close()
	q.run(NewHTTPTypingNotifier(srv.URL, nil), "tok-123", uuid.New(), slog.New(slog.NewTextHandler(io.Discard, nil)))

	want := "/v1/chat/conversations/" + conv.String() + "/typing"
	if len(calls) != 2 || calls[0].path != want || calls[0].auth != "Bearer tok-123" || !calls[0].isTyping || calls[1].isTyping {
		t.Fatalf("unexpected calls: %+v", calls)
	}
	if q.push(typingEvent{conversationID: conv}) {
		t.Fatal("push after close must be refused, not panic")
	}
}

// --- presence store wiring ---------------------------------------------

func TestPresenceLookupAfterConnect(t *testing.T) {
	h := newFrameHarness(t)
	ctx := context.Background()
	if _, err := h.s.activity.ConnectionOpened(ctx, h.user.String(), "c1", time.Now()); err != nil {
		t.Fatal(err)
	}
	got, err := presence.Lookup(ctx, h.rdb, []string{h.user.String()})
	if err != nil || !got[h.user.String()].Online || got[h.user.String()].LastActiveAt.IsZero() {
		t.Fatalf("connect must mark online + last active: %+v %v", got, err)
	}
}

// A real time.AfterFunc can fire just as a new start re-arms it (Stop then
// returns false). The superseded timer must not stop a typist who is still
// typing: only the latest arming may end the indicator.
func TestSupersededTimerCannotStopActiveTyping(t *testing.T) {
	h := newFrameHarness(t)
	h.clock.leaky = true
	h.subscribe(h.clock.Now().Add(5 * time.Minute))
	h.frame("typing.start")
	h.rec.take()
	h.clock.Advance(3 * time.Second)
	h.frame("typing.start")          // re-arms; the first timer is NOT stopped
	h.clock.Advance(3 * time.Second) // first timer fires (6s after the first start)
	if got := h.rec.take(); len(got) != 0 {
		t.Fatalf("superseded timer stopped an active typist: %v", got)
	}
	h.clock.Advance(3 * time.Second)
	if got := h.rec.take(); len(got) != 1 || got[0].isTyping {
		t.Fatalf("latest timer must stop it, got %v", got)
	}
}
