package aggregation

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
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
	// One. A viewer who loops a flick all afternoon is a real viewer and
	// their loops are real sessions — replay analytics keep every one of
	// them, in watch time and in the hourly table — but one person earns
	// the creator one view a day for one video, whichever of their
	// sessions was the best. Five was already a ceiling on the money
	// printer; one is the decision (plan, 11 September 2026).
	defaultViewCapPerViewerPerDay = 1

	// rollupInterval is how often the catch-up walk runs. Every pass
	// re-rolls every open day from the watermark to yesterday, and each
	// day is a delete-then-insert inside one transaction, so a pass is
	// idempotent and a missed pass costs nothing but freshness.
	rollupInterval = 15 * time.Minute

	// freezeWindow is how long after a UTC day ends it may still be
	// rewritten. Late events, late finalisations and corrections land
	// inside it; outside it the day is frozen and only the forced
	// operator path touches it. Money settles on frozen days.
	freezeWindow = 48 * time.Hour

	settingDailyRollupWatermark = "daily_rollup_watermark"
)

// ErrDayFrozen is returned by RollupDay for a day outside the
// reprocessing window. Nothing was written.
var ErrDayFrozen = errors.New("day is frozen: outside the 48h reprocessing window; use the forced path to rewrite it")

// IsFrozen reports whether the UTC day may no longer be rewritten at
// `now`: a day is frozen once freezeWindow has passed since it ended.
// Today, yesterday and the day before are open; anything older is not.
func IsFrozen(day, now time.Time) bool {
	dayEnd := day.UTC().Truncate(24*time.Hour).AddDate(0, 0, 1)
	return !dayEnd.Add(freezeWindow).After(now.UTC())
}

type DailyRollup struct {
	pg  *pgxpool.Pool
	rdb *redis.Client

	// viewCapPerViewerPerDay is read once, at construction. The rollup
	// runs one statement per day and the cap is a query parameter, so
	// re-reading the environment per query would buy nothing but a
	// chance for the number to change halfway through a settlement.
	viewCapPerViewerPerDay int

	// source decides, per day bucket, whether display views come from
	// play_end rows or from finalised playback sessions. nil is play_end.
	source ViewSourceResolver
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

// WithViewSource sets the per-bucket source resolver. Without it every
// day is rolled up from play_end rows, exactly as before sessions
// existed.
func (d *DailyRollup) WithViewSource(r ViewSourceResolver) *DailyRollup {
	d.source = r
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

// Start runs the catch-up walk on start and then every rollupInterval.
// There is no midnight gate any more: a restart at 00:30, or a pod down
// over midnight, used to mean the day was never written.
func (d *DailyRollup) Start(ctx context.Context) {
	ticker := time.NewTicker(rollupInterval)
	defer ticker.Stop()

	now := time.Now().UTC()
	watermark, err := d.Watermark(ctx)
	watermarkText := "unknown"
	if err != nil {
		log.Printf("[DailyRollup] watermark read error: %v", err)
	} else {
		watermarkText = watermark.Format("2006-01-02")
	}
	log.Printf("[DailyRollup] started (cap %d, source=%s, watermark=%s, window=%s)",
		d.viewCapPerViewerPerDay, resolveViewSource(d.source, now.Truncate(24*time.Hour)), watermarkText,
		"48h")

	d.runPending(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			d.runPending(ctx)
		}
	}
}

func (d *DailyRollup) runPending(ctx context.Context) {
	report, err := d.RollupPending(ctx)
	if err != nil {
		log.Printf("[DailyRollup] catch-up error: %v", err)
		return
	}
	if len(report.RolledUp) > 0 || len(report.Frozen) > 0 || len(report.Failed) > 0 {
		log.Printf("[DailyRollup] catch-up: rolled_up=%s frozen=%s failed=%d watermark=%s",
			formatDays(report.RolledUp), formatDays(report.Frozen), len(report.Failed),
			report.Watermark.Format("2006-01-02"))
	}
}

// RollupReport is what one catch-up pass did.
type RollupReport struct {
	// Watermark is the earliest day the next pass will start from.
	Watermark time.Time
	RolledUp  []time.Time
	Frozen    []time.Time
	Failed    map[time.Time]error
}

// RollupPending walks every day from the persisted watermark to
// yesterday. Open days are rolled up (again — late events and late
// finalisations land inside the window). A day that has aged past the
// window without a completed rollup is recorded as frozen with the
// reason, warned about, and skipped: the forced path is the only way to
// write it. The watermark then moves to the earliest day that is still
// open, so a frozen day is reported once, not every fifteen minutes.
func (d *DailyRollup) RollupPending(ctx context.Context) (RollupReport, error) {
	now := time.Now().UTC()
	yesterday := now.Truncate(24*time.Hour).AddDate(0, 0, -1)

	watermark, err := d.Watermark(ctx)
	if err != nil {
		return RollupReport{}, err
	}
	report := RollupReport{Watermark: watermark, Failed: map[time.Time]error{}}

	next := watermark
	earliestOpen := time.Time{}
	for day := watermark; !day.After(yesterday); day = day.AddDate(0, 0, 1) {
		if IsFrozen(day, now) {
			d.markFrozen(ctx, day)
			report.Frozen = append(report.Frozen, day)
			continue
		}
		if earliestOpen.IsZero() {
			earliestOpen = day
		}
		if err := d.rollupDay(ctx, day); err != nil {
			report.Failed[day] = err
			continue
		}
		report.RolledUp = append(report.RolledUp, day)
	}
	switch {
	case !earliestOpen.IsZero():
		next = earliestOpen
	case !watermark.After(yesterday):
		// Everything from the watermark to yesterday was frozen: the
		// next pass starts at today.
		next = yesterday.AddDate(0, 0, 1)
	}
	if !next.Equal(watermark) {
		if err := d.setWatermark(ctx, next); err != nil {
			return report, err
		}
	}
	report.Watermark = next
	return report, nil
}

// Watermark reads the persisted catch-up start. A missing row means the
// service is starting for the first time under these rules; it seeds
// today - 2 (the migration seeds the same) rather than walking history.
func (d *DailyRollup) Watermark(ctx context.Context) (time.Time, error) {
	var raw string
	err := d.pg.QueryRow(ctx, `
		SELECT value FROM analytics.aggregation_settings WHERE key = $1`,
		settingDailyRollupWatermark).Scan(&raw)
	if errors.Is(err, pgx.ErrNoRows) {
		seed := time.Now().UTC().Truncate(24*time.Hour).AddDate(0, 0, -2)
		return seed, d.setWatermark(ctx, seed)
	}
	if err != nil {
		return time.Time{}, fmt.Errorf("read rollup watermark: %w", err)
	}
	day, err := time.Parse("2006-01-02", strings.TrimSpace(raw))
	if err != nil {
		return time.Time{}, fmt.Errorf("rollup watermark %q is not a date: %w", raw, err)
	}
	return day.UTC(), nil
}

func (d *DailyRollup) setWatermark(ctx context.Context, day time.Time) error {
	_, err := d.pg.Exec(ctx, `
		INSERT INTO analytics.aggregation_settings (key, value, updated_at)
		VALUES ($1, $2, NOW())
		ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value, updated_at = NOW()`,
		settingDailyRollupWatermark, day.UTC().Format("2006-01-02"))
	if err != nil {
		return fmt.Errorf("write rollup watermark: %w", err)
	}
	return nil
}

// markFrozen records that a day has left the window. A day that never
// completed a rollup keeps the reason on its row and is warned about:
// its summary is whatever was there, and only an operator can change
// that.
func (d *DailyRollup) markFrozen(ctx context.Context, day time.Time) {
	var completed bool
	if err := d.pg.QueryRow(ctx, `
		INSERT INTO analytics.rollup_progress (day, status, last_run_at, error)
		VALUES ($1, 'frozen', NOW(), $2)
		ON CONFLICT (day) DO UPDATE SET
			status = 'frozen',
			error = CASE WHEN analytics.rollup_progress.completed_at IS NULL
			             THEN COALESCE(analytics.rollup_progress.error, EXCLUDED.error)
			             ELSE analytics.rollup_progress.error END,
			updated_at = NOW()
		RETURNING completed_at IS NOT NULL`,
		day, "aged past the 48h window before its first rollup; rewrite it with ?force=1",
	).Scan(&completed); err != nil {
		log.Printf("[DailyRollup] mark frozen %s error: %v", day.Format("2006-01-02"), err)
		return
	}
	if !completed {
		log.Printf("[DailyRollup] WARNING day %s is frozen and was never rolled up; its summary is whatever is there. Rewrite with POST /v1/analytics/internal/aggregate?day=%s&force=1",
			day.Format("2006-01-02"), day.Format("2006-01-02"))
	}
}

// RollupDay rebuilds analytics.content_daily_summary for one UTC day, or
// refuses with ErrDayFrozen if the day is outside the window. Exported
// so a specific day can be rebuilt on demand — after an outage, after a
// bad hour was corrected, or so that money owed for today can be
// measured without waiting for the timer.
func (d *DailyRollup) RollupDay(ctx context.Context, day time.Time) error {
	day = day.UTC().Truncate(24 * time.Hour)
	if IsFrozen(day, time.Now().UTC()) {
		return fmt.Errorf("%w: %s", ErrDayFrozen, day.Format("2006-01-02"))
	}
	return d.rollupDay(ctx, day)
}

// ForceRollupDay rewrites a day whether or not it is frozen. It is the
// only way to touch a frozen day, it is reachable only through the
// internal aggregate route with ?force=1, and it says so in the log
// because money that was settled on the old numbers may now disagree
// with the new ones; that correction goes through monetization's
// reversal path, not through here.
func (d *DailyRollup) ForceRollupDay(ctx context.Context, day time.Time) error {
	day = day.UTC().Truncate(24 * time.Hour)
	if IsFrozen(day, time.Now().UTC()) {
		log.Printf("[DailyRollup] FORCED REWRITE of frozen day %s: any settlement on the previous numbers must be reconciled through monetization's reversal path",
			day.Format("2006-01-02"))
	}
	return d.rollupDay(ctx, day)
}

// rollupDay is the rebuild itself: delete the day, insert it again from
// the hourly rows and the view source, and record progress — all in
// one transaction, so a day with no hourly rows ends with no rows, a
// hand-written row cannot survive, and a failure leaves the previous
// numbers in place rather than a half-written day.
//
// Every column but two is still the sum (or average) of the hourly
// rows. The two exceptions are views_display and view_score_total,
// which are what a creator is paid on, and which are recomputed here
// with the per-viewer daily cap applied. They cannot come from the
// hourly table: the cap is per UTC day, and an hourly aggregate can
// only ever enforce a per-hour ceiling. So the hourly table keeps its
// uncapped counts (it is the real-time surface, not the money surface)
// and the day is derived from the sessions — or, before the cutover,
// the play_end rows — directly.
func (d *DailyRollup) rollupDay(ctx context.Context, day time.Time) error {
	dayEnd := day.AddDate(0, 0, 1)
	source := resolveViewSource(d.source, day)

	log.Printf("[DailyRollup] rolling up day: %s (cap %d, source=%s)",
		day.Format("2006-01-02"), d.viewCapPerViewerPerDay, source)

	err := d.rebuildDayTx(ctx, day, dayEnd, source)
	if err != nil {
		log.Printf("[DailyRollup] rollup error for %s: %v", day.Format("2006-01-02"), err)
		if _, recordErr := d.pg.Exec(ctx, `
			INSERT INTO analytics.rollup_progress (day, status, last_run_at, error)
			VALUES ($1, 'open', NOW(), $2)
			ON CONFLICT (day) DO UPDATE SET last_run_at = NOW(), error = EXCLUDED.error, updated_at = NOW()`,
			day, err.Error()); recordErr != nil {
			log.Printf("[DailyRollup] progress record error for %s: %v", day.Format("2006-01-02"), recordErr)
		}
		return err
	}

	// Refresh CQS cache in Redis for all content with activity
	d.refreshCQSCache(ctx, day, dayEnd)
	return nil
}

func (d *DailyRollup) rebuildDayTx(ctx context.Context, day, dayEnd time.Time, source string) error {
	tx, err := d.pg.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	// One rollup of a day at a time, across instances and the operator
	// route. Blocking rather than try: an operator asking for a day the
	// timer is on should get the rebuilt day, not a silent skip. The
	// two-key form keeps this namespace apart from the hourly lock,
	// whose key is the bucket's unix time — a day start is also an
	// hour start.
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(2, $1)`, int32(day.Unix()/86400)); err != nil {
		return err
	}

	if _, err := tx.Exec(ctx, `DELETE FROM analytics.content_daily_summary WHERE day_bucket = $1`, day); err != nil {
		return fmt.Errorf("delete day: %w", err)
	}
	sql := dailyRollupSQL
	if source == ViewSourceSessions {
		sql = dailySessionRollupSQL
	}
	if _, err := tx.Exec(ctx, sql, day, day, dayEnd, int64(d.viewCapPerViewerPerDay)); err != nil {
		return fmt.Errorf("insert day: %w", err)
	}

	status := "open"
	if IsFrozen(day, time.Now().UTC()) {
		status = "frozen"
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO analytics.rollup_progress (day, status, last_run_at, completed_at, error)
		VALUES ($1, $2, NOW(), NOW(), NULL)
		ON CONFLICT (day) DO UPDATE SET
			status = EXCLUDED.status, last_run_at = NOW(), completed_at = NOW(), error = NULL, updated_at = NOW()`,
		day, status); err != nil {
		return fmt.Errorf("record progress: %w", err)
	}
	return tx.Commit(ctx)
}

// dailyRollupSQL folds one UTC day into analytics.content_daily_summary
// from play_end rows — the pre-cutover path, kept intact.
//
//	$1 the day, as a date (the day_bucket key)
//	$2 the start of the day    $3 the start of the next day
//	$4 display views one viewer may contribute to one content item today;
//	   <= 0 means no cap, and then these numbers are exactly the uncapped
//	   ones the hourly table already carries.
//
// The row is emitted only where the content is eligible on the day: an
// ownership row that is ineligible or deleted with an effective date on
// or before the day contributes nothing to the summary (its hourly rows
// stay, so dashboards still see it). Content with no ownership row —
// pre-projection legacy — is eligible, as it always was.
//
// A note on NULL user_id, because the window function partitions by it.
// analytics.events_raw.user_id is nullable in the schema, but no writer
// in this service can produce a play_end without one: play_end rows come
// only from IngestService.IngestEvents, which rejects the whole batch
// unless the gateway actor parses as a non-nil UUID, and the two Kafka
// consumers that write events_raw directly only ever write engagement
// types. Were that to change, note that PARTITION BY treats all NULLs as
// one group, which would cap an entire anonymous audience at $4 views a
// day for a video — far worse than the abuse being fixed.
var dailyRollupSQL = strings.NewReplacer(
	"{{play_end}}", nonSelfPlayEnd,
	"{{hourly}}", dailyHourlyCTE,
	"{{insert}}", dailyInsertSQL,
).Replace(`
	WITH {{hourly}},
	-- Every qualifying display view of the day, one row each. The
	-- predicates mirror hourlyScanSQL exactly (self-views excluded), so
	-- with the cap disabled this reproduces SUM(hourly.views_display).
	display_views AS (
		SELECT
			payload->>'content_id' AS content_id,
			user_id                AS viewer_id,
			LEAST(COALESCE((payload->>'percent_viewed')::double precision, 0), 100.0) / 100.0
				AS view_score,
			COALESCE((payload->>'watched_ms_total')::bigint, 0) AS watched_ms,
			ts                     AS finalized_at
		FROM analytics.events_raw
		WHERE ts >= $2 AND ts < $3
		  AND {{play_end}}
		  AND COALESCE((payload->>'is_display_view')::boolean, false)
		  AND payload->>'content_id' IS NOT NULL
		  AND payload->>'creator_id' IS NOT NULL
	),
	-- Rank each viewer's sessions on one content item, best first: most
	-- of the video watched, then the longest, then the earliest. The
	-- cap keeps the highest-ranked $4 rather than the first $4 in time.
	ranked AS (
		SELECT
			content_id,
			view_score,
			ROW_NUMBER() OVER (
				PARTITION BY content_id, viewer_id
				ORDER BY view_score DESC, watched_ms DESC, finalized_at ASC
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
	{{insert}}`)

// dailySessionRollupSQL is the post-cutover path: the same fold, with
// display views drawn from finalised, non-self playback sessions
// attributed to the day of their first_seen. The ranking is the plan's:
// view_score, then unique coverage, then watch time, then the earliest
// finalisation.
var dailySessionRollupSQL = strings.NewReplacer(
	"{{hourly}}", dailyHourlyCTE,
	"{{insert}}", dailyInsertSQL,
).Replace(`
	WITH {{hourly}},
	display_views AS (
		SELECT
			content_id::text AS content_id,
			actor_id         AS viewer_id,
			view_score, covered_ms, watched_ms, finalized_at
		FROM analytics.playback_sessions
		WHERE first_seen >= $2 AND first_seen < $3
		  AND finalized_at IS NOT NULL
		  AND is_display_view
		  AND NOT is_self_view
	),
	ranked AS (
		SELECT
			content_id,
			view_score,
			ROW_NUMBER() OVER (
				PARTITION BY content_id, viewer_id
				ORDER BY view_score DESC, covered_ms DESC, watched_ms DESC, finalized_at ASC
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
	{{insert}}`)

// dailyHourlyCTE sums the day's hourly rows per content item. Grouped
// on content_id alone: a reclassification mid-day gives a content item
// hourly rows under two content_type labels, and grouping on the label
// too would produce two summary rows for one primary key. The latest
// hour's label wins.
const dailyHourlyCTE = `
	hourly AS (
		SELECT
			content_id,
			MIN(creator_id::text)::uuid AS creator_id,
			(array_agg(content_type ORDER BY hour_bucket DESC))[1] AS content_type,
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
		GROUP BY content_id
	)`

// dailyInsertSQL writes the day. No ON CONFLICT: the day was deleted in
// the same transaction, so the insert is the whole day. The eligibility
// gate is the WHERE clause; see dailyRollupSQL.
const dailyInsertSQL = `
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
	LEFT JOIN analytics.content_ownership o ON o.content_id = h.content_id
	WHERE o.content_id IS NULL
	   OR o.eligibility_state = 'eligible'
	   OR (o.eligibility_effective_from IS NOT NULL AND o.eligibility_effective_from >= $3)`

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

func formatDays(days []time.Time) string {
	if len(days) == 0 {
		return "[]"
	}
	parts := make([]string, 0, len(days))
	for _, d := range days {
		parts = append(parts, d.Format("2006-01-02"))
	}
	return "[" + strings.Join(parts, " ") + "]"
}
