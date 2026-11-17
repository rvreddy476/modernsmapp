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

// REG-5 (migration 022): a "post became visible" trigger that lands while
// the job is RUNNING (claimed, post read as not visible, defer not yet
// written) is remembered, so the pass leaves the job due now instead of on
// the backoff curve. The running pass is not disturbed, a completed job is
// never reopened, and a live job is never marked.
func TestFanoutJobsIntegration_TriggerDuringRunningPassIsKept(t *testing.T) {
	pool := notificationTestPool(t)
	store := New(pool)
	ctx := context.Background()
	far := time.Now().Add(time.Hour)

	newJob := func(t *testing.T) *FanoutJob {
		t.Helper()
		post := uuid.New()
		t.Cleanup(func() {
			_, _ = pool.Exec(context.Background(), `DELETE FROM subscriber_fanout_jobs WHERE post_id = $1`, post)
		})
		return &FanoutJob{
			PostID: post, ChannelID: uuid.New(), AuthorID: uuid.New(),
			ContentType: "long_video", DeepLink: "/tube/watch/" + post.String(),
			NotifType: "creator_uploaded_video", Visibility: "public",
			PostCreatedAt: time.Now().UTC(),
		}
	}
	wake := func(t *testing.T, post uuid.UUID) bool {
		t.Helper()
		var w bool
		if err := pool.QueryRow(context.Background(),
			`SELECT wake_requested FROM subscriber_fanout_jobs WHERE post_id = $1`, post).Scan(&w); err != nil {
			t.Fatalf("read wake: %v", err)
		}
		return w
	}
	// startPass enqueues, claims, and fires the trigger mid-pass.
	startPass := func(t *testing.T, triggered bool) *FanoutJob {
		t.Helper()
		job := newJob(t)
		if err := store.EnqueueFanoutJob(ctx, job); err != nil {
			t.Fatal(err)
		}
		if !claimable(t, store, job.PostID) {
			t.Fatal("fresh job not claimable")
		}
		if triggered {
			if err := store.EnqueueFanoutJob(ctx, job); err != nil {
				t.Fatal(err)
			}
			if st, _, _, _ := jobStatus(t, store, job.PostID); st != "running" {
				t.Fatalf("the trigger disturbed the running pass: %s", st)
			}
			if !wake(t, job.PostID) {
				t.Fatal("the trigger on a running job was dropped")
			}
		}
		return job
	}

	ends := map[string]func(post uuid.UUID) error{
		"defer":   func(p uuid.UUID) error { return store.DeferFanoutJob(ctx, p, "processing", far) },
		"release": func(p uuid.UUID) error { return store.ReleaseFanoutJob(ctx, p, "post-service 502", far) },
		"fail":    func(p uuid.UUID) error { return store.FailFanoutJob(ctx, p, "exceeded max attempts") },
		"expire":  func(p uuid.UUID) error { return store.ExpireFanoutJob(ctx, p, "processing") },
	}
	for name, end := range ends {
		t.Run(name+" after trigger is due now", func(t *testing.T) {
			job := startPass(t, true)
			if err := end(job.PostID); err != nil {
				t.Fatal(err)
			}
			if st, reason, waits, attempts := jobStatus(t, store, job.PostID); st != "pending" || reason != "" || waits != 0 || attempts != 0 {
				t.Fatalf("after %s: %s %q waits=%d attempts=%d", name, st, reason, waits, attempts)
			}
			if wake(t, job.PostID) {
				t.Fatal("wake flag survived the pass")
			}
			if !claimable(t, store, job.PostID) {
				t.Fatalf("a trigger during the pass left the job parked after %s", name)
			}
		})
		t.Run(name+" without trigger keeps its schedule", func(t *testing.T) {
			job := startPass(t, false)
			if err := end(job.PostID); err != nil {
				t.Fatal(err)
			}
			if claimable(t, store, job.PostID) {
				t.Fatalf("an untriggered %s made the job due at once", name)
			}
		})
	}

	t.Run("a pass that completes is final", func(t *testing.T) {
		job := startPass(t, true)
		if err := store.CompleteFanoutJob(ctx, job.PostID); err != nil {
			t.Fatal(err)
		}
		if err := store.EnqueueFanoutJob(ctx, job); err != nil {
			t.Fatal(err)
		}
		if st, _, _, _ := jobStatus(t, store, job.PostID); st != "completed" || wake(t, job.PostID) {
			t.Fatalf("completed job touched: %s wake=%v", st, wake(t, job.PostID))
		}
	})

	t.Run("a new claim starts clean", func(t *testing.T) {
		job := startPass(t, true)
		// The worker died mid-pass: the stale claim is reclaimed and reads
		// the post afresh, so the flag it carried is spent.
		if _, err := pool.Exec(ctx, `UPDATE subscriber_fanout_jobs SET claimed_at = NOW() - INTERVAL '2 hours' WHERE post_id = $1`, job.PostID); err != nil {
			t.Fatal(err)
		}
		if !claimable(t, store, job.PostID) {
			t.Fatal("stale running job not reclaimed")
		}
		if wake(t, job.PostID) {
			t.Fatal("a fresh claim kept the previous pass's wake")
		}
	})

	t.Run("a running live job is never marked", func(t *testing.T) {
		job := newJob(t)
		job.NotifType, job.ContentType, job.Phase = "creator_went_live", "live", FanoutPhaseReminders
		if err := store.EnqueueFanoutJob(ctx, job); err != nil {
			t.Fatal(err)
		}
		if !claimable(t, store, job.PostID) {
			t.Fatal("live job not claimable")
		}
		if err := store.EnqueueFanoutJob(ctx, job); err != nil {
			t.Fatal(err)
		}
		if wake(t, job.PostID) {
			t.Fatal("a redelivered live.stream.started marked the running live job")
		}
	})
}
