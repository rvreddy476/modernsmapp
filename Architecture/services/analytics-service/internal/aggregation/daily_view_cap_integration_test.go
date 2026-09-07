//go:build integration

package aggregation

import (
	"context"
	"encoding/json"
	"math"
	"testing"
	"time"

	"github.com/atpost/analytics-service/internal/testsupport"
	"github.com/google/uuid"
)

// The one that matters: a viewer who loops a video all day is a real
// viewer, and their loops are real views — but only the first few are
// paid views. Uncapped, a single phone mints unlimited creator-fund
// revenue on its owner's own content.
func TestLiveDailyRollupCapsDisplayViewsPerViewerPerDay(t *testing.T) {
	ctx := context.Background()
	// Own database, own truncates: see internal/testsupport.
	pool := testsupport.Pool(t, "aggregation")
	if _, err := pool.Exec(ctx, `TRUNCATE analytics.content_hourly_agg, analytics.content_daily_summary,
		analytics.ingest_receipts, analytics.events_raw, analytics.content_ownership CASCADE`); err != nil {
		t.Fatal(err)
	}

	content, creator := uuid.New(), uuid.New()
	viewerA, viewerB, viewerC := uuid.New(), uuid.New(), uuid.New()

	// A day that is safely closed, so nothing else can be writing to it.
	day := time.Now().UTC().AddDate(0, 0, -2).Truncate(24 * time.Hour)

	// percentFor gives each of a viewer's sessions a different watched
	// fraction, ascending, so "highest N" and "first N" would disagree
	// if the rollup ever took the wrong ones.
	percentFor := func(i int) float64 { return float64(10 + i*5) }

	insertPlayEnd := func(viewer uuid.UUID, at time.Time, percent float64) {
		t.Helper()
		payload, err := json.Marshal(map[string]any{
			"content_id":          content.String(),
			"creator_id":          creator.String(),
			"content_type":        "reel",
			"surface":             "feed",
			"watched_ms_total":    int64(percent * 100),
			"content_duration_ms": 10_000,
			"percent_viewed":      percent,
			"is_display_view":     true,
			"end_reason":          "ended",
			"loop_count":          1,
		})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := pool.Exec(ctx, `
			INSERT INTO analytics.events_raw
				(id, user_id, session_id, type, payload, ts, received_at)
			VALUES ($1,$2,$3,'play_end',$4,$5,NOW())`,
			uuid.New(), viewer, uuid.New(), payload, at); err != nil {
			t.Fatal(err)
		}
	}

	// Viewer A loops it 12 times, viewer B watches twice, viewer C five
	// times. Deliberately spread across several hours of the day: the cap
	// is per day, and an hourly aggregate cannot express that.
	type viewerPlan struct {
		id uuid.UUID
		n  int
	}
	plans := []viewerPlan{{viewerA, 12}, {viewerB, 2}, {viewerC, 5}}
	uncappedScore := 0.0
	for _, plan := range plans {
		for i := 0; i < plan.n; i++ {
			at := day.Add(time.Duration(i%20) * time.Hour).Add(time.Duration(i) * time.Minute)
			insertPlayEnd(plan.id, at, percentFor(i))
			uncappedScore += percentFor(i) / 100
		}
	}

	// The hourly pass has to have run first: it is what supplies every
	// other column, and the rollup only writes content that has hourly
	// rows.
	hourly := NewHourlyAggregator(pool, nil)
	for hr := day; hr.Before(day.AddDate(0, 0, 1)); hr = hr.Add(time.Hour) {
		hourly.AggregateHour(ctx, hr)
	}

	// Hourly is the real-time surface, not the money surface: it still
	// carries every loop, uncapped. This assertion is the guard that the
	// cap did not leak backwards into it.
	var hourlyViews int64
	var hourlyScore float64
	if err := pool.QueryRow(ctx, `
		SELECT COALESCE(SUM(views_display),0), COALESCE(SUM(view_score_total),0)
		FROM analytics.content_hourly_agg
		WHERE content_id = $1 AND hour_bucket >= $2 AND hour_bucket < $3`,
		content, day, day.AddDate(0, 0, 1)).Scan(&hourlyViews, &hourlyScore); err != nil {
		t.Fatal(err)
	}
	if hourlyViews != 19 {
		t.Fatalf("hourly views_display = %d, want 19 (the hourly table stays uncapped)", hourlyViews)
	}
	if math.Abs(hourlyScore-uncappedScore) > 1e-6 {
		t.Fatalf("hourly view_score_total = %.6f, want %.6f (uncapped)", hourlyScore, uncappedScore)
	}

	// Capped at 5: 5 (of A's 12) + 2 (B) + 5 (C) = 12 paid views, not 19.
	if err := NewDailyRollup(pool, nil).WithViewCap(5).RollupDay(ctx, day); err != nil {
		t.Fatal(err)
	}

	var dailyViews int64
	var dailyScore float64
	if err := pool.QueryRow(ctx, `
		SELECT views_display, view_score_total
		FROM analytics.content_daily_summary
		WHERE content_id = $1 AND day_bucket = $2`,
		content, day).Scan(&dailyViews, &dailyScore); err != nil {
		t.Fatal(err)
	}
	if dailyViews != 12 {
		t.Fatalf("capped views_display = %d, want 12 (5+2+5), not 19", dailyViews)
	}

	// The same cap on the quality-weighted count, taking each viewer's
	// five best watches. A: sessions 8..12 (percent 45,50,55,60,65);
	// B: both (10,15); C: all five (10,15,20,25,30).
	wantScore := 0.0
	for _, plan := range plans {
		scores := make([]float64, 0, plan.n)
		for i := 0; i < plan.n; i++ {
			scores = append(scores, percentFor(i)/100)
		}
		// percentFor ascends, so the best N are the last N.
		keep := 5
		if len(scores) < keep {
			keep = len(scores)
		}
		for _, s := range scores[len(scores)-keep:] {
			wantScore += s
		}
	}
	if math.Abs(dailyScore-wantScore) > 1e-6 {
		t.Fatalf("capped view_score_total = %.6f, want %.6f (each viewer's best 5)", dailyScore, wantScore)
	}
	if dailyScore >= uncappedScore {
		t.Fatalf("capped view_score_total %.6f is not below the uncapped %.6f", dailyScore, uncappedScore)
	}

	// The escape hatch: cap <= 0 disables it and must reproduce the
	// uncapped numbers exactly — the same 19 the hourly rows sum to.
	if err := NewDailyRollup(pool, nil).WithViewCap(0).RollupDay(ctx, day); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `
		SELECT views_display, view_score_total
		FROM analytics.content_daily_summary
		WHERE content_id = $1 AND day_bucket = $2`,
		content, day).Scan(&dailyViews, &dailyScore); err != nil {
		t.Fatal(err)
	}
	if dailyViews != 19 {
		t.Fatalf("uncapped views_display = %d, want 19", dailyViews)
	}
	if dailyViews != hourlyViews {
		t.Fatalf("uncapped daily views_display %d disagrees with the hourly sum %d", dailyViews, hourlyViews)
	}
	if math.Abs(dailyScore-uncappedScore) > 1e-6 {
		t.Fatalf("uncapped view_score_total = %.6f, want %.6f", dailyScore, uncappedScore)
	}
}

// Rolling the same day up twice must converge, not accumulate — the
// operator endpoint and the midnight timer both re-run days, and the
// creator-fund settlement reads whatever is there afterwards.
func TestLiveDailyRollupIsIdempotent(t *testing.T) {
	ctx := context.Background()
	// Own database, own truncates: see internal/testsupport.
	pool := testsupport.Pool(t, "aggregation")
	if _, err := pool.Exec(ctx, `TRUNCATE analytics.content_hourly_agg, analytics.content_daily_summary,
		analytics.ingest_receipts, analytics.events_raw, analytics.content_ownership CASCADE`); err != nil {
		t.Fatal(err)
	}

	content, creator, viewer := uuid.New(), uuid.New(), uuid.New()
	day := time.Now().UTC().AddDate(0, 0, -3).Truncate(24 * time.Hour)

	insert := func(eventType string, at time.Time, extra map[string]any) {
		t.Helper()
		payload := map[string]any{
			"content_id": content.String(), "creator_id": creator.String(),
			"content_type": "reel", "surface": "feed",
		}
		for k, v := range extra {
			payload[k] = v
		}
		raw, err := json.Marshal(payload)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := pool.Exec(ctx, `
			INSERT INTO analytics.events_raw
				(id, user_id, session_id, type, payload, ts, received_at)
			VALUES ($1,$2,$3,$4,$5,$6,NOW())`,
			uuid.New(), viewer, uuid.New(), eventType, raw, at); err != nil {
			t.Fatal(err)
		}
	}

	for i := 0; i < 6; i++ {
		insert("impression", day.Add(time.Duration(i)*time.Hour), map[string]any{"visible_ms": 800})
	}
	for i := 0; i < 3; i++ {
		insert("play_end", day.Add(time.Duration(i)*time.Hour), map[string]any{
			"watched_ms_total": 8_000, "content_duration_ms": 10_000,
			"percent_viewed": 80.0, "is_display_view": true,
			"end_reason": "ended", "loop_count": i,
		})
	}
	insert("like", day.Add(time.Hour), nil)

	hourly := NewHourlyAggregator(pool, nil)
	for hr := day; hr.Before(day.AddDate(0, 0, 1)); hr = hr.Add(time.Hour) {
		hourly.AggregateHour(ctx, hr)
	}

	rollup := NewDailyRollup(pool, nil).WithViewCap(5)
	read := func() (rows, impressions, views int64, score float64) {
		t.Helper()
		if err := pool.QueryRow(ctx, `
			SELECT count(*), COALESCE(SUM(impressions),0), COALESCE(SUM(views_display),0),
			       COALESCE(SUM(view_score_total),0)
			FROM analytics.content_daily_summary
			WHERE content_id = $1 AND day_bucket = $2`,
			content, day).Scan(&rows, &impressions, &views, &score); err != nil {
			t.Fatal(err)
		}
		return
	}

	if err := rollup.RollupDay(ctx, day); err != nil {
		t.Fatal(err)
	}
	rows1, impressions1, views1, score1 := read()
	if rows1 != 1 || impressions1 != 6 || views1 != 3 {
		t.Fatalf("first run: rows=%d impressions=%d views=%d, want 1/6/3", rows1, impressions1, views1)
	}

	for range 2 {
		if err := rollup.RollupDay(ctx, day); err != nil {
			t.Fatal(err)
		}
	}
	rows2, impressions2, views2, score2 := read()
	if rows2 != rows1 || impressions2 != impressions1 || views2 != views1 ||
		math.Abs(score2-score1) > 1e-9 {
		t.Fatalf("re-runs accumulated: rows=%d impressions=%d views=%d score=%.6f, want %d/%d/%d/%.6f",
			rows2, impressions2, views2, score2, rows1, impressions1, views1, score1)
	}
}
