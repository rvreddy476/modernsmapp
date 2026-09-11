//go:build integration

package service

import (
	"context"
	"testing"
	"time"

	"github.com/atpost/monetization-service/internal/store/postgres"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// One flick view is worth 0.3 paise at the launch rate (300 paise per
// 1000 views). Before Phase 2B every day truncated on its own, so a
// creator with one view a day earned nothing, ever, no matter how many
// days it went on. With the carry, the sub-paise remainder rolls forward
// and the fourth day pays the first whole paise: 4 x 300,000 micro-paise
// = 1,200,000 = 1 paise with 200,000 micro-paise carried on.
//
// The test drives the accrual day by day in order, exactly as the nightly
// worker does, and reads the money back from the rows the settlement
// would sum.
func TestFlickSingleViewCarriesNotZero(t *testing.T) {
	dsn := requireTestDSN(t)
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)

	svc := New(postgres.New(pool), nil)
	first := time.Date(2025, 7, 10, 0, 0, 0, 0, time.UTC)
	seedRateAndBandFor(ctx, t, pool, "flick", 300, first, false)

	creator, content := uuid.New(), uuid.New()
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM creator_fund_earnings WHERE creator_id = $1`, creator)
		_, _ = pool.Exec(ctx, `DELETE FROM creator_fund_carry WHERE creator_id = $1`, creator)
		_, _ = pool.Exec(ctx, `DELETE FROM creator_fund_eligibility WHERE creator_id = $1`, creator)
		_, _ = pool.Exec(ctx, `DELETE FROM analytics.content_daily_summary WHERE creator_id = $1`, creator)
	})
	makeEligible(ctx, t, pool, creator)

	days := []time.Time{first, first.AddDate(0, 0, 1), first.AddDate(0, 0, 2), first.AddDate(0, 0, 3)}
	for _, day := range days {
		insertDailySummary(ctx, t, pool, content, creator, day, "flick", 1, 100, 0.3)
	}
	for _, day := range days {
		if _, err := svc.AccrueCreatorFundDay(ctx, creator, day); err != nil {
			t.Fatalf("accrue %s: %v", day.Format("2006-01-02"), err)
		}
	}

	grossOn := func(day time.Time) int64 {
		var gross int64
		if err := pool.QueryRow(ctx, `
			SELECT COALESCE(SUM(gross_paise), 0)::BIGINT FROM creator_fund_earnings
			WHERE creator_id = $1 AND day_bucket = $2 AND status = 'settled'`,
			creator, day).Scan(&gross); err != nil {
			t.Fatal(err)
		}
		return gross
	}
	for _, day := range days[:3] {
		if got := grossOn(day); got != 0 {
			t.Errorf("%s gross_paise = %d, want 0 (0.3 paise a day cannot pay a whole paise yet)", day.Format("2006-01-02"), got)
		}
	}
	if got := grossOn(days[3]); got != 1 {
		t.Fatalf("day 4 gross_paise = %d, want 1: four views at 0.3 paise each is 1.2 paise, and the first whole paise is due today", got)
	}

	// The remainder is carried, not lost: 1,200,000 - 1,000,000.
	var carry int64
	if err := pool.QueryRow(ctx, `
		SELECT carry_micro_paise FROM creator_fund_carry
		WHERE creator_id = $1 AND content_type = 'flick' AND region_code = 'IN'`, creator).Scan(&carry); err != nil {
		t.Fatalf("read carry: %v", err)
	}
	if carry != 200_000 {
		t.Fatalf("carry after day 4 = %d micro-paise, want 200000", carry)
	}
	// And the row records its own arithmetic so the paise can be re-derived.
	var grossMicro, carryIn, carryOut int64
	var multiplierBps int64
	if err := pool.QueryRow(ctx, `
		SELECT gross_micro_paise, carry_in_micro_paise, carry_out_micro_paise, quality_multiplier_bps
		FROM creator_fund_earnings WHERE creator_id = $1 AND day_bucket = $2`,
		creator, days[3]).Scan(&grossMicro, &carryIn, &carryOut, &multiplierBps); err != nil {
		t.Fatal(err)
	}
	if grossMicro != 300_000 || carryIn != 900_000 || carryOut != 200_000 {
		t.Errorf("day 4 micro arithmetic = gross %d + carry_in %d -> carry_out %d, want 300000 + 900000 -> 200000", grossMicro, carryIn, carryOut)
	}
	// The band seeded above is DISABLED. The frozen multiplier must be read
	// off the band as 1.0x, never assumed.
	if multiplierBps != 10_000 {
		t.Errorf("quality_multiplier_bps = %d, want 10000 from the disabled band", multiplierBps)
	}
}

// seedRateAndBandFor writes a rate and a quality band for one content type
// over a +/-7 day window around `day`, tagged as fixtures, and removes them
// on cleanup. `enabled=false` reproduces the production freeze: a disabled
// band that the accrual must read as 1.0x.
func seedRateAndBandFor(ctx context.Context, t *testing.T, pool *pgxpool.Pool, contentType string, rpmPaise int64, day time.Time, enabled bool) (rateID, bandID uuid.UUID) {
	t.Helper()
	from := day.AddDate(0, 0, -7)
	until := day.AddDate(0, 0, 7)
	rateID, bandID = uuid.New(), uuid.New()
	if _, err := pool.Exec(ctx, `
		INSERT INTO monetization_rpm_rates
			(id, content_type, region_code, rpm_paise, effective_from, effective_to, notes)
		VALUES ($1, $2, 'IN', $3, $4, $5, 'integration test window')`,
		rateID, contentType, rpmPaise, from, until); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO monetization_quality_bands
			(id, content_type, region_code, floor_bps, ceiling_bps, pivot_cqs,
			 confidence_impressions, enabled, effective_from, effective_to, notes)
		VALUES ($1, $2, 'IN', 8500, 12500, 0.35, 1000, $3, $4, $5, 'integration test window')`,
		bandID, contentType, enabled, from, until); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM monetization_rpm_rates WHERE id = $1`, rateID)
		_, _ = pool.Exec(ctx, `DELETE FROM monetization_quality_bands WHERE id = $1`, bandID)
	})
	return rateID, bandID
}

func makeEligible(ctx context.Context, t *testing.T, pool *pgxpool.Pool, creator uuid.UUID) {
	t.Helper()
	if _, err := pool.Exec(ctx, `
		INSERT INTO creator_fund_eligibility (creator_id, status, eligible_since)
		VALUES ($1, 'eligible', NOW())
		ON CONFLICT (creator_id) DO UPDATE SET status = 'eligible'`, creator); err != nil {
		t.Fatal(err)
	}
}

func insertDailySummary(ctx context.Context, t *testing.T, pool *pgxpool.Pool, content, creator uuid.UUID, day time.Time, contentType string, views, impressions int64, cqs float64) {
	t.Helper()
	if _, err := pool.Exec(ctx, `
		INSERT INTO analytics.content_daily_summary (
			content_id, day_bucket, creator_id, content_type,
			impressions, plays, views_display, unique_viewers,
			watch_time_total_ms, content_quality_score
		) VALUES ($1, $2, $3, $4, $5, $6, $6, $6, $7, $8)`,
		content, day, creator, contentType, impressions, views, views*30_000, cqs); err != nil {
		t.Fatal(err)
	}
}
