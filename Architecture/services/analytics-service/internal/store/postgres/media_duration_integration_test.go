//go:build integration

package postgres_test

import (
	"context"
	"testing"
	"time"

	"github.com/atpost/analytics-service/internal/store/postgres"
	"github.com/google/uuid"
)

// Audit M6: the server-measured duration on the ownership projection
// (migration 015). It is what ingest judges every view against, so the
// write rules are pinned against the real statements.
func TestMediaDurationProjection(t *testing.T) {
	ctx := context.Background()
	pool := migrationPool(t)
	store := postgres.New(pool)
	creator, content := uuid.New(), uuid.New()
	created := time.Now().Add(-time.Hour).UTC()

	read := func() int64 {
		t.Helper()
		o, err := store.GetContentOwnership(ctx, content)
		if err != nil {
			t.Fatal(err)
		}
		return o.MediaDurationMS
	}

	// Published while processing: no duration yet.
	if err := store.UpsertContentOwnership(ctx, postgres.ContentOwnership{
		ContentID: content, CreatorID: creator, ContentType: "long_video", CreatedAt: created,
	}); err != nil {
		t.Fatal(err)
	}
	if got := read(); got != 0 {
		t.Fatalf("fresh row duration=%d want 0 (unknown)", got)
	}

	// The eligibility event that publishes it after transcode carries it.
	applied, err := store.ApplyContentEligibility(ctx, postgres.ContentEligibility{
		ContentID: content, CreatorID: creator, State: postgres.EligibilityEligible,
		EffectiveFrom: time.Now().UTC(), Rev: 2, MediaDurationMS: 7_200_000,
	})
	if err != nil || !applied {
		t.Fatalf("eligibility applied=%v err=%v", applied, err)
	}
	if got := read(); got != 7_200_000 {
		t.Fatalf("after eligibility duration=%d want 7200000", got)
	}

	// A stale eligibility event cannot rewrite it.
	applied, err = store.ApplyContentEligibility(ctx, postgres.ContentEligibility{
		ContentID: content, CreatorID: creator, State: postgres.EligibilityEligible,
		EffectiveFrom: time.Now().UTC(), Rev: 1, MediaDurationMS: 2_000,
	})
	if err != nil || applied {
		t.Fatalf("stale eligibility applied=%v err=%v", applied, err)
	}
	if got := read(); got != 7_200_000 {
		t.Fatalf("a stale event rewrote the duration to %d", got)
	}

	// An eligibility change without a duration (an ineligible transition
	// carries no body) leaves it alone.
	applied, err = store.ApplyContentEligibility(ctx, postgres.ContentEligibility{
		ContentID: content, CreatorID: creator, State: postgres.EligibilityIneligible,
		EffectiveFrom: time.Now().UTC(), Rev: 3,
	})
	if err != nil || !applied {
		t.Fatalf("ineligible applied=%v err=%v", applied, err)
	}
	if got := read(); got != 7_200_000 {
		t.Fatalf("a duration-less event erased the duration: %d", got)
	}

	// A replayed PostCreated from before transcode does not erase it ...
	if err := store.UpsertContentOwnership(ctx, postgres.ContentOwnership{
		ContentID: content, CreatorID: creator, ContentType: "long_video", CreatedAt: created,
	}); err != nil {
		t.Fatal(err)
	}
	if got := read(); got != 7_200_000 {
		t.Fatalf("a replayed PostCreated erased the duration: %d", got)
	}
	// ... and one that carries a measurement updates it.
	if err := store.UpsertContentOwnership(ctx, postgres.ContentOwnership{
		ContentID: content, CreatorID: creator, ContentType: "long_video", CreatedAt: created,
		MediaDurationMS: 7_100_000,
	}); err != nil {
		t.Fatal(err)
	}
	if got := read(); got != 7_100_000 {
		t.Fatalf("a measured PostCreated did not land: %d", got)
	}
}

// Migration 015's backfill reads post-service's post_media and
// media-service's media_assets where they share the database. This
// package's private database has neither, so the test creates minimal
// copies, runs the migration text again (it is written to tolerate a
// re-run), and drops them.
func TestMigration015BackfillsTheMeasuredDuration(t *testing.T) {
	ctx := context.Background()
	pool := migrationPool(t)
	store := postgres.New(pool)
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DROP TABLE IF EXISTS public.post_media, public.media_assets`)
	})
	if _, err := pool.Exec(ctx, `
		DROP TABLE IF EXISTS public.post_media, public.media_assets;
		CREATE TABLE public.media_assets (id UUID PRIMARY KEY, duration_seconds INT, duration_ms INT);
		CREATE TABLE public.post_media (post_id UUID NOT NULL, media_id UUID NOT NULL, kind TEXT NOT NULL,
			PRIMARY KEY (post_id, media_id))`); err != nil {
		t.Fatal(err)
	}

	creator := uuid.New()
	measured, secondsOnly, imageOnly, alreadySet := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	for _, c := range []uuid.UUID{measured, secondsOnly, imageOnly, alreadySet} {
		if err := store.UpsertContentOwnership(ctx, postgres.ContentOwnership{
			ContentID: c, CreatorID: creator, ContentType: "long_video", CreatedAt: time.Now().Add(-time.Hour),
		}); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.UpsertContentOwnership(ctx, postgres.ContentOwnership{
		ContentID: alreadySet, CreatorID: creator, ContentType: "long_video", CreatedAt: time.Now().Add(-time.Hour),
		MediaDurationMS: 5_000,
	}); err != nil {
		t.Fatal(err)
	}
	media := func(post uuid.UUID, kind string, seconds, ms *int) {
		t.Helper()
		id := uuid.New()
		if _, err := pool.Exec(ctx, `INSERT INTO public.media_assets (id, duration_seconds, duration_ms) VALUES ($1, $2, $3)`, id, seconds, ms); err != nil {
			t.Fatal(err)
		}
		if _, err := pool.Exec(ctx, `INSERT INTO public.post_media (post_id, media_id, kind) VALUES ($1, $2, $3)`, post, id, kind); err != nil {
			t.Fatal(err)
		}
	}
	i := func(v int) *int { return &v }
	media(measured, "video", i(61), i(61_500))
	media(measured, "video", i(30), i(30_000)) // the longest video wins
	media(secondsOnly, "video", i(42), nil)
	media(imageOnly, "image", nil, nil)
	media(alreadySet, "video", i(9), i(9_000))

	if _, err := pool.Exec(ctx, migrationSQL(t, "015_content_media_duration.sql")); err != nil {
		t.Fatalf("migration 015: %v", err)
	}
	for content, want := range map[uuid.UUID]int64{measured: 61_500, secondsOnly: 42_000, imageOnly: 0, alreadySet: 5_000} {
		o, err := store.GetContentOwnership(ctx, content)
		if err != nil {
			t.Fatal(err)
		}
		if o.MediaDurationMS != want {
			t.Fatalf("content %s backfilled to %d, want %d", content, o.MediaDurationMS, want)
		}
	}
}
