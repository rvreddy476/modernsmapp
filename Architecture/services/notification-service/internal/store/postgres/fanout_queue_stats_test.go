package postgres

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
)

// Integration (lv-21): the queue gauges' read sees a due job by how long it
// has gone unclaimed, a waiting job by its age, and a running job by its
// last progress. Runs only against NOTIFICATION_TEST_DSN (name *_test).
// Other tests may leave rows behind, so the checks are lower bounds.
func TestFanoutQueueStatsIntegration(t *testing.T) {
	pool := notificationTestPool(t)
	store := New(pool)
	ctx := context.Background()
	due, waiting, running := uuid.New(), uuid.New(), uuid.New()
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM subscriber_fanout_jobs WHERE post_id = ANY($1)`,
			[]uuid.UUID{due, waiting, running})
	})
	for _, id := range []uuid.UUID{due, waiting, running} {
		if err := store.EnqueueFanoutJob(ctx, &FanoutJob{
			PostID: id, ChannelID: uuid.New(), AuthorID: uuid.New(), ContentType: "long_video",
			DeepLink: "/tube/watch/" + id.String(), NotifType: "creator_uploaded_video",
			Visibility: "public", PostCreatedAt: time.Now().UTC(),
		}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := pool.Exec(ctx, `UPDATE subscriber_fanout_jobs SET available_at = NOW() - INTERVAL '20 minutes' WHERE post_id = $1`, due); err != nil {
		t.Fatal(err)
	}
	if err := store.DeferFanoutJob(ctx, waiting, "processing", time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE subscriber_fanout_jobs SET created_at = NOW() - INTERVAL '3 hours' WHERE post_id = $1`, waiting); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE subscriber_fanout_jobs SET status = 'running', claimed_at = NOW() - INTERVAL '2 minutes' WHERE post_id = $1`, running); err != nil {
		t.Fatal(err)
	}

	st, err := store.FanoutQueueStats(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if st.Due < 1 || st.OldestDue < 19*time.Minute {
		t.Errorf("due = %d oldest %v, want >= 1 and >= 19m", st.Due, st.OldestDue)
	}
	if st.Waiting < 1 || st.OldestWaiting < 179*time.Minute {
		t.Errorf("waiting = %d oldest %v, want >= 1 and >= 179m", st.Waiting, st.OldestWaiting)
	}
	if st.Running < 1 || st.OldestRunning < 110*time.Second {
		t.Errorf("running = %d oldest %v, want >= 1 and >= 110s", st.Running, st.OldestRunning)
	}
}
