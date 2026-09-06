package aggregation

import (
	"context"
	"log"
	"time"

	"github.com/atpost/analytics-service/internal/scoring"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
)

// aggregationInterval is how often the aggregator wakes. It used to be
// one hour, which meant a creator's dashboard could be up to two hours
// behind the events they had just generated — indistinguishable from
// "analytics is broken" when you are testing your own upload. Each pass
// fully recomputes its hour buckets from events_raw, so running more
// often is idempotent, just fresher.
const aggregationInterval = 5 * time.Minute

type HourlyAggregator struct {
	pg  *pgxpool.Pool
	rdb *redis.Client
}

func NewHourlyAggregator(pg *pgxpool.Pool, rdb *redis.Client) *HourlyAggregator {
	return &HourlyAggregator{pg: pg, rdb: rdb}
}

// Start runs the aggregation loop. Blocks until ctx is cancelled.
func (a *HourlyAggregator) Start(ctx context.Context) {
	ticker := time.NewTicker(aggregationInterval)
	defer ticker.Stop()

	log.Printf("[HourlyAggregator] started (%s interval)", aggregationInterval)

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

// runAggregation rebuilds the previous complete hour and the current
// in-progress hour. The previous hour is the one that matters for
// correctness (it is closed, so its numbers are final); the current hour
// is rebuilt too so a creator watching their own upload sees movement
// within minutes instead of after the hour rolls over.
func (a *HourlyAggregator) runAggregation(ctx context.Context) {
	now := time.Now().UTC()
	currentHour := now.Truncate(time.Hour)
	a.AggregateHour(ctx, currentHour.Add(-time.Hour))
	a.AggregateHour(ctx, currentHour)
}

// contentAgg is one content item's hour, assembled from events_raw.
type contentAgg struct {
	creatorID   string
	contentType string

	impressions  int64
	plays        int64
	viewsDisplay int64
	views1s      int64
	views3s      int64
	views10s     int64
	views30s     int64
	views60s     int64

	playEnds      int64
	uniqueViewers int64
	watchTimeMS   int64
	avgPercent    float64
	completions   int64
	rewatches     int64
	earlySwipes   int64
	viewScore     float64

	likes         int64
	comments      int64
	shares        int64
	saves         int64
	follows       int64
	notInterested int64
	reports       int64
	blocks        int64
}

// AggregateHour recomputes analytics.content_hourly_agg for every piece
// of content with events in [hourStart, hourStart+1h). Exported so the
// daily rollup and tests can rebuild a specific hour deterministically.
func (a *HourlyAggregator) AggregateHour(ctx context.Context, hourStart time.Time) {
	hourStart = hourStart.UTC().Truncate(time.Hour)

	tx, err := a.pg.Begin(ctx)
	if err != nil {
		log.Printf("[HourlyAggregator] begin tx error: %v", err)
		return
	}
	defer tx.Rollback(ctx)

	// Advisory lock based on hour bucket (prevents concurrent aggregation
	// of the same hour across instances).
	lockKey := hourStart.Unix()
	var locked bool
	if err := tx.QueryRow(ctx, "SELECT pg_try_advisory_xact_lock($1)", lockKey).Scan(&locked); err != nil || !locked {
		return // another instance is processing this hour
	}

	aggs, err := a.scanHour(ctx, tx, hourStart)
	if err != nil {
		log.Printf("[HourlyAggregator] query error: %v", err)
		return
	}
	if len(aggs) == 0 {
		_ = tx.Commit(ctx)
		return
	}

	for contentID, ca := range aggs {
		avgWatchTime := int64(0)
		if ca.playEnds > 0 {
			avgWatchTime = ca.watchTimeMS / ca.playEnds
		}

		// Rates are all "per completed playback", the only denominator
		// that is meaningful for them. A zero denominator yields zero
		// rather than a division by zero.
		completionRate := ratio(ca.completions, ca.playEnds)
		rewatchRate := ratio(ca.rewatches, ca.playEnds)
		earlySwipeRate := ratio(ca.earlySwipes, ca.playEnds)
		// A skip is an impression that never became a display view.
		skipRate := 0.0
		if ca.impressions > 0 {
			skipped := ca.impressions - ca.viewsDisplay
			if skipped < 0 {
				skipped = 0
			}
			skipRate = float64(skipped) / float64(ca.impressions)
		}

		vqsAvg := 0.0
		if ca.viewsDisplay > 0 {
			vqsAvg = ca.viewScore / float64(ca.viewsDisplay)
		}

		// The quality score the creator fund reads. Impressions are the
		// denominator, so a content item with no impressions scores 0
		// rather than dividing by zero — ComputeCQS guards this itself.
		cqs := scoring.ComputeCQS(&scoring.AggregateMetrics{
			AvgPercentViewed:   ca.avgPercent,
			Impressions:        ca.impressions,
			Likes:              ca.likes,
			Comments:           ca.comments,
			Shares:             ca.shares,
			Saves:              ca.saves,
			FollowsFromContent: ca.follows,
			Reports:            ca.reports,
			NotInterested:      ca.notInterested,
		})

		// Repeat viewers: completed playbacks beyond the first from the
		// same viewer in this hour.
		repeatViewers := ca.playEnds - ca.uniqueViewers
		if repeatViewers < 0 {
			repeatViewers = 0
		}

		if _, err := tx.Exec(ctx, `
			INSERT INTO analytics.content_hourly_agg (
				content_id, hour_bucket, creator_id, content_type,
				impressions, plays, views_display,
				views_1s, views_3s, views_10s, views_30s, views_60s,
				unique_viewers, repeat_viewers,
				watch_time_total_ms, avg_watch_time_ms, avg_percent_viewed,
				completion_rate, rewatch_rate, skip_rate, early_swipe_rate,
				likes, comments, shares, saves,
				follows_from_content, not_interested, reports, blocks,
				view_score_total, vqs_avg, content_quality_score,
				updated_at
			) VALUES (
				$1, $2, $3, $4,
				$5, $6, $7,
				$8, $9, $10, $11, $12,
				$13, $14,
				$15, $16, $17,
				$18, $19, $20, $21,
				$22, $23, $24, $25,
				$26, $27, $28, $29,
				$30, $31, $32,
				NOW())
			ON CONFLICT (content_id, hour_bucket)
			DO UPDATE SET
				creator_id = EXCLUDED.creator_id,
				content_type = EXCLUDED.content_type,
				impressions = EXCLUDED.impressions, plays = EXCLUDED.plays,
				views_display = EXCLUDED.views_display,
				views_1s = EXCLUDED.views_1s, views_3s = EXCLUDED.views_3s,
				views_10s = EXCLUDED.views_10s, views_30s = EXCLUDED.views_30s,
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
				likes = EXCLUDED.likes, comments = EXCLUDED.comments,
				shares = EXCLUDED.shares, saves = EXCLUDED.saves,
				follows_from_content = EXCLUDED.follows_from_content,
				not_interested = EXCLUDED.not_interested,
				reports = EXCLUDED.reports, blocks = EXCLUDED.blocks,
				view_score_total = EXCLUDED.view_score_total,
				vqs_avg = EXCLUDED.vqs_avg,
				content_quality_score = EXCLUDED.content_quality_score,
				updated_at = NOW()`,
			contentID, hourStart, ca.creatorID, ca.contentType,
			ca.impressions, ca.plays, ca.viewsDisplay,
			ca.views1s, ca.views3s, ca.views10s, ca.views30s, ca.views60s,
			ca.uniqueViewers, repeatViewers,
			ca.watchTimeMS, avgWatchTime, ca.avgPercent,
			completionRate, rewatchRate, skipRate, earlySwipeRate,
			ca.likes, ca.comments, ca.shares, ca.saves,
			ca.follows, ca.notInterested, ca.reports, ca.blocks,
			ca.viewScore, vqsAvg, cqs,
		); err != nil {
			log.Printf("[HourlyAggregator] upsert error for %s: %v", contentID, err)
		}
	}

	if err := tx.Commit(ctx); err != nil {
		log.Printf("[HourlyAggregator] commit error: %v", err)
		return
	}
	log.Printf("[HourlyAggregator] aggregated %d content items for %s",
		len(aggs), hourStart.Format(time.RFC3339))
}

// scanHour reads one hour of events_raw and folds it into per-content
// aggregates. Every one of the thirteen ingested event types contributes
// something: impressions and play_starts set the denominators, milestones
// fill the view-duration buckets, play_end carries the watch time and
// percent-viewed, and the eight engagement types feed the quality score.
func (a *HourlyAggregator) scanHour(ctx context.Context, q pgx.Tx, hourStart time.Time) (map[string]*contentAgg, error) {
	rows, err := q.Query(ctx, hourlyScanSQL, hourStart, hourStart.Add(time.Hour))
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	aggs := make(map[string]*contentAgg)
	for rows.Next() {
		var contentID string
		ca := &contentAgg{}
		if err := rows.Scan(
			&contentID, &ca.creatorID, &ca.contentType,
			&ca.impressions, &ca.plays, &ca.viewsDisplay,
			&ca.views1s, &ca.views3s, &ca.views10s, &ca.views30s, &ca.views60s,
			&ca.playEnds, &ca.uniqueViewers,
			&ca.watchTimeMS, &ca.avgPercent, &ca.viewScore,
			&ca.completions, &ca.rewatches, &ca.earlySwipes,
			&ca.likes, &ca.comments, &ca.shares, &ca.saves,
			&ca.follows, &ca.notInterested, &ca.reports, &ca.blocks,
		); err != nil {
			log.Printf("[HourlyAggregator] scan error: %v", err)
			continue
		}
		aggs[contentID] = ca
	}
	return aggs, rows.Err()
}

const hourlyScanSQL = `
	SELECT
		payload->>'content_id'                                   AS content_id,
		MIN(payload->>'creator_id')                              AS creator_id,
		COALESCE(MIN(payload->>'content_type'), 'reel')          AS content_type,

		COUNT(*) FILTER (WHERE type = 'impression')              AS impressions,
		COUNT(*) FILTER (WHERE type = 'play_start')              AS plays,
		COUNT(*) FILTER (WHERE type = 'play_end'
			AND COALESCE((payload->>'is_display_view')::boolean, false)) AS views_display,

		COUNT(*) FILTER (WHERE type = 'milestone' AND payload->>'view_bucket' = 'views_1s')   AS views_1s,
		COUNT(*) FILTER (WHERE type = 'milestone' AND payload->>'view_bucket' = 'views_3s')   AS views_3s,
		COUNT(*) FILTER (WHERE type = 'milestone' AND payload->>'view_bucket' = 'views_10s')  AS views_10s,
		COUNT(*) FILTER (WHERE type = 'milestone' AND payload->>'view_bucket' = 'views_30s')  AS views_30s,
		COUNT(*) FILTER (WHERE type = 'milestone' AND payload->>'view_bucket' = 'views_60s')  AS views_60s,

		COUNT(*) FILTER (WHERE type = 'play_end')                        AS play_ends,
		COUNT(DISTINCT user_id) FILTER (WHERE type = 'play_end')         AS unique_viewers,

		COALESCE(SUM((payload->>'watched_ms_total')::bigint)
			FILTER (WHERE type = 'play_end'), 0)                         AS watch_time_ms,
		COALESCE(AVG((payload->>'percent_viewed')::double precision)
			FILTER (WHERE type = 'play_end'), 0)                         AS avg_percent_viewed,
		-- Quality-weighted view count: each display view contributes the
		-- fraction of the video actually watched. This is what the
		-- creator fund's eligibility view-score threshold reads.
		COALESCE(SUM(LEAST((payload->>'percent_viewed')::double precision, 100.0) / 100.0)
			FILTER (WHERE type = 'play_end'
				AND COALESCE((payload->>'is_display_view')::boolean, false)), 0) AS view_score_total,

		COUNT(*) FILTER (WHERE type = 'play_end'
			AND (payload->>'percent_viewed')::double precision >= 95)    AS completions,
		COUNT(*) FILTER (WHERE type = 'play_end'
			AND COALESCE((payload->>'loop_count')::int, 0) > 0)          AS rewatches,
		COUNT(*) FILTER (WHERE type = 'play_end'
			AND payload->>'end_reason' = 'swipe_next'
			AND (payload->>'percent_viewed')::double precision < 25)     AS early_swipes,

		COUNT(*) FILTER (WHERE type = 'like')                AS likes,
		COUNT(*) FILTER (WHERE type = 'comment_create')      AS comments,
		COUNT(*) FILTER (WHERE type = 'share')               AS shares,
		COUNT(*) FILTER (WHERE type = 'save')                AS saves,
		COUNT(*) FILTER (WHERE type = 'follow_from_content') AS follows,
		COUNT(*) FILTER (WHERE type = 'not_interested')      AS not_interested,
		COUNT(*) FILTER (WHERE type = 'report')              AS reports,
		COUNT(*) FILTER (WHERE type = 'block_creator')       AS blocks
	FROM analytics.events_raw
	WHERE ts >= $1 AND ts < $2
	  AND type IN ('impression','play_start','play_end','milestone','watch_heartbeat',
	               'like','comment_create','share','save','follow_from_content',
	               'not_interested','report','block_creator')
	  AND payload->>'content_id' IS NOT NULL
	  AND payload->>'creator_id' IS NOT NULL
	GROUP BY payload->>'content_id'`

func ratio(numerator, denominator int64) float64 {
	if denominator <= 0 {
		return 0
	}
	return float64(numerator) / float64(denominator)
}
