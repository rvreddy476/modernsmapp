package postgres

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
)

// Integration: a "creator is live" job in the real subscriber_fanout_jobs
// table (migration 011). Runs only against NOTIFICATION_TEST_DSN, whose
// database name must end in _test (notificationTestPool refuses others).

func claimOne(t *testing.T, store *Store, postID uuid.UUID) FanoutJob {
	t.Helper()
	// Other rows may be claimable in a shared scratch database; claim in
	// batches until ours turns up.
	for i := 0; i < 20; i++ {
		jobs, err := store.ClaimFanoutJobs(context.Background(), time.Hour, 50)
		if err != nil {
			t.Fatalf("claim: %v", err)
		}
		for _, j := range jobs {
			if j.PostID == postID {
				return j
			}
		}
		if len(jobs) == 0 {
			break
		}
	}
	t.Fatalf("job %s was not claimable", postID)
	return FanoutJob{}
}

func TestFanoutJobsIntegration_LiveJobPhasesAndIdempotency(t *testing.T) {
	pool := notificationTestPool(t)
	store := New(pool)
	ctx := context.Background()
	stream, creator := uuid.New(), uuid.New()
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM subscriber_fanout_jobs WHERE post_id = $1`, stream)
		_, _ = pool.Exec(context.Background(), `DELETE FROM subscriber_fanout_delivered WHERE post_id = $1`, stream)
	})

	job := &FanoutJob{
		PostID: stream, ChannelID: uuid.Nil, AuthorID: creator,
		ContentType: "live", DeepLink: "/posttube/live/" + stream.String(),
		NotifType: "creator_went_live", Visibility: "public",
		PostCreatedAt: time.Now().UTC(), Title: "Friday Q&A",
		Phase: FanoutPhaseReminders,
	}
	if err := store.EnqueueFanoutJob(ctx, job); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	// live.stream.started delivered twice: still one job, and the second
	// delivery does not reset the first one's content.
	dup := *job
	dup.Title = "a redelivery must not overwrite"
	if err := store.EnqueueFanoutJob(ctx, &dup); err != nil {
		t.Fatalf("enqueue again: %v", err)
	}
	var n int
	if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM subscriber_fanout_jobs WHERE post_id = $1`, stream).Scan(&n); err != nil || n != 1 {
		t.Fatalf("jobs for the stream = %d (%v), want 1", n, err)
	}

	got := claimOne(t, store, stream)
	if got.Phase != FanoutPhaseReminders || got.ReminderCursor != "" {
		t.Fatalf("claimed phase %q cursor %q, want reminders from the start", got.Phase, got.ReminderCursor)
	}
	if got.ChannelID != uuid.Nil || got.NotifType != "creator_went_live" || got.Title != "Friday Q&A" {
		t.Fatalf("claimed job: %+v", got)
	}

	// A page of reminders delivered, more to come: the token moves, the
	// phase does not.
	if err := store.AdvanceFanoutReminders(ctx, stream, "tok-1", 3, false); err != nil {
		t.Fatalf("advance: %v", err)
	}
	if err := store.ReleaseFanoutJob(ctx, stream, "retry"); err != nil {
		t.Fatal(err)
	}
	got = claimOne(t, store, stream)
	if got.Phase != FanoutPhaseReminders || got.ReminderCursor != "tok-1" || got.Delivered != 3 {
		t.Fatalf("after a page: phase %q cursor %q delivered %d", got.Phase, got.ReminderCursor, got.Delivered)
	}

	// Reminders exhausted: the job hands over to the subscriber phase and
	// the subscriber cursor is untouched.
	if err := store.AdvanceFanoutReminders(ctx, stream, "tok-1", 2, true); err != nil {
		t.Fatalf("advance done: %v", err)
	}
	if err := store.ReleaseFanoutJob(ctx, stream, "retry"); err != nil {
		t.Fatal(err)
	}
	got = claimOne(t, store, stream)
	if got.Phase != FanoutPhaseSubscribers || got.Delivered != 5 || got.Cursor != uuid.Nil {
		t.Fatalf("after reminders: phase %q delivered %d cursor %s", got.Phase, got.Delivered, got.Cursor)
	}

	// One notification per (stream, user): the marker is taken once.
	user := uuid.New()
	first, err := store.MarkDelivered(ctx, stream, user)
	if err != nil || !first {
		t.Fatalf("first marker = %v, %v", first, err)
	}
	second, err := store.MarkDelivered(ctx, stream, user)
	if err != nil || second {
		t.Fatalf("second marker = %v, %v; want already delivered", second, err)
	}
	if already, err := store.AlreadyDelivered(ctx, stream, user); err != nil || !already {
		t.Fatalf("AlreadyDelivered = %v, %v", already, err)
	}
}

// An upload job is unchanged by migration 011: it is born in the
// subscriber phase without anyone saying so.
func TestFanoutJobsIntegration_UploadJobDefaultsToSubscriberPhase(t *testing.T) {
	pool := notificationTestPool(t)
	store := New(pool)
	ctx := context.Background()
	post := uuid.New()
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM subscriber_fanout_jobs WHERE post_id = $1`, post)
	})
	if err := store.EnqueueFanoutJob(ctx, &FanoutJob{
		PostID: post, ChannelID: uuid.New(), AuthorID: uuid.New(),
		ContentType: "long_video", DeepLink: "/tube/watch/" + post.String(),
		NotifType: "creator_uploaded_video", Visibility: "public", PostCreatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	got := claimOne(t, store, post)
	if got.Phase != FanoutPhaseSubscribers || got.ReminderCursor != "" {
		t.Fatalf("upload job phase %q cursor %q", got.Phase, got.ReminderCursor)
	}
}
