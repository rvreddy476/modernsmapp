//go:build integration

package postgres_test

import (
	"context"
	"testing"
	"time"

	"github.com/atpost/analytics-service/internal/store/postgres"
	"github.com/atpost/analytics-service/internal/testsupport"
	"github.com/google/uuid"
)

// The creator dashboard counts only content that still exists: a deleted
// post's aggregate rows stay (money was computed from them) but its
// engagement must not show in the Overview beside lists that no longer
// contain it (17 Oct 2026: "Comments 5" with an empty inbox).
func TestCreatorDashboardSkipsDeletedContent(t *testing.T) {
	testsupport.RequireTestDSN(t)
	pool := testsupport.Pool(t, "pgstore")
	ctx := context.Background()
	agg := postgres.NewAggregateStore(pool)

	creator, live, gone := uuid.New(), uuid.New(), uuid.New()
	hour := time.Now().UTC().Truncate(time.Hour).Add(-2 * time.Hour)
	t.Cleanup(func() {
		pool.Exec(ctx, `DELETE FROM analytics.content_hourly_agg WHERE creator_id = $1`, creator)
		pool.Exec(ctx, `DELETE FROM analytics.content_ownership WHERE creator_id = $1`, creator)
	})
	for _, c := range []struct {
		id    uuid.UUID
		state string
	}{{live, "eligible"}, {gone, "deleted"}} {
		if _, err := pool.Exec(ctx, `
			INSERT INTO analytics.content_ownership (content_id, creator_id, content_type, created_at, eligibility_state)
			VALUES ($1, $2, 'flick', now(), $3)`, c.id, creator, c.state); err != nil {
			t.Fatal(err)
		}
		if _, err := pool.Exec(ctx, `
			INSERT INTO analytics.content_hourly_agg (content_id, hour_bucket, creator_id, content_type, views_display, likes, comments, shares)
			VALUES ($1, $2, $3, 'flick', 10, 2, 3, 1)`, c.id, hour, creator); err != nil {
			t.Fatal(err)
		}
	}

	since := hour.Add(-time.Hour)
	stats, err := agg.GetCreatorAggStats(ctx, creator, since)
	if err != nil {
		t.Fatal(err)
	}
	if stats.TotalViews != 10 || stats.TotalLikes != 2 || stats.TotalComments != 3 || stats.TotalShares != 1 {
		t.Fatalf("agg stats = %+v, want only the live post (10 views, 2 likes, 3 comments, 1 share)", stats)
	}
	overview, err := agg.GetCreatorOverview(ctx, creator, since)
	if err != nil || overview.TotalViews != 10 {
		t.Fatalf("overview views = %v (err %v), want 10", overview, err)
	}
	totals, err := agg.GetCreatorPeriodTotals(ctx, creator, since, time.Now().UTC())
	if err != nil || totals.Views != 10 {
		t.Fatalf("period totals = %+v (err %v), want 10 views", totals, err)
	}
	top, err := agg.GetCreatorTopContent(ctx, creator, since, time.Now().UTC(), 10)
	if err != nil || len(top) != 1 || top[0].ContentID != live {
		t.Fatalf("top content = %+v (err %v), want only the live post", top, err)
	}
	list, err := agg.GetContentList(ctx, creator, 10, time.Now().Add(time.Hour), "views_display")
	if err != nil || len(list) != 1 {
		t.Fatalf("content list = %d rows (err %v), want 1", len(list), err)
	}
	series, err := agg.GetCreatorHourlyViews(ctx, creator, time.Now().UTC(), 48)
	if err != nil {
		t.Fatal(err)
	}
	var sum int64
	for _, v := range series {
		sum += v
	}
	if sum != 10 {
		t.Fatalf("hourly series sums to %d, want 10", sum)
	}
}
