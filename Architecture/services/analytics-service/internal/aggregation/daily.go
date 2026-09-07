package aggregation

import (
	"context"
	"fmt"
	"log"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
)

const (
	// viewCapEnv sets how many display views a single viewer may
	// contribute to one content item in one UTC day. A value <= 0
	// disables the cap entirely and reproduces the uncapped numbers
	// exactly — the escape hatch, not the default.
	viewCapEnv = "ANALYTICS_VIEW_CAP_PER_VIEWER_PER_DAY"

	// defaultViewCapPerViewerPerDay is the paid-view ceiling per viewer
	// per content item per UTC day.
	//
	// A re-watch is a real view: looping is the behaviour the flick feed
	// is built around, and refusing to count loops would punish exactly
	// the content that works. What it must not be is unbounded — with no
	// ceiling, one person on one phone replaying their own video in a
	// loop mints unlimited paid views. Five is generous for a genuine
	// viewer and useless as a money printer.
	defaultViewCapPerViewerPerDay = 5
)

type DailyRollup struct {
	pg  *pgxpool.Pool
	rdb *redis.Client

	// viewCapPerViewerPerDay is read once, at construction. The rollup
	// runs one statement per day and the cap is a query parameter, so
	// re-reading the environment per query would buy nothing but a
	// chance for the number to change halfway through a settlement.
	viewCapPerViewerPerDay int
}

func NewDailyRollup(pg *pgxpool.Pool, rdb *redis.Client) *DailyRollup {
	return &DailyRollup{pg: pg, rdb: rdb, viewCapPerViewerPerDay: viewCapFromEnv()}
}

// WithViewCap overrides the per-viewer daily display-view cap that
// NewDailyRollup read from the environment. Exported so a test can pin
// the number it is asserting against, and so the cap can be disabled
// (any value <= 0) without an environment variable.
func (d *DailyRollup) WithViewCap(perViewerPerDay int) *DailyRollup {
	d.viewCapPerViewerPerDay = perViewerPerDay
	return d
}

// ViewCap reports the cap in force, for logging and for tests.
func (d *DailyRollup) ViewCap() int { return d.viewCapPerViewerPerDay }

func viewCapFromEnv() int {
	raw := strings.TrimSpace(os.Getenv(viewCapEnv))
	if raw == "" {
		return defaultViewCapPerViewerPerDay
	}
	parsed, err := strconv.Atoi(raw)
	if err != nil {
		log.Printf("[DailyRollup] %s=%q is not an integer; using default %d",
			viewCapEnv, raw, defaultViewCapPerViewerPerDay)
		return defaultViewCapPerViewerPerDay
	}
	return parsed
}

// Start runs daily at the top of every hour, checks if day boundary crossed.
func (d *DailyRollup) Start(ctx context.Context) {
	ticker := time.NewTicker(1 * time.Hour)
	defer ticker.Stop()

	log.Printf("[DailyRollup] started (display-view cap %d per viewer per day)", d.viewCapPerViewerPerDay)

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
	d.RollupDay(ctx, yesterday)
}

// RollupDay rebuilds analytics.content_daily_summary for one UTC day.
// Exported so a specific day can be rebuilt on demand — after an outage,
// after a bad hour was corrected, or (the case that forced it out of an
// unexported method) so that money owed for a day can be measured
// without waiting for the next midnight tick. The INSERT ... ON CONFLICT
// DO UPDATE means running it repeatedly converges on the same numbers
// rather than accumulating them.
//
// Every column but two is still the sum (or average) of the hourly rows.
// The two exceptions are views_display and view_score_total, which are
// what a creator is paid on, and which are recomputed here from
// analytics.events_raw with a per-viewer daily cap applied. They cannot
// come from the hourly table: the cap is per UTC day, and an hourly
// aggregate can only ever enforce a per-hour ceiling — 5/hour is 120/day,
// which is not the rule. So the hourly table keeps its uncapped counts
// (it is the real-time surface, not the money surface) and the day is
// derived from the events directly.
func (d *DailyRollup) RollupDay(ctx context.Context, day time.Time) error {
	day = day.UTC().Truncate(24 * time.Hour)
	dayEnd := day.AddDate(0, 0, 1)

	log.Printf("[DailyRollup] rolling up day: %s (view cap %d)",
		day.Format("2006-01-02"), d.viewCapPerViewerPerDay)

	_, err := d.pg.Exec(ctx, dailyRollupSQL, day, day, dayEnd, int64(d.viewCapPerViewerPerDay))
	if err != nil {
		log.Printf("[DailyRollup] rollup error: %v", err)
		return err
	}

	// Refresh CQS cache in Redis for all content with activity
	d.refreshCQSCache(ctx, day, dayEnd)
	return nil
}

// dailyRollupSQL folds one UTC day into analytics.content_daily_summary.
//
//	$1 the day, as a date (the day_bucket key)
//	$2 the start of the day    $3 the start of the next day
//	$4 display views one viewer may contribute to one content item today;
//	   <= 0 means no cap, and then these numbers are exactly the uncapped
//	   ones the hourly table already carries.
//
// A note on NULL user_id, because the window function partitions by it.
// analytics.events_raw.user_id is nullable in the schema, but no writer
// in this service can produce a play_end without one: play_end rows come
// only from IngestService.IngestEvents, which rejects the whole batch
// unless the gateway actor parses as a non-nil UUID, and the two Kafka
// consumers that write events_raw directly only ever write engagement
// types (like / comment_create / reel_share / reel_save). So there is no
// signed-out play_end to decide about, and nothing is added for one.
// Were that to change, note that PARTITION BY treats all NULLs as one
// group, which would cap an entire anonymous audience at $4 views a day
// for a video — far worse than the abuse being fixed. Whoever starts
// ingesting anonymous playback must give the anonymous rows a per-viewer
// key (the session, or the event id) before this ships against them.
const dailyRollupSQL = `
	WITH hourly AS (
		SELECT
			content_id, creator_id, content_type,
			SUM(impressions)          AS impressions,
			SUM(plays)                AS plays,
			SUM(unique_viewers)       AS unique_viewers,
			SUM(watch_time_total_ms)  AS watch_time_total_ms,
			AVG(avg_percent_viewed)   AS avg_percent_viewed,
			AVG(completion_rate)      AS completion_rate,
			SUM(likes)                AS likes,
			SUM(comments)             AS comments,
			SUM(shares)               AS shares,
			SUM(saves)                AS saves,
			AVG(content_quality_score) AS content_quality_score
		FROM analytics.content_hourly_agg
		WHERE hour_bucket >= $2 AND hour_bucket < $3
		GROUP BY content_id, creator_id, content_type
	),
	-- Every qualifying display view of the day, one row each. The
	-- predicates mirror hourlyScanSQL exactly (including the
	-- creator_id IS NOT NULL that the hourly scan filters on), so with
	-- the cap disabled this reproduces SUM(hourly.views_display).
	display_views AS (
		SELECT
			payload->>'content_id' AS content_id,
			user_id,
			LEAST(COALESCE((payload->>'percent_viewed')::double precision, 0), 100.0) / 100.0
				AS view_score
		FROM analytics.events_raw
		WHERE ts >= $2 AND ts < $3
		  AND type = 'play_end'
		  AND COALESCE((payload->>'is_display_view')::boolean, false)
		  AND payload->>'content_id' IS NOT NULL
		  AND payload->>'creator_id' IS NOT NULL
	),
	-- Rank each viewer's sessions on one content item by how much of the
	-- video they actually watched, best first. The cap then keeps the
	-- highest-scoring $4 rather than the first $4 in time: a viewer whose
	-- best watch happened to be their third should not be paid less for
	-- the order their sessions arrived in, and "first N" would also make
	-- the day's number depend on tie-breaking inside events_raw.
	ranked AS (
		SELECT
			content_id,
			view_score,
			ROW_NUMBER() OVER (
				PARTITION BY content_id, user_id
				ORDER BY view_score DESC
			) AS rank_for_viewer
		FROM display_views
	),
	capped AS (
		SELECT
			content_id,
			COUNT(*) FILTER (
				WHERE $4::bigint <= 0 OR rank_for_viewer <= $4::bigint) AS views_display,
			COALESCE(SUM(view_score) FILTER (
				WHERE $4::bigint <= 0 OR rank_for_viewer <= $4::bigint), 0) AS view_score_total
		FROM ranked
		GROUP BY content_id
	)
	INSERT INTO analytics.content_daily_summary (
		content_id, day_bucket, creator_id, content_type,
		impressions, plays, views_display, unique_viewers, watch_time_total_ms,
		avg_percent_viewed, completion_rate,
		likes, comments, shares, saves,
		view_score_total, content_quality_score
	)
	SELECT
		h.content_id, $1::date, h.creator_id, h.content_type,
		h.impressions, h.plays,
		COALESCE(c.views_display, 0), h.unique_viewers, h.watch_time_total_ms,
		h.avg_percent_viewed, h.completion_rate,
		h.likes, h.comments, h.shares, h.saves,
		COALESCE(c.view_score_total, 0), h.content_quality_score
	FROM hourly h
	LEFT JOIN capped c ON c.content_id = h.content_id::text
	ON CONFLICT (content_id, day_bucket)
	DO UPDATE SET
		impressions = EXCLUDED.impressions, plays = EXCLUDED.plays,
		views_display = EXCLUDED.views_display, unique_viewers = EXCLUDED.unique_viewers,
		watch_time_total_ms = EXCLUDED.watch_time_total_ms,
		avg_percent_viewed = EXCLUDED.avg_percent_viewed, completion_rate = EXCLUDED.completion_rate,
		likes = EXCLUDED.likes, comments = EXCLUDED.comments, shares = EXCLUDED.shares, saves = EXCLUDED.saves,
		view_score_total = EXCLUDED.view_score_total, content_quality_score = EXCLUDED.content_quality_score,
		updated_at = NOW()`

func (d *DailyRollup) refreshCQSCache(ctx context.Context, dayStart, dayEnd time.Time) {
	// The cache is an accelerator, not a source of truth, so a rollup
	// run without Redis — an operator rebuild, a test — does the durable
	// work and skips the warm-up rather than panicking on a nil client.
	if d.rdb == nil {
		return
	}
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
