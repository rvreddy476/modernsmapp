//go:build integration

// fund_settlement_test.go — end-to-end numeric proof that Tier 3a
// creator-fund settlement (a) reads analytics rollups, (b) applies
// the active RPM rate, (c) writes a settled earnings row, (d) credits
// the wallet, and (e) is idempotent on re-run.
//
// The formula-only unit tests in monetization-service/internal/service
// cover the math; the wiring tests in creator_fund_test.go prove the
// HTTP shape. This test sits between them: it seeds the two DBs
// (analytics.content_daily_summary + creator_fund_eligibility) the
// way a real production day would, then calls the admin force-settle
// endpoint and asserts the result.
//
// Gated on ATPOST_RUN_INTEGRATION=1 + two DSN env vars:
//   ATPOST_ANALYTICS_DB_URL       — analytics-db conn string
//   ATPOST_MONETIZATION_DB_URL    — monetization-db conn string

package integration

import (
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// TestCreatorFundSettlementEndToEnd is the numeric end-to-end:
//
//  1. Seed eligibility (eligible, 30-day-old)
//  2. Seed analytics.content_daily_summary with 1000 long_video views
//     on `day`.
//  3. POST /v1/monetization/admin/creator-fund/settle?day=<day>
//  4. Assert creator_fund_earnings row exists with gross=5000 paise
//     (1000 views × 5000 paise per 1000 views = 5000), net=3500 paise
//     (after 30% platform fee at launch baseline).
//  5. Assert wallets.balance += 3500.
//  6. Re-run step 3 → assert NO double-credit (idempotency invariant).
func TestCreatorFundSettlementEndToEnd(t *testing.T) {
	SkipIfNotIntegration(t)
	urls := LoadServiceURLs()
	SkipIfDown(t, urls.Monetization)

	analyticsDSN := os.Getenv("ATPOST_ANALYTICS_DB_URL")
	monetizationDSN := os.Getenv("ATPOST_MONETIZATION_DB_URL")
	if analyticsDSN == "" || monetizationDSN == "" {
		t.Skip("ATPOST_ANALYTICS_DB_URL and ATPOST_MONETIZATION_DB_URL must be set")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	analyticsPool, err := pgxpool.New(ctx, analyticsDSN)
	if err != nil {
		t.Fatalf("connect analytics db: %v", err)
	}
	defer analyticsPool.Close()

	monPool, err := pgxpool.New(ctx, monetizationDSN)
	if err != nil {
		t.Fatalf("connect monetization db: %v", err)
	}
	defer monPool.Close()

	creatorID := uuid.New()
	contentID := uuid.New()
	day := time.Now().UTC().AddDate(0, 0, -1).Truncate(24 * time.Hour)
	dayStr := day.Format("2006-01-02")

	cleanup := func() {
		// Order matters — earnings before eligibility (no FK but
		// keeps the cleanup deterministic). Wallet row stays because
		// the wallet exists across days.
		_, _ = monPool.Exec(ctx,
			`DELETE FROM creator_fund_earnings WHERE creator_id = $1 AND day_bucket = $2`,
			creatorID, day)
		_, _ = monPool.Exec(ctx,
			`DELETE FROM creator_fund_eligibility WHERE creator_id = $1`, creatorID)
		_, _ = monPool.Exec(ctx,
			`DELETE FROM wallets WHERE user_id = $1`, creatorID)
		_, _ = analyticsPool.Exec(ctx,
			`DELETE FROM analytics.content_daily_summary
			 WHERE creator_id = $1 AND day_bucket = $2`, creatorID, day)
	}
	cleanup()
	defer cleanup()

	if err := seedEligibility(ctx, monPool, creatorID); err != nil {
		t.Fatalf("seed eligibility: %v", err)
	}
	if err := seedDailyMetrics(ctx, analyticsPool, contentID, creatorID, day, "long_video", 1000, 5*60*1000); err != nil {
		t.Fatalf("seed daily metrics: %v", err)
	}

	// Step 3 — settle.
	admin := NewHTTPClient(urls.Monetization, uuid.New()).WithAdminRole()
	settlePath := "/v1/monetization/admin/creator-fund/settle?day=" + dayStr
	resp := admin.MustOK(t, ctx, "POST", settlePath, nil)
	var settleEnv struct {
		Day          string `json:"day"`
		RowsCredited int    `json:"rows_credited"`
	}
	if err := json.Unmarshal(resp, &settleEnv); err != nil {
		t.Fatalf("decode settle response: %v", err)
	}
	if settleEnv.Day != dayStr {
		t.Errorf("day echoed wrong: want %s, got %s", dayStr, settleEnv.Day)
	}
	if settleEnv.RowsCredited < 1 {
		t.Errorf("want >= 1 row credited, got %d", settleEnv.RowsCredited)
	}

	// Step 4 — assert earnings row.
	var gross, net, fee int64
	if err := monPool.QueryRow(ctx, `
		SELECT gross_paise, net_paise, platform_fee_paise
		FROM creator_fund_earnings
		WHERE creator_id = $1 AND day_bucket = $2 AND content_type = 'long_video'
	`, creatorID, day).Scan(&gross, &net, &fee); err != nil {
		t.Fatalf("earnings row missing: %v", err)
	}
	// 1000 views × 5000 paise / 1000 views = 5000 paise gross.
	// Platform fee 30% = 1500. Net = 3500.
	if gross != 5000 {
		t.Errorf("gross paise: want 5000, got %d", gross)
	}
	if fee != 1500 {
		t.Errorf("platform fee paise: want 1500, got %d", fee)
	}
	if net != 3500 {
		t.Errorf("net paise: want 3500, got %d", net)
	}

	// Step 5 — assert wallet credited.
	var balance int64
	if err := monPool.QueryRow(ctx,
		`SELECT balance_paise FROM wallets WHERE user_id = $1`, creatorID).Scan(&balance); err != nil {
		t.Fatalf("wallet row missing: %v", err)
	}
	if balance != 3500 {
		t.Errorf("wallet balance after settle: want 3500, got %d", balance)
	}

	// Step 6 — re-run idempotency. Second settle for the same day
	// should NOT double-credit. The earnings row is UNIQUE on
	// (creator_id, day_bucket, content_type, region_code); the worker
	// is expected to skip already-settled days.
	admin.MustOK(t, ctx, "POST", settlePath, nil)
	if err := monPool.QueryRow(ctx,
		`SELECT balance_paise FROM wallets WHERE user_id = $1`, creatorID).Scan(&balance); err != nil {
		t.Fatalf("wallet row missing after re-run: %v", err)
	}
	if balance != 3500 {
		t.Errorf("idempotency broken: wallet balance after re-run = %d, want 3500", balance)
	}
}

// seedEligibility inserts a row in creator_fund_eligibility marking
// the creator eligible as of 30 days ago. Idempotent.
func seedEligibility(ctx context.Context, pool *pgxpool.Pool, creatorID uuid.UUID) error {
	_, err := pool.Exec(ctx, `
		INSERT INTO creator_fund_eligibility
			(creator_id, status, view_score_90d, watch_time_ms_90d,
			 qualifying_content_count, eligible_since, last_evaluated_at)
		VALUES ($1, 'eligible', 100000, 3600000000, 50, NOW() - INTERVAL '30 days', NOW())
		ON CONFLICT (creator_id) DO UPDATE
		SET status = 'eligible',
		    view_score_90d = EXCLUDED.view_score_90d,
		    watch_time_ms_90d = EXCLUDED.watch_time_ms_90d,
		    qualifying_content_count = EXCLUDED.qualifying_content_count,
		    last_evaluated_at = NOW()
	`, creatorID)
	return err
}

// seedDailyMetrics inserts a row in analytics.content_daily_summary
// representing one content's metrics for one day. views is the headline
// count, watchMs is total watch time across all viewers. Other columns
// are zeroed — they're not used by the settlement formula.
func seedDailyMetrics(ctx context.Context, pool *pgxpool.Pool, contentID, creatorID uuid.UUID, day time.Time, contentType string, views, watchMs int64) error {
	_, err := pool.Exec(ctx, `
		INSERT INTO analytics.content_daily_summary
			(content_id, day_bucket, creator_id, content_type,
			 views_display, watch_time_total_ms, view_score_total)
		VALUES ($1, $2, $3, $4, $5, $6, $5::float8)
		ON CONFLICT (content_id, day_bucket) DO UPDATE
		SET views_display = EXCLUDED.views_display,
		    watch_time_total_ms = EXCLUDED.watch_time_total_ms,
		    view_score_total = EXCLUDED.view_score_total,
		    creator_id = EXCLUDED.creator_id,
		    content_type = EXCLUDED.content_type
	`, contentID, day, creatorID, contentType, views, watchMs)
	return err
}
