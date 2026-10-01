package postgres

// Integration tests against a real Postgres (1 Oct 2026). They run only
// with LIVE_V2_TEST_DSN set, and refuse any database whose name does not
// end in _test — never the app database:
//
//	LIVE_V2_TEST_DSN=postgres://.../live_v2_it_test?sslmode=disable go test ./internal/store/postgres -run Integration

import (
	"context"
	"errors"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	dbschema "github.com/atpost/live-service-v2/database"
)

func integrationStore(t *testing.T) (*Store, *pgxpool.Pool) {
	t.Helper()
	dsn := os.Getenv("LIVE_V2_TEST_DSN")
	if dsn == "" {
		t.Skip("LIVE_V2_TEST_DSN not set")
	}
	u, err := url.Parse(dsn)
	if err != nil {
		t.Fatalf("LIVE_V2_TEST_DSN does not parse")
	}
	if name := strings.TrimPrefix(u.Path, "/"); !strings.HasSuffix(name, "_test") {
		t.Fatalf("refusing to run against database %q: the name must end in _test", name)
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	if err := BootstrapSchema(ctx, pool, dbschema.SetupSQL, dbschema.Migrations); err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	return New(pool), pool
}

func itStream(t *testing.T, s *Store, creator uuid.UUID) *LiveStream {
	t.Helper()
	id := uuid.New()
	st, err := s.CreateStream(context.Background(), CreateStreamParams{
		CreatorUserID: creator, LiveKitRoom: "stream_" + id.String(), Title: "it", Visibility: "public",
	})
	if err != nil {
		t.Fatal(err)
	}
	return st
}

func to(status, reason string) TransitionFunc {
	return func(*LiveStream, time.Duration) (Decision, bool) { return Decision{To: status, Reason: reason}, true }
}

func outboxCount(t *testing.T, pool *pgxpool.Pool, key string) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(context.Background(),
		`SELECT COUNT(*) FROM live_v2.outbox_events WHERE idempotency_key = $1`, key).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// TestIntegrationTransitionOutboxAtomic: the status change and its outbox
// row commit together; an events error rolls the status back; the
// idempotency key keeps one row per stream and event.
func TestIntegrationTransitionOutboxAtomic(t *testing.T) {
	s, pool := integrationStore(t)
	ctx := context.Background()
	st := itStream(t, s, uuid.New())
	key := "live.started:" + st.ID.String()
	ev := func(prev, next *LiveStream) ([]OutboxEvent, error) {
		return []OutboxEvent{{EventType: "live.stream.started", PartitionKey: next.CreatorUserID.String(), IdempotencyKey: key, Payload: []byte(`{"x":1}`)}}, nil
	}
	if _, err := s.ApplyTransition(ctx, st.ID, to(StatusStarting, ""), nil, nil); err != nil {
		t.Fatal(err)
	}
	// events fails -> nothing committed.
	boom := errors.New("boom")
	if _, err := s.ApplyTransition(ctx, st.ID, to(StatusLive, ""), func(*LiveStream, *LiveStream) ([]OutboxEvent, error) { return nil, boom }, nil); !errors.Is(err, boom) {
		t.Fatalf("want boom, got %v", err)
	}
	got, _ := s.GetByID(ctx, st.ID)
	if got.Status != StatusStarting || got.StartedAt != nil {
		t.Fatalf("rolled-back transition left %s started_at=%v", got.Status, got.StartedAt)
	}
	// An outbox insert that fails inside the transaction (payload is not
	// JSON) rolls the status back too.
	bad := func(*LiveStream, *LiveStream) ([]OutboxEvent, error) {
		return []OutboxEvent{{EventType: "x", PartitionKey: "p", Payload: []byte("not json")}}, nil
	}
	if _, err := s.ApplyTransition(ctx, st.ID, to(StatusLive, ""), bad, nil); err == nil {
		t.Fatal("a failing outbox insert committed")
	}
	if got, _ := s.GetByID(ctx, st.ID); got.Status != StatusStarting {
		t.Fatalf("status after a failed outbox insert: %s", got.Status)
	}
	// Success: both.
	res, err := s.ApplyTransition(ctx, st.ID, to(StatusLive, ""), ev, nil)
	if err != nil || !res.Changed || res.Next.Status != StatusLive || res.Next.StartedAt == nil {
		t.Fatalf("live: %+v %v", res, err)
	}
	if n := outboxCount(t, pool, key); n != 1 {
		t.Fatalf("outbox rows = %d", n)
	}
	// Same key again (reconnect -> live): still one row.
	_, _ = s.ApplyTransition(ctx, st.ID, to(StatusReconnecting, ""), nil, nil)
	_, _ = s.ApplyTransition(ctx, st.ID, to(StatusLive, ""), ev, nil)
	if n := outboxCount(t, pool, key); n != 1 {
		t.Fatalf("outbox rows after a repeat = %d", n)
	}
	// Refusal and no-op.
	if _, err := s.ApplyTransition(ctx, st.ID, func(*LiveStream, time.Duration) (Decision, bool) { return Decision{}, false }, nil, nil); !errors.Is(err, ErrStateConflict) {
		t.Fatalf("refusal: %v", err)
	}
	if res, err := s.ApplyTransition(ctx, st.ID, to(StatusLive, ""), nil, nil); err != nil || res.Changed {
		t.Fatalf("no-op: %+v %v", res, err)
	}
	// Terminal: reason, ended_at, viewer_count reset; reason is required.
	if _, err := s.ApplyTransition(ctx, st.ID, to(StatusEnded, ""), nil, nil); err == nil {
		t.Fatal("ended without a reason was accepted")
	}
	audit := &AuditEntry{ActorID: uuid.New(), Action: "stream.stop", TargetType: "stream", TargetID: st.ID.String(), Reason: "r"}
	res, err = s.ApplyTransition(ctx, st.ID, to(StatusEnded, "admin_stopped"), nil, audit)
	if err != nil || res.Next.EndedReason == nil || *res.Next.EndedReason != "admin_stopped" || res.Next.EndedAt == nil {
		t.Fatalf("ended: %+v %v", res, err)
	}
	var audits int
	_ = pool.QueryRow(ctx, `SELECT COUNT(*) FROM live_admin_audit WHERE target_id = $1 AND action = 'stream.stop' AND actor_id = $2`,
		st.ID.String(), audit.ActorID).Scan(&audits)
	if audits != 1 {
		t.Fatalf("admin stop audit rows = %d", audits)
	}
	if _, err := s.ApplyTransition(ctx, uuid.New(), to(StatusLive, ""), nil, nil); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing stream: %v", err)
	}
	// The status CHECK refuses anything else.
	if _, err := pool.Exec(ctx, `UPDATE live_streams SET status = 'bogus' WHERE id = $1`, st.ID); err == nil {
		t.Fatal("status check accepted 'bogus'")
	}
	if _, err := pool.Exec(ctx, `UPDATE live_streams SET ended_reason = 'whatever' WHERE id = $1`, st.ID); err == nil {
		t.Fatal("ended_reason check accepted 'whatever'")
	}
}

// TestIntegrationTimeoutsUseTheDatabaseClock.
func TestIntegrationTimeoutsUseTheDatabaseClock(t *testing.T) {
	s, pool := integrationStore(t)
	ctx := context.Background()
	fresh := itStream(t, s, uuid.New())
	old := itStream(t, s, uuid.New())
	_, _ = s.ApplyTransition(ctx, fresh.ID, to(StatusStarting, ""), nil, nil)
	_, _ = s.ApplyTransition(ctx, old.ID, to(StatusStarting, ""), nil, nil)
	if _, err := pool.Exec(ctx, `UPDATE live_streams SET status_changed_at = NOW() - interval '121 seconds' WHERE id = $1`, old.ID); err != nil {
		t.Fatal(err)
	}
	ids, err := s.ListDueForTimeout(ctx, 120*time.Second, 600*time.Second, 60*time.Second, 1000)
	if err != nil {
		t.Fatal(err)
	}
	has := func(id uuid.UUID) bool {
		for _, x := range ids {
			if x == id {
				return true
			}
		}
		return false
	}
	if !has(old.ID) || has(fresh.ID) {
		t.Fatalf("due = %v (old %s fresh %s)", ids, old.ID, fresh.ID)
	}
	var seenAge time.Duration
	_, err = s.ApplyTransition(ctx, old.ID, func(cur *LiveStream, age time.Duration) (Decision, bool) {
		seenAge = age
		return Decision{To: StatusFailed, Reason: "no_media"}, true
	}, nil, nil)
	if err != nil || seenAge < 120*time.Second || seenAge > 10*time.Minute {
		t.Fatalf("age by the database clock = %s, err %v", seenAge, err)
	}
}

// TestIntegrationPresence: count excludes the host, duplicates are
// idempotent, peak is the maximum.
func TestIntegrationPresence(t *testing.T) {
	s, _ := integrationStore(t)
	ctx := context.Background()
	host := uuid.New()
	st := itStream(t, s, host)
	_, _ = s.ApplyTransition(ctx, st.ID, to(StatusStarting, ""), nil, nil)
	_, _ = s.ApplyTransition(ctx, st.ID, to(StatusLive, ""), nil, nil)
	a, b := uuid.New(), uuid.New()
	for _, u := range []uuid.UUID{host, a, b, b} {
		if _, _, err := s.ApplyPresence(ctx, st.ID, u, true); err != nil {
			t.Fatal(err)
		}
	}
	got, _ := s.GetByID(ctx, st.ID)
	if got.ViewerCount != 2 || got.ViewerPeak != 2 {
		t.Fatalf("count=%d peak=%d", got.ViewerCount, got.ViewerPeak)
	}
	next, changed, _ := s.ApplyPresence(ctx, st.ID, a, false)
	if !changed || next.ViewerCount != 1 || next.ViewerPeak != 2 {
		t.Fatalf("after leave: %+v changed=%v", next, changed)
	}
	if _, _, err := s.ApplyPresence(ctx, uuid.New(), a, true); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing stream: %v", err)
	}
}

// TestIntegrationModerationAndReports.
func TestIntegrationModerationAndReports(t *testing.T) {
	s, pool := integrationStore(t)
	ctx := context.Background()
	host, author, reporter, admin := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	st := itStream(t, s, host)
	msg, err := s.InsertChatMessage(ctx, st.ID, author, "buy coins")
	if err != nil {
		t.Fatal(err)
	}
	r1, err := s.CreateReport(ctx, NewReport{StreamID: st.ID, ReporterID: reporter, MessageID: &msg.ID, TargetUserID: author, Reason: "scam"}, 20, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateReport(ctx, NewReport{StreamID: st.ID, ReporterID: reporter, MessageID: &msg.ID, TargetUserID: author, Reason: "spam"}, 20, time.Hour); !errors.Is(err, ErrDuplicate) {
		t.Fatalf("duplicate: %v", err)
	}
	if _, err := s.CreateReport(ctx, NewReport{StreamID: st.ID, ReporterID: reporter, TargetUserID: host, Reason: "hate"}, 1, time.Hour); !errors.Is(err, ErrReportRateLimited) {
		t.Fatalf("rate limit: %v", err)
	}
	if _, err := s.CreateReport(ctx, NewReport{StreamID: st.ID, ReporterID: reporter, TargetUserID: host, Reason: "nope"}, 20, time.Hour); err == nil {
		t.Fatal("reason check accepted 'nope'")
	}
	// Resolve with ban_user: ban + resolution + audit in one transaction.
	done, err := s.AdminResolveReport(ctx, r1.ID, ResolveAction{Action: "ban_user", Reason: "scam"}, nil,
		AuditEntry{ActorID: admin, Action: "report.resolve", TargetType: "report", TargetID: r1.ID.String(), Reason: "scam"})
	if err != nil || done.Status != "resolved" || *done.Resolution != "ban_user" {
		t.Fatalf("resolve: %+v %v", done, err)
	}
	if banned, _ := s.IsBannedFromStream(ctx, st.ID, author); !banned {
		t.Fatal("ban_user did not ban")
	}
	if _, err := s.AdminResolveReport(ctx, r1.ID, ResolveAction{Action: "dismiss", Reason: "x"}, nil, AuditEntry{ActorID: admin, Action: "a", TargetType: "t", TargetID: "x"}); !errors.Is(err, ErrReportResolved) {
		t.Fatalf("second resolve: %v", err)
	}
	// A check error rolls the whole resolution back (no audit row).
	r2, _ := s.CreateReport(ctx, NewReport{StreamID: st.ID, ReporterID: uuid.New(), TargetUserID: host, Reason: "hate"}, 20, time.Hour)
	var before int
	_ = pool.QueryRow(ctx, `SELECT COUNT(*) FROM live_admin_audit`).Scan(&before)
	refuse := errors.New("refuse")
	if _, err := s.AdminResolveReport(ctx, r2.ID, ResolveAction{Action: "ban_user", Reason: "x"}, func(*Report) error { return refuse },
		AuditEntry{ActorID: admin, Action: "report.resolve", TargetType: "report", TargetID: r2.ID.String()}); !errors.Is(err, refuse) {
		t.Fatalf("check: %v", err)
	}
	var after int
	_ = pool.QueryRow(ctx, `SELECT COUNT(*) FROM live_admin_audit`).Scan(&after)
	if after != before {
		t.Fatal("a refused resolution wrote an audit row")
	}
	// Admin listings: open reports per stream, report rows with the text.
	rows, err := s.ListByStatuses(ctx, []string{StatusScheduled}, 200)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, r := range rows {
		if r.ID == st.ID {
			found = r.OpenReports == 1
		}
	}
	if !found {
		t.Fatal("ListByStatuses: the stream with one open report is missing or miscounted")
	}
	all, err := s.ListReports(ctx, "all", 200)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range all {
		if r.ID == r1.ID && (r.MessageText == nil || *r.MessageText != "buy coins" || r.Status != "resolved") {
			t.Fatalf("ListReports row = %+v", r)
		}
	}
	// Removal hides the message from viewers, idempotently.
	if removed, err := s.RemoveChatMessage(ctx, st.ID, msg.ID, host); err != nil || !removed {
		t.Fatalf("remove: %v %v", removed, err)
	}
	if removed, err := s.RemoveChatMessage(ctx, st.ID, msg.ID, host); err != nil || removed {
		t.Fatalf("remove again: %v %v", removed, err)
	}
	if _, err := s.RemoveChatMessage(ctx, st.ID, uuid.New(), host); !errors.Is(err, ErrNotFound) {
		t.Fatalf("remove unknown: %v", err)
	}
	list, _ := s.ListRecentChatMessages(ctx, st.ID, 50)
	if len(list) != 0 {
		t.Fatalf("removed message listed")
	}
	// Moderators: replace, ban drops the seat.
	m1, m2 := uuid.New(), uuid.New()
	_ = s.ReplaceModerators(ctx, st.ID, []uuid.UUID{m1, m2}, host)
	_ = s.ReplaceModerators(ctx, st.ID, []uuid.UUID{m2}, host)
	if mods, _ := s.ListModerators(ctx, st.ID); len(mods) != 1 || mods[0] != m2 {
		t.Fatalf("moderators = %v", mods)
	}
	_ = s.BanFromStream(ctx, st.ID, m2, host, "")
	if ok, _ := s.IsModerator(ctx, st.ID, m2); ok {
		t.Fatal("banned moderator kept the seat")
	}
	// Platform ban + audit, listed newest first.
	u := uuid.New()
	if err := s.AdminSetPlatformBan(ctx, u, true, "abuse", AuditEntry{ActorID: admin, Action: "user.live_ban", TargetType: "user", TargetID: u.String(), Reason: "abuse"}); err != nil {
		t.Fatal(err)
	}
	if ok, _ := s.IsPlatformBanned(ctx, u); !ok {
		t.Fatal("not banned")
	}
	bans, _ := s.ListPlatformBans(ctx, 1, 0)
	if len(bans) != 1 || bans[0].UserID != u {
		t.Fatalf("bans = %+v", bans)
	}
	// The audit table is append-only.
	if _, err := pool.Exec(ctx, `UPDATE live_admin_audit SET reason = 'x'`); err == nil {
		t.Fatal("audit UPDATE was allowed")
	}
	if _, err := pool.Exec(ctx, `DELETE FROM live_admin_audit`); err == nil {
		t.Fatal("audit DELETE was allowed")
	}
}

// TestIntegrationRecordingImport: the job flow and vod_ready only on
// CompleteImport.
func TestIntegrationRecordingImport(t *testing.T) {
	s, pool := integrationStore(t)
	ctx := context.Background()
	st := itStream(t, s, uuid.New())
	key := "recordings/" + st.ID.String() + ".mp4"
	if _, err := s.SetRecording(ctx, st.ID, "https://s3/x.mp4", 90, RecordingImport{Bucket: "live-recordings", ObjectKey: key, DurationMs: 90000}); err != nil {
		t.Fatal(err)
	}
	// A second egress_ended keeps the first job.
	_, _ = s.SetRecording(ctx, st.ID, "https://s3/other.mp4", 1, RecordingImport{Bucket: "b2", ObjectKey: "k2"})
	jobs, err := s.ClaimDueImports(ctx, 1000, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	var mine *RecordingImport
	for i := range jobs {
		if jobs[i].StreamID == st.ID {
			mine = &jobs[i]
		}
	}
	if mine == nil || mine.Bucket != "live-recordings" || mine.ObjectKey != key || mine.State != ImportPending || mine.OwnerUserID != st.CreatorUserID {
		t.Fatalf("claimed = %+v", mine)
	}
	// Leased: not claimable again right away.
	again, _ := s.ClaimDueImports(ctx, 1000, time.Minute)
	for _, j := range again {
		if j.StreamID == st.ID {
			t.Fatal("a leased job was claimed twice")
		}
	}
	mediaID := uuid.New()
	if err := s.RetryImport(ctx, st.ID, &mediaID, "processing", "", 0); err != nil {
		t.Fatal(err)
	}
	vodKey := "live.vod_ready:" + st.ID.String()
	if n := outboxCount(t, pool, vodKey); n != 0 {
		t.Fatal("vod_ready before ready")
	}
	if err := s.CompleteImport(ctx, st.ID, mediaID, func(ls *LiveStream, j RecordingImport) ([]OutboxEvent, error) {
		return []OutboxEvent{{EventType: "live.stream.vod_ready", PartitionKey: ls.CreatorUserID.String(), IdempotencyKey: vodKey, Payload: []byte(`{}`)}}, nil
	}); err != nil {
		t.Fatal(err)
	}
	j, _ := s.GetImport(ctx, st.ID)
	if j.State != ImportDone || j.MediaID == nil || *j.MediaID != mediaID || outboxCount(t, pool, vodKey) != 1 {
		t.Fatalf("done job = %+v", j)
	}
	// Terminal path on another stream.
	st2 := itStream(t, s, uuid.New())
	_, _ = s.SetRecording(ctx, st2.ID, "u", 1, RecordingImport{Bucket: "live-recordings", ObjectKey: "recordings/" + st2.ID.String() + ".mp4"})
	if err := s.TerminateImport(ctx, st2.ID, nil, "failed", "asset failed"); err != nil {
		t.Fatal(err)
	}
	j2, _ := s.GetImport(ctx, st2.ID)
	if j2.State != ImportFailed || j2.DoneAt == nil {
		t.Fatalf("terminated job = %+v", j2)
	}
	if err := s.CompleteImport(ctx, st2.ID, uuid.New(), func(*LiveStream, RecordingImport) ([]OutboxEvent, error) {
		t.Fatal("a failed job was completed")
		return nil, nil
	}); err != nil {
		t.Fatal(err)
	}
}

// TestIntegrationPurgeCoversNewTables.
func TestIntegrationPurgeCoversNewTables(t *testing.T) {
	s, pool := integrationStore(t)
	ctx := context.Background()
	host, viewer := uuid.New(), uuid.New()
	st := itStream(t, s, host)
	_, _ = s.ApplyTransition(ctx, st.ID, to(StatusStarting, ""), nil, nil)
	_, _ = s.ApplyTransition(ctx, st.ID, to(StatusLive, ""), nil, nil)
	_, _, _ = s.ApplyPresence(ctx, st.ID, viewer, true)
	_ = s.BanFromStream(ctx, st.ID, viewer, host, "")
	_ = s.ReplaceModerators(ctx, st.ID, []uuid.UUID{uuid.New()}, host)
	_, _ = s.CreateReport(ctx, NewReport{StreamID: st.ID, ReporterID: viewer, TargetUserID: host, Reason: "spam"}, 20, time.Hour)
	if err := s.SetUserHidden(ctx, host, true, ""); err != nil {
		t.Fatal(err)
	}
	got, _ := s.GetByID(ctx, st.ID)
	if got.Status != StatusEnded || got.EndedReason == nil || *got.EndedReason != "host_ended" {
		t.Fatalf("hide: %+v", got)
	}
	if err := s.PurgeUser(ctx, host); err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{
		`SELECT COUNT(*) FROM live_streams WHERE id = $1`,
		`SELECT COUNT(*) FROM live_stream_presence WHERE stream_id = $1`,
		`SELECT COUNT(*) FROM live_stream_bans WHERE stream_id = $1`,
		`SELECT COUNT(*) FROM live_stream_moderators WHERE stream_id = $1`,
		`SELECT COUNT(*) FROM live_reports WHERE stream_id = $1`,
	} {
		var n int
		if err := pool.QueryRow(ctx, q, st.ID).Scan(&n); err != nil || n != 0 {
			t.Fatalf("%s: %d %v", q, n, err)
		}
	}
}
