package aggregation

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
)

type DailyRollup struct {
	pg  *pgxpool.Pool
	rdb *redis.Client
}

func NewDailyRollup(pg *pgxpool.Pool, rdb *redis.Client) *DailyRollup {
	return &DailyRollup{pg: pg, rdb: rdb}
}

// Start runs daily at the top of every hour, checks if day boundary crossed.
func (d *DailyRollup) Start(ctx context.Context) {
	ticker := time.NewTicker(1 * time.Hour)
	defer ticker.Stop()

	log.Println("[DailyRollup] started")

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			now := time.Now().UTC()
			// Only run in the first hour of the day (00:xx UTC)
			if now.Hour() == 0 {
				d.rollupPreviousDay(ctx)
			}
		}
	}
}

func (d *DailyRollup) rollupPreviousDay(ctx context.Context) {
	yesterday := time.Now().UTC().AddDate(0, 0, -1).Truncate(24 * time.Hour)
	dayEnd := yesterday.AddDate(0, 0, 1)

	log.Printf("[DailyRollup] rolling up day: %s", yesterday.Format("2006-01-02"))

	_, err := d.pg.Exec(ctx, `
		INSERT INTO analytics.content_daily_summary (
			content_id, day_bucket, creator_id, content_type,
			impressions, plays, views_display, unique_viewers, watch_time_total_ms,
			avg_percent_viewed, completion_rate,
			likes, comments, shares, saves,
			view_score_total, content_quality_score
		)
		SELECT
			content_id, $1::date, creator_id, content_type,
			SUM(impressions), SUM(plays), SUM(views_display), SUM(unique_viewers),
			SUM(watch_time_total_ms),
			AVG(avg_percent_viewed), AVG(completion_rate),
			SUM(likes), SUM(comments), SUM(shares), SUM(saves),
			SUM(view_score_total), AVG(content_quality_score)
		FROM analytics.content_hourly_agg
		WHERE hour_bucket >= $2 AND hour_bucket < $3
		GROUP BY content_id, creator_id, content_type
		ON CONFLICT (content_id, day_bucket)
		DO UPDATE SET
			impressions = EXCLUDED.impressions, plays = EXCLUDED.plays,
			views_display = EXCLUDED.views_display, unique_viewers = EXCLUDED.unique_viewers,
			watch_time_total_ms = EXCLUDED.watch_time_total_ms,
			avg_percent_viewed = EXCLUDED.avg_percent_viewed, completion_rate = EXCLUDED.completion_rate,
			likes = EXCLUDED.likes, comments = EXCLUDED.comments, shares = EXCLUDED.shares, saves = EXCLUDED.saves,
			view_score_total = EXCLUDED.view_score_total, content_quality_score = EXCLUDED.content_quality_score,
			updated_at = NOW()`,
		yesterday, yesterday, dayEnd,
	)
	if err != nil {
		log.Printf("[DailyRollup] rollup error: %v", err)
		return
	}

	// Refresh CQS cache in Redis for all content with activity
	d.refreshCQSCache(ctx, yesterday, dayEnd)
}

func (d *DailyRollup) refreshCQSCache(ctx context.Context, dayStart, dayEnd time.Time) {
	rows, err := d.pg.Query(ctx, `
		SELECT content_id, content_quality_score
		FROM analytics.content_daily_summary
		WHERE day_bucket = $1`,
		dayStart,
	)
	if err != nil {
		log.Printf("[DailyRollup] CQS cache refresh query error: %v", err)
		return
	}
	defer rows.Close()

	pipe := d.rdb.Pipeline()
	count := 0
	for rows.Next() {
		var contentID string
		var cqs float64
		if err := rows.Scan(&contentID, &cqs); err != nil {
			continue
		}
		pipe.Set(ctx, fmt.Sprintf("post:cqs:%s", contentID), cqs, time.Hour)
		count++
	}

	if count > 0 {
		if _, err := pipe.Exec(ctx); err != nil {
			log.Printf("[DailyRollup] CQS cache refresh pipeline error: %v", err)
		} else {
			log.Printf("[DailyRollup] refreshed CQS cache for %d content items", count)
		}
	}
}
