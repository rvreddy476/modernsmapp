package service

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/atpost/live-service-v2/internal/store/postgres"
)

func followersOnly(r *rig, host uuid.UUID) *postgres.LiveStream {
	st := r.store.AddStream(host)
	r.store.Streams[st.ID].Visibility = visibilityFollowers
	return r.store.Stream(st.ID)
}

// TestChatAuthorization: send and list both pass the viewer gate.
func TestChatAuthorization(t *testing.T) {
	host, viewer := uuid.New(), uuid.New()
	r := newRig(host)
	pub := r.store.AddStream(host)

	if _, err := r.svc.SendChat(ctx, pub.ID, viewer, "hi"); err != nil {
		t.Fatalf("public chat: %v", err)
	}
	if _, err := r.svc.ListChat(ctx, pub.ID, uuid.Nil, 50); err != nil {
		t.Fatalf("signed-out list of a public stream: %v", err)
	}

	// Blocked (either direction) -> hidden.
	r.graph.blocked[viewer.String()+":"+host.String()] = true
	if _, err := r.svc.SendChat(ctx, pub.ID, viewer, "hi"); !errors.Is(err, ErrViewerBlocked) {
		t.Fatalf("blocked send: %v", err)
	}
	if _, err := r.svc.ListChat(ctx, pub.ID, viewer, 50); !errors.Is(err, ErrViewerBlocked) {
		t.Fatalf("blocked list: %v", err)
	}
	delete(r.graph.blocked, viewer.String()+":"+host.String())

	// Followers-only.
	fo := followersOnly(r, host)
	if _, err := r.svc.SendChat(ctx, fo.ID, viewer, "hi"); !errors.Is(err, ErrNotFollower) {
		t.Fatalf("non-follower send: %v", err)
	}
	if _, err := r.svc.ListChat(ctx, fo.ID, viewer, 50); !errors.Is(err, ErrNotFollower) {
		t.Fatalf("non-follower list: %v", err)
	}
	if _, err := r.svc.ListChat(ctx, fo.ID, uuid.Nil, 50); !errors.Is(err, ErrNotFollower) {
		t.Fatalf("signed-out list of a followers stream: %v", err)
	}
	r.graph.follows[viewer.String()+":"+host.String()] = true
	if _, err := r.svc.SendChat(ctx, fo.ID, viewer, "hi"); err != nil {
		t.Fatalf("follower send: %v", err)
	}

	// Stream ban.
	if err := r.svc.Ban(ctx, pub.ID, host, viewer, "spam"); err != nil {
		t.Fatal(err)
	}
	if _, err := r.svc.SendChat(ctx, pub.ID, viewer, "hi"); !errors.Is(err, ErrBannedFromStream) {
		t.Fatalf("banned send: %v", err)
	}
	if _, err := r.svc.ListChat(ctx, pub.ID, viewer, 50); !errors.Is(err, ErrBannedFromStream) {
		t.Fatalf("banned list: %v", err)
	}
	if _, err := r.svc.IssueViewerToken(ctx, pub.ID, viewer); !errors.Is(err, ErrBannedFromStream) {
		t.Fatalf("banned token: %v", err)
	}

	// Platform live ban: no chat anywhere.
	other := uuid.New()
	r.store.PlatformBans[other] = postgres.PlatformBan{UserID: other}
	if _, err := r.svc.SendChat(ctx, pub.ID, other, "hi"); !errors.Is(err, ErrLiveBanned) {
		t.Fatalf("live-banned send: %v", err)
	}

	// Graph down: refused, not waved through.
	r.graph.err = errors.New("graph down")
	if _, err := r.svc.SendChat(ctx, pub.ID, uuid.New(), "hi"); !errors.Is(err, ErrAuthorityUnavailable) {
		t.Fatalf("graph down: %v", err)
	}
	r.graph.err = nil

	// Not on air.
	starting := r.store.AddStreamStatus(host, stStarting)
	if _, err := r.svc.SendChat(ctx, starting.ID, uuid.New(), "hi"); !errors.Is(err, ErrStreamNotLive) {
		t.Fatalf("chat on a starting stream: %v", err)
	}
}

// TestMayWatch is the ws-gateway's question.
func TestMayWatch(t *testing.T) {
	host, viewer := uuid.New(), uuid.New()
	r := newRig(host)
	st := r.store.AddStream(host)
	check := func(want bool, label string) {
		t.Helper()
		got, err := r.svc.MayWatch(ctx, st.ID, viewer)
		if err != nil || got != want {
			t.Fatalf("%s: got %v %v want %v", label, got, err, want)
		}
	}
	check(true, "plain viewer")
	r.graph.blocked[viewer.String()+":"+host.String()] = true
	check(false, "blocked by the host")
	delete(r.graph.blocked, viewer.String()+":"+host.String())
	r.store.BanFromStream(ctx, st.ID, viewer, host, "")
	check(false, "stream-banned")
	r.store.UnbanFromStream(ctx, st.ID, viewer)
	r.store.PlatformBans[viewer] = postgres.PlatformBan{UserID: viewer}
	check(false, "platform live-banned")
	delete(r.store.PlatformBans, viewer)
	check(true, "unbanned")
	if got, err := r.svc.MayWatch(ctx, uuid.New(), viewer); got || err != nil {
		t.Fatalf("missing stream: %v %v", got, err)
	}
	r.graph.err = errors.New("down")
	if got, err := r.svc.MayWatch(ctx, st.ID, viewer); got || !errors.Is(err, ErrAuthorityUnavailable) {
		t.Fatalf("graph down: %v %v", got, err)
	}
}

// TestRemoveMessageRules: host and moderators remove; anyone else is
// refused; removed messages leave the list and fire chat.removed.
func TestRemoveMessageRules(t *testing.T) {
	host, mod, viewer := uuid.New(), uuid.New(), uuid.New()
	r := newRig(host)
	st := r.store.AddStream(host)
	if _, err := r.svc.SetModerators(ctx, st.ID, host, []uuid.UUID{mod}); err != nil {
		t.Fatal(err)
	}
	m1, _ := r.svc.SendChat(ctx, st.ID, viewer, "one")
	m2, _ := r.svc.SendChat(ctx, st.ID, viewer, "two")

	if err := r.svc.RemoveChatMessage(ctx, st.ID, viewer, m1.ID); !errors.Is(err, ErrNotModerator) {
		t.Fatalf("viewer removed a message: %v", err)
	}
	if err := r.svc.RemoveChatMessage(ctx, st.ID, mod, m1.ID); err != nil {
		t.Fatal(err)
	}
	if err := r.svc.RemoveChatMessage(ctx, st.ID, host, m2.ID); err != nil {
		t.Fatal(err)
	}
	if err := r.svc.RemoveChatMessage(ctx, st.ID, host, m2.ID); err != nil {
		t.Fatalf("second removal is a no-op: %v", err)
	}
	if err := r.svc.RemoveChatMessage(ctx, st.ID, host, uuid.New()); !errors.Is(err, ErrMessageNotFound) {
		t.Fatalf("unknown message: %v", err)
	}
	items, _ := r.svc.ListChat(ctx, st.ID, viewer, 50)
	if len(items) != 0 {
		t.Fatalf("removed messages still listed: %d", len(items))
	}
	evs := r.ev.ofType(EventChatRemoved)
	if len(evs) != 2 || evs[0]["message_id"] != m1.ID.String() || evs[0]["by_role"] != RoleModerator {
		t.Fatalf("chat.removed = %v", evs)
	}
}

// TestBanRules.
func TestBanRules(t *testing.T) {
	host, mod, mod2, viewer := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	r := newRig(host)
	st := r.store.AddStream(host)
	_, _ = r.svc.SetModerators(ctx, st.ID, host, []uuid.UUID{mod, mod2})

	if err := r.svc.Ban(ctx, st.ID, viewer, uuid.New(), ""); !errors.Is(err, ErrNotModerator) {
		t.Fatalf("viewer banned someone: %v", err)
	}
	if err := r.svc.Ban(ctx, st.ID, mod, host, ""); !errors.Is(err, ErrInvalidTarget) {
		t.Fatalf("moderator banned the host: %v", err)
	}
	if err := r.svc.Ban(ctx, st.ID, mod, mod2, ""); !errors.Is(err, ErrNotCreator) {
		t.Fatalf("moderator banned a moderator: %v", err)
	}
	if err := r.svc.Ban(ctx, st.ID, mod, viewer, strings.Repeat("x", 501)); !errors.Is(err, ErrReasonRequired) {
		t.Fatalf("over-long reason: %v", err)
	}
	if err := r.svc.Ban(ctx, st.ID, mod, viewer, "spam"); err != nil {
		t.Fatal(err)
	}
	if len(r.lk.removed) != 1 || r.lk.removed[0] != st.LiveKitRoom+"/"+viewer.String() {
		t.Fatalf("banned viewer not dropped from the room: %v", r.lk.removed)
	}
	if _, err := r.svc.IssueViewerToken(ctx, st.ID, viewer); !errors.Is(err, ErrBannedFromStream) {
		t.Fatalf("banned viewer got a token: %v", err)
	}
	bans, err := r.svc.ListBans(ctx, st.ID, mod)
	if err != nil || len(bans) != 1 || bans[0].UserID != viewer {
		t.Fatalf("bans = %+v %v", bans, err)
	}
	if _, err := r.svc.ListBans(ctx, st.ID, viewer); !errors.Is(err, ErrNotModerator) {
		t.Fatalf("viewer listed bans: %v", err)
	}
	// The host can ban a moderator, which drops the seat.
	if err := r.svc.Ban(ctx, st.ID, host, mod2, ""); err != nil {
		t.Fatal(err)
	}
	if ok, _ := r.store.IsModerator(ctx, st.ID, mod2); ok {
		t.Fatal("a banned moderator kept the seat")
	}
	if err := r.svc.Unban(ctx, st.ID, mod, viewer); err != nil {
		t.Fatal(err)
	}
	if _, err := r.svc.IssueViewerToken(ctx, st.ID, viewer); err != nil {
		t.Fatalf("unbanned viewer: %v", err)
	}
	if len(r.ev.ofType(EventModerationBan)) != 2 || len(r.ev.ofType(EventModerationUnban)) != 1 {
		t.Fatalf("ban events: %d/%d", len(r.ev.ofType(EventModerationBan)), len(r.ev.ofType(EventModerationUnban)))
	}
}

// TestModeratorRules.
func TestModeratorRules(t *testing.T) {
	host := uuid.New()
	r := newRig(host)
	st := r.store.AddStream(host)
	six := []uuid.UUID{uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New()}
	if _, err := r.svc.SetModerators(ctx, st.ID, host, six); !errors.Is(err, ErrTooManyModerators) {
		t.Fatalf("six moderators: %v", err)
	}
	if _, err := r.svc.SetModerators(ctx, st.ID, host, []uuid.UUID{host}); !errors.Is(err, ErrInvalidTarget) {
		t.Fatalf("host as moderator: %v", err)
	}
	if _, err := r.svc.SetModerators(ctx, st.ID, six[0], six[:1]); !errors.Is(err, ErrNotCreator) {
		t.Fatalf("non-host set moderators: %v", err)
	}
	five := append(six[:5:5], six[0]) // a duplicate collapses
	got, err := r.svc.SetModerators(ctx, st.ID, host, five)
	if err != nil || len(got) != 5 {
		t.Fatalf("five moderators: %v %v", got, err)
	}
	// Host and moderators see moderator_user_ids; others do not.
	hv, _ := r.svc.GetStream(ctx, st.ID, host)
	mv, _ := r.svc.GetStream(ctx, st.ID, six[1])
	ov, _ := r.svc.GetStream(ctx, st.ID, uuid.New())
	if hv.ModeratorUserIDs == nil || len(*hv.ModeratorUserIDs) != 5 || mv.ModeratorUserIDs == nil || ov.ModeratorUserIDs != nil {
		t.Fatalf("moderator_user_ids: host=%v mod=%v other=%v", hv.ModeratorUserIDs, mv.ModeratorUserIDs, ov.ModeratorUserIDs)
	}
	// A moderator may mute; a stranger may not.
	if err := r.svc.Mute(ctx, st.ID, six[1], uuid.New()); err != nil {
		t.Fatalf("moderator mute: %v", err)
	}
	evs := r.ev.ofType(EventModerationModerators)
	if len(evs) != 1 {
		t.Fatalf("moderation.moderators events = %d", len(evs))
	}
	if ids, ok := evs[0]["user_ids"].([]any); !ok || len(ids) != 5 {
		t.Fatalf("moderators event = %v", evs[0])
	}
}

// TestReportRules.
func TestReportRules(t *testing.T) {
	host, viewer, author := uuid.New(), uuid.New(), uuid.New()
	r := newRig(host)
	st := r.store.AddStream(host)
	msg, _ := r.svc.SendChat(ctx, st.ID, author, "buy coins")

	if _, err := r.svc.Report(ctx, st.ID, viewer, ReportInput{Reason: "rude"}); !errors.Is(err, ErrInvalidReportReason) {
		t.Fatalf("bad reason: %v", err)
	}
	if _, err := r.svc.Report(ctx, st.ID, viewer, ReportInput{Reason: "spam", Note: strings.Repeat("n", 501)}); !errors.Is(err, ErrInvalidNote) {
		t.Fatalf("long note: %v", err)
	}
	rep, err := r.svc.Report(ctx, st.ID, viewer, ReportInput{Reason: "scam", MessageID: &msg.ID})
	if err != nil || rep.TargetUserID != author || rep.Status != "open" {
		t.Fatalf("message report: %+v %v", rep, err)
	}
	if _, err := r.svc.Report(ctx, st.ID, viewer, ReportInput{Reason: "spam", MessageID: &msg.ID}); !errors.Is(err, ErrAlreadyReported) {
		t.Fatalf("second report of the same message: %v", err)
	}
	if _, err := r.svc.Report(ctx, st.ID, viewer, ReportInput{Reason: "hate"}); err != nil {
		t.Fatalf("stream report is a different target: %v", err)
	}
	if _, err := r.svc.Report(ctx, st.ID, author, ReportInput{Reason: "spam", MessageID: &msg.ID}); !errors.Is(err, ErrInvalidTarget) {
		t.Fatalf("reporting your own message: %v", err)
	}
	if _, err := r.svc.Report(ctx, st.ID, host, ReportInput{Reason: "spam"}); !errors.Is(err, ErrInvalidTarget) {
		t.Fatalf("host reporting their own stream: %v", err)
	}
	unknown := uuid.New()
	if _, err := r.svc.Report(ctx, st.ID, viewer, ReportInput{Reason: "spam", MessageID: &unknown}); !errors.Is(err, ErrMessageNotFound) {
		t.Fatalf("unknown message: %v", err)
	}
	// A banned viewer may still report.
	_ = r.svc.Ban(ctx, st.ID, host, viewer, "")
	m2, _ := r.svc.SendChat(ctx, st.ID, author, "again")
	if _, err := r.svc.Report(ctx, st.ID, viewer, ReportInput{Reason: "harassment", MessageID: &m2.ID}); err != nil {
		t.Fatalf("banned viewer report: %v", err)
	}
	// Rate limit: reportsPerWindow per reporter per hour.
	spammer := uuid.New()
	for i := 0; i < reportsPerWindow; i++ {
		s2 := r.store.AddStream(uuid.New())
		if _, err := r.svc.Report(ctx, s2.ID, spammer, ReportInput{Reason: "spam"}); err != nil {
			t.Fatalf("report %d: %v", i, err)
		}
	}
	s3 := r.store.AddStream(uuid.New())
	if _, err := r.svc.Report(ctx, s3.ID, spammer, ReportInput{Reason: "spam"}); !errors.Is(err, ErrReportRateLimited) {
		t.Fatalf("over the limit: %v", err)
	}
	r.clock.Advance(reportWindow + time.Second)
	if _, err := r.svc.Report(ctx, s3.ID, spammer, ReportInput{Reason: "spam"}); err != nil {
		t.Fatalf("after the window: %v", err)
	}
}

// TestPilotAllowlistFailsClosed.
func TestPilotAllowlistFailsClosed(t *testing.T) {
	pilot, other := uuid.New(), uuid.New()

	empty := newRig()
	if _, err := empty.svc.CreateStream(ctx, pilot, CreateStreamParams{Title: "x"}); !errors.Is(err, ErrLiveNotEnabled) {
		t.Fatalf("empty allowlist admitted: %v", err)
	}

	r := newRig(pilot)
	if _, err := r.svc.CreateStream(ctx, other, CreateStreamParams{Title: "x"}); !errors.Is(err, ErrLiveNotEnabled) {
		t.Fatalf("non-pilot create: %v", err)
	}
	if _, err := r.svc.CreateStream(ctx, uuid.Nil, CreateStreamParams{Title: "x"}); !errors.Is(err, ErrLiveNotEnabled) {
		t.Fatalf("nil user create: %v", err)
	}
	st, err := r.svc.CreateStream(ctx, pilot, CreateStreamParams{Title: "x"})
	if err != nil {
		t.Fatal(err)
	}
	// A stream row created for someone else (pre-pilot data) cannot start.
	legacy := r.store.AddStreamStatus(other, stScheduled)
	if _, err := r.svc.StartStream(ctx, legacy.ID, other); !errors.Is(err, ErrLiveNotEnabled) {
		t.Fatalf("non-pilot start: %v", err)
	}
	r.store.PlatformBans[pilot] = postgres.PlatformBan{UserID: pilot}
	if _, err := r.svc.StartStream(ctx, st.ID, pilot); !errors.Is(err, ErrLiveBanned) {
		t.Fatalf("live-banned pilot start: %v", err)
	}
	if _, err := r.svc.CreateStream(ctx, pilot, CreateStreamParams{Title: "x"}); !errors.Is(err, ErrLiveBanned) {
		t.Fatalf("live-banned pilot create: %v", err)
	}
	// Viewing is unaffected by the allowlist.
	viewer := uuid.New()
	live := r.store.AddStream(other)
	if _, err := r.svc.IssueViewerToken(ctx, live.ID, viewer); err != nil {
		t.Fatalf("viewing needs no pilot seat: %v", err)
	}
	ids, bad := ParsePilotUserIDs(" " + pilot.String() + ",nope,, " + other.String() + ",00000000-0000-0000-0000-000000000000")
	if len(ids) != 2 || len(bad) != 2 {
		t.Fatalf("parse: %v %v", ids, bad)
	}
}

// TestAdminActionsAudited.
func TestAdminActionsAudited(t *testing.T) {
	host, viewer, author, admin := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	r := newRig(host)
	st := r.store.AddStream(host)
	msg, _ := r.svc.SendChat(ctx, st.ID, author, "scam link")
	rep, _ := r.svc.Report(ctx, st.ID, viewer, ReportInput{Reason: "scam", MessageID: &msg.ID})
	streamRep, _ := r.svc.Report(ctx, st.ID, viewer, ReportInput{Reason: "violence"})

	if _, err := r.svc.AdminResolveReport(ctx, admin, rep.ID, AdminResolveInput{Action: "delete", Reason: "x"}); !errors.Is(err, ErrInvalidAction) {
		t.Fatalf("bad action: %v", err)
	}
	if _, err := r.svc.AdminResolveReport(ctx, admin, rep.ID, AdminResolveInput{Action: ResolveDismiss}); !errors.Is(err, ErrReasonRequired) {
		t.Fatalf("no reason: %v", err)
	}
	if _, err := r.svc.AdminResolveReport(ctx, admin, streamRep.ID, AdminResolveInput{Action: ResolveBanUser, Reason: "x"}); !errors.Is(err, ErrInvalidAction) {
		t.Fatalf("ban_user on a stream report: %v", err)
	}
	done, err := r.svc.AdminResolveReport(ctx, admin, rep.ID, AdminResolveInput{Action: ResolveBanUser, Reason: "scammer"})
	if err != nil || done.Status != "resolved" {
		t.Fatalf("resolve: %+v %v", done, err)
	}
	if banned, _ := r.store.IsBannedFromStream(ctx, st.ID, author); !banned {
		t.Fatal("ban_user did not ban the author")
	}
	if _, err := r.svc.AdminResolveReport(ctx, admin, rep.ID, AdminResolveInput{Action: ResolveDismiss, Reason: "x"}); !errors.Is(err, ErrReportResolved) {
		t.Fatalf("resolving twice: %v", err)
	}
	if _, err := r.svc.AdminResolveReport(ctx, admin, uuid.New(), AdminResolveInput{Action: ResolveDismiss, Reason: "x"}); !errors.Is(err, ErrReportNotFound) {
		t.Fatalf("unknown report: %v", err)
	}
	if err := r.svc.AdminRemoveChatMessage(ctx, admin, st.ID, msg.ID, "scam"); err != nil {
		t.Fatal(err)
	}
	res, err := r.svc.AdminLiveBan(ctx, admin, host, "repeat offender")
	if err != nil || len(res.StoppedStreams) != 1 || res.StoppedStreams[0] != st.ID {
		t.Fatalf("live ban: %+v %v", res, err)
	}
	mustStatus(t, r, st.ID, stEnded, ReasonAdminStopped)
	if _, err := r.svc.AdminLiveBan(ctx, admin, viewer, " "); !errors.Is(err, ErrReasonRequired) {
		t.Fatalf("ban without reason: %v", err)
	}
	if _, err := r.svc.AdminLiveUnban(ctx, admin, host, ""); err != nil {
		t.Fatal(err)
	}
	want := []string{AuditReportResolve, AuditChatRemove, AuditUserLiveBan, AuditStreamStop, AuditUserLiveUnban}
	if len(r.store.Audits) != len(want) {
		t.Fatalf("audit rows = %+v", r.store.Audits)
	}
	for i, a := range r.store.Audits {
		if a.Action != want[i] || a.ActorID != admin {
			t.Fatalf("audit %d = %+v, want %s by %s", i, a, want[i], admin)
		}
	}
	bans, _ := r.svc.AdminListLiveBans(ctx, 50, 0)
	if len(bans) != 0 {
		t.Fatalf("unbanned user still listed: %+v", bans)
	}
}
