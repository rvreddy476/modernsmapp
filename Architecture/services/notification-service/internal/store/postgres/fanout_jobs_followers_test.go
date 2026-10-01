package postgres

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
)

// Integration: the follower phase of a "creator is live" job in the real
// subscriber_fanout_jobs table (migration 012). Runs only against
// NOTIFICATION_TEST_DSN, whose database name must end in _test
// (notificationTestPool refuses others).

func TestFanoutJobsIntegration_LiveJobFollowerPhaseAndCursor(t *testing.T) {
	pool := notificationTestPool(t)
	store := New(pool)
	ctx := context.Background()
	stream, creator := uuid.New(), uuid.New()
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM subscriber_fanout_jobs WHERE post_id = $1`, stream)
		_, _ = pool.Exec(context.Background(), `DELETE FROM subscriber_fanout_delivered WHERE post_id = $1`, stream)
	})
	reclaim := func() FanoutJob {
		t.Helper()
		if err := store.ReleaseFanoutJob(ctx, stream, "retry"); err != nil {
			t.Fatal(err)
		}
		return claimOne(t, store, stream)
	}

	if err := store.EnqueueFanoutJob(ctx, &FanoutJob{
		PostID: stream, ChannelID: uuid.New(), AuthorID: creator,
		ContentType: "live", DeepLink: "/posttube/live/" + stream.String(),
		NotifType: "creator_went_live", Visibility: "public",
		PostCreatedAt: time.Now().UTC(), Title: "Friday Q&A",
		Phase: FanoutPhaseReminders,
	}); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	got := claimOne(t, store, stream)
	if got.Phase != FanoutPhaseReminders || got.FollowerCursor != "" {
		t.Fatalf("new job: phase %q follower cursor %q", got.Phase, got.FollowerCursor)
	}

	// Reminders, then a page of subscribers: the first two walks.
	if err := store.AdvanceFanoutReminders(ctx, stream, "rem-tok", 2, true); err != nil {
		t.Fatalf("reminders done: %v", err)
	}
	subCursor := uuid.New()
	if err := store.AdvanceFanoutCursor(ctx, stream, subCursor, 3); err != nil {
		t.Fatalf("subscriber page: %v", err)
	}
	got = reclaim()
	if got.Phase != FanoutPhaseSubscribers {
		t.Fatalf("phase = %q before the hand-over, want subscribers", got.Phase)
	}

	// Hand-over: the job is in the follower phase after a re-claim, with
	// the follower walk at its start and the other cursors untouched.
	if err := store.BeginFanoutFollowers(ctx, stream); err != nil {
		t.Fatalf("begin followers: %v", err)
	}
	got = reclaim()
	if got.Phase != FanoutPhaseFollowers || got.FollowerCursor != "" {
		t.Fatalf("after hand-over: phase %q follower cursor %q", got.Phase, got.FollowerCursor)
	}
	if got.ReminderCursor != "rem-tok" || got.Cursor != subCursor || got.Delivered != 5 {
		t.Fatalf("hand-over disturbed the earlier walks: %+v", got)
	}

	// A follower page delivered: the token moves and survives a re-claim
	// (which is what a crash looks like to the next worker).
	tok := "1759400000000000:" + uuid.NewString()
	if err := store.AdvanceFanoutFollowers(ctx, stream, tok, 100); err != nil {
		t.Fatalf("advance followers: %v", err)
	}
	got = reclaim()
	if got.Phase != FanoutPhaseFollowers || got.FollowerCursor != tok || got.Delivered != 105 {
		t.Fatalf("after a follower page: phase %q cursor %q delivered %d", got.Phase, got.FollowerCursor, got.Delivered)
	}

	// A page with a failed recipient writes the same token back with the
	// partial count: pinned, and the phase holds.
	if err := store.AdvanceFanoutFollowers(ctx, stream, tok, 7); err != nil {
		t.Fatalf("pinned advance: %v", err)
	}
	// A late duplicate hand-over must not reset anything.
	if err := store.BeginFanoutFollowers(ctx, stream); err != nil {
		t.Fatalf("second begin: %v", err)
	}
	got = reclaim()
	if got.Phase != FanoutPhaseFollowers || got.FollowerCursor != tok || got.Delivered != 112 {
		t.Fatalf("after a pinned page: phase %q cursor %q delivered %d", got.Phase, got.FollowerCursor, got.Delivered)
	}
	if got.ReminderCursor != "rem-tok" || got.Cursor != subCursor {
		t.Fatalf("follower writes disturbed the earlier cursors: %+v", got)
	}

	// The follower advance refreshes the claim, so a long walk is not
	// reclaimed from under a live worker as stale.
	if _, err := pool.Exec(ctx, `UPDATE subscriber_fanout_jobs SET claimed_at = NOW() - INTERVAL '2 hours' WHERE post_id = $1`, stream); err != nil {
		t.Fatal(err)
	}
	if err := store.AdvanceFanoutFollowers(ctx, stream, tok, 0); err != nil {
		t.Fatal(err)
	}
	var fresh bool
	if err := pool.QueryRow(ctx, `SELECT claimed_at > NOW() - INTERVAL '1 minute' FROM subscriber_fanout_jobs WHERE post_id = $1`, stream).Scan(&fresh); err != nil || !fresh {
		t.Fatalf("claimed_at refreshed = %v (%v)", fresh, err)
	}

	// Still one notification per (stream, user) in the third phase.
	user := uuid.New()
	if first, err := store.MarkDelivered(ctx, stream, user); err != nil || !first {
		t.Fatalf("first marker = %v, %v", first, err)
	}
	if second, err := store.MarkDelivered(ctx, stream, user); err != nil || second {
		t.Fatalf("second marker = %v, %v; want already delivered", second, err)
	}
}

// An upload job is unchanged by migration 012: born in the subscriber
// phase with no follower cursor.
func TestFanoutJobsIntegration_UploadJobHasNoFollowerCursor(t *testing.T) {
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
	if got.Phase != FanoutPhaseSubscribers || got.FollowerCursor != "" {
		t.Fatalf("upload job phase %q follower cursor %q", got.Phase, got.FollowerCursor)
	}
}
