//go:build integration

package service

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/atpost/post-service/database"
	"github.com/atpost/post-service/internal/store/postgres"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Author standing on the DB-bound publication paths (Copyright Match plan
// 6.4, P-5) against a real Postgres, so migration 055 (post_publish_blocks,
// reel_drafts 'blocked') is itself under test:
//
//	POSTGRES_DSN=postgres://…/post_it_test?sslmode=disable go test -tags integration ./internal/service/ -run PublishStandingIT
//
// Refuses any database whose name does not end in _test.

func openStandingDB(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("POSTGRES_DSN")
	if dsn == "" {
		t.Skip("POSTGRES_DSN not set")
	}
	cfg, err := pgx.ParseConfig(dsn)
	if err != nil {
		t.Fatalf("POSTGRES_DSN does not parse: %v", err)
	}
	if cfg.Database == "" || cfg.Database == "app" || !strings.HasSuffix(cfg.Database, "_test") {
		t.Fatalf("refusing to run integration tests against database %q: use a scratch database whose name ends in _test (e.g. post_it_test)", cfg.Database)
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	for _, ddl := range []string{
		`CREATE TABLE IF NOT EXISTS media_assets (
			id UUID PRIMARY KEY, uploader_id UUID NOT NULL, file_type TEXT NOT NULL,
			processing_status TEXT NOT NULL, moderation_status TEXT NOT NULL DEFAULT 'pending',
			duration_seconds INTEGER, created_at TIMESTAMPTZ NOT NULL DEFAULT NOW())`,
		`CREATE TABLE IF NOT EXISTS user_preferences (user_id UUID PRIMARY KEY, created_at TIMESTAMPTZ NOT NULL DEFAULT NOW())`,
	} {
		if _, err := pool.Exec(ctx, ddl); err != nil {
			pool.Close()
			t.Fatal(err)
		}
	}
	if err := postgres.BootstrapSchema(ctx, pool, database.SetupSQL, database.Migrations); err != nil {
		pool.Close()
		t.Fatalf("bootstrap schema: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS post_outbox_events (
			id UUID PRIMARY KEY DEFAULT gen_random_uuid(), event_type TEXT NOT NULL,
			aggregate_type TEXT NOT NULL, aggregate_id UUID NOT NULL, payload JSONB NOT NULL,
			published BOOLEAN NOT NULL DEFAULT FALSE, published_at TIMESTAMPTZ,
			created_at TIMESTAMPTZ NOT NULL DEFAULT NOW())`); err != nil {
		pool.Close()
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	return pool
}

// standingITService is a Service with a real store and a fake trust-safety.
func standingITService(t *testing.T, pool *pgxpool.Pool, body string) (*Service, *standingFixture, *postgres.Store) {
	t.Helper()
	st := postgres.New(pool)
	f := newStandingFixture(t, body)
	svc := &Service{pgStore: st, standing: standingTestClient(t, f, nil), requireStandingCheck: true}
	return svc, f, st
}

func newScheduledPostIT(t *testing.T, st *postgres.Store, author uuid.UUID, publishAt time.Time) *postgres.Post {
	t.Helper()
	p := &postgres.Post{
		ID: uuid.New(), AuthorID: author, Text: "standing proof " + uuid.NewString()[:8],
		Visibility: "public", ContentType: "post", PostType: "text", AppOrigin: "postbook",
		ReviewStatus: "approved", CreatedAt: time.Now().UTC(),
	}
	at := publishAt.UTC()
	p.PublishAt = &at
	if err := st.CreatePostWithEvent(context.Background(), p, "", nil); err != nil {
		t.Fatalf("create scheduled post: %v", err)
	}
	return p
}

func dueIDs(t *testing.T, st *postgres.Store) map[uuid.UUID]bool {
	t.Helper()
	cands, err := st.ListDueScheduledPosts(context.Background(), time.Now().UTC().Add(time.Second), 500)
	if err != nil {
		t.Fatal(err)
	}
	out := map[uuid.UUID]bool{}
	for _, c := range cands {
		out[c.PostID] = true
	}
	return out
}

func TestPublishStandingIT_WorkerParksASuspendedAuthorsScheduledPost(t *testing.T) {
	pool := openStandingDB(t)
	svc, f, st := standingITService(t, pool, standingSuspendedBody)
	ctx := context.Background()
	author := uuid.New()
	p := newScheduledPostIT(t, st, author, time.Now().Add(-time.Minute))
	if !dueIDs(t, st)[p.ID] {
		t.Fatal("fixture: the post must be due")
	}

	// The worker's flip refuses and parks the post.
	published, err := svc.PublishScheduled(ctx, p.ID, nil, true)
	assertSuspended(t, err)
	if published {
		t.Fatal("published through a refusal")
	}
	reason, err := st.PublishBlockReason(ctx, p.ID)
	if err != nil || reason != "author_suspended until 2026-12-27T09:30:00Z" {
		t.Fatalf("block reason=%q err=%v", reason, err)
	}
	got, _ := st.GetPost(ctx, p.ID)
	if got == nil || got.PublishAt == nil {
		t.Fatal("a parked post keeps its publish_at (the author's intent)")
	}
	// … which the due scan now skips, so it is not retried every tick.
	if dueIDs(t, st)[p.ID] {
		t.Fatal("a parked post must not be listed as due")
	}
	// The author's Scheduled list shows why.
	mine, _, err := st.ListScheduledPostsByAuthor(ctx, author, 20, "")
	if err != nil {
		t.Fatal(err)
	}
	var seen bool
	for _, m := range mine {
		if m.ID == p.ID {
			seen = true
			if m.PublishBlockedReason == nil || !strings.HasPrefix(*m.PublishBlockedReason, BlockReasonAuthorSuspended) {
				t.Fatalf("publish_blocked_reason=%v", m.PublishBlockedReason)
			}
		}
	}
	if !seen {
		t.Fatal("the parked post must stay on the author's Scheduled list")
	}
	raw, _ := json.Marshal(mine)
	if !strings.Contains(string(raw), `"publish_blocked_reason":"author_suspended until`) {
		t.Fatalf("wire: %s", raw)
	}

	// "Publish now" by the author while still suspended: 403, still parked.
	_, err = svc.ReschedulePost(ctx, p.ID, author, nil)
	assertSuspended(t, err)
	if got, _ := st.GetPost(ctx, p.ID); got.PublishAt == nil {
		t.Fatal("publish-now must not flip a refused post")
	}

	// Rescheduling re-arms it (the author acted), and the worker parks it
	// again while the refusal stands.
	later := time.Now().Add(-30 * time.Second)
	if err := st.ReschedulePost(ctx, p.ID, author, later); err != nil {
		t.Fatal(err)
	}
	if reason, _ := st.PublishBlockReason(ctx, p.ID); reason != "" {
		t.Fatalf("reschedule must clear the block, got %q", reason)
	}
	if !dueIDs(t, st)[p.ID] {
		t.Fatal("a re-armed post is due again")
	}
	if _, err := svc.PublishScheduled(ctx, p.ID, nil, true); !errors.Is(err, ErrAuthorSuspended) {
		t.Fatalf("second worker pass: %v", err)
	}
	if reason, _ := st.PublishBlockReason(ctx, p.ID); reason == "" {
		t.Fatal("parked again")
	}

	// Once trust-safety says ok, the flip goes through and the block is gone.
	f.body.Store(standingOKBody)
	if err := st.ReschedulePost(ctx, p.ID, author, later); err != nil {
		t.Fatal(err)
	}
	svc.standing.(interface{ Forget(uuid.UUID) }).Forget(author)
	published, err = svc.PublishScheduled(ctx, p.ID, nil, true)
	if err != nil || !published {
		t.Fatalf("ok must publish: published=%v err=%v", published, err)
	}
	if got, _ := st.GetPost(ctx, p.ID); got.PublishAt != nil || got.PublishedAt == nil {
		t.Fatal("not live after the flip")
	}
	if reason, _ := st.PublishBlockReason(ctx, p.ID); reason != "" {
		t.Fatalf("a live post carries no block, got %q", reason)
	}
}

func TestPublishStandingIT_WorkerUnknownRetriesThenParksAfter24h(t *testing.T) {
	pool := openStandingDB(t)
	svc, f, st := standingITService(t, pool, standingOKBody)
	f.status.Store(http.StatusServiceUnavailable)
	ck := &fakeClock{t: time.Now()}
	svc.standingBackoff = newStandingBackoff(ck.now)
	ctx := context.Background()
	author := uuid.New()
	p := newScheduledPostIT(t, st, author, time.Now().Add(-time.Minute))

	published, err := svc.PublishScheduled(ctx, p.ID, nil, true)
	assertUnknown(t, err)
	if published {
		t.Fatal("published through uncertainty")
	}
	if reason, _ := st.PublishBlockReason(ctx, p.ID); reason != "" {
		t.Fatalf("a first unknown is retried, not parked: %q", reason)
	}
	if !dueIDs(t, st)[p.ID] {
		t.Fatal("still due (the in-memory backoff decides when to ask again)")
	}
	// Inside the backoff window the worker does not even ask.
	calls := f.calls.Load()
	if _, err := svc.PublishScheduled(ctx, p.ID, nil, true); !errors.Is(err, ErrStandingUnknown) || f.calls.Load() != calls {
		t.Fatalf("backoff: err=%v calls %d→%d", err, calls, f.calls.Load())
	}
	// 24 h of unknown: parked with standing_unavailable.
	ck.add(standingBlockAfter)
	if _, err := svc.PublishScheduled(ctx, p.ID, nil, true); !errors.Is(err, ErrStandingUnknown) {
		t.Fatalf("after 24h: %v", err)
	}
	if reason, _ := st.PublishBlockReason(ctx, p.ID); reason != BlockReasonStandingUnavailable {
		t.Fatalf("reason=%q", reason)
	}
	if dueIDs(t, st)[p.ID] {
		t.Fatal("parked: not due")
	}
	// Interactive "publish now" during an outage is 503, and does not park.
	f.status.Store(http.StatusInternalServerError)
	if _, err := svc.ReschedulePost(ctx, p.ID, author, nil); !errors.Is(err, ErrStandingUnknown) {
		t.Fatalf("publish now during outage: %v", err)
	}
}

func TestPublishStandingIT_ComposerDraft(t *testing.T) {
	pool := openStandingDB(t)
	svc, f, st := standingITService(t, pool, standingSuspendedBody)
	ctx := context.Background()
	author := uuid.New()
	payload := json.RawMessage(`{"text":"scheduled words"}`)

	// Worker: a due draft of a suspended author is parked as blocked.
	// (CreatePostDraft tolerates a schedule_at up to one minute old.)
	due := time.Now().Add(-30 * time.Second)
	d, err := svc.CreatePostDraft(ctx, author, "post", payload, &due)
	if err != nil {
		t.Fatal(err)
	}
	n, err := svc.PublishScheduledPostDrafts(ctx)
	if err != nil || n != 0 {
		t.Fatalf("n=%d err=%v", n, err)
	}
	got, _ := st.GetPostDraft(ctx, d.ID, author)
	if got == nil || got.Status != "blocked" || got.BlockedReason == nil || !strings.HasPrefix(*got.BlockedReason, BlockReasonAuthorSuspended) {
		t.Fatalf("draft after worker: %+v", got)
	}
	if post, _ := st.GetPost(ctx, d.ID); post != nil {
		t.Fatal("no post for a suspended author")
	}

	// Interactive: publish now is 403 and the draft is parked with the reason.
	d2, err := svc.CreatePostDraft(ctx, author, "post", payload, nil)
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = svc.PublishPostDraft(ctx, d2.ID, author, nil)
	assertSuspended(t, err)
	got, _ = st.GetPostDraft(ctx, d2.ID, author)
	if got.Status != "blocked" || got.BlockedReason == nil || !strings.HasPrefix(*got.BlockedReason, BlockReasonAuthorSuspended) {
		t.Fatalf("draft after interactive refusal: %+v", got)
	}

	// Interactive during an outage: 503, the claim is released (draft again).
	f.status.Store(http.StatusServiceUnavailable)
	d3, err := svc.CreatePostDraft(ctx, uuid.New(), "post", payload, nil)
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = svc.PublishPostDraft(ctx, d3.ID, d3.AuthorID, nil)
	assertUnknown(t, err)
	got, _ = st.GetPostDraft(ctx, d3.ID, d3.AuthorID)
	if got.Status != "draft" || got.ClaimToken != nil {
		t.Fatalf("draft after unknown must be released for retry: %+v", got)
	}

	// Worker during an outage: released, not parked; parked after 24 h.
	ck := &fakeClock{t: time.Now()}
	svc.standingBackoff = newStandingBackoff(ck.now)
	d4, err := svc.CreatePostDraft(ctx, uuid.New(), "post", payload, &due)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.PublishScheduledPostDrafts(ctx); err != nil {
		t.Fatal(err)
	}
	got, _ = st.GetPostDraft(ctx, d4.ID, d4.AuthorID)
	if got.Status != "draft" {
		t.Fatalf("worker unknown must release: %+v", got)
	}
	ck.add(standingBlockAfter)
	if _, err := svc.PublishScheduledPostDrafts(ctx); err != nil {
		t.Fatal(err)
	}
	got, _ = st.GetPostDraft(ctx, d4.ID, d4.AuthorID)
	if got.Status != "blocked" || got.BlockedReason == nil || *got.BlockedReason != BlockReasonStandingUnavailable {
		t.Fatalf("worker after 24h unknown: %+v", got)
	}
}

func TestPublishStandingIT_ReelDraft(t *testing.T) {
	pool := openStandingDB(t)
	svc, _, st := standingITService(t, pool, standingSuspendedBody)
	ctx := context.Background()
	author := uuid.New()
	due := time.Now().Add(-time.Minute)

	// Worker: parked as 'blocked' (migration 055 widened the CHECK) with the reason.
	rd := &postgres.ReelDraft{AuthorID: author, Caption: "reel", Visibility: "public", ScheduleAt: &due,
		License: "standard", RemixSetting: "allow", CommentModeration: "none", CommentAccess: "everyone"}
	if err := st.CreateDraft(ctx, rd); err != nil {
		t.Fatal(err)
	}
	n, err := svc.PublishScheduledDrafts(ctx)
	if err != nil || n != 0 {
		t.Fatalf("n=%d err=%v", n, err)
	}
	got, err := st.GetDraft(ctx, rd.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != "blocked" || got.BlockedReason == nil || !strings.HasPrefix(*got.BlockedReason, BlockReasonAuthorSuspended) {
		t.Fatalf("reel draft after worker: status=%q reason=%v", got.Status, got.BlockedReason)
	}
	if post, _ := st.GetPost(ctx, rd.ID); post != nil {
		t.Fatal("no post for a suspended author")
	}
	// Parked drafts are not claimed again.
	claimed, err := st.ClaimDueReelDrafts(ctx, time.Now().UTC(), time.Hour, 100)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range claimed {
		if c.ID == rd.ID {
			t.Fatal("a blocked reel draft must not be claimed")
		}
	}

	// Interactive: 403, the draft is left as it was.
	rd2 := &postgres.ReelDraft{AuthorID: author, Caption: "reel two", Visibility: "public",
		License: "standard", RemixSetting: "allow", CommentModeration: "none", CommentAccess: "everyone"}
	if err := st.CreateDraft(ctx, rd2); err != nil {
		t.Fatal(err)
	}
	_, err = svc.PublishDraft(ctx, rd2.ID, author, nil)
	assertSuspended(t, err)
	if got, _ := st.GetDraft(ctx, rd2.ID); got.Status != "draft" {
		t.Fatalf("interactive refusal leaves the reel draft editable: %q", got.Status)
	}
}
