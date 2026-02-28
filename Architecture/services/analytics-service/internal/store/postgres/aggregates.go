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
		FROM analytics.content_daily_summary
		WHERE creator_id = $1 AND day_bucket >= $2`,
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
		SELECT day_bucket, SUM(views_display), SUM(watch_time_total_ms),
		       AVG(content_quality_score)
		FROM analytics.content_daily_summary
		WHERE creator_id = $1 AND day_bucket >= $2
		GROUP BY day_bucket
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
	orderCol := "views_display"
	switch sortBy {
	case "cqs":
		orderCol = "content_quality_score"
	case "watch_time":
		orderCol = "watch_time_total_ms"
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

// UpsertDailySummary upserts a daily summary row.
func (s *AggregateStore) UpsertDailySummary(ctx context.Context, contentID, creatorID uuid.UUID, dayBucket time.Time, contentType string,
	impressions, plays, viewsDisplay, uniqueViewers, watchTimeMS, likes, comments, shares, saves int64,
	avgPercentViewed, completionRate, viewScoreTotal, cqs float64) error {
	_, err := s.db.Exec(ctx, `
		INSERT INTO analytics.content_daily_summary (
			content_id, day_bucket, creator_id, content_type,
			impressions, plays, views_display, unique_viewers, watch_time_total_ms,
			avg_percent_viewed, completion_rate, likes, comments, shares, saves,
			view_score_total, content_quality_score, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, NOW())
		ON CONFLICT (content_id, day_bucket)
		DO UPDATE SET
			impressions = EXCLUDED.impressions, plays = EXCLUDED.plays,
			views_display = EXCLUDED.views_display, unique_viewers = EXCLUDED.unique_viewers,
			watch_time_total_ms = EXCLUDED.watch_time_total_ms,
			avg_percent_viewed = EXCLUDED.avg_percent_viewed, completion_rate = EXCLUDED.completion_rate,
			likes = EXCLUDED.likes, comments = EXCLUDED.comments, shares = EXCLUDED.shares, saves = EXCLUDED.saves,
			view_score_total = EXCLUDED.view_score_total, content_quality_score = EXCLUDED.content_quality_score,
			updated_at = NOW()`,
		contentID, dayBucket, creatorID, contentType,
		impressions, plays, viewsDisplay, uniqueViewers, watchTimeMS,
		avgPercentViewed, completionRate, likes, comments, shares, saves,
		viewScoreTotal, cqs,
	)
	return err
}
