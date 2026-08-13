//go:build integration

package postgres

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Module 4 M4-P0-4 — live PostgreSQL proof for migration 032 and the atomic
// story create path.
//
//	STORY_POSTGRES_DSN=postgres://postgres:postgres@127.0.0.1:5432/m4story?sslmode=disable \
//	go test -tags integration ./internal/store/postgres/ -run Live -v
//
// WHY THIS EXISTS RATHER THAN MORE UNIT TESTS
//
// The policy logic is pure and already covered without a database. What was
// NOT covered is everything the database itself enforces: the FK against
// media_assets, the CHECK constraints, the moderation default, the legacy
// retirement UPDATE, and the idempotency index — plus whether the create query
// names columns that actually exist.
//
// That last one is not hypothetical. The first version of CreateStoryPending
// selected `user_id` and `deleted_at` from media_assets. The real columns are
// `uploader_id`, and there is no deleted_at at all. Every story create would
// have failed at runtime with 42703, and no unit test could have caught it.

func liveStoryPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("STORY_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("STORY_POSTGRES_DSN not set; live story suite skipped")
	}
	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(pool.Close)
	applyStorySchema(t, pool)
	return pool
}

// applyStorySchema installs the minimum parent schema plus the REAL migration
// 032 file. The migration is read from disk, not restated here — a restated
// migration proves only that the restatement works.
func applyStorySchema(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	ctx := context.Background()

	// media_assets, with the column names the deployed schema actually uses.
	// Only the columns migration 032 and CreateStoryPending touch.
	if _, err := pool.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS media_assets (
			id UUID PRIMARY KEY,
			uploader_id UUID NOT NULL,
			file_type TEXT NOT NULL,
			processing_status TEXT NOT NULL,
			moderation_status TEXT NOT NULL DEFAULT 'pending',
			created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
		);
		CREATE TABLE IF NOT EXISTS post_outbox_events (
			id BIGSERIAL PRIMARY KEY,
			event_type TEXT NOT NULL,
			aggregate_type TEXT NOT NULL,
			aggregate_id UUID NOT NULL,
			payload JSONB NOT NULL,
			published BOOLEAN NOT NULL DEFAULT FALSE,
			published_at TIMESTAMPTZ,
			created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
		);
		CREATE TABLE IF NOT EXISTS stories (
			id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
			author_id UUID NOT NULL,
			media_url TEXT,
			media_type TEXT NOT NULL,
			caption TEXT NOT NULL DEFAULT '',
			stickers JSONB,
			music_track JSONB,
			visibility TEXT NOT NULL DEFAULT 'public',
			view_count INTEGER NOT NULL DEFAULT 0,
			expires_at TIMESTAMPTZ NOT NULL,
			is_highlight BOOLEAN NOT NULL DEFAULT FALSE,
			highlight_group TEXT,
			created_at TIMESTAMPTZ NOT NULL DEFAULT now()
		);
	`); err != nil {
		t.Fatalf("parent schema: %v", err)
	}

	// A legacy story, inserted BEFORE migration 032 runs, so the retirement
	// step has something real to act on.
	//
	// Seed it ONLY on the first run against this database. Once the migration
	// has applied, stories_media_or_retired_chk correctly refuses a new row
	// with no media_id that is not already retired — so re-seeding would fail,
	// and it would fail for the right reason. Gating on the constraint keeps
	// the suite re-runnable without weakening what it proves.
	var migrated bool
	if err := pool.QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM pg_constraint WHERE conname = 'stories_media_or_retired_chk')`,
	).Scan(&migrated); err != nil {
		t.Fatalf("check migration state: %v", err)
	}
	if !migrated {
		if _, err := pool.Exec(ctx, `
			INSERT INTO stories (id, author_id, media_url, media_type, visibility, expires_at, is_highlight, highlight_group)
			VALUES ($1, $2, 'https://evil.example/legacy.jpg', 'image', 'public', NOW() + interval '10 years', TRUE, 'best')
		`, uuid.New(), uuid.New()); err != nil {
			t.Fatalf("seed legacy story: %v", err)
		}
	}

	raw, err := os.ReadFile("../../../database/migrations/032_story_canonical_media_and_moderation.sql")
	if err != nil {
		t.Fatalf("read migration 032: %v", err)
	}
	// Split on ';' at statement level would break the DO $$ ... $$ blocks, so
	// the whole file is executed as one batch exactly as the migration runner
	// does for a multi-statement migration.
	if _, err := pool.Exec(ctx, string(raw)); err != nil {
		t.Fatalf("apply migration 032: %v", err)
	}
}

func seedMedia(t *testing.T, pool *pgxpool.Pool, owner uuid.UUID, fileType, processing, moderation string) uuid.UUID {
	t.Helper()
	id := uuid.New()
	if _, err := pool.Exec(context.Background(), `
		INSERT INTO media_assets (id, uploader_id, file_type, processing_status, moderation_status)
		VALUES ($1,$2,$3,$4,$5)
	`, id, owner, fileType, processing, moderation); err != nil {
		t.Fatalf("seed media: %v", err)
	}
	return id
}

func newStory(author uuid.UUID, media uuid.UUID, mediaType string) *Story {
	return &Story{
		ID:         uuid.New(),
		AuthorID:   author,
		MediaID:    &media,
		MediaType:  mediaType,
		Visibility: "followers",
		ExpiresAt:  time.Now().Add(24 * time.Hour),
		CreatedAt:  time.Now(),
	}
}

func countRows(t *testing.T, pool *pgxpool.Pool, q string, args ...any) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(context.Background(), q, args...).Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	return n
}

// ─────────────────────────────────────────────────────────────────────────────
// PROOF 1 — a valid create writes a PENDING story and its moderation request
// in one transaction.
// ─────────────────────────────────────────────────────────────────────────────
func TestLiveStoryCreateIsPendingAndAtomic(t *testing.T) {
	pool := liveStoryPool(t)
	st := &Store{db: pool}
	author := uuid.New()
	media := seedMedia(t, pool, author, "image", "ready", "passed")

	story, err := st.CreateStoryPending(context.Background(), newStory(author, media, "image"), "")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if story.ModerationState != "pending" {
		t.Fatalf("new story is %q, want pending. A story must never be publishable "+
			"before a decision exists.", story.ModerationState)
	}
	if n := countRows(t, pool,
		`SELECT count(*) FROM post_outbox_events WHERE aggregate_id=$1 AND event_type='StoryModerationRequested'`,
		story.ID); n != 1 {
		t.Fatalf("%d moderation requests for a new story, want exactly 1. Without one, "+
			"the story would sit pending forever with nothing scheduled to review it.", n)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// PROOF 2 — invalid media is rejected, and rejection writes NOTHING.
// ─────────────────────────────────────────────────────────────────────────────
func TestLiveInvalidMediaCreatesNoStoryAndNoOutbox(t *testing.T) {
	pool := liveStoryPool(t)
	st := &Store{db: pool}
	author := uuid.New()
	other := uuid.New()

	cases := []struct {
		name  string
		media uuid.UUID
		typ   string
	}{
		{"another user's media", seedMedia(t, pool, other, "image", "ready", "passed"), "image"},
		{"missing media", uuid.New(), "image"},
		{"still processing", seedMedia(t, pool, author, "image", "processing", "passed"), "image"},
		{"media not moderated", seedMedia(t, pool, author, "image", "ready", "pending"), "image"},
		{"type mismatch", seedMedia(t, pool, author, "video", "ready", "passed"), "image"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := newStory(author, tc.media, tc.typ)
			_, err := st.CreateStoryPending(context.Background(), s, "")
			if !errors.Is(err, ErrStoryMediaInvalid) {
				t.Fatalf("got %v, want ErrStoryMediaInvalid", err)
			}
			if n := countRows(t, pool, `SELECT count(*) FROM stories WHERE id=$1`, s.ID); n != 0 {
				t.Fatalf("a rejected create left %d story rows behind", n)
			}
			if n := countRows(t, pool,
				`SELECT count(*) FROM post_outbox_events WHERE aggregate_id=$1`, s.ID); n != 0 {
				t.Fatalf("a rejected create left %d outbox rows behind", n)
			}
		})
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// PROOF 3 — the FK stops a referenced asset being deleted out from under a
// story. This is the reference-integrity invariant, enforced by the database
// rather than by a check that a concurrent transaction could race.
// ─────────────────────────────────────────────────────────────────────────────
func TestLiveReferencedMediaCannotBeDeleted(t *testing.T) {
	pool := liveStoryPool(t)
	st := &Store{db: pool}
	author := uuid.New()
	media := seedMedia(t, pool, author, "image", "ready", "passed")

	if _, err := st.CreateStoryPending(context.Background(), newStory(author, media, "image"), ""); err != nil {
		t.Fatalf("create: %v", err)
	}
	_, err := pool.Exec(context.Background(), `DELETE FROM media_assets WHERE id=$1`, media)
	if err == nil {
		t.Fatal("media referenced by a story was deleted. The story now points at " +
			"bytes that no longer exist, and orphan reclamation can race story creation.")
	}
	if !strings.Contains(err.Error(), "23503") && !strings.Contains(strings.ToLower(err.Error()), "foreign key") {
		t.Fatalf("delete failed for the wrong reason: %v", err)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// PROOF 4 — idempotency. A retried create returns the same story rather than a
// second pending story and a second moderation request.
// ─────────────────────────────────────────────────────────────────────────────
func TestLiveCreateIsIdempotent(t *testing.T) {
	pool := liveStoryPool(t)
	st := &Store{db: pool}
	author := uuid.New()
	media := seedMedia(t, pool, author, "image", "ready", "passed")
	key := "idem-" + uuid.NewString()

	first, err := st.CreateStoryPending(context.Background(), newStory(author, media, "image"), key)
	if err != nil {
		t.Fatalf("first create: %v", err)
	}
	second, err := st.CreateStoryPending(context.Background(), newStory(author, media, "image"), key)
	if err != nil {
		t.Fatalf("retry: %v", err)
	}
	if first.ID != second.ID {
		t.Fatalf("retry created a second story (%s vs %s)", first.ID, second.ID)
	}
	if n := countRows(t, pool,
		`SELECT count(*) FROM post_outbox_events WHERE aggregate_id=$1`, first.ID); n != 1 {
		t.Fatalf("%d moderation requests after a retry, want 1", n)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// PROOF 5 — only a revision-matched decision can approve, and an approval is
// what makes a story readable.
// ─────────────────────────────────────────────────────────────────────────────
func TestLiveOnlyRevisionMatchedDecisionApproves(t *testing.T) {
	pool := liveStoryPool(t)
	st := &Store{db: pool}
	ctx := context.Background()
	author := uuid.New()
	media := seedMedia(t, pool, author, "image", "ready", "passed")
	story, err := st.CreateStoryPending(ctx, newStory(author, media, "image"), "")
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	// A stale revision describes content that is not what is stored now.
	applied, err := st.ApplyStoryModerationDecision(ctx, story.ID, 99, "approved", "d1", "", "v1")
	if err != nil {
		t.Fatalf("stale decision: %v", err)
	}
	if applied {
		t.Fatal("a decision carrying a stale revision approved the story")
	}
	if n := countRows(t, pool,
		`SELECT count(*) FROM stories WHERE id=$1 AND moderation_state='approved'`, story.ID); n != 0 {
		t.Fatal("story was approved by a stale decision")
	}

	// The matching revision applies.
	applied, err = st.ApplyStoryModerationDecision(ctx, story.ID, 1, "approved", "d2", "", "v1")
	if err != nil || !applied {
		t.Fatalf("matching decision did not apply: applied=%v err=%v", applied, err)
	}

	// Replay of the same decision must not re-apply: the guard requires the
	// story to still be pending/manual_review.
	applied, err = st.ApplyStoryModerationDecision(ctx, story.ID, 1, "rejected", "d2", "", "v1")
	if err != nil {
		t.Fatalf("replay: %v", err)
	}
	if applied {
		t.Fatal("a terminal story was moved again by a replayed decision")
	}

	// Only now is it visible to the feed query.
	rows, err := st.GetStoriesByAuthor(ctx, author)
	if err != nil {
		t.Fatalf("author read: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("approved story not returned by the author query (%d rows)", len(rows))
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// PROOF 6 — a pending story is invisible to the read queries, which is the
// database half of the moderation gate.
// ─────────────────────────────────────────────────────────────────────────────
func TestLivePendingStoryIsInvisibleToReads(t *testing.T) {
	pool := liveStoryPool(t)
	st := &Store{db: pool}
	ctx := context.Background()
	author := uuid.New()
	media := seedMedia(t, pool, author, "image", "ready", "passed")
	story, err := st.CreateStoryPending(ctx, newStory(author, media, "image"), "")
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	rows, err := st.GetStoriesByAuthor(ctx, author)
	if err != nil {
		t.Fatalf("author read: %v", err)
	}
	if len(rows) != 0 {
		t.Fatalf("a pending story appeared in the author feed (%d rows)", len(rows))
	}
	feed, err := st.GetStoriesFeed(ctx, []uuid.UUID{author})
	if err != nil {
		t.Fatalf("feed read: %v", err)
	}
	if len(feed) != 0 {
		t.Fatalf("a pending story appeared in the story feed (%d rows)", len(feed))
	}

	// The owner still sees it, truthfully, with its state.
	owned, err := st.GetStoriesForOwner(ctx, author)
	if err != nil {
		t.Fatalf("owner read: %v", err)
	}
	if len(owned) != 1 || owned[0].ID != story.ID || owned[0].ModerationState != "pending" {
		t.Fatalf("owner could not see their own pending story truthfully: %+v", owned)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// PROOF 7 — legacy retirement. Migration 032 must leave no pre-existing story
// publishable, and must clear legacy highlights, which would otherwise outlive
// their expiry forever.
// ─────────────────────────────────────────────────────────────────────────────
func TestLiveLegacyStoriesAreRetiredAndNotPublishable(t *testing.T) {
	pool := liveStoryPool(t)
	ctx := context.Background()

	n := countRows(t, pool, `
		SELECT count(*) FROM stories
		WHERE media_id IS NULL AND moderation_state <> 'manual_review'`)
	if n != 0 {
		t.Fatalf("%d legacy stories survived migration 032 in a state other than "+
			"manual_review. A story with no canonical media and no review evidence "+
			"must never be publishable.", n)
	}
	if n := countRows(t, pool,
		`SELECT count(*) FROM stories WHERE media_id IS NULL AND is_highlight = TRUE`); n != 0 {
		t.Fatalf("%d legacy highlights survived. A highlight outlives its expiry, so "+
			"an unreviewed one would persist indefinitely.", n)
	}
	if n := countRows(t, pool,
		`SELECT count(*) FROM stories WHERE media_id IS NULL AND expires_at > now()`); n != 0 {
		t.Fatalf("%d legacy stories are still unexpired", n)
	}
	// Nothing was destroyed: the rows are held, not deleted.
	if n := countRows(t, pool, `SELECT count(*) FROM stories WHERE media_id IS NULL`); n == 0 {
		t.Fatal("legacy stories were deleted rather than retired; retirement must be " +
			"reversible by a human reviewer")
	}
	_ = ctx
}

// ─────────────────────────────────────────────────────────────────────────────
// PROOF 8 — the CHECK constraint refuses an invalid moderation state, so an
// unexpected value cannot be written by any path.
// ─────────────────────────────────────────────────────────────────────────────
func TestLiveModerationStateIsConstrained(t *testing.T) {
	pool := liveStoryPool(t)
	st := &Store{db: pool}
	ctx := context.Background()
	author := uuid.New()
	media := seedMedia(t, pool, author, "image", "ready", "passed")
	story, err := st.CreateStoryPending(ctx, newStory(author, media, "image"), "")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	_, err = pool.Exec(ctx, `UPDATE stories SET moderation_state='definitely_fine' WHERE id=$1`, story.ID)
	if err == nil {
		t.Fatal("an arbitrary moderation_state was accepted; the closed enum is not enforced")
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// PROOF 9 — M4-P0-5: the media-to-owner-content lookup.
//
// Byte delivery is authorized by the content that references the asset, so the
// lookup has to resolve an asset to its live story and stop resolving when that
// story is deleted.
// ─────────────────────────────────────────────────────────────────────────────
func TestLiveMediaResolvesToItsLiveStoryOnly(t *testing.T) {
	pool := liveStoryPool(t)
	st := &Store{db: pool}
	ctx := context.Background()
	author := uuid.New()
	media := seedMedia(t, pool, author, "image", "ready", "passed")

	// Uploader is resolvable before any content exists, which is what lets a
	// creator preview their own in-progress upload.
	owner, err := st.MediaUploader(ctx, media)
	if err != nil {
		t.Fatalf("uploader: %v", err)
	}
	if owner != author {
		t.Fatalf("uploader=%s want %s", owner, author)
	}
	if got, err := st.StoryForMedia(ctx, media); err != nil || got != nil {
		t.Fatalf("unreferenced media resolved to a story: %+v (err %v)", got, err)
	}

	story, err := st.CreateStoryPending(ctx, newStory(author, media, "image"), "")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	got, err := st.StoryForMedia(ctx, media)
	if err != nil || got == nil || got.ID != story.ID {
		t.Fatalf("media did not resolve to its story: %+v (err %v)", got, err)
	}

	// Deleting the story must stop it authorizing the bytes immediately. The
	// signed-URL TTL bounds links already issued; this bounds new ones.
	if _, err := pool.Exec(ctx, `UPDATE stories SET deleted_at = now() WHERE id = $1`, story.ID); err != nil {
		t.Fatalf("soft delete: %v", err)
	}
	if got, err := st.StoryForMedia(ctx, media); err != nil || got != nil {
		t.Fatalf("a deleted story still authorizes its media: %+v (err %v)", got, err)
	}

	// An unknown asset denies rather than erroring.
	if owner, err := st.MediaUploader(ctx, uuid.New()); err != nil || owner != uuid.Nil {
		t.Fatalf("unknown media: owner=%s err=%v", owner, err)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// PROOF 10 — the moderation loop actually closes.
//
// Every other proof shows a story staying invisible. This one shows it becoming
// visible, because "nothing is ever published" is a safe failure that is still
// a broken product, and until this pass it was the literal behaviour: no
// consumer existed, so every story sat pending forever.
// ─────────────────────────────────────────────────────────────────────────────
func TestLiveApprovedStoryBecomesVisibleAndRejectedNeverDoes(t *testing.T) {
	pool := liveStoryPool(t)
	st := &Store{db: pool}
	ctx := context.Background()
	author := uuid.New()

	approvedMedia := seedMedia(t, pool, author, "image", "ready", "passed")
	rejectedMedia := seedMedia(t, pool, author, "image", "ready", "passed")

	good, err := st.CreateStoryPending(ctx, newStory(author, approvedMedia, "image"), "")
	if err != nil {
		t.Fatalf("create good: %v", err)
	}
	bad, err := st.CreateStoryPending(ctx, newStory(author, rejectedMedia, "image"), "")
	if err != nil {
		t.Fatalf("create bad: %v", err)
	}

	// Both invisible while pending.
	if rows, _ := st.GetStoriesByAuthor(ctx, author); len(rows) != 0 {
		t.Fatalf("%d stories visible before any decision", len(rows))
	}

	if ok, err := st.ApplyStoryModerationDecision(ctx, good.ID, 1,
		"approved", "d-approve", "", "keyword-v1"); err != nil || !ok {
		t.Fatalf("approve did not apply: ok=%v err=%v", ok, err)
	}
	if ok, err := st.ApplyStoryModerationDecision(ctx, bad.ID, 1,
		"rejected", "d-reject", "caption violates content policy", "keyword-v1"); err != nil || !ok {
		t.Fatalf("reject did not apply: ok=%v err=%v", ok, err)
	}

	rows, err := st.GetStoriesByAuthor(ctx, author)
	if err != nil {
		t.Fatalf("author read: %v", err)
	}
	if len(rows) != 1 || rows[0].ID != good.ID {
		t.Fatalf("expected exactly the approved story to be visible, got %d rows", len(rows))
	}

	// The author sees BOTH, with truthful states and the rejection reason —
	// otherwise a rejected upload is indistinguishable from one that vanished.
	owned, err := st.GetStoriesForOwner(ctx, author)
	if err != nil {
		t.Fatalf("owner read: %v", err)
	}
	if len(owned) != 2 {
		t.Fatalf("owner sees %d of their 2 stories", len(owned))
	}
	states := map[string]string{}
	for _, s := range owned {
		states[s.ID.String()] = s.ModerationState
		if s.ID == bad.ID && s.ModerationReason == "" {
			t.Error("a rejected story gives its author no reason")
		}
	}
	if states[good.ID.String()] != "approved" || states[bad.ID.String()] != "rejected" {
		t.Fatalf("owner states wrong: %+v", states)
	}
}
