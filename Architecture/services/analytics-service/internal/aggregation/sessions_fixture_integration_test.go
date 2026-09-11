//go:build integration

package aggregation

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// fixedSource is a ViewSourceResolver that answers the same thing for
// every bucket — the two ends of the cutover, pinned.
type fixedSource string

func (s fixedSource) ViewSourceFor(time.Time) string { return string(s) }

// ensurePlaybackSessions makes analytics.playback_sessions available to
// a test. Migration 008 (Phase 1A, owned by the sessions stream) creates
// the real table; once it is in database/migrations this is a no-op,
// because testsupport bootstraps every migration. Until then the table
// is created here, in the package's private scratch database only, with
// exactly the column list Phase 1A specifies, so the session-source
// aggregation can be exercised before the writer lands. If 008 ships a
// different shape, the fixture inserts below fail loudly rather than
// the aggregation silently reading the wrong columns.
func ensurePlaybackSessions(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	ctx := context.Background()
	var exists bool
	if err := pool.QueryRow(ctx, `SELECT to_regclass('analytics.playback_sessions') IS NOT NULL`).Scan(&exists); err != nil {
		t.Fatal(err)
	}
	if exists {
		return
	}
	if _, err := pool.Exec(ctx, `
		CREATE TABLE analytics.playback_sessions (
			actor_id            UUID NOT NULL,
			session_id          UUID NOT NULL,
			content_id          UUID NOT NULL,
			creator_id          UUID NOT NULL,
			content_type        TEXT NOT NULL,
			first_seen          TIMESTAMPTZ NOT NULL,
			last_seen           TIMESTAMPTZ NOT NULL,
			content_duration_ms BIGINT NOT NULL DEFAULT 0,
			watched_ms          BIGINT NOT NULL DEFAULT 0,
			watched_ms_reported BIGINT NOT NULL DEFAULT 0,
			max_playhead_ms     BIGINT NOT NULL DEFAULT 0,
			max_continuous_ms   BIGINT NOT NULL DEFAULT 0,
			loop_count          INT NOT NULL DEFAULT 0,
			seek_count          INT NOT NULL DEFAULT 0,
			playback_speed      DOUBLE PRECISION NOT NULL DEFAULT 1,
			coverage            BYTEA,
			covered_ms          BIGINT NOT NULL DEFAULT 0,
			percent_viewed      DOUBLE PRECISION NOT NULL DEFAULT 0,
			percent_covered     DOUBLE PRECISION NOT NULL DEFAULT 0,
			is_self_view        BOOLEAN NOT NULL DEFAULT false,
			end_reason          TEXT,
			finalized_at        TIMESTAMPTZ,
			finalize_reason     TEXT CHECK (finalize_reason IN ('play_end','inactivity','superseded')),
			is_display_view     BOOLEAN NOT NULL DEFAULT false,
			view_score          DOUBLE PRECISION NOT NULL DEFAULT 0,
			source              TEXT NOT NULL DEFAULT 'live' CHECK (source IN ('live','backfill')),
			PRIMARY KEY (actor_id, session_id, content_id)
		)`); err != nil {
		t.Fatal(err)
	}
}

// sessionFixture is one finalised playback session, in the fields the
// aggregators read.
type sessionFixture struct {
	Actor, Content, Creator uuid.UUID
	ContentType             string
	FirstSeen               time.Time
	DurationMS, WatchedMS   int64
	CoveredMS               int64
	PercentViewed           float64
	PercentCovered          float64
	LoopCount               int
	EndReason               string
	IsSelfView              bool
	IsDisplayView           bool
	ViewScore               float64
	FinalizedAt             time.Time
	Open                    bool // not yet finalised
}

func insertSession(t *testing.T, pool *pgxpool.Pool, f sessionFixture) {
	t.Helper()
	var finalizedAt *time.Time
	var reason *string
	if !f.Open {
		at := f.FinalizedAt
		if at.IsZero() {
			at = f.FirstSeen.Add(time.Minute)
		}
		finalizedAt = &at
		r := "play_end"
		reason = &r
	}
	if f.EndReason == "" {
		f.EndReason = "ended"
	}
	if _, err := pool.Exec(context.Background(), `
		INSERT INTO analytics.playback_sessions (
			actor_id, session_id, content_id, creator_id, content_type,
			first_seen, last_seen, content_duration_ms,
			watched_ms, watched_ms_reported, max_playhead_ms, max_continuous_ms,
			loop_count, seek_count, playback_speed, covered_ms,
			percent_viewed, percent_covered, is_self_view, end_reason,
			finalized_at, finalize_reason, is_display_view, view_score, source
		) VALUES (
			$1, $2, $3, $4, $5,
			$6, $7, $8,
			$9, $9, $9, $9,
			$10, 0, 1, $11,
			$12, $13, $14, $15,
			$16, $17, $18, $19, 'live')`,
		f.Actor, uuid.New(), f.Content, f.Creator, f.ContentType,
		f.FirstSeen, f.FirstSeen.Add(30*time.Second), f.DurationMS,
		f.WatchedMS,
		f.LoopCount, f.CoveredMS,
		f.PercentViewed, f.PercentCovered, f.IsSelfView, f.EndReason,
		finalizedAt, reason, f.IsDisplayView, f.ViewScore,
	); err != nil {
		t.Fatal(err)
	}
}

// setWatermark pins the daily rollup watermark for a test.
func setWatermark(t *testing.T, pool *pgxpool.Pool, day time.Time) {
	t.Helper()
	if _, err := pool.Exec(context.Background(), `
		INSERT INTO analytics.aggregation_settings (key, value)
		VALUES ($1, $2)
		ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value, updated_at = NOW()`,
		settingDailyRollupWatermark, day.UTC().Format("2006-01-02")); err != nil {
		t.Fatal(err)
	}
}

// insertHourlyRow writes a minimal content_hourly_agg row so the daily
// rollup has something to fold. The rollup only emits content that has
// hourly rows, whatever the session or event tables say.
func insertHourlyRow(t *testing.T, pool *pgxpool.Pool, content, creator uuid.UUID, contentType string, hour time.Time, impressions int64) {
	t.Helper()
	if _, err := pool.Exec(context.Background(), `
		INSERT INTO analytics.content_hourly_agg
			(content_id, hour_bucket, creator_id, content_type, impressions, plays)
		VALUES ($1, $2, $3, $4, $5, 1)
		ON CONFLICT (content_id, hour_bucket) DO UPDATE SET impressions = EXCLUDED.impressions`,
		content, hour, creator, contentType, impressions); err != nil {
		t.Fatal(err)
	}
}

func insertSummaryRow(t *testing.T, pool *pgxpool.Pool, content, creator uuid.UUID, day time.Time, views int64) {
	t.Helper()
	if _, err := pool.Exec(context.Background(), `
		INSERT INTO analytics.content_daily_summary
			(content_id, day_bucket, creator_id, content_type, views_display, view_score_total)
		VALUES ($1, $2, $3, 'flick', $4, $5)`,
		content, day, creator, views, float64(views)); err != nil {
		t.Fatal(err)
	}
}

func summaryViews(t *testing.T, pool *pgxpool.Pool, content uuid.UUID, day time.Time) (rows, views int64, score float64, watch int64) {
	t.Helper()
	if err := pool.QueryRow(context.Background(), `
		SELECT count(*), COALESCE(SUM(views_display),0), COALESCE(SUM(view_score_total),0),
		       COALESCE(SUM(watch_time_total_ms),0)
		FROM analytics.content_daily_summary
		WHERE content_id = $1 AND day_bucket = $2`, content, day).Scan(&rows, &views, &score, &watch); err != nil {
		t.Fatal(err)
	}
	return
}

func truncateAggregation(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	if _, err := pool.Exec(context.Background(), `TRUNCATE analytics.content_hourly_agg, analytics.content_daily_summary,
		analytics.ingest_receipts, analytics.events_raw, analytics.content_ownership,
		analytics.rollup_progress, analytics.playback_sessions CASCADE`); err != nil {
		t.Fatal(err)
	}
}

// dayAgo returns the UTC day n days before today, truncated.
func dayAgo(n int) time.Time {
	return time.Now().UTC().AddDate(0, 0, -n).Truncate(24 * time.Hour)
}
