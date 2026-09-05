//go:build integration

package postgres_test

import (
	"context"
	"errors"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/atpost/post-service/database"
	"github.com/atpost/post-service/internal/store/postgres"
	"github.com/atpost/shared/events"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Scheduled publish against a real Postgres (2026-09-05).
//
//	SCHEDULE_POSTGRES_DSN=postgres://… go test -tags integration ./internal/store/postgres/ -run Scheduled
//
// Boots the real schema path (setup.sql + every migration) on the given
// database, so the proof covers migration 042 itself.

func openScheduleDB(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("SCHEDULE_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("SCHEDULE_POSTGRES_DSN not set")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	// Cross-service parent tables post-service's FKs point at.
	if _, err := pool.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS media_assets (
			id UUID PRIMARY KEY,
			uploader_id UUID NOT NULL,
			file_type TEXT NOT NULL,
			processing_status TEXT NOT NULL,
			moderation_status TEXT NOT NULL DEFAULT 'pending',
			duration_seconds INTEGER,
			created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
		)`); err != nil {
		pool.Close()
		t.Fatal(err)
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
			created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
		)`); err != nil {
		pool.Close()
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	return pool
}

func newScheduledPost(t *testing.T, st *postgres.Store, author uuid.UUID, publishAt time.Time) *postgres.Post {
	t.Helper()
	p := &postgres.Post{
		ID: uuid.New(), AuthorID: author, Text: "scheduled proof " + uuid.NewString()[:8],
		Visibility: "public", ContentType: "post", PostType: "text", AppOrigin: "postbook",
		ReviewStatus: "approved", Hashtags: []string{"momentum", "test"},
		MentionUsernames: []string{"call.usera"}, CreatedAt: time.Now().UTC(),
	}
	at := publishAt.UTC()
	p.PublishAt = &at
	if err := st.CreatePostWithEvent(context.Background(), p, "", nil); err != nil {
		t.Fatalf("create scheduled post: %v", err)
	}
	return p
}

func outboxCount(t *testing.T, pool *pgxpool.Pool, postID uuid.UUID, eventType string) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(context.Background(),
		`SELECT COUNT(*) FROM post_outbox_events WHERE aggregate_id = $1 AND event_type = $2`,
		postID, eventType).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestScheduled_StoredAuthorOnlyAndListed(t *testing.T) {
	pool := openScheduleDB(t)
	st := postgres.New(pool)
	ctx := context.Background()
	author := uuid.New()
	p := newScheduledPost(t, st, author, time.Now().Add(time.Hour))

	got, err := st.GetPost(ctx, p.ID)
	if err != nil || got == nil {
		t.Fatalf("GetPost: %v %v", got, err)
	}
	if got.PublishAt == nil || !got.IsScheduled || got.PublishedAt != nil {
		t.Fatalf("scheduled state not round-tripped: publish_at=%v is_scheduled=%v published_at=%v",
			got.PublishAt, got.IsScheduled, got.PublishedAt)
	}
	if len(got.MentionUsernames) != 1 || got.MentionUsernames[0] != "call.usera" || len(got.Hashtags) != 2 {
		t.Fatalf("tags not round-tripped: %v %v", got.Hashtags, got.MentionUsernames)
	}

	// Absent from the public lists and the author's own grid …
	recent, _, err := st.GetRecentPosts(ctx, nil, nil, "", 50, "")
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range recent {
		if r.ID == p.ID {
			t.Fatal("scheduled post leaked into GetRecentPosts")
		}
	}
	byAuthor, _, err := st.GetPostsByAuthor(ctx, author, "", 50, "", true)
	if err != nil {
		t.Fatal(err)
	}
	if len(byAuthor) != 0 {
		t.Fatalf("scheduled post leaked into the author grid: %d rows", len(byAuthor))
	}
	tagged, _, err := st.GetPostsByHashtag(ctx, "momentum", 50, "", postgres.HashtagSortRecent, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range tagged {
		if r.ID == p.ID {
			t.Fatal("scheduled post leaked into the hashtag page")
		}
	}

	// … and present in the author's scheduled list, newest publish_at first.
	later := newScheduledPost(t, st, author, time.Now().Add(2*time.Hour))
	list, next, err := st.ListScheduledPostsByAuthor(ctx, author, 1, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].ID != later.ID || next == "" {
		t.Fatalf("first page: %d rows first=%v next=%q", len(list), list[0].ID, next)
	}
	list, next, err = st.ListScheduledPostsByAuthor(ctx, author, 1, next)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].ID != p.ID || next != "" {
		t.Fatalf("second page: %d rows next=%q", len(list), next)
	}

	// The search projection reports it as scheduled (ineligible).
	rows, err := st.ScanEligibility(ctx, uuid.Nil, 1000)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range rows {
		if r.PostID == p.ID && (!r.Scheduled || r.Eligible()) {
			t.Fatalf("eligibility scan must mark a scheduled post ineligible: %+v", r)
		}
	}
}

func TestScheduled_RescheduleAndNotDueIsRefused(t *testing.T) {
	pool := openScheduleDB(t)
	st := postgres.New(pool)
	ctx := context.Background()
	author, stranger := uuid.New(), uuid.New()
	p := newScheduledPost(t, st, author, time.Now().Add(time.Hour))

	// The worker (dueOnly) must not take a future post.
	res, err := st.PublishScheduledPost(ctx, p.ID, nil, time.Now().UTC(), true, nil)
	if err != nil || res != nil {
		t.Fatalf("future post published by dueOnly: %v %v", res, err)
	}

	// Reschedule: author only.
	newAt := time.Now().Add(3 * time.Hour).UTC().Truncate(time.Millisecond)
	if err := st.ReschedulePost(ctx, p.ID, stranger, newAt); !errors.Is(err, postgres.ErrPostNotScheduled) {
		t.Fatalf("stranger reschedule: want ErrPostNotScheduled, got %v", err)
	}
	if err := st.ReschedulePost(ctx, p.ID, author, newAt); err != nil {
		t.Fatalf("reschedule: %v", err)
	}
	got, _ := st.GetPost(ctx, p.ID)
	if got.PublishAt == nil || !got.PublishAt.Equal(newAt) {
		t.Fatalf("reschedule not persisted: %v want %v", got.PublishAt, newAt)
	}

	// A soft-deleted scheduled post is never published.
	if _, err := st.DeleteUploadCascade(ctx, p.ID, author, time.Hour); err != nil {
		t.Fatalf("soft delete: %v", err)
	}
	res, err = st.PublishScheduledPost(ctx, p.ID, nil, time.Now().Add(4*time.Hour).UTC(), false, nil)
	if err != nil || res != nil {
		t.Fatalf("deleted post published: %v %v", res, err)
	}
	if err := st.ReschedulePost(ctx, p.ID, author, newAt); !errors.Is(err, postgres.ErrPostNotScheduled) {
		t.Fatalf("deleted reschedule: want ErrPostNotScheduled, got %v", err)
	}
}

// The publish flip is exactly-once under concurrent runs: N racers on one
// due post, one PostCreated on the outbox, created_at moved to the publish
// moment, search_rev bumped, and afterwards the post is public and no
// longer reschedulable.
func TestScheduled_PublishExactlyOnceUnderConcurrency(t *testing.T) {
	pool := openScheduleDB(t)
	st := postgres.New(pool)
	ctx := context.Background()
	author := uuid.New()
	p := newScheduledPost(t, st, author, time.Now().Add(-time.Minute)) // already due
	originalCreatedAt := p.CreatedAt

	const racers = 8
	now := time.Now().UTC().Truncate(time.Microsecond)
	var wg sync.WaitGroup
	results := make([]*postgres.PublishedPost, racers)
	errs := make([]error, racers)
	for i := 0; i < racers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			results[i], errs[i] = st.PublishScheduledPost(ctx, p.ID, nil, now, true,
				func(rev int64) (string, interface{}, error) {
					return events.PostCreated, events.PostCreatedPayload{
						PostID: p.ID.String(), AuthorID: author.String(), SearchRev: rev,
						ReviewStatus: "approved", Visibility: "public", CreatedAt: now,
					}, nil
				})
		}(i)
	}
	wg.Wait()

	published := 0
	for i := range results {
		if errs[i] != nil {
			t.Fatalf("racer %d: %v", i, errs[i])
		}
		if results[i] != nil {
			published++
			if results[i].SearchRev != 2 {
				t.Fatalf("search_rev after publish: %d want 2", results[i].SearchRev)
			}
		}
	}
	if published != 1 {
		t.Fatalf("published %d times, want exactly once", published)
	}
	if n := outboxCount(t, pool, p.ID, events.PostCreated); n != 1 {
		t.Fatalf("PostCreated outbox rows: %d want 1", n)
	}

	got, _ := st.GetPost(ctx, p.ID)
	if got.PublishAt != nil || got.IsScheduled || got.PublishedAt == nil {
		t.Fatalf("still scheduled after publish: %+v", got)
	}
	if !got.PublishedAt.Equal(now) || !got.CreatedAt.Equal(now) || got.CreatedAt.Equal(originalCreatedAt) {
		t.Fatalf("created_at/published_at not moved to the publish moment: created=%v published=%v", got.CreatedAt, got.PublishedAt)
	}

	// Now public.
	recent, _, err := st.GetRecentPosts(ctx, nil, nil, "", 50, "")
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, r := range recent {
		if r.ID == p.ID {
			found = true
		}
	}
	if !found {
		t.Fatal("published post missing from GetRecentPosts")
	}
	if list, _, _ := st.ListScheduledPostsByAuthor(ctx, author, 50, ""); len(list) != 0 {
		t.Fatalf("published post still in the scheduled list: %d", len(list))
	}
	if err := st.ReschedulePost(ctx, p.ID, author, time.Now().Add(time.Hour)); !errors.Is(err, postgres.ErrPostNotScheduled) {
		t.Fatalf("reschedule after publish: want ErrPostNotScheduled, got %v", err)
	}
	// And a second publish is a no-op, not a second event.
	res, err := st.PublishScheduledPost(ctx, p.ID, nil, time.Now().UTC(), false, nil)
	if err != nil || res != nil {
		t.Fatalf("republish: %v %v", res, err)
	}
	if n := outboxCount(t, pool, p.ID, events.PostCreated); n != 1 {
		t.Fatalf("PostCreated outbox rows after republish: %d want 1", n)
	}
}
