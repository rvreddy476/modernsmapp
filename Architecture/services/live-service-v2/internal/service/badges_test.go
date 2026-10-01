package service

// Founding creator badge (2 Oct 2026), over the in-memory store on a fake
// clock.

import (
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/atpost/live-service-v2/internal/store/postgres"
)

// onAirFor puts the host's new stream on air for d and returns it, still
// live.
func onAirFor(t *testing.T, r *surfRig, host uuid.UUID, d time.Duration) *postgres.LiveStream {
	t.Helper()
	st := r.store.AddStreamStatus(host, stStarting)
	if err := r.svc.HandleWebhook(ctx, hostEvent(st, "track_published")); err != nil {
		t.Fatal(err)
	}
	mustStatus(t, r.rig, st.ID, stLive, "")
	r.clock.Advance(d)
	return r.store.Stream(st.ID)
}

func hasFounding(t *testing.T, r *surfRig, user uuid.UUID) bool {
	t.Helper()
	list, err := r.svc.UserBadges(ctx, user)
	if err != nil {
		t.Fatal(err)
	}
	for _, b := range list {
		if b.Badge == postgres.BadgeFoundingCreator {
			return true
		}
	}
	return false
}

// TestFoundingBadgeDurationBoundary: 5 minutes on air earns it, a second
// less does not.
func TestFoundingBadgeDurationBoundary(t *testing.T) {
	short, exact := uuid.New(), uuid.New()
	r := newSurfRig(short, exact)

	st := onAirFor(t, r, short, 5*time.Minute-time.Second)
	if _, err := r.svc.EndStream(ctx, st.ID, short); err != nil {
		t.Fatal(err)
	}
	if hasFounding(t, r, short) {
		t.Fatal("4m59s on air earned the badge")
	}

	st = onAirFor(t, r, exact, 5*time.Minute)
	ended, err := r.svc.EndStream(ctx, st.ID, exact)
	if err != nil {
		t.Fatal(err)
	}
	if !hasFounding(t, r, exact) {
		t.Fatal("exactly 5m on air did not earn the badge")
	}
	// The end answer's creator card already carries it.
	if ended.Creator == nil || !reflect.DeepEqual(ended.Creator.Badges, []string{"founding_creator"}) {
		t.Fatalf("end answer's creator card: %+v", ended.Creator)
	}
	list, _ := r.svc.UserBadges(ctx, exact)
	if len(list) != 1 || !list[0].GrantedAt.Equal(*ended.EndedAt) {
		t.Fatalf("granted_at %v, want the stream's end %v", list, ended.EndedAt)
	}
	if row := r.store.Badges[exact]; row == nil || row.StreamID != st.ID {
		t.Fatalf("badge row: %+v", row)
	}

	// LIVE_FOUNDING_MIN_LIVE moves the boundary.
	quick := uuid.New()
	r2 := newSurfRig(quick)
	r2.store.Founding = postgres.FoundingRule{MinLive: time.Minute}
	st = onAirFor(t, r2, quick, time.Minute)
	_, _ = r2.svc.EndStream(ctx, st.ID, quick)
	if !hasFounding(t, r2, quick) {
		t.Fatal("a 1m rule did not grant after 1m on air")
	}
}

// TestFoundingBadgeWindowBoundary: the stream must have STARTED before
// LIVE_FOUNDING_CREATOR_UNTIL.
func TestFoundingBadgeWindowBoundary(t *testing.T) {
	inside, onTheLine, after := uuid.New(), uuid.New(), uuid.New()
	r := newSurfRig(inside, onTheLine, after)
	until := r.clock.Now().Add(time.Hour)
	r.store.Founding = postgres.FoundingRule{Until: &until}

	// Started a second before the window closed; ends long after it.
	r.clock.Advance(time.Hour - time.Second)
	a := onAirFor(t, r, inside, 0)
	// Started exactly at the closing time.
	r.clock.Advance(time.Second)
	b := onAirFor(t, r, onTheLine, 0)
	r.clock.Advance(time.Second)
	c := onAirFor(t, r, after, 0)
	r.clock.Advance(10 * time.Minute)
	for st, host := range map[*postgres.LiveStream]uuid.UUID{a: inside, b: onTheLine, c: after} {
		if _, err := r.svc.EndStream(ctx, st.ID, host); err != nil {
			t.Fatal(err)
		}
	}
	if !hasFounding(t, r, inside) {
		t.Fatal("a stream that started inside the window did not earn the badge")
	}
	if hasFounding(t, r, onTheLine) {
		t.Fatal("a stream that started AT the closing time earned the badge")
	}
	if hasFounding(t, r, after) {
		t.Fatal("a stream that started after the window earned the badge")
	}
}

// TestFoundingBadgeEveryEndGrants: the sweeper, LiveKit and an admin end
// streams too; a stream that never went live, or failed, earns nothing.
func TestFoundingBadgeEveryEndGrants(t *testing.T) {
	swept, finished, stopped, never, failed := uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New()
	r := newSurfRig(swept, finished, stopped, never, failed)

	// The sweeper: host lost, grace runs out -> ended host_lost.
	a := onAirFor(t, r, swept, 5*time.Minute)
	_ = r.svc.HandleWebhook(ctx, hostEvent(a, "participant_left"))
	mustStatus(t, r.rig, a.ID, stReconnecting, "")
	r.clock.Advance(DefaultReconnectGrace + time.Second)
	if err := r.svc.Sweep(ctx); err != nil {
		t.Fatal(err)
	}
	mustStatus(t, r.rig, a.ID, stEnded, ReasonHostLost)
	if !hasFounding(t, r, swept) {
		t.Fatal("a stream the sweeper ended did not earn the badge")
	}

	b := onAirFor(t, r, finished, 6*time.Minute)
	_ = r.svc.HandleWebhook(ctx, WebhookEvent{ID: uuid.NewString(), Event: "room_finished", Room: b.LiveKitRoom})
	mustStatus(t, r.rig, b.ID, stEnded, ReasonRoomFinished)
	if !hasFounding(t, r, finished) {
		t.Fatal("room_finished did not earn the badge")
	}

	c := onAirFor(t, r, stopped, 6*time.Minute)
	if _, err := r.svc.AdminStopStream(ctx, uuid.New(), c.ID, "test"); err != nil {
		t.Fatal(err)
	}
	if !hasFounding(t, r, stopped) {
		t.Fatal("an admin stop did not earn the badge")
	}

	// Scheduled for an hour, never on air, then ended.
	d := r.store.AddStreamStatus(never, stScheduled)
	r.clock.Advance(time.Hour)
	if _, err := r.svc.EndStream(ctx, d.ID, never); err != nil {
		t.Fatal(err)
	}
	if hasFounding(t, r, never) {
		t.Fatal("a stream that was never live earned the badge")
	}
	// Starting for longer than the start timeout: failed(no_media).
	e := r.store.AddStreamStatus(failed, stScheduled)
	if _, err := r.svc.StartStream(ctx, e.ID, failed); err != nil {
		t.Fatal(err)
	}
	r.clock.Advance(10 * time.Minute)
	_ = r.svc.Sweep(ctx)
	mustStatus(t, r.rig, e.ID, stFailed, ReasonNoMedia)
	if hasFounding(t, r, failed) {
		t.Fatal("a failed start earned the badge")
	}
	if len(r.store.Badges) != 3 {
		t.Fatalf("badge rows: %d, want 3", len(r.store.Badges))
	}
}

// TestFoundingBadgeIdempotentAndPermanent: a second qualifying stream
// changes nothing, and a later short one does not take it away.
func TestFoundingBadgeIdempotentAndPermanent(t *testing.T) {
	host := uuid.New()
	r := newSurfRig(host)
	first := onAirFor(t, r, host, 5*time.Minute)
	_, _ = r.svc.EndStream(ctx, first.ID, host)
	row := *r.store.Badges[host]

	r.clock.Advance(time.Hour)
	second := onAirFor(t, r, host, 30*time.Minute)
	_, _ = r.svc.EndStream(ctx, second.ID, host)
	short := onAirFor(t, r, host, time.Second)
	_, _ = r.svc.EndStream(ctx, short.ID, host)

	if got := *r.store.Badges[host]; !reflect.DeepEqual(got, row) {
		t.Fatalf("the badge row changed: %+v -> %+v", row, got)
	}
	if !hasFounding(t, r, host) {
		t.Fatal("a later stream removed the badge")
	}
	// Ending an ended stream again is a no-op too.
	_, _ = r.svc.EndStream(ctx, second.ID, host)
	if got := *r.store.Badges[host]; !reflect.DeepEqual(got, row) {
		t.Fatalf("a repeated end changed the badge: %+v", got)
	}
}

// TestFoundingBadgeRevoke: audited, reason required, and never granted
// again.
func TestFoundingBadgeRevoke(t *testing.T) {
	host, admin := uuid.New(), uuid.New()
	r := newSurfRig(host)
	st := onAirFor(t, r, host, 5*time.Minute)
	_, _ = r.svc.EndStream(ctx, st.ID, host)

	if _, err := r.svc.AdminRevokeBadge(ctx, admin, host, "founding_creator", "  "); !errors.Is(err, ErrReasonRequired) {
		t.Fatalf("revoke without a reason: %v", err)
	}
	if _, err := r.svc.AdminRevokeBadge(ctx, admin, host, "top_fan", "x"); !errors.Is(err, ErrBadgeNotFound) {
		t.Fatalf("unknown badge: %v", err)
	}
	if _, err := r.svc.AdminRevokeBadge(ctx, admin, uuid.New(), "founding_creator", "x"); !errors.Is(err, ErrBadgeNotFound) {
		t.Fatalf("a user without the badge: %v", err)
	}
	if !hasFounding(t, r, host) || len(r.store.Audits) != 0 {
		t.Fatalf("a refused revoke changed something: audits=%d", len(r.store.Audits))
	}

	res, err := r.svc.AdminRevokeBadge(ctx, admin, host, "founding_creator", "bought viewers")
	if err != nil || !res.Revoked || res.UserID != host || res.Badge != "founding_creator" {
		t.Fatalf("revoke: %+v %v", res, err)
	}
	if hasFounding(t, r, host) {
		t.Fatal("a revoked badge is still listed")
	}
	if len(r.store.Audits) != 1 {
		t.Fatalf("audit rows: %d", len(r.store.Audits))
	}
	if a := r.store.Audits[0]; a.Action != AuditUserBadgeRevoke || a.ActorID != admin || a.TargetType != "user" || a.TargetID != host.String() || a.Reason != "bought viewers" {
		t.Fatalf("audit row: %+v", a)
	}
	// Revoking again answers the same and writes no second audit row.
	if res, err := r.svc.AdminRevokeBadge(ctx, admin, host, "founding_creator", "again"); err != nil || !res.Revoked || len(r.store.Audits) != 1 {
		t.Fatalf("second revoke: %+v %v audits=%d", res, err, len(r.store.Audits))
	}
	// A later qualifying stream does not grant it back.
	again := onAirFor(t, r, host, time.Hour)
	ended, _ := r.svc.EndStream(ctx, again.ID, host)
	if hasFounding(t, r, host) {
		t.Fatal("a revoked badge was granted again")
	}
	if ended.Creator == nil || len(ended.Creator.Badges) != 0 {
		t.Fatalf("creator card after a revoke: %+v", ended.Creator)
	}
}

// TestFoundingBadgeHiddenWhileLiveBanned: hidden, not lost.
func TestFoundingBadgeHiddenWhileLiveBanned(t *testing.T) {
	host, fan, admin := uuid.New(), uuid.New(), uuid.New()
	r := newHeartRig(host, fan)
	st := onAirFor(t, r.surfRig, host, 5*time.Minute)
	_, _ = r.svc.EndStream(ctx, st.ID, host)
	// A fan who is a founding creator too, on someone's supporters list.
	fs := onAirFor(t, r.surfRig, fan, 5*time.Minute)
	_, _ = r.svc.EndStream(ctx, fs.ID, fan)
	watch := r.store.AddStream(host)
	_, _ = r.svc.SendHearts(ctx, watch.ID, fan, 3)

	badgesOn := func() (creator, supporter []string) {
		t.Helper()
		got, err := r.svc.GetStream(ctx, watch.ID, uuid.Nil)
		if err != nil {
			t.Fatal(err)
		}
		sup, err := r.svc.ListSupporters(ctx, watch.ID, uuid.Nil, 10)
		if err != nil {
			t.Fatal(err)
		}
		if len(sup) == 1 {
			supporter = sup[0].User.Badges
		}
		return got.Creator.Badges, supporter
	}
	c, s := badgesOn()
	if !reflect.DeepEqual(c, []string{"founding_creator"}) || !reflect.DeepEqual(s, []string{"founding_creator"}) {
		t.Fatalf("badges before any ban: creator %v supporter %v", c, s)
	}

	_ = r.store.AdminSetPlatformBan(ctx, host, true, "x", postgres.AuditEntry{ActorID: admin})
	c, _ = badgesOn()
	if len(c) != 0 || hasFounding(t, r.surfRig, host) {
		t.Fatalf("a live-banned creator still shows the badge: %v", c)
	}
	list, _ := r.svc.UserBadges(ctx, host)
	if list == nil || len(list) != 0 {
		t.Fatalf("badges route while banned: %#v, want an empty list", list)
	}
	_ = r.store.AdminSetPlatformBan(ctx, host, false, "", postgres.AuditEntry{ActorID: admin})
	c, _ = badgesOn()
	if !reflect.DeepEqual(c, []string{"founding_creator"}) || !hasFounding(t, r.surfRig, host) {
		t.Fatalf("the badge did not come back after the unban: %v", c)
	}
	// Nobody: an empty list.
	none, err := r.svc.UserBadges(ctx, uuid.New())
	if err != nil || none == nil || len(none) != 0 {
		t.Fatalf("a user with no badges: %#v %v", none, err)
	}
}

func TestParseFoundingUntil(t *testing.T) {
	for _, raw := range []string{"", "   "} {
		if got, err := ParseFoundingUntil(raw); err != nil || got != nil {
			t.Fatalf("%q: %v %v, want the window open", raw, got, err)
		}
	}
	got, err := ParseFoundingUntil(" 2026-12-31T23:59:59+05:30 ")
	if err != nil || got == nil || !got.Equal(time.Date(2026, 12, 31, 18, 29, 59, 0, time.UTC)) {
		t.Fatalf("RFC3339: %v %v", got, err)
	}
	for _, bad := range []string{"2026-12-31", "tomorrow", "31/12/2026", "1767225599"} {
		if got, err := ParseFoundingUntil(bad); err == nil {
			t.Fatalf("%q parsed as %v; an unparseable window must refuse to boot", bad, got)
		}
	}
}

func TestFoundingRuleQualifies(t *testing.T) {
	t0 := time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)
	at := func(d time.Duration) *time.Time { v := t0.Add(d); return &v }
	rule := postgres.FoundingRule{}
	cases := []struct {
		name string
		st   *postgres.LiveStream
		want bool
	}{
		{"nil", nil, false},
		{"ended, 5m", &postgres.LiveStream{Status: stEnded, StartedAt: at(0), EndedAt: at(5 * time.Minute)}, true},
		{"ended, 4m59s", &postgres.LiveStream{Status: stEnded, StartedAt: at(0), EndedAt: at(5*time.Minute - time.Second)}, false},
		{"failed, 10m", &postgres.LiveStream{Status: stFailed, StartedAt: at(0), EndedAt: at(10 * time.Minute)}, false},
		{"still live", &postgres.LiveStream{Status: stLive, StartedAt: at(0)}, false},
		{"ended, never started", &postgres.LiveStream{Status: stEnded, EndedAt: at(time.Hour)}, false},
	}
	for _, tc := range cases {
		if got := rule.Qualifies(tc.st); got != tc.want {
			t.Fatalf("%s: %v, want %v", tc.name, got, tc.want)
		}
	}
}
