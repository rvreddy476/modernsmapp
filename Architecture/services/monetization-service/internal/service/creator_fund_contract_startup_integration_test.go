//go:build integration

package service

import (
	"context"
	"strings"
	"testing"

	"github.com/atpost/monetization-service/internal/store/postgres"
	"github.com/jackc/pgx/v5/pgxpool"
)

// The contract has a name, and the process checks for it before it
// serves. A monetization image deployed against an analytics schema that
// has not applied migration 012 must say so at boot, not at the first
// settlement.
//
// The view is dropped inside a transaction that is rolled back, so the
// scratch database is untouched; the check is run on that same
// transaction so it sees the drop.
func TestStartupRefusesMissingContractView(t *testing.T) {
	dsn := requireTestDSN(t)
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	store := postgres.New(pool)

	// With the view present the check passes and reports the columns
	// monetization reads, in the view's order.
	cols, err := store.CheckAnalyticsContract(ctx)
	if err != nil {
		t.Fatalf("contract check with the view present: %v", err)
	}
	want := []string{"creator_id", "content_id", "day_bucket", "content_type", "views_display",
		"watch_time_total_ms", "impressions", "content_quality_score", "view_score_total",
		"eligibility_state", "eligibility_effective_from", "updated_at"}
	if strings.Join(cols, ",") != strings.Join(want, ",") {
		t.Fatalf("contract columns = %v, want %v", cols, want)
	}

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `DROP VIEW analytics.v_creator_daily_metrics_v1`); err != nil {
		t.Fatalf("drop the view inside the transaction: %v", err)
	}
	_, err = store.CheckAnalyticsContractTx(ctx, tx)
	if err == nil {
		t.Fatal("the contract check passed with the view missing")
	}
	for _, needle := range []string{"v_creator_daily_metrics_v1", "012"} {
		if !strings.Contains(err.Error(), needle) {
			t.Errorf("error %q does not name %q; an operator must be able to tell which view and which migration", err, needle)
		}
	}
	t.Logf("refused as expected: %v", err)
}
