//go:build integration

package postgres_test

import (
	"context"
	"testing"
	"time"

	"github.com/atpost/post-service/internal/store/postgres"
	"github.com/atpost/shared/events"
	"github.com/google/uuid"
)

// GET /v1/posts/live-recordings against a real Postgres (2026-09-27).
//
//	POSTGRES_DSN=postgres://…/post_it_test go test -tags integration ./internal/store/postgres/ -run LiveRecordings -v
//
// The listing is a filter and nothing else, so each exclusion gets its own
// row: a recording that is still unlisted, one held for review, one soft
// deleted, one still scheduled, and an ordinary public upload. Only the two
// public, approved, live-sourced rows may come back, newest first, and the
// keyset cursor must walk from one to the other.
func TestMTubeLiveRecordingsListsOnlyPublicApprovedLiveRows(t *testing.T) {
	r := newMTubeRig(t)
	ctx := context.Background()
	// Future timestamps put this test's rows at the head of the listing
	// whatever else the scratch database holds.
	base := time.Now().Add(48 * time.Hour).UTC().Truncate(time.Millisecond)

	mk := func(label string, at time.Time, source, visibility, review string, deleted, scheduled bool) uuid.UUID {
		t.Helper()
		p := r.newPost(t, r.owner, "long_video", visibility, "")
		if _, err := r.pool.Exec(ctx, `UPDATE posts SET created_at = $2, review_status = $3 WHERE id = $1`, p.ID, at, review); err != nil {
			t.Fatalf("%s: %v", label, err)
		}
		if source == "live" {
			if _, err := r.pool.Exec(ctx, `UPDATE posts SET source = 'live', live_stream_id = $2 WHERE id = $1`, p.ID, uuid.New()); err != nil {
				t.Fatalf("%s: %v", label, err)
			}
		}
		if deleted {
			if _, err := r.pool.Exec(ctx, `UPDATE posts SET deleted_at = NOW() WHERE id = $1`, p.ID); err != nil {
				t.Fatalf("%s: %v", label, err)
			}
		}
		if scheduled {
			if _, err := r.pool.Exec(ctx, `UPDATE posts SET publish_at = $2 WHERE id = $1`, p.ID, at.Add(time.Hour)); err != nil {
				t.Fatalf("%s: %v", label, err)
			}
		}
		return p.ID
	}

	newer := mk("newer", base.Add(2*time.Second), "live", "public", "approved", false, false)
	older := mk("older", base.Add(1*time.Second), "live", "public", "approved", false, false)
	excluded := map[uuid.UUID]string{
		mk("unlisted", base.Add(3*time.Second), "live", "unlisted", "approved", false, false): "unlisted recording",
		mk("pending", base.Add(4*time.Second), "live", "public", "pending", false, false):     "recording held for review",
		mk("deleted", base.Add(5*time.Second), "live", "public", "approved", true, false):     "deleted recording",
		mk("scheduled", base.Add(6*time.Second), "live", "public", "approved", false, true):   "scheduled recording",
		mk("upload", base.Add(7*time.Second), "upload", "public", "approved", false, false):   "ordinary upload",
	}

	page, next, err := r.store.ListLiveRecordings(ctx, 50, "")
	if err != nil {
		t.Fatal(err)
	}
	pos := map[uuid.UUID]int{}
	for i, p := range page {
		if why, bad := excluded[p.ID]; bad {
			t.Fatalf("the listing returned a %s", why)
		}
		if p.Source != "live" || p.Visibility != "public" || p.ReviewStatus != "approved" {
			t.Fatalf("row %s: source=%q visibility=%q review=%q", p.ID, p.Source, p.Visibility, p.ReviewStatus)
		}
		pos[p.ID] = i
	}
	pn, okN := pos[newer]
	po, okO := pos[older]
	if !okN || !okO || pn >= po {
		t.Fatalf("want both live recordings, newest first: newer=%d(%v) older=%d(%v) next=%q", pn, okN, po, okO, next)
	}

	// Keyset: a page of one is the newest; its cursor leads to the other.
	first, cursor, err := r.store.ListLiveRecordings(ctx, 1, "")
	if err != nil || len(first) != 1 || first[0].ID != newer || cursor == "" {
		t.Fatalf("first page: %v cursor=%q err=%v", first, cursor, err)
	}
	second, _, err := r.store.ListLiveRecordings(ctx, 1, cursor)
	if err != nil || len(second) != 1 || second[0].ID != older {
		t.Fatalf("second page: %v err=%v", second, err)
	}
}

// The eligibility event carries the frame height post-service recorded in
// video_metadata, and leaves has_subtitles unset.
func TestMTubeSearchEligibilityCarriesVideoHeight(t *testing.T) {
	r := newMTubeRig(t)
	ctx := context.Background()
	p := r.newPost(t, r.owner, "long_video", "public", "")
	if _, err := r.pool.Exec(ctx, `
		INSERT INTO video_metadata (post_id, duration_seconds, height, orientation, trim_start_ms, computed_category, final_category, upload_status, created_at, updated_at)
		VALUES ($1, 90, 1080, 'landscape', 0, 'long_video', 'long_video', 'ready', NOW(), NOW())`, p.ID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = r.pool.Exec(context.Background(), `DELETE FROM video_metadata WHERE post_id = $1`, p.ID)
	})

	tx, err := r.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	if err := postgres.BumpSearchRevAndEmitTx(ctx, tx, p.ID); err != nil {
		t.Fatal(err)
	}
	var payload events.PostSearchEligibilityChangedPayload
	if err := tx.QueryRow(ctx, `
		SELECT payload FROM post_outbox_events
		WHERE aggregate_id = $1 AND event_type = $2 ORDER BY created_at DESC LIMIT 1`,
		p.ID, events.PostSearchEligibilityChanged).Scan(&payload); err != nil {
		t.Fatal(err)
	}
	if payload.Height != 1080 || payload.HasSubtitles {
		t.Fatalf("height=%d has_subtitles=%v, want 1080/false", payload.Height, payload.HasSubtitles)
	}
}
