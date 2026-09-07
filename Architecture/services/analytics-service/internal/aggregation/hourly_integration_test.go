//go:build integration

package aggregation

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	pgstore "github.com/atpost/analytics-service/internal/store/postgres"
	"github.com/atpost/analytics-service/internal/testsupport"
	"github.com/google/uuid"
)

func TestLiveHourlyAggregationIsLockedAndCountsDisplayViews(t *testing.T) {
	ctx := context.Background()
	// Own database, own truncates: see internal/testsupport.
	pool := testsupport.Pool(t, "aggregation")
	if _, err := pool.Exec(ctx, `TRUNCATE analytics.content_hourly_agg, analytics.events_raw, analytics.content_ownership CASCADE`); err != nil {
		t.Fatal(err)
	}
	content, creator, actor, session := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	hour := time.Now().UTC().Truncate(time.Hour).Add(-time.Hour)
	payload, _ := json.Marshal(map[string]any{
		"content_id": content.String(), "creator_id": creator.String(),
		"content_type": "long_video", "watched_ms_total": 40_000,
		"percent_viewed": 80.0, "is_display_view": true,
	})
	if _, err := pool.Exec(ctx, `
		INSERT INTO analytics.events_raw
			(id, user_id, session_id, type, payload, ts, received_at)
		VALUES ($1,$2,$3,'play_end',$4,$5,NOW())`,
		uuid.New(), actor, session, payload, hour.Add(10*time.Minute)); err != nil {
		t.Fatal(err)
	}

	aggregator := NewHourlyAggregator(pool, nil)
	var wait sync.WaitGroup
	wait.Add(2)
	for range 2 {
		go func() {
			defer wait.Done()
			aggregator.runAggregation(ctx)
		}()
	}
	wait.Wait()
	var rows, views, watch int64
	if err := pool.QueryRow(ctx, `
		SELECT count(*), COALESCE(sum(views_display),0), COALESCE(sum(watch_time_total_ms),0)
		FROM analytics.content_hourly_agg
		WHERE content_id=$1 AND hour_bucket=$2`, content, hour).Scan(&rows, &views, &watch); err != nil {
		t.Fatal(err)
	}
	if rows != 1 || views != 1 || watch != 40_000 {
		t.Fatalf("rows=%d views=%d watch=%d", rows, views, watch)
	}

	stats, err := pgstore.NewAggregateStore(pool).GetCreatorAggStats(ctx, creator, hour.Add(-time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if stats.TotalViews != 1 {
		t.Fatalf("creator views=%d want=1", stats.TotalViews)
	}
}
