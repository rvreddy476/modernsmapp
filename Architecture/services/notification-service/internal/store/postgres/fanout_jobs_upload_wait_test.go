package postgres

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
)

// Integration (migration 021): an upload job waits for its post, wakes on
// the second trigger, is reopened after failing, and is never run twice
// once completed. Runs only against NOTIFICATION_TEST_DSN (name *_test).

func claimable(t *testing.T, store *Store, postID uuid.UUID) bool {
	t.Helper()
	for i := 0; i < 20; i++ {
		jobs, err := store.ClaimFanoutJobs(context.Background(), time.Hour, 50)
		if err != nil {
			t.Fatalf("claim: %v", err)
		}
		for _, j := range jobs {
			if j.PostID == postID {
				return true
			}
		}
		if len(jobs) == 0 {
			return false
		}
	}
	return false
}

func jobStatus(t *testing.T, store *Store, postID uuid.UUID) (status, waitReason string, waits, attempts int) {
	t.Helper()
	if err := store.db.QueryRow(context.Background(),
		`SELECT status, wait_reason, waits, attempts FROM subscriber_fanout_jobs WHERE post_id = $1`, postID).
		Scan(&status, &waitReason, &waits, &attempts); err != nil {
		t.Fatalf("read job: %v", err)
	}
	return
}

func TestFanoutJobsIntegration_UploadWaitWakeAndExactlyOnce(t *testing.T) {
	pool := notificationTestPool(t)
	store := New(pool)
	ctx := context.Background()
	post := uuid.New()
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM subscriber_fanout_jobs WHERE post_id = $1`, post)
	})
	job := &FanoutJob{
		PostID: post, ChannelID: uuid.New(), AuthorID: uuid.New(),
		ContentType: "long_video", DeepLink: "/tube/watch/" + post.String(),
		NotifType: "creator_uploaded_video", Visibility: "public",
		PostCreatedAt: time.Now().UTC(),
	}
	if err := store.EnqueueFanoutJob(ctx, job); err != nil {
		t.Fatal(err)
	}
	if !claimable(t, store, post) {
		t.Fatal("fresh job not claimable")
	}

	// Post still processing: deferred an hour, the attempt given back.
	if err := store.DeferFanoutJob(ctx, post, "processing", time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if st, reason, waits, attempts := jobStatus(t, store, post); st != "pending" || reason != "processing" || waits != 1 || attempts != 0 {
		t.Fatalf("after defer: %s %s waits=%d attempts=%d", st, reason, waits, attempts)
	}
	if claimable(t, store, post) {
		t.Fatal("a deferred job was claimed before it was due")
	}

	// The second trigger (ready flip) wakes it, title filled if missing.
	again := *job
	again.Title = "Now ready"
	if err := store.EnqueueFanoutJob(ctx, &again); err != nil {
		t.Fatal(err)
	}
	if !claimable(t, store, post) {
		t.Fatal("the visible trigger did not wake the waiting job")
	}

	// Transient failure: released with backoff, not due yet.
	if err := store.ReleaseFanoutJob(ctx, post, "post-service 502", time.Now().Add(10*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if claimable(t, store, post) {
		t.Fatal("a released job was claimed before its backoff")
	}

	// Failed, then replayed by the sweep (cooldown 0 for the test).
	if err := store.FailFanoutJob(ctx, post, "exceeded max attempts"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.RequeueFailedFanoutJobs(ctx, 2, 0, time.Hour); err != nil {
		t.Fatal(err)
	}
	if !claimable(t, store, post) {
		t.Fatal("replay did not requeue the failed job")
	}

	// Completed: no trigger ever runs it again.
	if err := store.CompleteFanoutJob(ctx, post); err != nil {
		t.Fatal(err)
	}
	if err := store.EnqueueFanoutJob(ctx, &again); err != nil {
		t.Fatal(err)
	}
	if st, _, _, _ := jobStatus(t, store, post); st != "completed" {
		t.Fatalf("a repeat trigger reopened a completed job: %s", st)
	}

	// An expired wait is not auto-replayed, but a new trigger reopens it.
	expired := uuid.New()
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM subscriber_fanout_jobs WHERE post_id = $1`, expired)
	})
	ej := *job
	ej.PostID = expired
	if err := store.EnqueueFanoutJob(ctx, &ej); err != nil {
		t.Fatal(err)
	}
	if err := store.ExpireFanoutJob(ctx, expired, "visibility_private"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.RequeueFailedFanoutJobs(ctx, 2, 0, time.Hour); err != nil {
		t.Fatal(err)
	}
	if st, _, _, _ := jobStatus(t, store, expired); st != "failed" {
		t.Fatalf("expired wait was auto-replayed: %s", st)
	}
	if err := store.EnqueueFanoutJob(ctx, &ej); err != nil {
		t.Fatal(err)
	}
	if st, _, _, _ := jobStatus(t, store, expired); st != "pending" {
		t.Fatalf("made-public trigger did not reopen the expired job: %s", st)
	}
}

// Live jobs keep DO NOTHING: a redelivered live.stream.started never
// reopens a failed live job (a late "is live" push is worse than none).
func TestFanoutJobsIntegration_LiveJobNeverReopened(t *testing.T) {
	pool := notificationTestPool(t)
	store := New(pool)
	ctx := context.Background()
	stream := uuid.New()
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM subscriber_fanout_jobs WHERE post_id = $1`, stream)
	})
	job := &FanoutJob{
		PostID: stream, AuthorID: uuid.New(), ContentType: "live",
		DeepLink: "/posttube/live/" + stream.String(), NotifType: "creator_went_live",
		Visibility: "public", PostCreatedAt: time.Now().UTC(), Phase: FanoutPhaseReminders,
	}
	if err := store.EnqueueFanoutJob(ctx, job); err != nil {
		t.Fatal(err)
	}
	if err := store.FailFanoutJob(ctx, stream, "boom"); err != nil {
		t.Fatal(err)
	}
	if err := store.EnqueueFanoutJob(ctx, job); err != nil {
		t.Fatal(err)
	}
	if _, err := store.RequeueFailedFanoutJobs(ctx, 2, 0, time.Hour); err != nil {
		t.Fatal(err)
	}
	if st, _, _, _ := jobStatus(t, store, stream); st != "failed" {
		t.Fatalf("live job reopened: %s", st)
	}
}
