//go:build integration

package aggregation

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/atpost/analytics-service/internal/testsupport"
	"github.com/google/uuid"
)

// The rollup used to run only if an hourly timer happened to fire in the
// midnight hour, and only for yesterday. A restart at 00:30, or a pod
// down over midnight, and the day was never written. Now the rollup
// walks every day from a persisted watermark to yesterday, on start and
// every fifteen minutes, so a missed day is repaired on the next pass.
func TestRollupCatchesUpMissedDays(t *testing.T) {
	ctx := context.Background()
	pool := testsupport.Pool(t, "aggregation")
	ensurePlaybackSessions(t, pool)
	truncateAggregation(t, pool)

	creator := uuid.New()
	contentX, contentY := uuid.New(), uuid.New()
	twoDaysAgo, yesterday := dayAgo(2), dayAgo(1)

	// Two days of hourly rows and no daily rows at all: the outage.
	insertHourlyRow(t, pool, contentX, creator, "flick", twoDaysAgo.Add(3*time.Hour), 10)
	insertHourlyRow(t, pool, contentY, creator, "flick", yesterday.Add(20*time.Hour), 7)
	setWatermark(t, pool, twoDaysAgo)

	report, err := NewDailyRollup(pool, nil).RollupPending(ctx)
	if err != nil {
		t.Fatalf("RollupPending: %v", err)
	}
	if len(report.RolledUp) != 2 {
		t.Fatalf("rolled up %v, want both %s and %s", report.RolledUp, twoDaysAgo.Format("2006-01-02"), yesterday.Format("2006-01-02"))
	}
	for _, c := range []struct {
		content uuid.UUID
		day     time.Time
		want    int64
	}{{contentX, twoDaysAgo, 10}, {contentY, yesterday, 7}} {
		var impressions int64
		if err := pool.QueryRow(ctx, `
			SELECT impressions FROM analytics.content_daily_summary
			WHERE content_id = $1 AND day_bucket = $2`, c.content, c.day).Scan(&impressions); err != nil {
			t.Fatalf("day %s was not rolled up: %v", c.day.Format("2006-01-02"), err)
		}
		if impressions != c.want {
			t.Fatalf("day %s impressions = %d, want %d", c.day.Format("2006-01-02"), impressions, c.want)
		}
	}

	// Each day has a completed progress row, and the watermark has not
	// moved past the earliest day that is still inside the window —
	// those days are re-rolled every pass so late events land.
	for _, day := range []time.Time{twoDaysAgo, yesterday} {
		var status string
		var completed *time.Time
		if err := pool.QueryRow(ctx, `
			SELECT status, completed_at FROM analytics.rollup_progress WHERE day = $1`, day).Scan(&status, &completed); err != nil {
			t.Fatalf("no progress row for %s: %v", day.Format("2006-01-02"), err)
		}
		if status != "open" || completed == nil {
			t.Fatalf("progress for %s = %s/%v, want open and completed", day.Format("2006-01-02"), status, completed)
		}
	}
	watermark, err := NewDailyRollup(pool, nil).Watermark(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !watermark.Equal(twoDaysAgo) {
		t.Fatalf("watermark = %s, want %s (the earliest open day)", watermark.Format("2006-01-02"), twoDaysAgo.Format("2006-01-02"))
	}

	// A second pass is a no-op that converges: same rows, same numbers.
	if _, err := NewDailyRollup(pool, nil).RollupPending(ctx); err != nil {
		t.Fatal(err)
	}
	var rows int64
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM analytics.content_daily_summary`).Scan(&rows); err != nil {
		t.Fatal(err)
	}
	if rows != 2 {
		t.Fatalf("summary rows after a second pass = %d, want 2", rows)
	}
}

// A day with no hourly rows for a content item ends with no daily row
// for it. The old INSERT ... ON CONFLICT DO UPDATE could only ever add
// or overwrite, so a hand-written summary row — or one left behind by a
// fixture — survived every rollup and was settled as money.
func TestRollupDayRemovesOrphanSummaryRow(t *testing.T) {
	ctx := context.Background()
	pool := testsupport.Pool(t, "aggregation")
	ensurePlaybackSessions(t, pool)
	truncateAggregation(t, pool)

	creator := uuid.New()
	real, orphan := uuid.New(), uuid.New()
	day := dayAgo(1)

	insertHourlyRow(t, pool, real, creator, "flick", day.Add(2*time.Hour), 5)
	insertSummaryRow(t, pool, orphan, creator, day, 4_448)

	if err := NewDailyRollup(pool, nil).RollupDay(ctx, day); err != nil {
		t.Fatal(err)
	}
	if rows, _, _, _ := summaryViews(t, pool, orphan, day); rows != 0 {
		t.Fatalf("orphan summary row survived the rollup (%d rows)", rows)
	}
	if rows, _, _, _ := summaryViews(t, pool, real, day); rows != 1 {
		t.Fatalf("content with hourly rows has %d summary rows, want 1", rows)
	}
}

// Days older than the 48-hour window are frozen: the rollup refuses to
// rewrite them, so a settled day cannot change under a new rule or a
// late event. Only the forced path touches one, and it says so.
func TestFrozenDayIsNotRewritten(t *testing.T) {
	ctx := context.Background()
	pool := testsupport.Pool(t, "aggregation")
	ensurePlaybackSessions(t, pool)
	truncateAggregation(t, pool)

	creator, content := uuid.New(), uuid.New()
	frozen := dayAgo(3)
	insertSummaryRow(t, pool, content, creator, frozen, 4_448)

	now := time.Now().UTC()
	if !IsFrozen(frozen, now) {
		t.Fatalf("%s must be frozen at %s", frozen.Format("2006-01-02"), now.Format(time.RFC3339))
	}
	if IsFrozen(dayAgo(2), now) || IsFrozen(dayAgo(1), now) || IsFrozen(dayAgo(0), now) {
		t.Fatal("the last two closed days and today must be open")
	}

	rollup := NewDailyRollup(pool, nil)
	err := rollup.RollupDay(ctx, frozen)
	if !errors.Is(err, ErrDayFrozen) {
		t.Fatalf("RollupDay on a frozen day returned %v, want ErrDayFrozen", err)
	}
	if rows, views, _, _ := summaryViews(t, pool, content, frozen); rows != 1 || views != 4_448 {
		t.Fatalf("frozen day was rewritten: rows=%d views=%d", rows, views)
	}

	// The catch-up walk skips it too, records that it did, and moves the
	// watermark on so the same day is not reported forever.
	setWatermark(t, pool, frozen)
	report, err := rollup.RollupPending(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Frozen) != 1 || !report.Frozen[0].Equal(frozen) {
		t.Fatalf("report.Frozen = %v, want [%s]", report.Frozen, frozen.Format("2006-01-02"))
	}
	if rows, views, _, _ := summaryViews(t, pool, content, frozen); rows != 1 || views != 4_448 {
		t.Fatalf("catch-up rewrote a frozen day: rows=%d views=%d", rows, views)
	}
	var status string
	if err := pool.QueryRow(ctx, `SELECT status FROM analytics.rollup_progress WHERE day = $1`, frozen).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != "frozen" {
		t.Fatalf("progress status = %q, want frozen", status)
	}
	watermark, err := rollup.Watermark(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !watermark.After(frozen) {
		t.Fatalf("watermark = %s, want it moved past the frozen day", watermark.Format("2006-01-02"))
	}

	// Force is the one way through, and it is a real rebuild: with no
	// hourly rows the hand-written row is gone afterwards.
	if err := rollup.ForceRollupDay(ctx, frozen); err != nil {
		t.Fatalf("ForceRollupDay: %v", err)
	}
	if rows, _, _, _ := summaryViews(t, pool, content, frozen); rows != 0 {
		t.Fatalf("forced rebuild left %d rows for a day with no hourly rows", rows)
	}
}

// Making a video private, taking it down or deleting it stops it
// accruing from the effective date. Days before it are untouched — what
// was earned while it was public stands — and the summary simply has no
// row for the content on and after the day the change took effect.
func TestIneligibleContentStopsAccruingFromEffectiveDate(t *testing.T) {
	ctx := context.Background()
	pool := testsupport.Pool(t, "aggregation")
	ensurePlaybackSessions(t, pool)
	truncateAggregation(t, pool)

	creator := uuid.New()
	private, public, deleted := uuid.New(), uuid.New(), uuid.New()
	twoDaysAgo, yesterday := dayAgo(2), dayAgo(1)

	for _, content := range []uuid.UUID{private, public, deleted} {
		insertHourlyRow(t, pool, content, creator, "flick", twoDaysAgo.Add(4*time.Hour), 3)
		insertHourlyRow(t, pool, content, creator, "flick", yesterday.Add(4*time.Hour), 3)
	}
	// Ownership rows: one made private at 15:00 yesterday, one still
	// public, one deleted with no revision (the old-producer case).
	if _, err := pool.Exec(ctx, `
		INSERT INTO analytics.content_ownership
			(content_id, creator_id, content_type, created_at, eligibility_state, eligibility_effective_from, eligibility_rev)
		VALUES
			($1, $4, 'flick', $5, 'ineligible', $6, 3),
			($2, $4, 'flick', $5, 'eligible', $5, 3),
			($3, $4, 'flick', $5, 'deleted', $6, 0)`,
		private, public, deleted, creator, twoDaysAgo.Add(-24*time.Hour), yesterday.Add(15*time.Hour)); err != nil {
		t.Fatal(err)
	}

	rollup := NewDailyRollup(pool, nil)
	for _, day := range []time.Time{twoDaysAgo, yesterday} {
		if err := rollup.RollupDay(ctx, day); err != nil {
			t.Fatal(err)
		}
	}

	for _, c := range []struct {
		name    string
		content uuid.UUID
		day     time.Time
		want    int64
	}{
		{"private content, day before the change", private, twoDaysAgo, 1},
		{"private content, the day of the change", private, yesterday, 0},
		{"public content, day before", public, twoDaysAgo, 1},
		{"public content, yesterday", public, yesterday, 1},
		{"deleted content, day before", deleted, twoDaysAgo, 1},
		{"deleted content, the day of the delete", deleted, yesterday, 0},
	} {
		if rows, _, _, _ := summaryViews(t, pool, c.content, c.day); rows != c.want {
			t.Errorf("%s: %d summary rows, want %d", c.name, rows, c.want)
		}
	}
}
