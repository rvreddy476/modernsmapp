package service

// Audit 7 Nov 2026, live-service-v2 (LV-M1, LV-L1..L4): a ban or block takes
// the viewer out of the room at once, the pinned message has the chat's
// viewer gate, a platform live ban refuses the viewer token, a moderator
// cannot lift the host's bans and mutes, and ?status=all's live phase does
// not end behind hidden streams.

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/atpost/live-service-v2/internal/store/postgres"
)

// ttlLiveKit records the TTL of every viewer token it mints.
type ttlLiveKit struct {
	*recLiveKit
	mu   sync.Mutex
	ttls []time.Duration
}

func (l *ttlLiveKit) IssueViewerToken(_ context.Context, _, _ string, ttl time.Duration) (string, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.ttls = append(l.ttls, ttl)
	return "viewer-token", nil
}

// TestBannedViewerRejoiningIsRemovedAndNotCounted is LV-M1's guard: the ban
// drops the connection, and a participant_joined from the old token removes
// the participant again and is never counted.
func TestBannedViewerRejoiningIsRemovedAndNotCounted(t *testing.T) {
	r, host, st, _ := joinRig(t)
	viewer, other := uuid.New(), uuid.New()
	mustHook(t, r, joinEv(st, viewer.String()))
	if got := r.store.Stream(st.ID).ViewerCount; got != 1 {
		t.Fatalf("viewer_count before the ban = %d", got)
	}
	if err := r.svc.Ban(ctx, st.ID, host, viewer, "harassment"); err != nil {
		t.Fatal(err)
	}
	mustHook(t, r, leaveEv(st, viewer.String())) // LiveKit reports the removal
	want := st.LiveKitRoom + "/" + viewer.String()
	if fmt.Sprint(r.lk.removed) != fmt.Sprint([]string{want}) {
		t.Fatalf("removed after the ban = %v", r.lk.removed)
	}

	// The client reconnects with the token it already holds.
	mustHook(t, r, joinEv(st, viewer.String()))
	if fmt.Sprint(r.lk.removed) != fmt.Sprint([]string{want, want}) {
		t.Fatalf("the rejoin was not removed: %v", r.lk.removed)
	}
	if got := r.store.Stream(st.ID).ViewerCount; got != 0 {
		t.Fatalf("the banned rejoin was counted: viewer_count = %d", got)
	}

	// Anyone else still joins and is counted.
	mustHook(t, r, joinEv(st, other.String()))
	if len(r.lk.removed) != 2 || r.store.Stream(st.ID).ViewerCount != 1 {
		t.Fatalf("an unbanned viewer: removed=%v count=%d", r.lk.removed, r.store.Stream(st.ID).ViewerCount)
	}
	// And the host's own connection is never put through the gate.
	r.store.PlatformBans[host] = postgres.PlatformBan{UserID: host}
	mustHook(t, r, joinEv(st, host.String()))
	if len(r.lk.removed) != 2 {
		t.Fatalf("the host was removed: %v", r.lk.removed)
	}
}

// TestViewerTokenIsShortAndSaysWhenItExpires: LV-M1's backstop. The token
// TTL is minutes, not hours, and the answer carries expires_at.
func TestViewerTokenIsShortAndSaysWhenItExpires(t *testing.T) {
	host, viewer := uuid.New(), uuid.New()
	r := newRig(host)
	r.svc.now = r.clock.Now
	lk := &ttlLiveKit{recLiveKit: r.lk}
	r.svc.livekit = lk
	st := r.store.AddStream(host)
	res, err := r.svc.IssueViewerToken(ctx, st.ID, viewer)
	if err != nil {
		t.Fatal(err)
	}
	if len(lk.ttls) != 1 || lk.ttls[0] != viewerTokenTTL {
		t.Fatalf("ttls = %v", lk.ttls)
	}
	if viewerTokenTTL > 30*time.Minute {
		t.Fatalf("viewer token TTL %v: a banned or blocked viewer could rejoin for that long", viewerTokenTTL)
	}
	if want := r.clock.Now().Add(viewerTokenTTL).UTC(); !res.ExpiresAt.Equal(want) {
		t.Fatalf("expires_at = %v, want %v", res.ExpiresAt, want)
	}
}

// TestViewerTokenRefusesPlatformLiveBan is LV-L2: a platform live ban
// refuses the token, as it refuses the real-time room.
func TestViewerTokenRefusesPlatformLiveBan(t *testing.T) {
	host, viewer := uuid.New(), uuid.New()
	r := newRig(host)
	st := r.store.AddStream(host)
	if _, err := r.svc.IssueViewerToken(ctx, st.ID, viewer); err != nil {
		t.Fatalf("before the ban: %v", err)
	}
	r.store.PlatformBans[viewer] = postgres.PlatformBan{UserID: viewer}
	if _, err := r.svc.IssueViewerToken(ctx, st.ID, viewer); !errors.Is(err, ErrLiveBanned) {
		t.Fatalf("live-banned viewer: %v", err)
	}
	// The host is not refused their own stream's token by this check.
	if _, err := r.svc.IssueViewerToken(ctx, st.ID, host); err != nil {
		t.Fatalf("host: %v", err)
	}
}

// TestPinnedMessageHasTheChatGate is LV-L1.
func TestPinnedMessageHasTheChatGate(t *testing.T) {
	host, viewer, fan := uuid.New(), uuid.New(), uuid.New()
	r := newRig(host)
	pub := r.store.AddStream(host)
	fo := followersOnly(r, host)
	for _, st := range []*postgres.LiveStream{pub, fo} {
		msg, err := r.svc.SendChat(ctx, st.ID, host, "pinned words")
		if err != nil {
			t.Fatal(err)
		}
		if err := r.svc.PinMessage(ctx, st.ID, host, msg.ID); err != nil {
			t.Fatal(err)
		}
	}
	r.graph.follows[fan.String()+":"+host.String()] = true

	read := func(st *postgres.LiveStream, who uuid.UUID) error {
		pinned, err := r.svc.GetPinnedMessage(ctx, st.ID, who)
		if err == nil && (pinned == nil || pinned.Text != "pinned words") {
			t.Fatalf("pinned = %+v", pinned)
		}
		if err != nil && pinned != nil {
			t.Fatalf("a refusal carried the message: %+v", pinned)
		}
		return err
	}
	if err := read(pub, uuid.Nil); err != nil {
		t.Fatalf("signed out, public: %v", err)
	}
	if err := read(fo, uuid.Nil); !errors.Is(err, ErrNotFollower) {
		t.Fatalf("signed out, followers-only: %v", err)
	}
	if err := read(fo, viewer); !errors.Is(err, ErrNotFollower) {
		t.Fatalf("non-follower, followers-only: %v", err)
	}
	if err := read(fo, fan); err != nil {
		t.Fatalf("follower: %v", err)
	}
	if err := read(fo, host); err != nil {
		t.Fatalf("host: %v", err)
	}
	r.graph.blocked[host.String()+":"+viewer.String()] = true // either direction hides
	r.graph.blocked[viewer.String()+":"+host.String()] = true
	if err := read(pub, viewer); !errors.Is(err, ErrViewerBlocked) {
		t.Fatalf("blocked: %v", err)
	}
	delete(r.graph.blocked, viewer.String()+":"+host.String())
	delete(r.graph.blocked, host.String()+":"+viewer.String())
	if err := r.svc.Ban(ctx, pub.ID, host, viewer, ""); err != nil {
		t.Fatal(err)
	}
	if err := read(pub, viewer); !errors.Is(err, ErrBannedFromStream) {
		t.Fatalf("stream-banned: %v", err)
	}
}

// TestModeratorCannotLiftTheHostsBansAndMutes is LV-L3.
func TestModeratorCannotLiftTheHostsBansAndMutes(t *testing.T) {
	host, mod, mod2, v1, v2, v3 := uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New()
	r := newRig(host)
	st := r.store.AddStream(host)
	if _, err := r.svc.SetModerators(ctx, st.ID, host, []uuid.UUID{mod, mod2}); err != nil {
		t.Fatal(err)
	}
	banned := func(u uuid.UUID) bool { ok, _ := r.store.IsBannedFromStream(ctx, st.ID, u); return ok }
	muted := func(u uuid.UUID) bool { ok, _ := r.store.IsUserMuted(ctx, st.ID, u); return ok }

	// The host's ban and mute.
	if err := r.svc.Ban(ctx, st.ID, host, v1, "host's call"); err != nil {
		t.Fatal(err)
	}
	if err := r.svc.Mute(ctx, st.ID, host, v1); err != nil {
		t.Fatal(err)
	}
	if err := r.svc.Unban(ctx, st.ID, mod, v1); !errors.Is(err, ErrNotCreator) {
		t.Fatalf("moderator lifted the host's ban: %v", err)
	}
	if err := r.svc.Unmute(ctx, st.ID, mod, v1); !errors.Is(err, ErrNotCreator) {
		t.Fatalf("moderator lifted the host's mute: %v", err)
	}
	// Re-banning / re-muting does not hand the moderator the row.
	if err := r.svc.Ban(ctx, st.ID, mod, v1, "mine now"); err != nil {
		t.Fatal(err)
	}
	if err := r.svc.Mute(ctx, st.ID, mod, v1); err != nil {
		t.Fatal(err)
	}
	if by, _, _ := r.store.StreamBanBy(ctx, st.ID, v1); by != host {
		t.Fatalf("ban owner after a moderator re-ban = %v, want the host", by)
	}
	if by, _, _ := r.store.MutedBy(ctx, st.ID, v1); by != host {
		t.Fatalf("mute owner after a moderator re-mute = %v, want the host", by)
	}
	if err := r.svc.Unban(ctx, st.ID, mod, v1); !errors.Is(err, ErrNotCreator) {
		t.Fatalf("moderator lifted the host's ban after re-banning: %v", err)
	}
	if err := r.svc.Unmute(ctx, st.ID, mod, v1); !errors.Is(err, ErrNotCreator) {
		t.Fatalf("moderator lifted the host's mute after re-muting: %v", err)
	}
	if !banned(v1) || !muted(v1) {
		t.Fatal("a refused lift changed something")
	}

	// A moderator lifts their own, and another current moderator's.
	if err := r.svc.Ban(ctx, st.ID, mod2, v2, ""); err != nil {
		t.Fatal(err)
	}
	if err := r.svc.Mute(ctx, st.ID, mod2, v2); err != nil {
		t.Fatal(err)
	}
	if err := r.svc.Unban(ctx, st.ID, mod, v2); err != nil {
		t.Fatalf("moderator lifting a moderator's ban: %v", err)
	}
	if err := r.svc.Unmute(ctx, st.ID, mod, v2); err != nil {
		t.Fatalf("moderator lifting a moderator's mute: %v", err)
	}
	if banned(v2) || muted(v2) {
		t.Fatal("the moderator's ban or mute is still in place")
	}

	// A ban someone else placed (an admin resolving a report) is the host's.
	admin := uuid.New()
	if err := r.store.BanFromStream(ctx, st.ID, v3, admin, "report"); err != nil {
		t.Fatal(err)
	}
	if err := r.svc.Unban(ctx, st.ID, mod, v3); !errors.Is(err, ErrNotCreator) {
		t.Fatalf("moderator lifted an admin's ban: %v", err)
	}

	// The host lifts anything.
	for _, u := range []uuid.UUID{v1, v3} {
		if err := r.svc.Unban(ctx, st.ID, host, u); err != nil {
			t.Fatal(err)
		}
	}
	if err := r.svc.Unmute(ctx, st.ID, host, v1); err != nil {
		t.Fatal(err)
	}
	if banned(v1) || banned(v3) || muted(v1) {
		t.Fatal("the host's lift did not land")
	}
	// A stale moderator row naming the host (SetModerators refuses one, but a
	// hand-written or old row may exist) does not make the host's ban a
	// moderator's to lift.
	if err := r.svc.Ban(ctx, st.ID, host, v1, ""); err != nil {
		t.Fatal(err)
	}
	if err := r.store.ReplaceModerators(ctx, st.ID, []uuid.UUID{mod, host}, host); err != nil {
		t.Fatal(err)
	}
	if err := r.svc.Unban(ctx, st.ID, mod, v1); !errors.Is(err, ErrNotCreator) {
		t.Fatalf("moderator lifted the host's ban through a stale row: %v", err)
	}

	// Lifting what is not there stays a no-op for a moderator.
	if err := r.svc.Unban(ctx, st.ID, mod, uuid.New()); err != nil {
		t.Fatalf("unban of nobody: %v", err)
	}
}

// TestBlockRemovesTheViewerFromLiveRooms is LV-M1's block half: a block
// either way between a present viewer and a live host removes the viewer;
// a viewer not in the room, or a block lifted before the event ran, is left
// alone; an undecidable gate removes them.
func TestBlockRemovesTheViewerFromLiveRooms(t *testing.T) {
	cases := map[string]struct {
		blockedByHost bool // else the viewer blocked the host
		present       bool
		stillBlocked  bool
		graphDown     bool
		wantRemoved   bool
	}{
		"host blocks a present viewer":   {blockedByHost: true, present: true, stillBlocked: true, wantRemoved: true},
		"viewer blocks the host":         {present: true, stillBlocked: true, wantRemoved: true},
		"viewer not in the room":         {blockedByHost: true, stillBlocked: true},
		"block lifted before the event":  {blockedByHost: true, present: true},
		"gate undecidable after a block": {blockedByHost: true, present: true, graphDown: true, wantRemoved: true},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			r, host, st, _ := joinRig(t)
			viewer := uuid.New()
			other := r.store.AddStreamStatus(uuid.New(), postgres.StatusLive) // someone else's room
			if tc.present {
				mustHook(t, r, joinEv(st, viewer.String()))
				mustHook(t, r, joinEv(other, viewer.String()))
			}
			if tc.stillBlocked {
				r.graph.blocked[viewer.String()+":"+host.String()] = true
			}
			if tc.graphDown {
				r.graph.err = errors.New("graph-service is down")
			}
			blocker, blocked := viewer, host
			if tc.blockedByHost {
				blocker, blocked = host, viewer
			}
			if err := r.svc.OnUserBlocked(ctx, blocker, blocked); err != nil {
				t.Fatal(err)
			}
			var want []string
			if tc.wantRemoved {
				want = []string{st.LiveKitRoom + "/" + viewer.String()}
			}
			if fmt.Sprint(r.lk.removed) != fmt.Sprint(want) {
				t.Fatalf("removed = %v, want %v", r.lk.removed, want)
			}
		})
	}
}

// TestListAllDoesNotEndBehindHiddenLiveStreams is LV-L4: ?status=all's live
// phase pages past a run of streams the viewer may not see (more than one
// over-fetched batch of them), with one relationship batch per round instead
// of a graph call per row.
func TestListAllDoesNotEndBehindHiddenLiveStreams(t *testing.T) {
	creator, viewer := uuid.New(), uuid.New()
	graph := &batchFakeGraph{fakeGraph: &fakeGraph{follows: map[string]bool{}, blocked: map[string]bool{}}}
	svc, store := newListingService(graph.fakeGraph)
	svc.graph = graph
	for i := 0; i < 6; i++ { // newest first: six the viewer may not see
		store.add(fmt.Sprintf("fans-%d", i), "live", visibilityFollowers, creator, -time.Duration(i+1)*time.Minute)
	}
	store.add("visible-live", "live", visibilityPublic, creator, -time.Hour)
	store.add("soon", "scheduled", visibilityPublic, creator, time.Hour)

	res, err := svc.ListStreams(context.Background(), viewer, StreamStatusAll, 2, "")
	if err != nil {
		t.Fatal(err)
	}
	if got := titles(res.Streams); got != "visible-live,soon" {
		t.Fatalf("first page = %q (the hidden run ended the live phase?)", got)
	}
	if graph.calls != 0 {
		t.Fatalf("per-row relationship calls = %d, want 0 (batched)", graph.calls)
	}
	if graph.batchCalls == 0 || graph.batchCalls > pageRounds+1 {
		t.Fatalf("relationship batches = %d", graph.batchCalls)
	}
	if strings.HasPrefix(res.NextCursor, allLiveCursorPrefix) {
		t.Fatalf("cursor %q resumes the live list it already finished", res.NextCursor)
	}
}
