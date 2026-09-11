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

// The one that matters: one viewer earns one paid view of one video per
// day, however many times they loop it. Sessions are the unit, self-views
// are excluded, and the cap keeps each viewer's best session — while
// watch time and unique viewers still count every non-self session, so
// replay analytics survive.
//
// Three finalised sessions for viewer A across three hours with
// different scores, one from viewer B, one self-view by the creator.
// Expected: views_display = 2 (A's best plus B's), view_score_total =
// A's best score plus B's, watch time = all four non-self sessions.
func TestLiveDailyRollupCapsOneDisplayViewPerViewerPerDay(t *testing.T) {
	ctx := context.Background()
	pool := testsupport.Pool(t, "aggregation")
	ensurePlaybackSessions(t, pool)
	truncateAggregation(t, pool)

	content, creator := uuid.New(), uuid.New()
	viewerA, viewerB := uuid.New(), uuid.New()
	day := dayAgo(2) // inside the 48-hour window, safely closed

	session := func(actor uuid.UUID, hour int, percent float64, self bool) sessionFixture {
		return sessionFixture{
			Actor: actor, Content: content, Creator: creator, ContentType: "flick",
			FirstSeen:  day.Add(time.Duration(hour) * time.Hour).Add(5 * time.Minute),
			DurationMS: 10_000, WatchedMS: int64(percent * 100), CoveredMS: int64(percent * 100),
			PercentViewed: percent, PercentCovered: percent,
			IsSelfView: self, IsDisplayView: true, ViewScore: percent / 100,
		}
	}
	// Viewer A: 40%, 90% and 60% in hours 1, 5 and 9. The best is the
	// middle one, so "first" and "best" disagree.
	insertSession(t, pool, session(viewerA, 1, 40, false))
	insertSession(t, pool, session(viewerA, 5, 90, false))
	insertSession(t, pool, session(viewerA, 9, 60, false))
	// Viewer B once.
	insertSession(t, pool, session(viewerB, 3, 70, false))
	// The creator watching their own upload.
	insertSession(t, pool, session(creator, 7, 100, true))

	sessions := fixedSource(ViewSourceSessions)
	hourly := NewHourlyAggregator(pool, nil).WithViewSource(sessions)
	for hr := day; hr.Before(day.AddDate(0, 0, 1)); hr = hr.Add(time.Hour) {
		hourly.AggregateHour(ctx, hr)
	}

	// Hourly is the real-time surface: uncapped, but never self-views.
	var hourlyViews, hourlyWatch, hourlyUnique int64
	var hourlyScore float64
	if err := pool.QueryRow(ctx, `
		SELECT COALESCE(SUM(views_display),0), COALESCE(SUM(view_score_total),0),
		       COALESCE(SUM(watch_time_total_ms),0), COALESCE(SUM(unique_viewers),0)
		FROM analytics.content_hourly_agg
		WHERE content_id = $1 AND hour_bucket >= $2 AND hour_bucket < $3`,
		content, day, day.AddDate(0, 0, 1)).Scan(&hourlyViews, &hourlyScore, &hourlyWatch, &hourlyUnique); err != nil {
		t.Fatal(err)
	}
	if hourlyViews != 4 {
		t.Fatalf("hourly views_display = %d, want 4 (uncapped, self-view excluded)", hourlyViews)
	}
	if math.Abs(hourlyScore-(0.4+0.9+0.6+0.7)) > 1e-6 {
		t.Fatalf("hourly view_score_total = %.6f, want 2.6", hourlyScore)
	}
	if hourlyWatch != 4_000+9_000+6_000+7_000 {
		t.Fatalf("hourly watch_time_total_ms = %d, want 26000", hourlyWatch)
	}

	rollup := NewDailyRollup(pool, nil).WithViewSource(sessions)
	if rollup.ViewCap() != 1 {
		t.Fatalf("default view cap = %d, want 1", rollup.ViewCap())
	}
	if err := rollup.RollupDay(ctx, day); err != nil {
		t.Fatal(err)
	}

	rows, views, score, watch := summaryViews(t, pool, content, day)
	if rows != 1 {
		t.Fatalf("summary rows = %d, want 1", rows)
	}
	if views != 2 {
		t.Fatalf("views_display = %d, want 2 (one per viewer; the self-view never counts)", views)
	}
	if math.Abs(score-(0.9+0.7)) > 1e-6 {
		t.Fatalf("view_score_total = %.6f, want 1.6 (A's best 0.9 plus B's 0.7)", score)
	}
	if watch != 26_000 {
		t.Fatalf("watch_time_total_ms = %d, want 26000 (all four non-self sessions)", watch)
	}
	var unique int64
	if err := pool.QueryRow(ctx, `
		SELECT unique_viewers FROM analytics.content_daily_summary
		WHERE content_id = $1 AND day_bucket = $2`, content, day).Scan(&unique); err != nil {
		t.Fatal(err)
	}
	if unique != 4 {
		// unique_viewers is the sum of per-hour distinct viewers, as it
		// always was: four hours each had one non-self viewer.
		t.Fatalf("unique_viewers = %d, want 4", unique)
	}

	// An open (unfinalised) session is not a view yet, and a session
	// finalised later lands in the bucket of its first_seen, so it is
	// picked up by a rebuild of that hour, not of the hour it closed in.
	insertSession(t, pool, sessionFixture{
		Actor: uuid.New(), Content: content, Creator: creator, ContentType: "flick",
		FirstSeen: day.Add(2 * time.Hour), DurationMS: 10_000, WatchedMS: 8_000,
		PercentViewed: 80, PercentCovered: 80, IsDisplayView: true, ViewScore: 0.8, Open: true,
	})
	hourly.AggregateHour(ctx, day.Add(2*time.Hour))
	if err := rollup.RollupDay(ctx, day); err != nil {
		t.Fatal(err)
	}
	if _, views, _, _ = summaryViews(t, pool, content, day); views != 2 {
		t.Fatalf("an open session counted as a view: views_display = %d, want 2", views)
	}

	// The escape hatch still exists: cap <= 0 reproduces the uncapped
	// per-session numbers, self-views still excluded.
	if err := NewDailyRollup(pool, nil).WithViewSource(sessions).WithViewCap(0).RollupDay(ctx, day); err != nil {
		t.Fatal(err)
	}
	if _, views, score, _ = summaryViews(t, pool, content, day); views != 4 || math.Abs(score-2.6) > 1e-6 {
		t.Fatalf("uncapped views_display = %d score = %.6f, want 4 / 2.6", views, score)
	}
}

// The pre-cutover path — play_end rows in events_raw — stays intact for
// buckets the resolver still routes there, with the same three rules:
// one paid view per viewer per day, best session kept, self-views out.
func TestLiveLegacyPlayEndPathCapsAtOneAndExcludesSelfViews(t *testing.T) {
	ctx := context.Background()
	pool := testsupport.Pool(t, "aggregation")
	ensurePlaybackSessions(t, pool)
	truncateAggregation(t, pool)

	content, creator := uuid.New(), uuid.New()
	viewerA, viewerB := uuid.New(), uuid.New()
	day := dayAgo(1)

	insertPlayEnd := func(viewer uuid.UUID, at time.Time, percent float64, self bool) {
		t.Helper()
		payload, err := json.Marshal(map[string]any{
			"content_id": content.String(), "creator_id": creator.String(),
			"content_type": "flick", "surface": "feed",
			"watched_ms_total": int64(percent * 100), "content_duration_ms": 10_000,
			"percent_viewed": percent, "is_display_view": true, "is_self_view": self,
			"end_reason": "ended", "loop_count": 1,
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
	insertPlayEnd(viewerA, day.Add(1*time.Hour), 40, false)
	insertPlayEnd(viewerA, day.Add(5*time.Hour), 90, false)
	insertPlayEnd(viewerA, day.Add(9*time.Hour), 60, false)
	insertPlayEnd(viewerB, day.Add(3*time.Hour), 70, false)
	insertPlayEnd(creator, day.Add(7*time.Hour), 100, true)

	// No resolver at all means play_end everywhere: the pre-cutover
	// default the service ships with.
	hourly := NewHourlyAggregator(pool, nil)
	for hr := day; hr.Before(day.AddDate(0, 0, 1)); hr = hr.Add(time.Hour) {
		hourly.AggregateHour(ctx, hr)
	}
	var hourlyViews, hourlyWatch int64
	if err := pool.QueryRow(ctx, `
		SELECT COALESCE(SUM(views_display),0), COALESCE(SUM(watch_time_total_ms),0)
		FROM analytics.content_hourly_agg
		WHERE content_id = $1 AND hour_bucket >= $2 AND hour_bucket < $3`,
		content, day, day.AddDate(0, 0, 1)).Scan(&hourlyViews, &hourlyWatch); err != nil {
		t.Fatal(err)
	}
	if hourlyViews != 4 || hourlyWatch != 26_000 {
		t.Fatalf("hourly views=%d watch=%d, want 4 / 26000 (self-view excluded, uncapped)", hourlyViews, hourlyWatch)
	}

	if err := NewDailyRollup(pool, nil).RollupDay(ctx, day); err != nil {
		t.Fatal(err)
	}
	rows, views, score, watch := summaryViews(t, pool, content, day)
	if rows != 1 || views != 2 || math.Abs(score-1.6) > 1e-6 || watch != 26_000 {
		t.Fatalf("rows=%d views=%d score=%.6f watch=%d, want 1 / 2 / 1.6 / 26000", rows, views, score, watch)
	}
}

// Rolling the same day up twice must converge, not accumulate — the
// timer and the operator route both re-run days inside the window, and
// the creator-fund settlement reads whatever is there afterwards.
func TestLiveDailyRollupIsIdempotent(t *testing.T) {
	ctx := context.Background()
	pool := testsupport.Pool(t, "aggregation")
	ensurePlaybackSessions(t, pool)
	truncateAggregation(t, pool)

	content, creator, viewer := uuid.New(), uuid.New(), uuid.New()
	day := dayAgo(2)

	insert := func(eventType string, at time.Time, extra map[string]any) {
		t.Helper()
		payload := map[string]any{
			"content_id": content.String(), "creator_id": creator.String(),
			"content_type": "flick", "surface": "feed",
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

	rollup := NewDailyRollup(pool, nil)
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
	if rows1 != 1 || impressions1 != 6 || views1 != 1 {
		t.Fatalf("first run: rows=%d impressions=%d views=%d, want 1/6/1 (one viewer, cap 1)", rows1, impressions1, views1)
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
