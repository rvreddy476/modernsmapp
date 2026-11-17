//go:build integration

package postgres_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/atpost/post-service/internal/store/postgres"
	"github.com/atpost/shared/events"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Video publish gate and scheduled video publish against a real Postgres
// (reels-1, lv-17; 2026-11-17). Same database as the scheduled tests:
//
//	SCHEDULE_POSTGRES_DSN=postgres://…_test go test -tags integration ./internal/store/postgres/ -run VideoGate

// newVideoPost creates a video post whose video_metadata row rides in the
// create transaction, pointing at a fresh media_assets row in the given state.
func newVideoPost(t *testing.T, pool *pgxpool.Pool, st *postgres.Store, review, uploadStatus, processing, moderation string, publishAt *time.Time) (*postgres.Post, uuid.UUID) {
	t.Helper()
	ctx := context.Background()
	author, media := uuid.New(), uuid.New()
	if _, err := pool.Exec(ctx, `INSERT INTO media_assets (id, uploader_id, file_type, processing_status, moderation_status)
		VALUES ($1, $2, 'video', $3, $4)`, media, author, processing, moderation); err != nil {
		t.Fatal(err)
	}
	p := &postgres.Post{
		ID: uuid.New(), AuthorID: author, Text: "video gate proof " + uuid.NewString()[:8],
		Visibility: "public", ContentType: "flick", PostType: "video", AppOrigin: "postbook",
		ReviewStatus: review, CreatedAt: time.Now().UTC().Add(-time.Hour),
		VideoMetadataWrite: &postgres.VideoMetadata{
			Orientation: "portrait", ComputedCategory: "flick", FinalCategory: "flick",
			UploadStatus: uploadStatus, MediaAssetID: &media, DurationSeconds: 30,
		},
	}
	if publishAt != nil {
		at := publishAt.UTC()
		p.PublishAt = &at
	}
	if err := st.CreatePostWithEvent(ctx, p, "", nil); err != nil {
		t.Fatalf("create video post: %v", err)
	}
	return p, media
}

// reels-1 (b): the create transaction writes video_metadata, so the
// consumer's lookup by media finds the post at once.
func TestVideoGate_CreateWritesVideoMetadata(t *testing.T) {
	pool := openScheduleDB(t)
	st := postgres.New(pool)
	p, media := newVideoPost(t, pool, st, "pending", "pending", "processing", "pending", nil)
	vm, err := st.GetVideoMetadataByMediaAsset(context.Background(), media)
	if err != nil || vm == nil || vm.PostID != p.ID {
		t.Fatalf("video_metadata by media: %+v %v", vm, err)
	}
}

// reels-1 (c): the reconciler's scan lists only old pending video posts with
// their media state; MarkVideoMetadataFailed leaves a ready row alone.
func TestVideoGate_PendingListAndFailedMark(t *testing.T) {
	pool := openScheduleDB(t)
	st := postgres.New(pool)
	ctx := context.Background()
	pending, _ := newVideoPost(t, pool, st, "pending", "pending", "failed", "", nil)
	approved, _ := newVideoPost(t, pool, st, "approved", "ready", "ready", "passed", nil)

	rows, err := st.ListPendingVideoPosts(ctx, time.Now().UTC(), nil, 10000)
	if err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, r := range rows {
		if r.PostID == approved.ID {
			t.Fatal("an approved post was listed as pending")
		}
		if r.PostID == pending.ID {
			found = true
			if r.ProcessingStatus != "failed" {
				t.Fatalf("media state = %q, want failed", r.ProcessingStatus)
			}
		}
	}
	if !found {
		t.Fatal("pending video post missing from the reconciler scan")
	}
	if rows, _ := st.ListPendingVideoPosts(ctx, time.Now().UTC().Add(-2*time.Hour), nil, 10000); containsPost(rows, pending.ID) {
		t.Fatal("a post younger than the threshold was listed")
	}

	if err := st.MarkVideoMetadataFailed(ctx, pending.ID); err != nil {
		t.Fatal(err)
	}
	if err := st.MarkVideoMetadataFailed(ctx, approved.ID); err != nil {
		t.Fatal(err)
	}
	var a, b string
	_ = pool.QueryRow(ctx, `SELECT upload_status FROM video_metadata WHERE post_id = $1`, pending.ID).Scan(&a)
	_ = pool.QueryRow(ctx, `SELECT upload_status FROM video_metadata WHERE post_id = $1`, approved.ID).Scan(&b)
	if a != "failed" || b != "ready" {
		t.Fatalf("upload_status pending=%q approved=%q, want failed/ready", a, b)
	}
	if ok, err := st.FlipReviewStatusFromPending(ctx, pending.ID, "rejected"); !ok || err != nil {
		t.Fatalf("flip: %v %v", ok, err)
	}
	if got, _ := st.GetPendingVideoPost(ctx, pending.ID); got != nil {
		t.Fatal("a flipped post is still reported pending")
	}
}

func containsPost(rows []postgres.PendingVideoPost, id uuid.UUID) bool {
	for _, r := range rows {
		if r.PostID == id {
			return true
		}
	}
	return false
}

// lv-17: a due scheduled video post still transcoding is neither listed nor
// flipped by the worker; once ready and approved it publishes exactly once.
func TestVideoGate_ScheduledVideoWaitsForReady(t *testing.T) {
	pool := openScheduleDB(t)
	st := postgres.New(pool)
	ctx := context.Background()
	due := time.Now().Add(-time.Minute)
	p, _ := newVideoPost(t, pool, st, "pending", "processing", "processing", "pending", &due)

	now := time.Now().UTC()
	build := func(rev int64) (string, interface{}, error) {
		return events.PostCreated, events.PostCreatedPayload{PostID: p.ID.String(), SearchRev: rev, CreatedAt: now}, nil
	}
	if list, err := st.ListDueScheduledPosts(ctx, now, 10000); err != nil || containsCandidate(list, p.ID) {
		t.Fatalf("a transcoding video was listed as due: err=%v", err)
	}
	if res, err := st.PublishScheduledPost(ctx, p.ID, nil, now, true, build); err != nil || res != nil {
		t.Fatalf("the worker published a transcoding video: %v %v", res, err)
	}

	// Ready but the review gate still pending: still waits.
	if _, err := pool.Exec(ctx, `UPDATE video_metadata SET upload_status = 'ready' WHERE post_id = $1`, p.ID); err != nil {
		t.Fatal(err)
	}
	if res, _ := st.PublishScheduledPost(ctx, p.ID, nil, now, true, build); res != nil {
		t.Fatal("published while the review gate was pending")
	}

	if ok, err := st.FlipReviewStatusFromPending(ctx, p.ID, "approved"); !ok || err != nil {
		t.Fatalf("approve: %v %v", ok, err)
	}
	if list, _ := st.ListDueScheduledPosts(ctx, now, 10000); !containsCandidate(list, p.ID) {
		t.Fatal("ready and approved, but not listed as due")
	}
	if res, err := st.PublishScheduledPost(ctx, p.ID, nil, now, true, build); err != nil || res == nil {
		t.Fatalf("ready post not published: %v %v", res, err)
	}
	if res, _ := st.PublishScheduledPost(ctx, p.ID, nil, now, true, build); res != nil {
		t.Fatal("published twice")
	}
	if n := outboxCount(t, pool, p.ID, events.PostCreated); n != 1 {
		t.Fatalf("PostCreated rows = %d, want 1", n)
	}
}

func containsCandidate(list []postgres.ScheduledCandidate, id uuid.UUID) bool {
	for _, c := range list {
		if c.PostID == id {
			return true
		}
	}
	return false
}

// lastEligibility is the newest PostSearchEligibilityChanged for a post.
func lastEligibility(t *testing.T, pool *pgxpool.Pool, id uuid.UUID) events.PostSearchEligibilityChangedPayload {
	t.Helper()
	var raw []byte
	if err := pool.QueryRow(context.Background(), `SELECT payload FROM post_outbox_events
		WHERE aggregate_id = $1 AND event_type = $2 ORDER BY created_at DESC, ctid DESC LIMIT 1`,
		id, events.PostSearchEligibilityChanged).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	var p events.PostSearchEligibilityChangedPayload
	if err := json.Unmarshal(raw, &p); err != nil {
		t.Fatal(err)
	}
	return p
}

// The notification lane's second trigger is PostSearchEligibilityChanged: a
// scheduled video publishing writes one, eligible, at the same revision as
// its PostCreated (the flip used to write only PostCreated). The review gate
// writes one, and so does an edit to an indexed field.
func TestVideoGate_ScheduledPublishWritesEligibility(t *testing.T) {
	pool := openScheduleDB(t)
	st := postgres.New(pool)
	ctx := context.Background()
	due := time.Now().Add(-time.Minute)
	p, _ := newVideoPost(t, pool, st, "pending", "ready", "ready", "passed", &due)

	if ok, err := st.FlipReviewStatusFromPending(ctx, p.ID, "approved"); !ok || err != nil {
		t.Fatalf("approve: %v %v", ok, err)
	}
	if el := lastEligibility(t, pool, p.ID); !el.Scheduled {
		t.Fatalf("the gate's event for a scheduled post = %+v, want scheduled (not yet public)", el)
	}
	before := outboxCount(t, pool, p.ID, events.PostSearchEligibilityChanged)
	now := time.Now().UTC()
	var createdRev int64
	res, err := st.PublishScheduledPost(ctx, p.ID, nil, now, true, func(rev int64) (string, interface{}, error) {
		createdRev = rev
		return events.PostCreated, events.PostCreatedPayload{PostID: p.ID.String(), SearchRev: rev, CreatedAt: now}, nil
	})
	if err != nil || res == nil {
		t.Fatalf("publish: %v %v", res, err)
	}
	if n := outboxCount(t, pool, p.ID, events.PostSearchEligibilityChanged); n != before+1 {
		t.Fatalf("eligibility events written by the publish = %d, want 1", n-before)
	}
	el := lastEligibility(t, pool, p.ID)
	if el.Scheduled || !events.SearchEligible(el.Visibility, el.ReviewStatus, el.Deleted) || el.SearchRev != createdRev || res.SearchRev != createdRev {
		t.Fatalf("publish eligibility = %+v, PostCreated rev %d, result rev %d", el, createdRev, res.SearchRev)
	}

	// An edit to an indexed field re-indexes; a non-indexed one does not.
	title, off := "new title "+uuid.NewString()[:6], false
	before = outboxCount(t, pool, p.ID, events.PostSearchEligibilityChanged)
	if _, err := st.UpdatePostFields(ctx, p.ID, p.AuthorID, postgres.PostEditPatch{Title: &title}); err != nil {
		t.Fatal(err)
	}
	if n := outboxCount(t, pool, p.ID, events.PostSearchEligibilityChanged); n != before+1 {
		t.Fatalf("title edit re-index events = %d, want 1", n-before)
	}
	if el := lastEligibility(t, pool, p.ID); el.Title != title {
		t.Fatalf("re-index carries title %q, want %q", el.Title, title)
	}
	if _, err := st.UpdatePostFields(ctx, p.ID, p.AuthorID, postgres.PostEditPatch{AllowDownload: &off}); err != nil {
		t.Fatal(err)
	}
	if n := outboxCount(t, pool, p.ID, events.PostSearchEligibilityChanged); n != before+1 {
		t.Fatal("a non-indexed edit re-indexed the post")
	}
}
