//go:build integration

package aggregation

import (
	"context"
	"encoding/json"
	"math"
	"os"
	"testing"
	"time"

	"github.com/atpost/analytics-service/database"
	"github.com/atpost/analytics-service/internal/scoring"
	pgstore "github.com/atpost/analytics-service/internal/store/postgres"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// A realistic hour of the full event model, folded into one aggregate
// row. Asserts the averages and the derived quality score against
// hand-computed values, so the numbers the creator dashboard and the
// creator fund read are pinned, not just "non-zero".
func TestLiveHourlyAggregationFoldsEveryEventTypeIntoCorrectAverages(t *testing.T) {
	dsn := os.Getenv("ANALYTICS_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("ANALYTICS_POSTGRES_DSN is required")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	if err := pgstore.BootstrapSchema(ctx, pool, database.SetupSQL, database.Migrations); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `TRUNCATE analytics.content_hourly_agg, analytics.ingest_receipts, analytics.events_raw, analytics.content_ownership CASCADE`); err != nil {
		t.Fatal(err)
	}

	content, creator := uuid.New(), uuid.New()
	viewerA, viewerB := uuid.New(), uuid.New()
	hour := time.Now().UTC().Truncate(time.Hour).Add(-time.Hour)

	insert := func(viewer uuid.UUID, eventType string, extra map[string]any) {
		t.Helper()
		payload := map[string]any{
			"content_id":   content.String(),
			"creator_id":   creator.String(),
			"content_type": "long_video",
			"surface":      "posttube",
		}
		for k, v := range extra {
			payload[k] = v
		}
		raw, _ := json.Marshal(payload)
		if _, err := pool.Exec(ctx, `
			INSERT INTO analytics.events_raw
				(id, user_id, session_id, type, payload, ts, received_at)
			VALUES ($1,$2,$3,$4,$5,$6,NOW())`,
			uuid.New(), viewer, uuid.New(), eventType, raw, hour.Add(10*time.Minute)); err != nil {
			t.Fatal(err)
		}
	}

	// 20 impressions - the denominator every engagement rate divides by.
	for i := 0; i < 20; i++ {
		viewer := viewerA
		if i%2 == 1 {
			viewer = viewerB
		}
		insert(viewer, "impression", map[string]any{"visible_ms": 700})
	}
	// 4 play_starts.
	for i := 0; i < 4; i++ {
		insert(viewerA, "play_start", map[string]any{"content_duration_ms": 200_000})
	}
	// Milestones fill the view-duration buckets.
	for _, m := range []struct {
		bucket string
		n      int
	}{{"views_1s", 4}, {"views_3s", 3}, {"views_10s", 3}, {"views_30s", 2}, {"views_60s", 1}} {
		for i := 0; i < m.n; i++ {
			insert(viewerA, "milestone", map[string]any{"view_bucket": m.bucket, "watched_ms": 1000})
		}
	}
	// Three completed playbacks: 100%, 50%, 30% of a 200s video.
	// viewerA appears twice, so unique_viewers is 2 and repeat is 1.
	insert(viewerA, "play_end", map[string]any{
		"watched_ms_total": 200_000, "content_duration_ms": 200_000,
		"percent_viewed": 100.0, "is_display_view": true, "end_reason": "ended", "loop_count": 0,
	})
	insert(viewerA, "play_end", map[string]any{
		"watched_ms_total": 100_000, "content_duration_ms": 200_000,
		"percent_viewed": 50.0, "is_display_view": true, "end_reason": "swipe_next", "loop_count": 0,
	})
	insert(viewerB, "play_end", map[string]any{
		"watched_ms_total": 60_000, "content_duration_ms": 200_000,
		"percent_viewed": 30.0, "is_display_view": true, "end_reason": "swipe_next", "loop_count": 1,
	})
	// Engagement, positive and negative.
	insert(viewerA, "like", nil)
	insert(viewerB, "like", nil)
	insert(viewerA, "comment_create", nil)
	insert(viewerA, "share", nil)
	insert(viewerB, "save", nil)
	insert(viewerA, "follow_from_content", nil)
	insert(viewerB, "not_interested", map[string]any{"reason": "repetitive"})
	insert(viewerB, "report", map[string]any{"reason": "spam"})
	insert(viewerB, "block_creator", map[string]any{"reason": "dislike_creator"})

	NewHourlyAggregator(pool, nil).AggregateHour(ctx, hour)

	var (
		impressions, plays, viewsDisplay              int64
		v1s, v3s, v10s, v30s, v60s                    int64
		uniqueViewers, repeatViewers                  int64
		watchMS, avgWatchMS                           int64
		avgPct, completionRate, rewatchRate, skipRate float64
		earlySwipeRate, viewScore, vqsAvg, cqs        float64
		likes, comments, shares, saves                int64
		follows, notInterested, reports, blocks       int64
		gotCreator                                    uuid.UUID
	)
	if err := pool.QueryRow(ctx, `
		SELECT creator_id, impressions, plays, views_display,
		       views_1s, views_3s, views_10s, views_30s, views_60s,
		       unique_viewers, repeat_viewers,
		       watch_time_total_ms, avg_watch_time_ms, avg_percent_viewed,
		       completion_rate, rewatch_rate, skip_rate, early_swipe_rate,
		       likes, comments, shares, saves,
		       follows_from_content, not_interested, reports, blocks,
		       view_score_total, vqs_avg, content_quality_score
		FROM analytics.content_hourly_agg
		WHERE content_id = $1 AND hour_bucket = $2`, content, hour).Scan(
		&gotCreator, &impressions, &plays, &viewsDisplay,
		&v1s, &v3s, &v10s, &v30s, &v60s,
		&uniqueViewers, &repeatViewers,
		&watchMS, &avgWatchMS, &avgPct,
		&completionRate, &rewatchRate, &skipRate, &earlySwipeRate,
		&likes, &comments, &shares, &saves,
		&follows, &notInterested, &reports, &blocks,
		&viewScore, &vqsAvg, &cqs,
	); err != nil {
		t.Fatal(err)
	}

	if gotCreator != creator {
		t.Fatalf("creator=%s want=%s", gotCreator, creator)
	}
	for _, c := range []struct {
		name      string
		got, want int64
	}{
		{"impressions", impressions, 20},
		{"plays", plays, 4},
		{"views_display", viewsDisplay, 3},
		{"views_1s", v1s, 4},
		{"views_3s", v3s, 3},
		{"views_10s", v10s, 3},
		{"views_30s", v30s, 2},
		{"views_60s", v60s, 1},
		{"unique_viewers", uniqueViewers, 2},
		{"repeat_viewers", repeatViewers, 1},
		// 200s + 100s + 60s of watch time.
		{"watch_time_total_ms", watchMS, 360_000},
		// 360000 / 3 completed playbacks.
		{"avg_watch_time_ms", avgWatchMS, 120_000},
		{"likes", likes, 2},
		{"comments", comments, 1},
		{"shares", shares, 1},
		{"saves", saves, 1},
		{"follows_from_content", follows, 1},
		{"not_interested", notInterested, 1},
		{"reports", reports, 1},
		{"blocks", blocks, 1},
	} {
		if c.got != c.want {
			t.Errorf("%s = %d, want %d", c.name, c.got, c.want)
		}
	}

	closeTo := func(name string, got, want float64) {
		t.Helper()
		if math.Abs(got-want) > 1e-6 {
			t.Errorf("%s = %.6f, want %.6f", name, got, want)
		}
	}
	// (100 + 50 + 30) / 3
	closeTo("avg_percent_viewed", avgPct, 60)
	// One of three playbacks reached 95%+.
	closeTo("completion_rate", completionRate, 1.0/3.0)
	// One of three had a loop.
	closeTo("rewatch_rate", rewatchRate, 1.0/3.0)
	// 20 impressions, 3 display views => 17 skipped.
	closeTo("skip_rate", skipRate, 17.0/20.0)
	// Two swipe_next endings, but only the 30% one was early (<25%
	// is the bar, and neither qualifies).
	closeTo("early_swipe_rate", earlySwipeRate, 0)
	// Quality-weighted views: 1.0 + 0.5 + 0.3
	closeTo("view_score_total", viewScore, 1.8)
	closeTo("vqs_avg", vqsAvg, 1.8/3.0)

	// And the score the creator fund pays against, recomputed from the
	// same inputs the aggregator wrote.
	wantCQS := scoring.ComputeCQS(&scoring.AggregateMetrics{
		AvgPercentViewed: 60, Impressions: 20,
		Likes: 2, Comments: 1, Shares: 1, Saves: 1,
		FollowsFromContent: 1, Reports: 1, NotInterested: 1,
	})
	closeTo("content_quality_score", cqs, wantCQS)
	if cqs <= 0 || cqs > 1 {
		t.Fatalf("content_quality_score = %v, outside (0,1]", cqs)
	}

	// Re-running the same hour must be idempotent, not additive.
	NewHourlyAggregator(pool, nil).AggregateHour(ctx, hour)
	var rerunImpressions, rerunRows int64
	if err := pool.QueryRow(ctx, `
		SELECT count(*), COALESCE(sum(impressions), 0)
		FROM analytics.content_hourly_agg
		WHERE content_id = $1 AND hour_bucket = $2`, content, hour).Scan(&rerunRows, &rerunImpressions); err != nil {
		t.Fatal(err)
	}
	if rerunRows != 1 || rerunImpressions != 20 {
		t.Fatalf("re-run produced rows=%d impressions=%d, want 1/20", rerunRows, rerunImpressions)
	}
}

// A content item that only ever got impressions - nobody played it -
// must aggregate to a zero score without dividing by zero anywhere.
func TestLiveAggregationOfContentWithNoPlaybacksIsSafe(t *testing.T) {
	dsn := os.Getenv("ANALYTICS_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("ANALYTICS_POSTGRES_DSN is required")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	if err := pgstore.BootstrapSchema(ctx, pool, database.SetupSQL, database.Migrations); err != nil {
		t.Fatal(err)
	}

	content, creator := uuid.New(), uuid.New()
	hour := time.Now().UTC().Truncate(time.Hour).Add(-2 * time.Hour)
	payload, _ := json.Marshal(map[string]any{
		"content_id": content.String(), "creator_id": creator.String(),
		"content_type": "reel", "visible_ms": 400,
	})
	if _, err := pool.Exec(ctx, `
		INSERT INTO analytics.events_raw (id, user_id, session_id, type, payload, ts, received_at)
		VALUES ($1,$2,$3,'impression',$4,$5,NOW())`,
		uuid.New(), uuid.New(), uuid.New(), payload, hour.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}

	NewHourlyAggregator(pool, nil).AggregateHour(ctx, hour)

	var avgWatch, playsCount int64
	var avgPct, completion, cqs float64
	if err := pool.QueryRow(ctx, `
		SELECT plays, avg_watch_time_ms, avg_percent_viewed, completion_rate, content_quality_score
		FROM analytics.content_hourly_agg WHERE content_id = $1 AND hour_bucket = $2`,
		content, hour).Scan(&playsCount, &avgWatch, &avgPct, &completion, &cqs); err != nil {
		t.Fatal(err)
	}
	if playsCount != 0 || avgWatch != 0 || avgPct != 0 || completion != 0 || cqs != 0 {
		t.Fatalf("impressions-only content produced plays=%d avgWatch=%d avgPct=%v completion=%v cqs=%v",
			playsCount, avgWatch, avgPct, completion, cqs)
	}
}
