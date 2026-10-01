package service

// Free hearts and top supporters (2 Oct 2026), over the in-memory store.

import (
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/atpost/live-service-v2/internal/store/postgres"
	"github.com/atpost/live-service-v2/internal/storetest"
)

// heartRig captures the trailing flush instead of waiting a second for it.
type heartRig struct {
	*surfRig
	timers []func()
}

func newHeartRig(pilot ...uuid.UUID) *heartRig {
	h := &heartRig{surfRig: newSurfRig(pilot...)}
	h.svc.heartState().after = func(_ time.Duration, f func()) { h.timers = append(h.timers, f) }
	return h
}

// fire runs the armed trailing flushes.
func (h *heartRig) fire() {
	pending := h.timers
	h.timers = nil
	for _, f := range pending {
		f()
	}
}

func TestSendHeartsCountRange(t *testing.T) {
	host, viewer := uuid.New(), uuid.New()
	r := newHeartRig(host)
	st := r.store.AddStream(host)
	for _, n := range []int{0, -1, 21, 1000} {
		if _, err := r.svc.SendHearts(ctx, st.ID, viewer, n); !errors.Is(err, ErrInvalidHeartCount) {
			t.Fatalf("count %d: %v, want ErrInvalidHeartCount", n, err)
		}
	}
	if got := r.store.Stream(st.ID).HeartCount; got != 0 {
		t.Fatalf("a refused batch counted: %d", got)
	}
	total, err := r.svc.SendHearts(ctx, st.ID, viewer, 1)
	if err != nil || total != 1 {
		t.Fatalf("count 1: %d %v", total, err)
	}
	total, err = r.svc.SendHearts(ctx, st.ID, viewer, HeartsMaxPerRequest)
	if err != nil || total != 21 {
		t.Fatalf("count 20: %d %v", total, err)
	}
	// Every row carries heart_count.
	got, _ := r.svc.GetStream(ctx, st.ID, uuid.Nil)
	if got.HeartCount != 21 {
		t.Fatalf("heart_count on the row: %d", got.HeartCount)
	}
	if _, err := r.svc.SendHearts(ctx, uuid.New(), viewer, 1); !errors.Is(err, ErrStreamNotFound) {
		t.Fatalf("unknown stream: %v", err)
	}
}

// TestSendHeartsViewerCheck: the full viewer gate, then the platform ban.
func TestSendHeartsViewerCheck(t *testing.T) {
	host, follower, stranger, blocked, banned, liveBanned := uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New()
	r := newHeartRig(host)
	st := followersOnly(r.rig, host)
	for _, u := range []uuid.UUID{follower, banned, liveBanned} {
		r.graph.follows[u.String()+":"+host.String()] = true
	}
	r.graph.blocked[blocked.String()+":"+host.String()] = true
	_ = r.store.BanFromStream(ctx, st.ID, banned, host, "")
	_ = r.store.AdminSetPlatformBan(ctx, liveBanned, true, "x", postgres.AuditEntry{ActorID: uuid.New()})

	cases := []struct {
		name string
		user uuid.UUID
		want error
	}{
		{"non-follower", stranger, ErrNotFollower},
		{"blocked", blocked, ErrViewerBlocked},
		{"banned from the stream", banned, ErrBannedFromStream},
		{"platform live-banned", liveBanned, ErrLiveBanned},
	}
	for _, tc := range cases {
		if _, err := r.svc.SendHearts(ctx, st.ID, tc.user, 5); !errors.Is(err, tc.want) {
			t.Fatalf("%s: %v, want %v", tc.name, err, tc.want)
		}
	}
	if got := r.store.Stream(st.ID).HeartCount; got != 0 {
		t.Fatalf("a refused viewer's hearts counted: %d", got)
	}
	if total, err := r.svc.SendHearts(ctx, st.ID, follower, 5); err != nil || total != 5 {
		t.Fatalf("follower: %d %v", total, err)
	}
	r.graph.err = errors.New("graph down")
	if _, err := r.svc.SendHearts(ctx, st.ID, uuid.New(), 1); !errors.Is(err, ErrAuthorityUnavailable) {
		t.Fatalf("graph down: %v", err)
	}
}

func TestSendHeartsOnlyWhileOnAir(t *testing.T) {
	host, viewer := uuid.New(), uuid.New()
	r := newHeartRig(host)
	for _, status := range []string{stScheduled, stStarting, stEnded, stFailed} {
		st := r.store.AddStreamStatus(host, status)
		if _, err := r.svc.SendHearts(ctx, st.ID, viewer, 1); !errors.Is(err, ErrStreamNotLive) {
			t.Fatalf("hearts on a %s stream: %v", status, err)
		}
		if got := r.store.Stream(st.ID).HeartCount; got != 0 {
			t.Fatalf("a %s stream counted hearts", status)
		}
	}
	for _, status := range []string{stLive, stReconnecting} {
		st := r.store.AddStreamStatus(host, status)
		if total, err := r.svc.SendHearts(ctx, st.ID, viewer, 2); err != nil || total != 2 {
			t.Fatalf("hearts on a %s stream: %d %v", status, total, err)
		}
	}
	// Refused before the rate limit: hearts sent at a stream that is not on
	// air do not use up the sender's budget for when it is.
	early := r.store.AddStreamStatus(host, stScheduled)
	for i := 0; i < 4; i++ {
		if _, err := r.svc.SendHearts(ctx, early.ID, viewer, 20); !errors.Is(err, ErrStreamNotLive) {
			t.Fatalf("batch %d at a scheduled stream: %v", i, err)
		}
	}
	r.store.Streams[early.ID].Status = stLive
	if total, err := r.svc.SendHearts(ctx, early.ID, viewer, 20); err != nil || total != 20 {
		t.Fatalf("first batch once live: %d %v — refused batches used the budget", total, err)
	}
}

// TestSendHeartsRateLimit: 60 hearts per 10 s per user per stream.
func TestSendHeartsRateLimit(t *testing.T) {
	host, viewer, other := uuid.New(), uuid.New(), uuid.New()
	r := newHeartRig(host)
	st := r.store.AddStream(host)
	st2 := r.store.AddStream(host)
	for i := 0; i < 3; i++ {
		if _, err := r.svc.SendHearts(ctx, st.ID, viewer, 20); err != nil {
			t.Fatalf("batch %d inside the limit: %v", i, err)
		}
	}
	if _, err := r.svc.SendHearts(ctx, st.ID, viewer, 1); !errors.Is(err, ErrHeartsRateLimited) {
		t.Fatalf("the 61st heart: %v, want ErrHeartsRateLimited", err)
	}
	if got := r.store.Stream(st.ID).HeartCount; got != 60 {
		t.Fatalf("a rate-limited batch counted: %d", got)
	}
	// Per user and per stream.
	if _, err := r.svc.SendHearts(ctx, st.ID, other, 20); err != nil {
		t.Fatalf("another user was limited: %v", err)
	}
	if _, err := r.svc.SendHearts(ctx, st2.ID, viewer, 20); err != nil {
		t.Fatalf("another stream was limited: %v", err)
	}
	// Still refused just inside the window; allowed once it has passed.
	r.clock.Advance(heartRateWindow - time.Second)
	if _, err := r.svc.SendHearts(ctx, st.ID, viewer, 1); !errors.Is(err, ErrHeartsRateLimited) {
		t.Fatalf("inside the window: %v", err)
	}
	r.clock.Advance(2 * time.Second)
	if total, err := r.svc.SendHearts(ctx, st.ID, viewer, 20); err != nil || total != 100 {
		t.Fatalf("after the window: %d %v", total, err)
	}
}

// TestHeartsPerUserCap: past 10,000 a viewer's hearts are accepted and
// ignored.
func TestHeartsPerUserCap(t *testing.T) {
	host, viewer, other := uuid.New(), uuid.New(), uuid.New()
	r := newHeartRig(host)
	st := r.store.AddStream(host)
	r.store.Hearts[st.ID] = map[uuid.UUID]*storetest.HeartRow{viewer: {Hearts: HeartsPerUserCap - 5, CreatedAt: r.clock.Now()}}
	r.store.Streams[st.ID].HeartCount = HeartsPerUserCap - 5

	total, err := r.svc.SendHearts(ctx, st.ID, viewer, 20)
	if err != nil || total != HeartsPerUserCap {
		t.Fatalf("crossing the cap: %d %v, want %d", total, err, HeartsPerUserCap)
	}
	total, err = r.svc.SendHearts(ctx, st.ID, viewer, 20)
	if err != nil || total != HeartsPerUserCap {
		t.Fatalf("past the cap: %d %v — accepted and ignored", total, err)
	}
	if got := r.store.Hearts[st.ID][viewer].Hearts; got != HeartsPerUserCap {
		t.Fatalf("the viewer's row: %d", got)
	}
	// The cap is per viewer, not per stream.
	total, err = r.svc.SendHearts(ctx, st.ID, other, 3)
	if err != nil || total != HeartsPerUserCap+3 {
		t.Fatalf("another viewer after the first hit the cap: %d %v", total, err)
	}
	// Only the 5 that counted, then the other viewer's 3, were announced.
	r.clock.Advance(heartFrameWindow)
	r.fire()
	sum := 0
	for _, f := range r.ev.ofType(EventHearts) {
		sum += int(f["count"].(float64))
	}
	if sum != 8 {
		t.Fatalf("frames announced %d hearts, want 8", sum)
	}
}

// TestHeartsFrameAggregated: at most one frame per second per stream,
// carrying what was counted since the last one, and no user ids.
func TestHeartsFrameAggregated(t *testing.T) {
	host, a, b := uuid.New(), uuid.New(), uuid.New()
	r := newHeartRig(host)
	st := r.store.AddStream(host)
	other := r.store.AddStream(host)

	_, _ = r.svc.SendHearts(ctx, st.ID, a, 3)
	frames := r.ev.ofType(EventHearts)
	if len(frames) != 1 || frames[0]["count"].(float64) != 3 || frames[0]["heart_count"].(float64) != 3 || frames[0]["stream_id"] != st.ID.String() {
		t.Fatalf("first frame: %v", frames)
	}
	// Inside the same second: counted, not announced yet.
	_, _ = r.svc.SendHearts(ctx, st.ID, b, 4)
	_, _ = r.svc.SendHearts(ctx, st.ID, a, 5)
	if n := len(r.ev.ofType(EventHearts)); n != 1 {
		t.Fatalf("%d frames inside one second, want 1", n)
	}
	if len(r.timers) != 1 {
		t.Fatalf("%d trailing flushes armed, want 1", len(r.timers))
	}
	// Another stream has its own second.
	_, _ = r.svc.SendHearts(ctx, other.ID, a, 2)
	if n := len(r.ev.ofType(EventHearts)); n != 2 {
		t.Fatalf("another stream's frame was throttled by this one: %d", n)
	}
	// The trailing flush announces the rest in one frame, with the total
	// read back from the store.
	r.clock.Advance(heartFrameWindow)
	r.fire()
	frames = r.ev.ofType(EventHearts)
	if len(frames) != 3 {
		t.Fatalf("frames after the flush: %d", len(frames))
	}
	last := frames[2]
	if last["count"].(float64) != 9 || last["heart_count"].(float64) != 12 || last["stream_id"] != st.ID.String() {
		t.Fatalf("trailing frame: %v", last)
	}
	// Nothing pending: a late timer says nothing.
	r.clock.Advance(heartFrameWindow)
	r.svc.flushHearts(ctx, st.ID, 0, false)
	if n := len(r.ev.ofType(EventHearts)); n != 3 {
		t.Fatalf("an empty flush published a frame: %d", n)
	}
	// A flush that loses the second to another frame re-arms instead of
	// dropping what is pending.
	r.clock.Advance(heartFrameWindow)
	_, _ = r.svc.SendHearts(ctx, st.ID, a, 1) // publishes (new second)
	_, _ = r.svc.SendHearts(ctx, st.ID, a, 1) // pending, armed
	r.fire()                                  // still inside the second
	if len(r.timers) != 1 {
		t.Fatalf("a throttled flush did not re-arm: %d timers", len(r.timers))
	}
	r.clock.Advance(heartFrameWindow)
	r.fire()
	frames = r.ev.ofType(EventHearts)
	if got := frames[len(frames)-1]; got["count"].(float64) != 1 || got["heart_count"].(float64) != 14 {
		t.Fatalf("re-armed frame: %v", got)
	}

	// No frame names anyone.
	for _, f := range frames {
		raw, _ := json.Marshal(f)
		for _, u := range []uuid.UUID{a, b, host} {
			if strings.Contains(string(raw), u.String()) {
				t.Fatalf("a hearts frame carries a user id: %s", raw)
			}
		}
		payload := f["payload"].(map[string]any)
		keys := []string{}
		for k := range payload {
			keys = append(keys, k)
		}
		if len(keys) != 3 {
			t.Fatalf("frame payload keys: %v, want stream_id, count, heart_count", keys)
		}
	}
}

func TestHostHeartsCountButHostIsNoSupporter(t *testing.T) {
	host, viewer := uuid.New(), uuid.New()
	r := newHeartRig(host)
	st := r.store.AddStream(host)
	if total, err := r.svc.SendHearts(ctx, st.ID, host, 7); err != nil || total != 7 {
		t.Fatalf("the host's hearts: %d %v", total, err)
	}
	_, _ = r.svc.SendChat(ctx, st.ID, host, "welcome")
	_, _ = r.svc.SendHearts(ctx, st.ID, viewer, 2)
	rows, err := r.svc.ListSupporters(ctx, st.ID, uuid.Nil, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].User.UserID != viewer {
		t.Fatalf("supporters: %+v", rows)
	}
	if got := r.store.Stream(st.ID).HeartCount; got != 9 {
		t.Fatalf("total: %d, want the host's 7 and the viewer's 2", got)
	}
}

// TestSupportersRanking: hearts, then messages not removed, then who was
// active first; the banned and the empty-handed are left out.
func TestSupportersRanking(t *testing.T) {
	host := uuid.New()
	r := newHeartRig(host)
	st := r.store.AddStream(host)
	u := map[string]uuid.UUID{}
	for _, n := range []string{"top", "chatty", "early", "late", "msgOnly", "removedOnly", "streamBanned", "liveBanned", "lurker"} {
		u[n] = uuid.New()
	}
	r.profiles.m[u["top"]] = Profile{Name: "Top Fan", Handle: "top", AvatarURL: "/a/top"}
	hearts := func(name string, n int) {
		t.Helper()
		if _, err := r.svc.SendHearts(ctx, st.ID, u[name], n); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		r.clock.Advance(time.Second)
	}
	chat := func(name, text string) *postgres.ChatMessage {
		t.Helper()
		m, err := r.svc.SendChat(ctx, st.ID, u[name], text)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		r.clock.Advance(time.Second)
		return m
	}
	hearts("early", 5)  // 5 hearts, 1 message, first active earliest
	hearts("late", 5)   // 5 hearts, 1 message, active later
	hearts("chatty", 5) // 5 hearts, 2 messages
	hearts("top", 20)   // most hearts
	chat("early", "hi")
	chat("late", "hi")
	chat("chatty", "one")
	chat("chatty", "two")
	chat("msgOnly", "no hearts, one message")
	gone := chat("removedOnly", "spam")
	if _, err := r.store.RemoveChatMessage(ctx, st.ID, gone.ID, host); err != nil {
		t.Fatal(err)
	}
	// chatty's third message is removed too: it must not count.
	extra := chat("chatty", "three")
	_, _ = r.store.RemoveChatMessage(ctx, st.ID, extra.ID, host)
	hearts("streamBanned", 19)
	hearts("liveBanned", 18)
	_ = r.store.BanFromStream(ctx, st.ID, u["streamBanned"], host, "")
	_ = r.store.AdminSetPlatformBan(ctx, u["liveBanned"], true, "x", postgres.AuditEntry{ActorID: uuid.New()})

	rows, err := r.svc.ListSupporters(ctx, st.ID, uuid.Nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	type row struct {
		name             string
		hearts, messages int
	}
	want := []row{{"top", 20, 0}, {"chatty", 5, 2}, {"early", 5, 1}, {"late", 5, 1}, {"msgOnly", 0, 1}}
	if len(rows) != len(want) {
		t.Fatalf("supporters: %d rows, want %d: %+v", len(rows), len(want), rows)
	}
	for i, w := range want {
		got := rows[i]
		if got.User == nil || got.User.UserID != u[w.name] || got.Hearts != w.hearts || got.Messages != w.messages || got.Rank != i+1 {
			t.Fatalf("rank %d: %+v (user %+v), want %s %d/%d", i+1, got, got.User, w.name, w.hearts, w.messages)
		}
	}
	if !reflect.DeepEqual(*rows[0].User, postgres.UserCard{UserID: u["top"], Name: "Top Fan", Handle: "top", AvatarURL: "/a/top"}) {
		t.Fatalf("supporter card: %+v", rows[0].User)
	}
	if !reflect.DeepEqual(*rows[1].User, postgres.UserCard{UserID: u["chatty"]}) {
		t.Fatalf("a supporter without a profile: %+v", rows[1].User)
	}

	// limit: default 10, at most 50.
	for limit, n := range map[int]int{1: 1, 3: 3, 0: 5, -1: 5, 50: 5, 500: 5} {
		got, _ := r.svc.ListSupporters(ctx, st.ID, uuid.Nil, limit)
		if len(got) != n {
			t.Fatalf("limit %d: %d rows, want %d", limit, len(got), n)
		}
	}
	big := r.store.AddStream(host)
	for i := 0; i < 60; i++ {
		_, _ = r.svc.SendHearts(ctx, big.ID, uuid.New(), 1)
	}
	for limit, n := range map[int]int{0: 10, 50: 50, 51: 50, 1000: 50} {
		got, _ := r.svc.ListSupporters(ctx, big.ID, uuid.Nil, limit)
		if len(got) != n {
			t.Fatalf("limit %d of 60 supporters: %d rows, want %d", limit, len(got), n)
		}
	}

	// It works after the stream ended.
	if _, err := r.svc.EndStream(ctx, st.ID, host); err != nil {
		t.Fatal(err)
	}
	after, err := r.svc.ListSupporters(ctx, st.ID, uuid.Nil, 10)
	if err != nil || len(after) != len(want) || after[0].User.UserID != u["top"] {
		t.Fatalf("after the stream ended: %+v %v", after, err)
	}
	// Nobody yet: an empty list, not null.
	fresh := r.store.AddStream(host)
	none, err := r.svc.ListSupporters(ctx, fresh.ID, uuid.Nil, 10)
	if err != nil || none == nil || len(none) != 0 {
		t.Fatalf("no supporters: %#v %v", none, err)
	}
}

// TestSupportersViewerCheck: the chat list's gate.
func TestSupportersViewerCheck(t *testing.T) {
	host, follower, stranger, banned := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	r := newHeartRig(host)
	st := followersOnly(r.rig, host)
	r.graph.follows[follower.String()+":"+host.String()] = true
	r.graph.follows[banned.String()+":"+host.String()] = true
	_ = r.store.BanFromStream(ctx, st.ID, banned, host, "")
	_, _ = r.svc.SendHearts(ctx, st.ID, follower, 1)

	if rows, err := r.svc.ListSupporters(ctx, st.ID, follower, 10); err != nil || len(rows) != 1 {
		t.Fatalf("follower: %+v %v", rows, err)
	}
	if _, err := r.svc.ListSupporters(ctx, st.ID, host, 10); err != nil {
		t.Fatalf("host: %v", err)
	}
	if _, err := r.svc.ListSupporters(ctx, st.ID, stranger, 10); !errors.Is(err, ErrNotFollower) {
		t.Fatalf("non-follower: %v", err)
	}
	if _, err := r.svc.ListSupporters(ctx, st.ID, uuid.Nil, 10); !errors.Is(err, ErrNotFollower) {
		t.Fatalf("signed out on a followers-only stream: %v", err)
	}
	if _, err := r.svc.ListSupporters(ctx, st.ID, banned, 10); !errors.Is(err, ErrBannedFromStream) {
		t.Fatalf("banned: %v", err)
	}
	if _, err := r.svc.ListSupporters(ctx, uuid.New(), follower, 10); !errors.Is(err, ErrStreamNotFound) {
		t.Fatalf("unknown stream: %v", err)
	}
}
