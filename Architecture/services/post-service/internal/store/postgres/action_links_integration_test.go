//go:build integration

// The Action button on the real schema (migration 072): one live link per
// post / profile and the replace rules, the create transaction, the
// resolvable read, tap counts and stats, the report threshold, the recheck
// claim and verdict, and the audited moderator decision.
//
//	ACTION_LINKS_POSTGRES_DSN=postgres://…/action_links_it_test go test -tags integration ./internal/store/postgres/ -run ActionLinksIT -v
//
// The database name must end in _test (openActionLinksDB refuses anything
// else).

package postgres_test

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/atpost/post-service/database"
	store "github.com/atpost/post-service/internal/store/postgres"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func openActionLinksDB(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("ACTION_LINKS_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("ACTION_LINKS_POSTGRES_DSN is required")
	}
	name := dsn[strings.LastIndex(dsn, "/")+1:]
	if i := strings.Index(name, "?"); i >= 0 {
		name = name[:i]
	}
	if !strings.HasSuffix(name, "_test") {
		t.Fatalf("refusing to run against a database whose name does not end in _test")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	for _, ddl := range []string{
		`CREATE TABLE IF NOT EXISTS media_assets (
			id UUID PRIMARY KEY, uploader_id UUID NOT NULL, file_type TEXT NOT NULL, processing_status TEXT NOT NULL,
			moderation_status TEXT NOT NULL DEFAULT 'pending', duration_seconds INTEGER, created_at TIMESTAMPTZ NOT NULL DEFAULT NOW())`,
		`ALTER TABLE media_assets ADD COLUMN IF NOT EXISTS duration_ms INT`,
		`ALTER TABLE media_assets ADD COLUMN IF NOT EXISTS alt_text TEXT`,
		`ALTER TABLE media_assets ADD COLUMN IF NOT EXISTS alt_decorative BOOLEAN NOT NULL DEFAULT FALSE`,
	} {
		if _, err := pool.Exec(ctx, ddl); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.BootstrapSchema(ctx, pool, database.SetupSQL, database.Migrations); err != nil {
		t.Fatalf("bootstrap real schema: %v", err)
	}
	return pool
}

func insertActionPost(t *testing.T, pool *pgxpool.Pool, author uuid.UUID) uuid.UUID {
	t.Helper()
	id := uuid.New()
	if _, err := pool.Exec(context.Background(), `
		INSERT INTO posts (id, author_id, text, visibility, content_type, review_status, created_at, updated_at)
		VALUES ($1, $2, 'hiring', 'public', 'post', 'approved', NOW(), NOW())`, id, author); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(context.Background(), `DELETE FROM posts WHERE id = $1`, id) })
	return id
}

func write(url, label string) *store.ActionLinkWrite {
	now := time.Now()
	return &store.ActionLinkWrite{Label: label, URL: url, Domain: "example.com", SafetyStatus: store.ActionSafetySafe, SafetyCheckedAt: &now}
}

func TestActionLinksITReplaceRules(t *testing.T) {
	pool := openActionLinksDB(t)
	ctx := context.Background()
	s := store.New(pool)
	author := uuid.New()
	post := insertActionPost(t, pool, author)

	l1, err := s.ReplacePostActionLink(ctx, post, author, write("https://example.com/a", "apply_now"))
	if err != nil || l1.Status != store.ActionStatusActive {
		t.Fatalf("set: %+v %v", l1, err)
	}
	l2, err := s.ReplacePostActionLink(ctx, post, author, write("https://example.com/a", "learn_more"))
	if err != nil || l2.ID != l1.ID || l2.Label != "learn_more" {
		t.Fatalf("label change: %+v %v", l2, err)
	}
	l3, err := s.ReplacePostActionLink(ctx, post, author, write("https://example.com/b", "learn_more"))
	if err != nil || l3.ID == l1.ID {
		t.Fatalf("URL change: %+v %v", l3, err)
	}
	links, err := s.ActionLinksForPosts(ctx, []uuid.UUID{post})
	if err != nil || len(links) != 1 || links[post].ID != l3.ID {
		t.Fatalf("live links: %v %v", links, err)
	}
	if r, _ := s.GetResolvableActionLink(ctx, l1.ID); r != nil {
		t.Fatal("the replaced link still resolves")
	}
	if _, err := s.ReplacePostActionLink(ctx, post, uuid.New(), write("https://example.com/c", "apply_now")); !errors.Is(err, store.ErrPostEditNotOwned) {
		t.Fatalf("stranger: %v", err)
	}
	if l, err := s.ReplacePostActionLink(ctx, post, author, nil); err != nil || l != nil {
		t.Fatalf("remove: %+v %v", l, err)
	}
	if links, _ := s.ActionLinksForPosts(ctx, []uuid.UUID{post}); len(links) != 0 {
		t.Fatalf("removed link still live: %v", links)
	}
	// The one-live-link index holds even against a direct second insert.
	if _, err := s.ReplacePostActionLink(ctx, post, author, write("https://example.com/d", "apply_now")); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO action_links (id, post_id, author_id, url, domain, label) VALUES ($1, $2, $3, 'https://example.com/e', 'example.com', 'apply_now')`,
		uuid.New(), post, author); err == nil {
		t.Fatal("a second live link was accepted")
	}
	// The CHECKs: an unknown label, a non-https URL.
	for _, q := range []string{
		`INSERT INTO action_links (id, profile_owner_id, author_id, url, domain, label) VALUES ($1, $2, $2, 'https://example.com', 'example.com', 'buy_now')`,
		`INSERT INTO action_links (id, profile_owner_id, author_id, url, domain, label) VALUES ($1, $2, $2, 'http://example.com', 'example.com', 'apply_now')`,
	} {
		if _, err := pool.Exec(ctx, q, uuid.New(), uuid.New()); err == nil {
			t.Errorf("CHECK accepted: %s", q)
		}
	}

	// Profiles.
	owner := uuid.New()
	t.Cleanup(func() { _, _ = pool.Exec(context.Background(), `DELETE FROM action_links WHERE author_id = $1`, owner) })
	p1, err := s.ReplaceProfileActionLink(ctx, owner, write("https://example.com/p", "contact_us"))
	if err != nil || p1.ProfileOwnerID == nil || *p1.ProfileOwnerID != owner {
		t.Fatalf("profile: %+v %v", p1, err)
	}
	if got, err := s.ActionLinkForProfile(ctx, owner); err != nil || got.ID != p1.ID {
		t.Fatalf("profile read: %+v %v", got, err)
	}
}

func TestActionLinksITCreateTapsAndStats(t *testing.T) {
	pool := openActionLinksDB(t)
	ctx := context.Background()
	s := store.New(pool)
	author := uuid.New()
	p := &store.Post{
		ID: uuid.New(), AuthorID: author, Text: "hiring", Visibility: "public", ContentType: "post",
		PostType: "text", AppOrigin: "postbook", Language: "en", License: "standard", RemixSetting: "allow",
		CommentModeration: "none", CommentAccess: "everyone", ReviewStatus: "approved", OriginalAudioVol: 1, OverlayAudioVol: 1,
		AllowDownload: true, CreatedAt: time.Now(),
	}
	w := write("https://example.com/jobs", "apply_now")
	w.ID = uuid.New()
	p.ActionWrite = w
	t.Cleanup(func() {
		bg := context.Background()
		_, _ = pool.Exec(bg, `DELETE FROM post_outbox_events WHERE aggregate_id = $1`, p.ID)
		_, _ = pool.Exec(bg, `DELETE FROM posts WHERE id = $1`, p.ID)
	})
	if err := s.CreatePostWithEventIdempotent(ctx, p, "", nil, nil); err != nil {
		t.Fatal(err)
	}
	r, err := s.GetResolvableActionLink(ctx, w.ID)
	if err != nil || r == nil || r.Link.PostID == nil || *r.Link.PostID != p.ID || r.PostAuthorID != author {
		t.Fatalf("created link: %+v %v", r, err)
	}
	today := time.Now().UTC()
	for i := 0; i < 3; i++ {
		if err := s.RecordActionLinkClick(ctx, w.ID, today); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.RecordActionLinkClick(ctx, w.ID, today.AddDate(0, 0, -10)); err != nil {
		t.Fatal(err)
	}
	pid := p.ID
	total, week, err := s.ActionLinkStats(ctx, &pid, nil, today.AddDate(0, 0, -6))
	if err != nil || total != 4 || week != 3 {
		t.Fatalf("stats = %d / %d, %v; want 4 / 3", total, week, err)
	}
	// A deleted post's link stops resolving.
	if _, err := pool.Exec(ctx, `UPDATE posts SET deleted_at = NOW() WHERE id = $1`, p.ID); err != nil {
		t.Fatal(err)
	}
	if r, _ := s.GetResolvableActionLink(ctx, w.ID); r != nil {
		t.Fatal("a deleted post's link resolves")
	}
}

func TestActionLinksITReportThresholdAndReview(t *testing.T) {
	pool := openActionLinksDB(t)
	ctx := context.Background()
	s := store.New(pool)
	author := uuid.New()
	post := insertActionPost(t, pool, author)
	l, err := s.ReplacePostActionLink(ctx, post, author, write("https://example.com/job", "apply_now"))
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	report := func(reporter uuid.UUID, at time.Time) store.ActionLinkReportOutcome {
		out, err := s.ApplyActionLinkReport(ctx, store.ActionLinkReportInput{
			TargetType: "post", TargetID: post, ReporterID: reporter, ReportID: uuid.New(), ReportedAt: at,
			Now: now, Window: 7 * 24 * time.Hour, Threshold: 3,
		})
		if err != nil {
			t.Fatal(err)
		}
		return out
	}
	a, b := uuid.New(), uuid.New()
	report(author, now)
	report(a, now)
	report(a, now) // the same reporter: once
	if out := report(b, now); out.Held || out.Reporters != 2 {
		t.Fatalf("two: %+v", out)
	}
	if out := report(uuid.New(), now); !out.Held || out.Reporters != 3 {
		t.Fatalf("three: %+v", out)
	}
	links, _ := s.ActionLinksForPosts(ctx, []uuid.UUID{post})
	if got := links[post]; got.Status != store.ActionStatusUnderReview || *got.StatusReason != store.ActionReasonReports {
		t.Fatalf("held link = %+v", got)
	}
	actor := uuid.New()
	if _, err := s.ReviewActionLink(ctx, l.ID, actor, store.ActionDecisionRestore, "genuine"); err != nil {
		t.Fatal(err)
	}
	var audits int
	_ = pool.QueryRow(ctx, `SELECT count(*) FROM post_admin_audit WHERE target_type = 'action_link' AND target_id = $1 AND actor_user_id = $2`, l.ID, actor).Scan(&audits)
	if audits != 1 {
		t.Fatalf("audit rows = %d", audits)
	}
	if _, err := s.ReviewActionLink(ctx, l.ID, actor, store.ActionDecisionRestore, ""); !errors.Is(err, store.ErrActionLinkTransition) {
		t.Fatalf("restore of an active link: %v", err)
	}
	// The spent reports do not count again.
	now = now.Add(time.Second)
	if out := report(uuid.New(), now); out.Held || out.Reporters != 1 {
		t.Fatalf("after restore: %+v", out)
	}
	if _, err := s.ReviewActionLink(ctx, l.ID, actor, store.ActionDecisionBlock, "scam"); err != nil {
		t.Fatal(err)
	}
	if r, _ := s.GetResolvableActionLink(ctx, l.ID); r != nil {
		t.Fatal("a blocked link resolves")
	}
}

func TestActionLinksITRecheckClaimAndVerdict(t *testing.T) {
	pool := openActionLinksDB(t)
	ctx := context.Background()
	s := store.New(pool)
	author := uuid.New()
	post := insertActionPost(t, pool, author)
	l, err := s.ReplacePostActionLink(ctx, post, author, &store.ActionLinkWrite{Label: "download", URL: "https://example.com/app", Domain: "example.com", SafetyStatus: store.ActionSafetyUnchecked})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	claimed, err := s.ClaimActionLinksForRecheck(ctx, now, now.Add(-24*time.Hour), now.Add(time.Hour), 1000)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, c := range claimed {
		found = found || c.ID == l.ID
	}
	if !found {
		t.Fatal("a never-checked link was not claimed")
	}
	again, _ := s.ClaimActionLinksForRecheck(ctx, now, now.Add(-24*time.Hour), now.Add(time.Hour), 1000)
	for _, c := range again {
		if c.ID == l.ID {
			t.Fatal("a claimed link was claimed twice")
		}
	}
	got, err := s.RecordActionLinkVerdict(ctx, l.ID, store.ActionSafetyUnsafe, now)
	if err != nil || got.Status != store.ActionStatusBlocked || *got.StatusReason != store.ActionReasonUnsafe {
		t.Fatalf("unsafe verdict: %+v %v", got, err)
	}
	// The author re-submitting the same URL after it is off the list.
	re, err := s.ReplacePostActionLink(ctx, post, author, write("https://example.com/app", "download"))
	if err != nil || re.ID != l.ID || re.Status != store.ActionStatusActive {
		t.Fatalf("safe again: %+v %v", re, err)
	}
}
