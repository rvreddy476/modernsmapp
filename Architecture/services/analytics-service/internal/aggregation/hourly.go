package aggregation

import (
	"context"
	"log"
	"sort"
	"strings"
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
// fully recomputes its hour buckets, so running more often is
// idempotent, just fresher.
const aggregationInterval = 5 * time.Minute

type HourlyAggregator struct {
	pg  *pgxpool.Pool
	rdb *redis.Client

	// source decides, per hour bucket, whether views come from play_end
	// rows or from finalised playback sessions. nil is play_end.
	source ViewSourceResolver

	// lastPass is when the previous runAggregation started. Sessions
	// finalised since then may belong to any hour (a session is
	// attributed to the bucket of its first_seen, and the finaliser
	// closes it up to ten minutes after its last heartbeat, or a late
	// play_end reopens it), so every such hour is rebuilt as well as
	// the recent ones.
	lastPass time.Time
}

func NewHourlyAggregator(pg *pgxpool.Pool, rdb *redis.Client) *HourlyAggregator {
	return &HourlyAggregator{pg: pg, rdb: rdb, lastPass: time.Now().UTC().Add(-2 * time.Hour)}
}

// WithViewSource sets the per-bucket source resolver. Without it every
// bucket is aggregated from play_end rows, exactly as before sessions
// existed.
func (a *HourlyAggregator) WithViewSource(r ViewSourceResolver) *HourlyAggregator {
	a.source = r
	return a
}

// Start runs the aggregation loop. Blocks until ctx is cancelled.
func (a *HourlyAggregator) Start(ctx context.Context) {
	ticker := time.NewTicker(aggregationInterval)
	defer ticker.Stop()

	log.Printf("[HourlyAggregator] started (%s interval, source=%s)",
		aggregationInterval, resolveViewSource(a.source, time.Now().UTC().Truncate(time.Hour)))

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

// runAggregation rebuilds the current in-progress hour and the previous
// two complete hours, plus every hour that has had a session finalised
// since the previous pass. The previous hours are the ones that matter
// for correctness (they are closed, so their numbers are final); the
// current hour is rebuilt too so a creator watching their own upload
// sees movement within minutes instead of after the hour rolls over.
func (a *HourlyAggregator) runAggregation(ctx context.Context) {
	now := time.Now().UTC()
	since := a.lastPass
	a.lastPass = now

	currentHour := now.Truncate(time.Hour)
	hours := map[time.Time]struct{}{
		currentHour:                     {},
		currentHour.Add(-time.Hour):     {},
		currentHour.Add(-2 * time.Hour): {},
	}
	for _, hr := range a.hoursWithSessionsFinalizedSince(ctx, since) {
		hours[hr] = struct{}{}
	}

	ordered := make([]time.Time, 0, len(hours))
	for hr := range hours {
		ordered = append(ordered, hr)
	}
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].Before(ordered[j]) })
	for _, hr := range ordered {
		a.AggregateHour(ctx, hr)
	}
}

// hoursWithSessionsFinalizedSince lists the first_seen hour buckets of
// every session finalised at or after `since`. Only consulted when a
// resolver is wired: with play_end everywhere, sessions cannot change a
// bucket. A query failure (the table not yet migrated, say) is logged
// and the pass carries on with the recent hours.
func (a *HourlyAggregator) hoursWithSessionsFinalizedSince(ctx context.Context, since time.Time) []time.Time {
	if a.source == nil {
		return nil
	}
	rows, err := a.pg.Query(ctx, `
		SELECT DISTINCT date_trunc('hour', first_seen AT TIME ZONE 'UTC') AT TIME ZONE 'UTC'
		FROM analytics.playback_sessions
		WHERE finalized_at >= $1`, since)
	if err != nil {
		log.Printf("[HourlyAggregator] finalised-session lookup error: %v", err)
		return nil
	}
	defer rows.Close()
	var hours []time.Time
	for rows.Next() {
		var hr time.Time
		if err := rows.Scan(&hr); err != nil {
			continue
		}
		hours = append(hours, hr.UTC().Truncate(time.Hour))
	}
	return hours
}

// contentAgg is one content item's hour, assembled from events_raw and,
// after cutover, playback_sessions.
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
	source := resolveViewSource(a.source, hourStart)

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

	aggs, err := a.scanHour(ctx, tx, hourStart, source)
	if err != nil {
		log.Printf("[HourlyAggregator] query error (source=%s): %v", source, err)
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
	log.Printf("[HourlyAggregator] aggregated %d content items for %s (source=%s)",
		len(aggs), hourStart.Format(time.RFC3339), source)
}

// scanHour reads one hour and folds it into per-content aggregates.
// Every one of the thirteen ingested event types contributes something:
// impressions and play_starts set the denominators, milestones fill the
// view-duration buckets, and the eight engagement types feed the
// quality score. What carries the view itself — watch time, percent
// viewed, the display-view flag, the view score — comes from play_end
// rows before the cutover and from finalised playback sessions after
// it; the two statements produce the same column set.
func (a *HourlyAggregator) scanHour(ctx context.Context, q pgx.Tx, hourStart time.Time, source string) (map[string]*contentAgg, error) {
	sql := hourlyScanSQL
	if source == ViewSourceSessions {
		sql = hourlySessionScanSQL
	}
	rows, err := q.Query(ctx, sql, hourStart, hourStart.Add(time.Hour))
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

// nonSelfPlayEnd is the play_end predicate on the legacy path. A
// creator watching their own upload is not a view for any purpose the
// hourly table serves — not a display view, not watch time, not a
// viewer — so every play_end-derived column filters on it. Raw rows,
// plays and impressions are untouched, so dashboards still see them.
const nonSelfPlayEnd = `type = 'play_end' AND NOT COALESCE((payload->>'is_self_view')::boolean, false)`

// canonicalContentTypeSQL wraps a content_type expression in the same
// mapping migration 007 applied to the stored rows and
// postclassify.CanonicalMonetizationType applies in code. Without it a
// rebuild of a bucket inside the window would re-derive 'reel' from an
// old payload and undo the migration for that bucket.
func canonicalContentTypeSQL(expr string) string {
	return `CASE WHEN ` + expr + ` IN ('reel','short') THEN 'flick'
	             WHEN ` + expr + ` = 'video' THEN 'long_video'
	             ELSE ` + expr + ` END`
}

// The legacy (pre-cutover) hour: everything from events_raw, the view
// carried by play_end. Kept intact for buckets the resolver routes here.
var hourlyScanSQL = strings.NewReplacer(
	"{{play_end}}", nonSelfPlayEnd,
	"{{content_type}}", canonicalContentTypeSQL(`COALESCE(MIN(payload->>'content_type'), 'unknown')`),
).Replace(`
	SELECT
		payload->>'content_id'                                   AS content_id,
		MIN(payload->>'creator_id')                              AS creator_id,
		-- The ownership projection always stamps a content_type, so the
		-- COALESCE only fires for pre-projection legacy rows. It must not
		-- invent a kind: 'unknown' is skipped by monetization's rate
		-- lookup, whereas the old 'reel' default quietly labelled
		-- unattributed rows as short-form.
		{{content_type}}                                         AS content_type,

		COUNT(*) FILTER (WHERE type = 'impression')              AS impressions,
		COUNT(*) FILTER (WHERE type = 'play_start')              AS plays,
		COUNT(*) FILTER (WHERE {{play_end}}
			AND COALESCE((payload->>'is_display_view')::boolean, false)) AS views_display,

		COUNT(*) FILTER (WHERE type = 'milestone' AND payload->>'view_bucket' = 'views_1s')   AS views_1s,
		COUNT(*) FILTER (WHERE type = 'milestone' AND payload->>'view_bucket' = 'views_3s')   AS views_3s,
		COUNT(*) FILTER (WHERE type = 'milestone' AND payload->>'view_bucket' = 'views_10s')  AS views_10s,
		COUNT(*) FILTER (WHERE type = 'milestone' AND payload->>'view_bucket' = 'views_30s')  AS views_30s,
		COUNT(*) FILTER (WHERE type = 'milestone' AND payload->>'view_bucket' = 'views_60s')  AS views_60s,

		COUNT(*) FILTER (WHERE {{play_end}})                             AS play_ends,
		COUNT(DISTINCT user_id) FILTER (WHERE {{play_end}})              AS unique_viewers,

		COALESCE(SUM((payload->>'watched_ms_total')::bigint)
			FILTER (WHERE {{play_end}}), 0)                              AS watch_time_ms,
		COALESCE(AVG((payload->>'percent_viewed')::double precision)
			FILTER (WHERE {{play_end}}), 0)                              AS avg_percent_viewed,
		-- Quality-weighted view count: each display view contributes the
		-- fraction of the video actually watched. This is what the
		-- creator fund's eligibility view-score threshold reads.
		COALESCE(SUM(LEAST((payload->>'percent_viewed')::double precision, 100.0) / 100.0)
			FILTER (WHERE {{play_end}}
				AND COALESCE((payload->>'is_display_view')::boolean, false)), 0) AS view_score_total,

		COUNT(*) FILTER (WHERE {{play_end}}
			AND (payload->>'percent_viewed')::double precision >= 95)    AS completions,
		COUNT(*) FILTER (WHERE {{play_end}}
			AND COALESCE((payload->>'loop_count')::int, 0) > 0)          AS rewatches,
		COUNT(*) FILTER (WHERE {{play_end}}
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
	GROUP BY payload->>'content_id'`)

// The post-cutover hour. Impressions, plays, milestones and engagement
// still come from events_raw; the view comes from finalised playback
// sessions, attributed to the hour of their first_seen, never a
// self-view. An open session is not a view yet; a session finalised
// late lands in its first_seen bucket and runAggregation rebuilds that
// bucket. The two sides are joined FULL OUTER so content with sessions
// but no other events in the hour (or the reverse) still gets a row.
//
// Coverage columns: percent_covered replaces percent_viewed for
// completions, early swipes and the quality score's proportion-viewed
// input, except on backfilled sessions (source = 'backfill'), which
// were reconstructed from play_end rows and have no coverage bitmap;
// their legacy percent_viewed stands so the backfilled hours reconcile
// exactly against the play_end path.
var hourlySessionScanSQL = strings.NewReplacer(
	"{{content_type}}", canonicalContentTypeSQL(`COALESCE(ps.content_type, ev.content_type, 'unknown')`),
).Replace(`
	WITH ev AS (
		SELECT
			payload->>'content_id'                                   AS content_id,
			MIN(payload->>'creator_id')                              AS creator_id,
			MIN(payload->>'content_type')                            AS content_type,
			COUNT(*) FILTER (WHERE type = 'impression')              AS impressions,
			COUNT(*) FILTER (WHERE type = 'play_start')              AS plays,
			COUNT(*) FILTER (WHERE type = 'milestone' AND payload->>'view_bucket' = 'views_1s')   AS views_1s,
			COUNT(*) FILTER (WHERE type = 'milestone' AND payload->>'view_bucket' = 'views_3s')   AS views_3s,
			COUNT(*) FILTER (WHERE type = 'milestone' AND payload->>'view_bucket' = 'views_10s')  AS views_10s,
			COUNT(*) FILTER (WHERE type = 'milestone' AND payload->>'view_bucket' = 'views_30s')  AS views_30s,
			COUNT(*) FILTER (WHERE type = 'milestone' AND payload->>'view_bucket' = 'views_60s')  AS views_60s,
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
		  AND type IN ('impression','play_start','milestone','watch_heartbeat',
		               'like','comment_create','share','save','follow_from_content',
		               'not_interested','report','block_creator')
		  AND payload->>'content_id' IS NOT NULL
		  AND payload->>'creator_id' IS NOT NULL
		GROUP BY payload->>'content_id'
	),
	ps AS (
		SELECT
			content_id::text                                          AS content_id,
			MIN(creator_id::text)                                     AS creator_id,
			MIN(content_type)                                         AS content_type,
			COUNT(*) FILTER (WHERE is_display_view)                   AS views_display,
			COUNT(*)                                                  AS play_ends,
			COUNT(DISTINCT actor_id)                                  AS unique_viewers,
			COALESCE(SUM(watched_ms), 0)                              AS watch_time_ms,
			COALESCE(AVG(pct), 0)                                     AS avg_percent_viewed,
			COALESCE(SUM(view_score) FILTER (WHERE is_display_view), 0) AS view_score_total,
			COUNT(*) FILTER (WHERE pct >= 95)                         AS completions,
			COUNT(*) FILTER (WHERE loop_count > 0)                    AS rewatches,
			COUNT(*) FILTER (WHERE end_reason = 'swipe_next' AND pct < 25) AS early_swipes
		FROM (
			SELECT s.*,
			       CASE WHEN s.source = 'backfill' THEN s.percent_viewed ELSE s.percent_covered END AS pct
			FROM analytics.playback_sessions s
			WHERE s.first_seen >= $1 AND s.first_seen < $2
			  AND s.finalized_at IS NOT NULL
			  AND NOT s.is_self_view
		) sessions
		GROUP BY content_id
	)
	SELECT
		COALESCE(ev.content_id, ps.content_id)         AS content_id,
		COALESCE(ev.creator_id, ps.creator_id)         AS creator_id,
		{{content_type}}                               AS content_type,
		COALESCE(ev.impressions, 0), COALESCE(ev.plays, 0), COALESCE(ps.views_display, 0),
		COALESCE(ev.views_1s, 0), COALESCE(ev.views_3s, 0), COALESCE(ev.views_10s, 0),
		COALESCE(ev.views_30s, 0), COALESCE(ev.views_60s, 0),
		COALESCE(ps.play_ends, 0), COALESCE(ps.unique_viewers, 0),
		COALESCE(ps.watch_time_ms, 0), COALESCE(ps.avg_percent_viewed, 0), COALESCE(ps.view_score_total, 0),
		COALESCE(ps.completions, 0), COALESCE(ps.rewatches, 0), COALESCE(ps.early_swipes, 0),
		COALESCE(ev.likes, 0), COALESCE(ev.comments, 0), COALESCE(ev.shares, 0), COALESCE(ev.saves, 0),
		COALESCE(ev.follows, 0), COALESCE(ev.not_interested, 0), COALESCE(ev.reports, 0), COALESCE(ev.blocks, 0)
	FROM ev
	FULL OUTER JOIN ps ON ps.content_id = ev.content_id`)

func ratio(numerator, denominator int64) float64 {
	if denominator <= 0 {
		return 0
	}
	return float64(numerator) / float64(denominator)
}
