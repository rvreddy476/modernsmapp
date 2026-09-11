package postgres

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

// ---------------------------------------------------------------------------
// The analytics contract (plan Phase 5C, audit M-15)
// ---------------------------------------------------------------------------
//
// Everything monetization-service reads from the analytics schema comes
// through one versioned view, analytics.v_creator_daily_metrics_v1, which
// analytics-service owns (its migration 012). Nothing in this package may
// name analytics.content_daily_summary or analytics.content_ownership;
// TestAccrualReadsOnlyTheContractView records the executed SQL and
// enforces that.
//
// The process checks the view at start-up and refuses to serve without
// it, so a monetization image rolled out ahead of the analytics migration
// fails at boot with the view's name in the log rather than at the first
// settlement with a bare "relation does not exist".

// AnalyticsContractView is the one analytics relation this service reads.
const AnalyticsContractView = "v_creator_daily_metrics_v1"

// AnalyticsContractMigration names the analytics-service migration that
// creates the view, for the operator reading the refusal.
const AnalyticsContractMigration = "analytics-service migration 012_creator_daily_metrics_view.sql"

// analyticsContractColumns is every column the queries in this package
// read off the view. The view may carry more; it may not carry fewer.
var analyticsContractColumns = []string{
	"creator_id", "content_id", "day_bucket", "content_type",
	"views_display", "watch_time_total_ms", "impressions", "content_quality_score",
	"view_score_total", "eligibility_state", "eligibility_effective_from", "updated_at",
}

// ErrAnalyticsContractMissing is returned when the view does not exist.
var ErrAnalyticsContractMissing = errors.New("ANALYTICS_CONTRACT_MISSING")

// CheckAnalyticsContract verifies that analytics.v_creator_daily_metrics_v1
// exists and carries every column this service reads, and returns the
// view's full column list in its declared order for the boot log.
func (s *Store) CheckAnalyticsContract(ctx context.Context) ([]string, error) {
	return s.CheckAnalyticsContractTx(ctx, s.db)
}

// CheckAnalyticsContractTx is CheckAnalyticsContract on the caller's
// query surface, so a test can run it inside a transaction that has
// dropped the view.
func (s *Store) CheckAnalyticsContractTx(ctx context.Context, q DBTX) ([]string, error) {
	rows, err := q.Query(ctx, `
		SELECT column_name
		FROM information_schema.columns
		WHERE table_schema = 'analytics' AND table_name = $1
		ORDER BY ordinal_position
	`, AnalyticsContractView)
	if err != nil {
		return nil, fmt.Errorf("read analytics contract columns: %w", err)
	}
	defer rows.Close()
	var cols []string
	for rows.Next() {
		var c string
		if err := rows.Scan(&c); err != nil {
			return nil, err
		}
		cols = append(cols, c)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(cols) == 0 {
		return nil, fmt.Errorf("%w: analytics.%s does not exist in this database; apply %s before starting monetization-service",
			ErrAnalyticsContractMissing, AnalyticsContractView, AnalyticsContractMigration)
	}
	have := make(map[string]bool, len(cols))
	for _, c := range cols {
		have[c] = true
	}
	var missing []string
	for _, want := range analyticsContractColumns {
		if !have[want] {
			missing = append(missing, want)
		}
	}
	if len(missing) > 0 {
		return cols, fmt.Errorf("%w: analytics.%s lacks column(s) %s that monetization reads; the contract is versioned, so a reshaped view must be a _v2 (%s)",
			ErrAnalyticsContractMissing, AnalyticsContractView, strings.Join(missing, ", "), AnalyticsContractMigration)
	}
	return cols, nil
}
