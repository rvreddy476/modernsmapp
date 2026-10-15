//go:build integration

package postgres_test

import (
	"context"
	"testing"
	"time"

	"github.com/atpost/analytics-service/internal/store/postgres"
	"github.com/atpost/analytics-service/internal/testsupport"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Migration 013 and the follower-delta store against a real database:
// receipts make redelivery a no-op, unfollows subtract, the window is
// UTC days, null vs 0 hangs on follower_tracking, and account erasure
// takes the creator's rows.

func followerPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	testsupport.RequireTestDSN(t)
	pool := testsupport.Pool(t, "pgstore")
	ctx := context.Background()
	if _, err := pool.Exec(ctx, `TRUNCATE analytics.creator_follower_daily, analytics.follower_event_receipts`); err != nil {
		t.Fatal(err)
	}
	// Every test starts with tracking on, whatever an earlier one did.
	if _, err := pool.Exec(ctx, `INSERT INTO analytics.follower_tracking (id) VALUES (1) ON CONFLICT (id) DO NOTHING`); err != nil {
		t.Fatal(err)
	}
	return pool
}

func day(y int, m time.Month, d int) time.Time { return time.Date(y, m, d, 15, 0, 0, 0, time.UTC) }

func applyFollower(t *testing.T, s *postgres.Store, id, kind string, deltas ...postgres.FollowerDelta) bool {
	t.Helper()
	applied, err := s.ApplyFollowerEvent(context.Background(), id, kind, deltas)
	if err != nil {
		t.Fatalf("apply %s: %v", id, err)
	}
	return applied
}

func TestFollowerDeltaRedeliveryIsIdempotentAndUnfollowsSubtract(t *testing.T) {
	pool := followerPool(t)
	ctx := context.Background()
	store := postgres.New(pool)
	agg := postgres.NewAggregateStore(pool)
	creator := uuid.New()
	today := day(2026, 10, 15)

	if !applyFollower(t, store, "evt-follow-1", "UserFollowed", postgres.FollowerDelta{CreatorID: creator, Day: today, Follows: 1}) {
		t.Fatal("first delivery not applied")
	}
	if applyFollower(t, store, "evt-follow-1", "UserFollowed", postgres.FollowerDelta{CreatorID: creator, Day: today, Follows: 1}) {
		t.Fatal("redelivery of the same event id was applied again")
	}
	applyFollower(t, store, "evt-follow-2", "UserFollowed", postgres.FollowerDelta{CreatorID: creator, Day: today, Follows: 1})
	applyFollower(t, store, "evt-follow-3", "UserFollowed", postgres.FollowerDelta{CreatorID: creator, Day: today, Follows: 1})
	applyFollower(t, store, "evt-unfollow-1", "UserUnfollowed", postgres.FollowerDelta{CreatorID: creator, Day: today, Unfollows: 1})
	applyFollower(t, store, "evt-unfollow-1", "UserUnfollowed", postgres.FollowerDelta{CreatorID: creator, Day: today, Unfollows: 1})
	// A no-op event (repeat unfollow) still gets its receipt and changes nothing.
	if !applyFollower(t, store, "evt-unfollow-noop", "UserUnfollowed") {
		t.Fatal("a no-op event should still record its receipt")
	}

	var follows, unfollows int64
	if err := pool.QueryRow(ctx, `SELECT follows, unfollows FROM analytics.creator_follower_daily
		WHERE creator_id = $1 AND day_bucket = '2026-10-15'`, creator).Scan(&follows, &unfollows); err != nil {
		t.Fatal(err)
	}
	if follows != 3 || unfollows != 1 {
		t.Fatalf("row follows=%d unfollows=%d, want 3 and 1", follows, unfollows)
	}
	delta, tracked, err := agg.CreatorFollowersDelta(ctx, creator, today.AddDate(0, 0, -7), today)
	if err != nil || !tracked || delta != 2 {
		t.Fatalf("delta=%d tracked=%v err=%v, want 2", delta, tracked, err)
	}

	// A block that severed follows both ways is one event, two creators.
	other := uuid.New()
	applyFollower(t, store, "evt-block-1", "UserBlocked",
		postgres.FollowerDelta{CreatorID: creator, Day: today, Unfollows: 1},
		postgres.FollowerDelta{CreatorID: other, Day: today, Unfollows: 1})
	applyFollower(t, store, "evt-block-1", "UserBlocked",
		postgres.FollowerDelta{CreatorID: creator, Day: today, Unfollows: 1},
		postgres.FollowerDelta{CreatorID: other, Day: today, Unfollows: 1})
	if delta, _, _ := agg.CreatorFollowersDelta(ctx, creator, today.AddDate(0, 0, -7), today); delta != 1 {
		t.Fatalf("after block delta=%d, want 1", delta)
	}
	if delta, _, _ := agg.CreatorFollowersDelta(ctx, other, today.AddDate(0, 0, -7), today); delta != -1 {
		t.Fatalf("other creator delta=%d, want -1 (net loss is not floored)", delta)
	}
}

func TestFollowerDeltaWindowSumsUTCDays(t *testing.T) {
	pool := followerPool(t)
	ctx := context.Background()
	store := postgres.New(pool)
	agg := postgres.NewAggregateStore(pool)
	creator := uuid.New()
	now := time.Date(2026, 10, 15, 12, 0, 0, 0, time.UTC)

	// follows on: today (+4), 7 days ago (+2, the window's first day),
	// 8 days ago (+8, outside 7d), 27 days ago (+16), 89 days ago (+32),
	// 120 days ago (+64, outside everything); one unfollow 3 days ago.
	for i, f := range []struct {
		daysAgo int
		follows int64
	}{{0, 4}, {7, 2}, {8, 8}, {27, 16}, {89, 32}, {120, 64}} {
		applyFollower(t, store, "win-"+string(rune('a'+i)), "UserFollowed",
			postgres.FollowerDelta{CreatorID: creator, Day: now.AddDate(0, 0, -f.daysAgo), Follows: f.follows})
	}
	applyFollower(t, store, "win-unfollow", "UserUnfollowed",
		postgres.FollowerDelta{CreatorID: creator, Day: now.AddDate(0, 0, -3), Unfollows: 1})
	// Another creator's rows never leak in.
	applyFollower(t, store, "win-other", "UserFollowed",
		postgres.FollowerDelta{CreatorID: uuid.New(), Day: now, Follows: 1000})

	for _, tc := range []struct {
		days int
		want int64
	}{{7, 4 + 2 - 1}, {28, 4 + 2 + 8 + 16 - 1}, {90, 4 + 2 + 8 + 16 + 32 - 1}} {
		got, tracked, err := agg.CreatorFollowersDelta(ctx, creator, now.AddDate(0, 0, -tc.days), now)
		if err != nil || !tracked || got != tc.want {
			t.Fatalf("%dd: delta=%d tracked=%v err=%v, want %d", tc.days, got, tracked, err, tc.want)
		}
	}
}

func TestFollowerDeltaNullVersusZero(t *testing.T) {
	pool := followerPool(t)
	ctx := context.Background()
	agg := postgres.NewAggregateStore(pool)
	now := time.Date(2026, 10, 15, 12, 0, 0, 0, time.UTC)
	quiet := uuid.New()

	delta, tracked, err := agg.CreatorFollowersDelta(ctx, quiet, now.AddDate(0, 0, -28), now)
	if err != nil || !tracked || delta != 0 {
		t.Fatalf("tracked creator with no rows: delta=%d tracked=%v err=%v, want a real 0", delta, tracked, err)
	}

	var started time.Time
	if err := pool.QueryRow(ctx, `SELECT started_at FROM analytics.follower_tracking WHERE id = 1`).Scan(&started); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `DELETE FROM analytics.follower_tracking`); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(),
			`INSERT INTO analytics.follower_tracking (id, started_at) VALUES (1, $1) ON CONFLICT (id) DO NOTHING`, started)
	})
	if _, tracked, err := agg.CreatorFollowersDelta(ctx, quiet, now.AddDate(0, 0, -28), now); err != nil || tracked {
		t.Fatalf("without a tracking row: tracked=%v err=%v, want untracked (null on the wire)", tracked, err)
	}
}

func TestFollowerDeltaErasedWithTheAccount(t *testing.T) {
	pool := followerPool(t)
	ctx := context.Background()
	store := postgres.New(pool)
	agg := postgres.NewAggregateStore(pool)
	gone, kept := uuid.New(), uuid.New()
	now := time.Date(2026, 10, 15, 12, 0, 0, 0, time.UTC)

	applyFollower(t, store, "erase-1", "UserFollowed", postgres.FollowerDelta{CreatorID: gone, Day: now, Follows: 1})
	applyFollower(t, store, "erase-2", "UserFollowed", postgres.FollowerDelta{CreatorID: kept, Day: now, Follows: 1})

	for i := 0; i < 2; i++ { // idempotent
		if err := store.DeleteEventsByUser(ctx, gone.String()); err != nil {
			t.Fatal(err)
		}
	}
	var rows int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM analytics.creator_follower_daily WHERE creator_id = $1`, gone).Scan(&rows); err != nil {
		t.Fatal(err)
	}
	if rows != 0 {
		t.Fatalf("erased creator still has %d follower rows", rows)
	}
	if delta, _, _ := agg.CreatorFollowersDelta(ctx, kept, now.AddDate(0, 0, -7), now); delta != 1 {
		t.Fatalf("another creator's delta=%d after the erase, want 1", delta)
	}
	// The receipt survives the erase, so a late redelivery is still a no-op.
	if applyFollower(t, store, "erase-1", "UserFollowed", postgres.FollowerDelta{CreatorID: gone, Day: now, Follows: 1}) {
		t.Fatal("redelivery after the erase re-created the creator's rows")
	}
}

func TestFollowerReceiptsPrune(t *testing.T) {
	pool := followerPool(t)
	ctx := context.Background()
	store := postgres.New(pool)
	applyFollower(t, store, "prune-old", "UserFollowed")
	applyFollower(t, store, "prune-new", "UserFollowed")
	if _, err := pool.Exec(ctx, `UPDATE analytics.follower_event_receipts SET accepted_at = NOW() - INTERVAL '40 days' WHERE event_id = 'prune-old'`); err != nil {
		t.Fatal(err)
	}
	n, err := store.PruneFollowerReceipts(ctx, time.Now().Add(-30*24*time.Hour))
	if err != nil || n != 1 {
		t.Fatalf("pruned=%d err=%v, want 1", n, err)
	}
	var left []string
	rows, err := pool.Query(ctx, `SELECT event_id FROM analytics.follower_event_receipts ORDER BY event_id`)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			t.Fatal(err)
		}
		left = append(left, id)
	}
	rows.Close()
	if len(left) != 1 || left[0] != "prune-new" {
		t.Fatalf("receipts left = %v, want [prune-new]", left)
	}
}
