package postgres

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// MTube creator analytics (2026-09-27): the reads behind
// GET /v1/analytics/creator/me and GET /v1/analytics/content/:id.
//
// Every "views" figure here is the display view the creator is paid on,
// summed the way ContentViewsBatch sums it: content_daily_summary for every
// settled day in the period, plus content_hourly_agg for today's open
// hours (today is never in the daily table). An hourly row inside a settled
// day is the rollup's input, not a second count, and is not summed.

// periodBounds splits a period into the settled-days range and the open
// (today) range. openFrom is today's UTC midnight, or `since` when the
// period starts later than that.
func periodBounds(since, now time.Time) (dailyFrom time.Time, openFrom time.Time) {
	now = now.UTC()
	today := now.Truncate(24 * time.Hour)
	since = since.UTC()
	openFrom = today
	if since.After(openFrom) {
		openFrom = since
	}
	return since.Truncate(24 * time.Hour), openFrom
}

// CreatorPeriodTotals is the headline set for one creator over a period.
type CreatorPeriodTotals struct {
	Views       int64
	WatchTimeMS int64
	// UniqueViewers is the sum of the per-day (and today's per-hour) unique
	// viewer counts: a viewer who came back on two days counts twice, which
	// is the best the aggregates can say without a per-viewer table.
	UniqueViewers int64
}

// GetCreatorPeriodTotals sums the creator's content over [since, now].
func (s *AggregateStore) GetCreatorPeriodTotals(ctx context.Context, creatorID uuid.UUID, since, now time.Time) (*CreatorPeriodTotals, error) {
	dailyFrom, openFrom := periodBounds(since, now)
	var t CreatorPeriodTotals
	err := s.db.QueryRow(ctx, `
		WITH daily AS (
			SELECT COALESCE(SUM(views_display), 0) AS views,
			       COALESCE(SUM(watch_time_total_ms), 0) AS watch_ms,
			       COALESCE(SUM(unique_viewers), 0) AS uniques
			  FROM analytics.content_daily_summary
			 WHERE creator_id = $1 AND day_bucket >= $2::date AND day_bucket < $3::date
		),
		open_hours AS (
			SELECT COALESCE(SUM(views_display), 0) AS views,
			       COALESCE(SUM(watch_time_total_ms), 0) AS watch_ms,
			       COALESCE(SUM(unique_viewers), 0) AS uniques
			  FROM analytics.content_hourly_agg
			 WHERE creator_id = $1 AND hour_bucket >= $3
		)
		SELECT d.views + h.views, d.watch_ms + h.watch_ms, d.uniques + h.uniques
		  FROM daily d, open_hours h`,
		creatorID, dailyFrom, openFrom,
	).Scan(&t.Views, &t.WatchTimeMS, &t.UniqueViewers)
	if err != nil {
		return nil, fmt.Errorf("creator period totals: %w", err)
	}
	return &t, nil
}

// ContentPeriodStat is one content item's period figures.
type ContentPeriodStat struct {
	ContentID   uuid.UUID `json:"content_id"`
	Views       int64     `json:"views"`
	WatchTimeMS int64     `json:"watch_time_ms"`
}

// GetCreatorTopContent lists the creator's content by period views.
func (s *AggregateStore) GetCreatorTopContent(ctx context.Context, creatorID uuid.UUID, since, now time.Time, limit int) ([]ContentPeriodStat, error) {
	if limit <= 0 {
		limit = 10
	}
	dailyFrom, openFrom := periodBounds(since, now)
	rows, err := s.db.Query(ctx, `
		WITH per_content AS (
			SELECT content_id, views_display AS views, watch_time_total_ms AS watch_ms
			  FROM analytics.content_daily_summary
			 WHERE creator_id = $1 AND day_bucket >= $2::date AND day_bucket < $3::date
			UNION ALL
			SELECT content_id, views_display, watch_time_total_ms
			  FROM analytics.content_hourly_agg
			 WHERE creator_id = $1 AND hour_bucket >= $3
		)
		SELECT content_id, SUM(views), SUM(watch_ms)
		  FROM per_content
		 GROUP BY content_id
		HAVING SUM(views) > 0 OR SUM(watch_ms) > 0
		 ORDER BY SUM(views) DESC, SUM(watch_ms) DESC, content_id
		 LIMIT $4`,
		creatorID, dailyFrom, openFrom, limit)
	if err != nil {
		return nil, fmt.Errorf("creator top content: %w", err)
	}
	defer rows.Close()
	var out []ContentPeriodStat
	for rows.Next() {
		var c ContentPeriodStat
		if err := rows.Scan(&c.ContentID, &c.Views, &c.WatchTimeMS); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// GetCreatorHourlyViews returns `hours` consecutive hour buckets of display
// views ending with the hour that contains `now`, oldest first. Hours with
// no row are zero. Today's hours are the aggregator's live figure; earlier
// hours are its input to the daily rollup, which is what a 48-hour
// real-time strip wants to show — activity, not settled money.
func (s *AggregateStore) GetCreatorHourlyViews(ctx context.Context, creatorID uuid.UUID, now time.Time, hours int) ([]int64, error) {
	if hours <= 0 {
		hours = 48
	}
	end := now.UTC().Truncate(time.Hour)
	start := end.Add(-time.Duration(hours-1) * time.Hour)
	rows, err := s.db.Query(ctx, `
		SELECT hour_bucket, SUM(views_display)
		  FROM analytics.content_hourly_agg
		 WHERE creator_id = $1 AND hour_bucket >= $2 AND hour_bucket <= $3
		 GROUP BY hour_bucket`,
		creatorID, start, end)
	if err != nil {
		return nil, fmt.Errorf("creator hourly views: %w", err)
	}
	defer rows.Close()
	series := make([]int64, hours)
	for rows.Next() {
		var bucket time.Time
		var views int64
		if err := rows.Scan(&bucket, &views); err != nil {
			return nil, err
		}
		i := int(bucket.UTC().Truncate(time.Hour).Sub(start) / time.Hour)
		if i >= 0 && i < hours {
			series[i] += views
		}
	}
	return series, rows.Err()
}

// DayViews is one calendar day of display views.
type DayViews struct {
	Day   string `json:"day"` // YYYY-MM-DD, UTC
	Views int64  `json:"views"`
}

// GetContentViewsByDay returns one entry per UTC day from `since` through
// today (dense, zeros filled), settled days from the daily summary and
// today from the open hourly buckets.
func (s *AggregateStore) GetContentViewsByDay(ctx context.Context, contentID uuid.UUID, since, now time.Time) ([]DayViews, error) {
	dailyFrom, openFrom := periodBounds(since, now)
	today := now.UTC().Truncate(24 * time.Hour)
	rows, err := s.db.Query(ctx, `
		SELECT day_bucket::date, views_display
		  FROM analytics.content_daily_summary
		 WHERE content_id = $1 AND day_bucket >= $2::date AND day_bucket < $3::date
		UNION ALL
		SELECT $4::date, COALESCE(SUM(views_display), 0)
		  FROM analytics.content_hourly_agg
		 WHERE content_id = $1 AND hour_bucket >= $3`,
		contentID, dailyFrom, openFrom, today)
	if err != nil {
		return nil, fmt.Errorf("content views by day: %w", err)
	}
	defer rows.Close()
	byDay := map[string]int64{}
	for rows.Next() {
		var day time.Time
		var views int64
		if err := rows.Scan(&day, &views); err != nil {
			return nil, err
		}
		byDay[day.UTC().Format("2006-01-02")] += views
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	var out []DayViews
	for d := dailyFrom; !d.After(today); d = d.AddDate(0, 0, 1) {
		key := d.Format("2006-01-02")
		out = append(out, DayViews{Day: key, Views: byDay[key]})
	}
	return out, nil
}

// SessionCoverage is what the retention curve and the averages read from
// one finalized session.
type SessionCoverage struct {
	WatchedMS      int64
	DurationMS     int64
	PercentCovered float64
	Coverage       []byte
}

// GetContentSessionCoverage loads the finalized, non-self sessions of one
// content item that started in the period. Bounded by the period, not by
// row count: a viral video over 365 days is a big read, which is why the
// handler caches nothing and the client asks for a period it can wait for.
func (s *AggregateStore) GetContentSessionCoverage(ctx context.Context, contentID uuid.UUID, since time.Time) ([]SessionCoverage, error) {
	rows, err := s.db.Query(ctx, `
		SELECT watched_ms, content_duration_ms, percent_covered, coverage
		  FROM analytics.playback_sessions
		 WHERE content_id = $1 AND finalized_at IS NOT NULL AND NOT is_self_view
		   AND first_seen >= $2`,
		contentID, since)
	if err != nil {
		return nil, fmt.Errorf("content session coverage: %w", err)
	}
	defer rows.Close()
	var out []SessionCoverage
	for rows.Next() {
		var sc SessionCoverage
		if err := rows.Scan(&sc.WatchedMS, &sc.DurationMS, &sc.PercentCovered, &sc.Coverage); err != nil {
			return nil, err
		}
		out = append(out, sc)
	}
	return out, rows.Err()
}

// SurfaceViews is the display views one surface sent.
type SurfaceViews struct {
	Surface string `json:"surface"`
	Views   int64  `json:"views"`
}

// GetContentTrafficBySurface counts the period's display-view sessions by
// the surface their play_start event named (the sessions table carries no
// surface; the raw event does, under the session index).
func (s *AggregateStore) GetContentTrafficBySurface(ctx context.Context, contentID uuid.UUID, since time.Time) ([]SurfaceViews, error) {
	rows, err := s.db.Query(ctx, `
		SELECT COALESCE(NULLIF(e.surface, ''), 'other') AS surface, COUNT(*)
		  FROM analytics.playback_sessions s
		  LEFT JOIN LATERAL (
			SELECT payload->>'surface' AS surface
			  FROM analytics.events_raw e
			 WHERE e.type = 'play_start'
			   AND e.payload->>'session_id' = s.session_id::text
			   AND e.payload->>'content_id' = s.content_id::text
			 ORDER BY e.ts ASC
			 LIMIT 1
		  ) e ON TRUE
		 WHERE s.content_id = $1 AND s.finalized_at IS NOT NULL AND s.is_display_view
		   AND NOT s.is_self_view AND s.first_seen >= $2
		 GROUP BY 1
		 ORDER BY 2 DESC, 1 ASC`,
		contentID, since)
	if err != nil {
		return nil, fmt.Errorf("content traffic by surface: %w", err)
	}
	defer rows.Close()
	var out []SurfaceViews
	for rows.Next() {
		var sv SurfaceViews
		if err := rows.Scan(&sv.Surface, &sv.Views); err != nil {
			return nil, err
		}
		out = append(out, sv)
	}
	return out, rows.Err()
}
