package aggregation

import (
	"context"
	"log"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
)

type HourlyAggregator struct {
	pg  *pgxpool.Pool
	rdb *redis.Client
}

func NewHourlyAggregator(pg *pgxpool.Pool, rdb *redis.Client) *HourlyAggregator {
	return &HourlyAggregator{pg: pg, rdb: rdb}
}

// Start runs the hourly aggregation loop. Blocks until ctx is cancelled.
func (a *HourlyAggregator) Start(ctx context.Context) {
	ticker := time.NewTicker(1 * time.Hour)
	defer ticker.Stop()

	log.Println("[HourlyAggregator] started (1 hour interval)")

	// Run once on startup for the previous hour
	a.runAggregation(ctx)

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			a.runAggregation(ctx)
		}
	}
}

func (a *HourlyAggregator) runAggregation(ctx context.Context) {
	// Aggregate the previous complete hour
	now := time.Now().UTC()
	hourStart := now.Truncate(time.Hour).Add(-time.Hour)

	// Use pg_advisory_xact_lock to prevent double-processing across instances
	tx, err := a.pg.Begin(ctx)
	if err != nil {
		log.Printf("[HourlyAggregator] begin tx error: %v", err)
		return
	}
	defer tx.Rollback(ctx)

	// Advisory lock based on hour bucket (prevents concurrent aggregation)
	lockKey := hourStart.Unix()
	var locked bool
	err = tx.QueryRow(ctx, "SELECT pg_try_advisory_xact_lock($1)", lockKey).Scan(&locked)
	if err != nil || !locked {
		return // another instance is processing this hour
	}

	log.Printf("[HourlyAggregator] aggregating hour: %s", hourStart.Format(time.RFC3339))

	// Query: group events_raw by content_id for this hour window
	rows, err := a.pg.Query(ctx, `
		SELECT
			payload->>'content_id' AS content_id,
			payload->>'creator_id' AS creator_id,
			payload->>'content_type' AS content_type,
			type,
			COUNT(*) as cnt,
			COALESCE(SUM((payload->>'watched_ms_total')::bigint), 0) as total_watched_ms,
			COALESCE(AVG((payload->>'percent_viewed')::float), 0) as avg_pct
		FROM analytics.events_raw
		WHERE ts >= $1 AND ts < $2
		  AND type IN ('impression','play_start','play_end','milestone','watch_heartbeat',
		               'like','comment_create','share','save','follow_from_content',
		               'not_interested','report','block_creator')
		  AND payload->>'content_id' IS NOT NULL
		GROUP BY payload->>'content_id', payload->>'creator_id', payload->>'content_type', type
		ORDER BY content_id`,
		hourStart, hourStart.Add(time.Hour),
	)
	if err != nil {
		log.Printf("[HourlyAggregator] query error: %v", err)
		return
	}
	defer rows.Close()

	// Build per-content aggregates
	type contentAgg struct {
		creatorID   string
		contentType string
		metrics     map[string]int64
		avgPct      float64
		totalWatch  int64
	}
	aggs := make(map[string]*contentAgg)

	for rows.Next() {
		var contentID, creatorID, contentType, eventType string
		var cnt, totalWatched int64
		var avgPct float64
		if err := rows.Scan(&contentID, &creatorID, &contentType, &eventType, &cnt, &totalWatched, &avgPct); err != nil {
			continue
		}

		ca, ok := aggs[contentID]
		if !ok {
			ca = &contentAgg{
				creatorID:   creatorID,
				contentType: contentType,
				metrics:     make(map[string]int64),
			}
			aggs[contentID] = ca
		}

		switch eventType {
		case "impression":
			ca.metrics["impressions"] = cnt
		case "play_start":
			ca.metrics["plays"] = cnt
		case "play_end":
			ca.totalWatch = totalWatched
			ca.avgPct = avgPct
		case "like":
			ca.metrics["likes"] = cnt
		case "comment_create":
			ca.metrics["comments"] = cnt
		case "share":
			ca.metrics["shares"] = cnt
		case "save":
			ca.metrics["saves"] = cnt
		case "follow_from_content":
			ca.metrics["follows_from_content"] = cnt
		case "not_interested":
			ca.metrics["not_interested"] = cnt
		case "report":
			ca.metrics["reports"] = cnt
		case "block_creator":
			ca.metrics["blocks"] = cnt
		}
	}

	// Write aggregates to content_hourly_agg
	for contentID, ca := range aggs {
		plays := ca.metrics["plays"]
		avgWatchTime := int64(0)
		if plays > 0 {
			avgWatchTime = ca.totalWatch / plays
		}

		_, err := a.pg.Exec(ctx, `
			INSERT INTO analytics.content_hourly_agg (
				content_id, hour_bucket, creator_id, content_type,
				impressions, plays, views_display,
				watch_time_total_ms, avg_watch_time_ms, avg_percent_viewed,
				likes, comments, shares, saves,
				follows_from_content, not_interested, reports, blocks,
				updated_at
			) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, NOW())
			ON CONFLICT (content_id, hour_bucket)
			DO UPDATE SET
				impressions = EXCLUDED.impressions, plays = EXCLUDED.plays,
				watch_time_total_ms = EXCLUDED.watch_time_total_ms,
				avg_watch_time_ms = EXCLUDED.avg_watch_time_ms,
				avg_percent_viewed = EXCLUDED.avg_percent_viewed,
				likes = EXCLUDED.likes, comments = EXCLUDED.comments,
				shares = EXCLUDED.shares, saves = EXCLUDED.saves,
				follows_from_content = EXCLUDED.follows_from_content,
				not_interested = EXCLUDED.not_interested,
				reports = EXCLUDED.reports, blocks = EXCLUDED.blocks,
				updated_at = NOW()`,
			contentID, hourStart, ca.creatorID, ca.contentType,
			ca.metrics["impressions"], plays, ca.metrics["views_display"],
			ca.totalWatch, avgWatchTime, ca.avgPct,
			ca.metrics["likes"], ca.metrics["comments"], ca.metrics["shares"], ca.metrics["saves"],
			ca.metrics["follows_from_content"], ca.metrics["not_interested"], ca.metrics["reports"], ca.metrics["blocks"],
		)
		if err != nil {
			log.Printf("[HourlyAggregator] upsert error for %s: %v", contentID, err)
		}
	}

	if err := tx.Commit(ctx); err != nil {
		log.Printf("[HourlyAggregator] commit error: %v", err)
	} else {
		log.Printf("[HourlyAggregator] aggregated %d content items for %s", len(aggs), hourStart.Format(time.RFC3339))
	}
}
