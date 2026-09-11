package postgres

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

type AggregateStore struct {
	db *pgxpool.Pool
}

func NewAggregateStore(db *pgxpool.Pool) *AggregateStore {
	return &AggregateStore{db: db}
}

func (s *AggregateStore) OwnsContent(ctx context.Context, creatorID, contentID uuid.UUID) (bool, error) {
	var owns bool
	err := s.db.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM analytics.content_ownership
			WHERE content_id = $1 AND creator_id = $2
		)`, contentID, creatorID).Scan(&owns)
	return owns, err
}

type CreatorOverview struct {
	TotalViews       int64   `json:"total_views"`
	TotalWatchTimeMS int64   `json:"total_watch_time_ms"`
	AvgCQS           float64 `json:"avg_cqs"`
	TotalLikes       int64   `json:"total_likes"`
	TotalShares      int64   `json:"total_shares"`
}

type ContentSummary struct {
	ContentID        uuid.UUID `json:"content_id"`
	ContentType      string    `json:"content_type"`
	ViewsDisplay     int64     `json:"views_display"`
	WatchTimeTotalMS int64     `json:"watch_time_total_ms"`
	AvgPercentViewed float64   `json:"avg_percent_viewed"`
	CQS              float64   `json:"content_quality_score"`
	Likes            int64     `json:"likes"`
	Shares           int64     `json:"shares"`
	CreatedAt        time.Time `json:"created_at"`
}

type HourlyDataPoint struct {
	Hour        time.Time `json:"hour"`
	Views       int64     `json:"views"`
	Plays       int64     `json:"plays"`
	WatchTimeMS int64     `json:"watch_time_ms"`
}

type DailySummaryRow struct {
	Date        time.Time `json:"date"`
	Views       int64     `json:"views"`
	WatchTimeMS int64     `json:"watch_time_ms"`
	CQS         float64   `json:"cqs"`
}

func (s *AggregateStore) GetCreatorOverview(ctx context.Context, creatorID uuid.UUID, since time.Time) (*CreatorOverview, error) {
	overview := &CreatorOverview{}
	err := s.db.QueryRow(ctx, `
		SELECT
			COALESCE(SUM(views_display), 0),
			COALESCE(SUM(watch_time_total_ms), 0),
			COALESCE(AVG(content_quality_score), 0),
			COALESCE(SUM(likes), 0),
			COALESCE(SUM(shares), 0)
		FROM analytics.content_hourly_agg
		WHERE creator_id = $1 AND hour_bucket >= $2`,
		creatorID, since,
	).Scan(&overview.TotalViews, &overview.TotalWatchTimeMS, &overview.AvgCQS,
		&overview.TotalLikes, &overview.TotalShares)
	if err != nil {
		return nil, err
	}
	return overview, nil
}

func (s *AggregateStore) GetCreatorDailyTrend(ctx context.Context, creatorID uuid.UUID, since time.Time) ([]DailySummaryRow, error) {
	rows, err := s.db.Query(ctx, `
		SELECT date_trunc('day', hour_bucket) AS day_bucket,
		       SUM(views_display), SUM(watch_time_total_ms),
		       AVG(content_quality_score)
		FROM analytics.content_hourly_agg
		WHERE creator_id = $1 AND hour_bucket >= $2
		GROUP BY date_trunc('day', hour_bucket)
		ORDER BY day_bucket ASC`,
		creatorID, since,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var result []DailySummaryRow
	for rows.Next() {
		var r DailySummaryRow
		if err := rows.Scan(&r.Date, &r.Views, &r.WatchTimeMS, &r.CQS); err != nil {
			return nil, err
		}
		result = append(result, r)
	}
	return result, nil
}

func (s *AggregateStore) GetContentList(ctx context.Context, creatorID uuid.UUID, limit int, cursor time.Time, sortBy string) ([]ContentSummary, error) {
	orderCol := "SUM(views_display)"
	switch sortBy {
	case "cqs":
		orderCol = "AVG(content_quality_score)"
	case "watch_time":
		orderCol = "SUM(watch_time_total_ms)"
	}

	rows, err := s.db.Query(ctx, `
		SELECT content_id, content_type,
		       SUM(views_display), SUM(watch_time_total_ms),
		       AVG(avg_percent_viewed), AVG(content_quality_score),
		       SUM(likes), SUM(shares), MIN(created_at)
		FROM analytics.content_hourly_agg
		WHERE creator_id = $1 AND created_at < $2
		GROUP BY content_id, content_type
		ORDER BY `+orderCol+` DESC
		LIMIT $3`,
		creatorID, cursor, limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var result []ContentSummary
	for rows.Next() {
		var c ContentSummary
		if err := rows.Scan(&c.ContentID, &c.ContentType, &c.ViewsDisplay,
			&c.WatchTimeTotalMS, &c.AvgPercentViewed, &c.CQS,
			&c.Likes, &c.Shares, &c.CreatedAt); err != nil {
			return nil, err
		}
		result = append(result, c)
	}
	return result, nil
}

func (s *AggregateStore) GetContentHourlyTrend(ctx context.Context, contentID uuid.UUID, since time.Time) ([]HourlyDataPoint, error) {
	rows, err := s.db.Query(ctx, `
		SELECT hour_bucket, views_display, plays, watch_time_total_ms
		FROM analytics.content_hourly_agg
		WHERE content_id = $1 AND hour_bucket >= $2
		ORDER BY hour_bucket ASC`,
		contentID, since,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var result []HourlyDataPoint
	for rows.Next() {
		var h HourlyDataPoint
		if err := rows.Scan(&h.Hour, &h.Views, &h.Plays, &h.WatchTimeMS); err != nil {
			return nil, err
		}
		result = append(result, h)
	}
	return result, nil
}

// UpsertHourlyAgg upserts a row into content_hourly_agg.
func (s *AggregateStore) UpsertHourlyAgg(ctx context.Context, contentID, creatorID uuid.UUID, hourBucket time.Time, contentType string, metrics map[string]interface{}) error {
	_, err := s.db.Exec(ctx, `
		INSERT INTO analytics.content_hourly_agg (
			content_id, hour_bucket, creator_id, content_type,
			impressions, plays, views_display, views_1s, views_3s, views_10s, views_30s, views_60s,
			unique_viewers, repeat_viewers, watch_time_total_ms, avg_watch_time_ms, avg_percent_viewed,
			completion_rate, rewatch_rate, skip_rate, early_swipe_rate,
			likes, comments, shares, saves, follows_from_content, not_interested, reports, blocks,
			view_score_total, vqs_avg, content_quality_score,
			updated_at
		) VALUES (
			$1, $2, $3, $4,
			$5, $6, $7, $8, $9, $10, $11, $12,
			$13, $14, $15, $16, $17,
			$18, $19, $20, $21,
			$22, $23, $24, $25, $26, $27, $28, $29,
			$30, $31, $32,
			NOW()
		) ON CONFLICT (content_id, hour_bucket)
		DO UPDATE SET
			impressions = EXCLUDED.impressions,
			plays = EXCLUDED.plays,
			views_display = EXCLUDED.views_display,
			views_1s = EXCLUDED.views_1s,
			views_3s = EXCLUDED.views_3s,
			views_10s = EXCLUDED.views_10s,
			views_30s = EXCLUDED.views_30s,
			views_60s = EXCLUDED.views_60s,
			unique_viewers = EXCLUDED.unique_viewers,
			repeat_viewers = EXCLUDED.repeat_viewers,
			watch_time_total_ms = EXCLUDED.watch_time_total_ms,
			avg_watch_time_ms = EXCLUDED.avg_watch_time_ms,
			avg_percent_viewed = EXCLUDED.avg_percent_viewed,
			completion_rate = EXCLUDED.completion_rate,
			rewatch_rate = EXCLUDED.rewatch_rate,
			skip_rate = EXCLUDED.skip_rate,
			early_swipe_rate = EXCLUDED.early_swipe_rate,
			likes = EXCLUDED.likes,
			comments = EXCLUDED.comments,
			shares = EXCLUDED.shares,
			saves = EXCLUDED.saves,
			follows_from_content = EXCLUDED.follows_from_content,
			not_interested = EXCLUDED.not_interested,
			reports = EXCLUDED.reports,
			blocks = EXCLUDED.blocks,
			view_score_total = EXCLUDED.view_score_total,
			vqs_avg = EXCLUDED.vqs_avg,
			content_quality_score = EXCLUDED.content_quality_score,
			updated_at = NOW()`,
		contentID, hourBucket, creatorID, contentType,
		metrics["impressions"], metrics["plays"], metrics["views_display"],
		metrics["views_1s"], metrics["views_3s"], metrics["views_10s"], metrics["views_30s"], metrics["views_60s"],
		metrics["unique_viewers"], metrics["repeat_viewers"],
		metrics["watch_time_total_ms"], metrics["avg_watch_time_ms"], metrics["avg_percent_viewed"],
		metrics["completion_rate"], metrics["rewatch_rate"], metrics["skip_rate"], metrics["early_swipe_rate"],
		metrics["likes"], metrics["comments"], metrics["shares"], metrics["saves"],
		metrics["follows_from_content"], metrics["not_interested"], metrics["reports"], metrics["blocks"],
		metrics["view_score_total"], metrics["vqs_avg"], metrics["content_quality_score"],
	)
	return err
}

// CreatorAggStats holds aggregated analytics totals for a creator.
type CreatorAggStats struct {
	TotalViews    int64
	TotalLikes    int64
	TotalComments int64
	TotalShares   int64
	// TotalFollows is follows_from_content: viewers who followed the
	// creator off the back of a specific video. This is the number that
	// backs the dashboard's follower_growth / followers_gained field,
	// which read a hard-coded zero before the video events were ingested.
	TotalFollows     int64
	TotalSaves       int64
	TotalWatchTimeMS int64
}

// GetCreatorAggStats returns aggregated analytics for a creator since a given time.
func (s *AggregateStore) GetCreatorAggStats(ctx context.Context, creatorID uuid.UUID, since time.Time) (*CreatorAggStats, error) {
	stats := &CreatorAggStats{}
	err := s.db.QueryRow(ctx, `
		SELECT
			COALESCE(SUM(views_display), 0),
			COALESCE(SUM(likes), 0),
			COALESCE(SUM(comments), 0),
			COALESCE(SUM(shares), 0),
			COALESCE(SUM(follows_from_content), 0),
			COALESCE(SUM(saves), 0),
			COALESCE(SUM(watch_time_total_ms), 0)
		FROM analytics.content_hourly_agg
		WHERE creator_id = $1 AND hour_bucket >= $2
	`, creatorID, since).Scan(
		&stats.TotalViews, &stats.TotalLikes, &stats.TotalComments, &stats.TotalShares,
		&stats.TotalFollows, &stats.TotalSaves, &stats.TotalWatchTimeMS,
	)
	if err != nil {
		return nil, err
	}
	return stats, nil
}

// The daily summary has exactly one writer: aggregation.DailyRollup,
// which rebuilds a whole day inside one transaction (delete, then
// insert). There is deliberately no per-row upsert here — a second path
// that could write a summary row is how a hand-written row survived
// every rollup and was settled as money.

// ContentViewBuckets is the lifetime view-counter set for one content
// item, in the exact field names GET /v1/analytics/content/:id/views
// returns. Sourced from content_hourly_agg, which the aggregator
// rebuilds from the ingested milestone and play_end events.
type ContentViewBuckets struct {
	Display  int64
	Views1s  int64
	Views3s  int64
	Views10s int64
	Views30s int64
	Views60s int64
}

// GetContentViewBuckets sums every hour bucket for one content item.
// Used as the durable fallback behind the Redis real-time counters:
// Redis is a cache that expires and is empty on a cold start, whereas
// these rows are the recorded truth.
func (s *AggregateStore) GetContentViewBuckets(ctx context.Context, contentID uuid.UUID) (*ContentViewBuckets, error) {
	var b ContentViewBuckets
	err := s.db.QueryRow(ctx, `
		SELECT COALESCE(SUM(views_display), 0), COALESCE(SUM(views_1s), 0),
		       COALESCE(SUM(views_3s), 0), COALESCE(SUM(views_10s), 0),
		       COALESCE(SUM(views_30s), 0), COALESCE(SUM(views_60s), 0)
		FROM analytics.content_hourly_agg
		WHERE content_id = $1`, contentID,
	).Scan(&b.Display, &b.Views1s, &b.Views3s, &b.Views10s, &b.Views30s, &b.Views60s)
	if err != nil {
		return nil, err
	}
	return &b, nil
}

// ContentViewsBatch returns the visible display-view count for each
// requested content id — the number post-service and feed-service put on
// a post (plan 5B, issue M-13). Every requested id is present in the
// result; unknown content is 0.
//
// The count is the same foundation the creator is paid from:
//
//   - content_daily_summary summed over every rolled-up day: capped at
//     one view per viewer per content per UTC day, self-views excluded,
//     eligibility applied. The rollup re-rolls open days every 15 minutes
//     for 48 hours, then the day is frozen.
//   - plus content_hourly_agg for hours from today's UTC midnight
//     onward: today is never in the daily table (the rollup walks up to
//     yesterday), so its hours are the only "open" buckets. They are
//     rebuilt every five minutes, self-views excluded, but NOT capped —
//     a viewer looping a flick today counts every finalised session
//     until midnight's rollup folds the day to the capped figure.
//
// An hourly row for a day that already has a daily row is the rollup's
// input, not a second count, so it is deliberately not summed; and an
// hourly row for a past day with no daily row is not summed either —
// that day was decided (ineligible, or before the rollup existed) and
// the daily table's silence is the answer.
//
// asOf is the wall-clock time the query ran; the caller reports it so a
// reader knows how stale a cached figure is.
func (s *AggregateStore) ContentViewsBatch(ctx context.Context, contentIDs []uuid.UUID) (map[uuid.UUID]int64, time.Time, error) {
	asOf := time.Now().UTC()
	out := make(map[uuid.UUID]int64, len(contentIDs))
	for _, id := range contentIDs {
		out[id] = 0
	}
	if len(contentIDs) == 0 {
		return out, asOf, nil
	}
	openFrom := asOf.Truncate(24 * time.Hour)
	rows, err := s.db.Query(ctx, `
		WITH ids AS (SELECT DISTINCT unnest($1::uuid[]) AS content_id),
		daily AS (
			SELECT content_id, SUM(views_display) AS views
			FROM analytics.content_daily_summary
			WHERE content_id = ANY($1::uuid[])
			GROUP BY content_id
		),
		open_hours AS (
			SELECT content_id, SUM(views_display) AS views
			FROM analytics.content_hourly_agg
			WHERE content_id = ANY($1::uuid[]) AND hour_bucket >= $2
			GROUP BY content_id
		)
		SELECT i.content_id, COALESCE(d.views, 0) + COALESCE(h.views, 0)
		FROM ids i
		LEFT JOIN daily d USING (content_id)
		LEFT JOIN open_hours h USING (content_id)`,
		contentIDs, openFrom)
	if err != nil {
		return nil, asOf, err
	}
	defer rows.Close()
	for rows.Next() {
		var id uuid.UUID
		var views int64
		if err := rows.Scan(&id, &views); err != nil {
			return nil, asOf, err
		}
		out[id] = views
	}
	if err := rows.Err(); err != nil {
		return nil, asOf, err
	}
	return out, asOf, nil
}
