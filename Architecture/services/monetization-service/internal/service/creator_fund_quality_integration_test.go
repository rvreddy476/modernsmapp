//go:build integration

package service

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/atpost/monetization-service/internal/store/postgres"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Settles three creators who earned the same number of views on the same
// day, differing only in the quality of what those views watched, and
// asserts the money actually differs — end to end, through the real
// analytics tables, the real rate sheet and the real quality band.
func TestLiveSettlementPaysDifferentlyForDifferentQuality(t *testing.T) {
	dsn := os.Getenv("MONETIZATION_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("MONETIZATION_POSTGRES_DSN is required")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()

	store := postgres.New(pool)
	svc := New(store, nil)
	day := time.Date(2026, 1, 15, 0, 0, 0, 0, time.UTC)
	seedHistoricalRateAndBand(ctx, t, pool, day)

	// Same views, same watch time, three different quality scores. The
	// impression count is well past the confidence threshold so the
	// shrink is not what is being measured here.
	cases := []struct {
		name string
		cqs  float64
	}{
		{"floor-scoring content", 0.0},
		{"pivot-scoring content", 0.35},
		{"ceiling-scoring content", 1.0},
	}

	type outcome struct {
		name        string
		earning     postgres.CreatorFundEarning
		explanation string
	}
	var results []outcome

	for _, tc := range cases {
		creator := uuid.New()
		content := uuid.New()
		t.Cleanup(func() {
			_, _ = pool.Exec(ctx, `DELETE FROM creator_fund_earnings WHERE creator_id = $1`, creator)
			_, _ = pool.Exec(ctx, `DELETE FROM creator_fund_eligibility WHERE creator_id = $1`, creator)
			_, _ = pool.Exec(ctx, `DELETE FROM analytics.content_daily_summary WHERE creator_id = $1`, creator)
			_, _ = pool.Exec(ctx, `DELETE FROM transactions WHERE wallet_id = $1`, creator)
			_, _ = pool.Exec(ctx, `DELETE FROM ledger_entries WHERE credit_owner_id = $1 OR debit_owner_id = $1`, creator)
			_, _ = pool.Exec(ctx, `DELETE FROM creator_ledger WHERE user_id = $1`, creator)
		})

		if _, err := pool.Exec(ctx, `
			INSERT INTO analytics.content_daily_summary (
				content_id, day_bucket, creator_id, content_type,
				impressions, plays, views_display, unique_viewers,
				watch_time_total_ms, avg_percent_viewed, completion_rate,
				likes, comments, shares, saves,
				view_score_total, content_quality_score
			) VALUES ($1, $2, $3, 'long_video', 50000, 12000, 10000, 9000,
			          3600000000, 55, 0.4, 400, 90, 200, 150, 6000, $4)
			ON CONFLICT (content_id, day_bucket) DO UPDATE
			SET content_quality_score = EXCLUDED.content_quality_score`,
			content, day, creator, tc.cqs); err != nil {
			t.Fatal(err)
		}

		if _, err := pool.Exec(ctx, `
			INSERT INTO creator_fund_eligibility (creator_id, status, eligible_since)
			VALUES ($1, 'eligible', NOW())
			ON CONFLICT (creator_id) DO UPDATE SET status = 'eligible'`, creator); err != nil {
			t.Fatal(err)
		}

		credited, err := svc.SettleCreatorFundDay(ctx, creator, day)
		if err != nil {
			t.Fatalf("%s: settle failed: %v", tc.name, err)
		}
		if credited != 1 {
			t.Fatalf("%s: credited %d rows, want 1", tc.name, credited)
		}

		var e postgres.CreatorFundEarning
		if err := pool.QueryRow(ctx, `
			SELECT view_count, rpm_paise, base_gross_paise, gross_paise,
			       platform_fee_paise, net_paise, quality_cqs,
			       quality_effective_cqs, quality_impressions, quality_multiplier_bps
			FROM creator_fund_earnings WHERE creator_id = $1 AND day_bucket = $2`,
			creator, day).Scan(&e.ViewCount, &e.RpmPaise, &e.BaseGrossPaise, &e.GrossPaise,
			&e.PlatformFeePaise, &e.NetPaise, &e.QualityCQS, &e.QualityEffectiveCQS,
			&e.QualityImpressions, &e.QualityMultiplierBps); err != nil {
			t.Fatal(err)
		}

		// Every settled row must add up, whatever the multiplier did.
		if e.PlatformFeePaise+e.NetPaise != e.GrossPaise {
			t.Errorf("%s: net %d + fee %d != gross %d", tc.name, e.NetPaise, e.PlatformFeePaise, e.GrossPaise)
		}
		wantGross := ApplyQualityMultiplier(e.BaseGrossPaise, e.QualityMultiplierBps)
		if e.GrossPaise != wantGross {
			t.Errorf("%s: gross %d, want base %d x %d bps = %d",
				tc.name, e.GrossPaise, e.BaseGrossPaise, e.QualityMultiplierBps, wantGross)
		}
		if e.QualityImpressions != 50000 {
			t.Errorf("%s: impressions %d did not reach the payout", tc.name, e.QualityImpressions)
		}

		summary, err := svc.GetCreatorFundEarnings(ctx, creator, 400)
		if err != nil {
			t.Fatal(err)
		}
		explanation := ""
		if len(summary.Breakdown) > 0 {
			explanation = summary.Breakdown[0].Explanation
		}
		if explanation == "" {
			t.Errorf("%s: the earnings endpoint returned no explanation", tc.name)
		}
		results = append(results, outcome{name: tc.name, earning: e, explanation: explanation})

		// Re-settling the same day must not pay twice.
		again, err := svc.SettleCreatorFundDay(ctx, creator, day)
		if err != nil || again != 0 {
			t.Errorf("%s: re-settlement credited %d rows (err=%v), want 0", tc.name, again, err)
		}
	}

	if len(results) != 3 {
		t.Fatalf("expected 3 settlements, got %d", len(results))
	}
	floor, pivot, ceiling := results[0].earning, results[1].earning, results[2].earning

	// The base amount is identical — only quality moved the payment.
	if floor.BaseGrossPaise != pivot.BaseGrossPaise || pivot.BaseGrossPaise != ceiling.BaseGrossPaise {
		t.Fatalf("base gross differed across cases: %d / %d / %d",
			floor.BaseGrossPaise, pivot.BaseGrossPaise, ceiling.BaseGrossPaise)
	}
	if !(floor.NetPaise < pivot.NetPaise && pivot.NetPaise < ceiling.NetPaise) {
		t.Fatalf("payouts are not ordered by quality: %d / %d / %d",
			floor.NetPaise, pivot.NetPaise, ceiling.NetPaise)
	}
	// The neutral case must pay exactly the pre-quality amount.
	if pivot.GrossPaise != pivot.BaseGrossPaise {
		t.Fatalf("pivot-scoring content was not paid the plain amount: %d vs %d",
			pivot.GrossPaise, pivot.BaseGrossPaise)
	}
	// And nobody is ever paid nothing for real views.
	if floor.NetPaise <= 0 {
		t.Fatalf("floor-scoring creator was paid %d for 10000 genuine views", floor.NetPaise)
	}

	for _, r := range results {
		fmt.Printf("\n%s\n  cqs=%.2f effective=%.4f multiplier=%d bps base=%d gross=%d net=%d\n  %s\n",
			r.name, r.earning.QualityCQS, r.earning.QualityEffectiveCQS,
			r.earning.QualityMultiplierBps, r.earning.BaseGrossPaise,
			r.earning.GrossPaise, r.earning.NetPaise, r.explanation)
	}
}

// A creator whose day recorded views but no impressions must be paid the
// plain amount, not zero and not a crash.
func TestLiveSettlementOfAZeroImpressionDayPaysTheNeutralAmount(t *testing.T) {
	dsn := os.Getenv("MONETIZATION_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("MONETIZATION_POSTGRES_DSN is required")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()

	svc := New(postgres.New(pool), nil)
	day := time.Date(2026, 1, 16, 0, 0, 0, 0, time.UTC)
	seedHistoricalRateAndBand(ctx, t, pool, day)
	creator, content := uuid.New(), uuid.New()
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM creator_fund_earnings WHERE creator_id = $1`, creator)
		_, _ = pool.Exec(ctx, `DELETE FROM creator_fund_eligibility WHERE creator_id = $1`, creator)
		_, _ = pool.Exec(ctx, `DELETE FROM analytics.content_daily_summary WHERE creator_id = $1`, creator)
		_, _ = pool.Exec(ctx, `DELETE FROM transactions WHERE wallet_id = $1`, creator)
		_, _ = pool.Exec(ctx, `DELETE FROM ledger_entries WHERE credit_owner_id = $1 OR debit_owner_id = $1`, creator)
		_, _ = pool.Exec(ctx, `DELETE FROM creator_ledger WHERE user_id = $1`, creator)
	})

	if _, err := pool.Exec(ctx, `
		INSERT INTO analytics.content_daily_summary (
			content_id, day_bucket, creator_id, content_type,
			impressions, views_display, watch_time_total_ms, content_quality_score
		) VALUES ($1, $2, $3, 'long_video', 0, 5000, 900000000, 0)`,
		content, day, creator); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO creator_fund_eligibility (creator_id, status, eligible_since)
		VALUES ($1, 'eligible', NOW())
		ON CONFLICT (creator_id) DO UPDATE SET status = 'eligible'`, creator); err != nil {
		t.Fatal(err)
	}

	if _, err := svc.SettleCreatorFundDay(ctx, creator, day); err != nil {
		t.Fatal(err)
	}

	var base, gross, multiplier int64
	if err := pool.QueryRow(ctx, `
		SELECT base_gross_paise, gross_paise, quality_multiplier_bps
		FROM creator_fund_earnings WHERE creator_id = $1 AND day_bucket = $2`,
		creator, day).Scan(&base, &gross, &multiplier); err != nil {
		t.Fatal(err)
	}
	if multiplier != NeutralMultiplierBps {
		t.Fatalf("zero-impression day settled at %d bps, want neutral %d", multiplier, NeutralMultiplierBps)
	}
	if gross != base || gross <= 0 {
		t.Fatalf("zero-impression day paid gross=%d base=%d", gross, base)
	}
}

// seedHistoricalRateAndBand opens a rate + quality-band window that
// contains `day`, and closes it again afterwards.
//
// Both sheets are resolved AS OF the settlement day, which is the right
// design — re-settling an old day must pay it at the rate and curve that
// applied then, not today's. It does mean a day earlier than the seeded
// launch rows has no rate at all, which is exactly the case a dev stack
// hits, so the test brings its own window rather than mutating the live
// sheet. effective_to is in the past, so present-day resolution is
// untouched while the test runs.
func seedHistoricalRateAndBand(ctx context.Context, t *testing.T, pool *pgxpool.Pool, day time.Time) {
	t.Helper()
	from := day.AddDate(0, 0, -7)
	until := day.AddDate(0, 0, 7)
	rateID, bandID := uuid.New(), uuid.New()

	if _, err := pool.Exec(ctx, `
		INSERT INTO monetization_rpm_rates
			(id, content_type, region_code, rpm_paise, effective_from, effective_to, notes)
		VALUES ($1, 'long_video', 'IN', 5000, $2, $3, 'integration test window')`,
		rateID, from, until); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO monetization_quality_bands
			(id, content_type, region_code, floor_bps, ceiling_bps, pivot_cqs,
			 confidence_impressions, enabled, effective_from, effective_to, notes)
		VALUES ($1, 'long_video', 'IN', 8500, 12500, 0.35, 1000, TRUE, $2, $3,
		        'integration test window')`,
		bandID, from, until); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM monetization_rpm_rates WHERE id = $1`, rateID)
		_, _ = pool.Exec(ctx, `DELETE FROM monetization_quality_bands WHERE id = $1`, bandID)
	})
}
