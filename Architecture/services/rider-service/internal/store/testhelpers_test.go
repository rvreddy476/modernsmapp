package store

import (
	"context"
	"os"
	"testing"

	"github.com/atpost/rider-service/database"
	"github.com/jackc/pgx/v5/pgxpool"
)

// riderTestStore returns a *Store backed by TEST_PG_DSN, applying the rider
// schema first so a fresh test container is fully ready. Skips the test if
// TEST_PG_DSN is unset (CI may run unit-only).
func riderTestStore(t *testing.T) (*Store, func()) {
	t.Helper()
	dsn := os.Getenv("TEST_PG_DSN")
	if dsn == "" {
		t.Skip("TEST_PG_DSN not set; skipping rider store integration tests")
	}
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatalf("parse dsn: %v", err)
	}
	pool, err := pgxpool.NewWithConfig(context.Background(), cfg)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	if err := database.BootstrapSchema(context.Background(), pool); err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	_, _ = pool.Exec(context.Background(), `
		TRUNCATE TABLE
			rider_rides,
			rider_ride_offers,
			rider_vehicles,
			rider_vehicle_documents,
			rider_partner_subscriptions,
			rider_subscription_payments,
			rider_partners,
			rider_idempotency,
			rider_daily_revenue,
			rider_share_tokens,
			rider_complaints,
			rider_safety_incidents,
			rider_safety_actions,
			rider_partner_locations,
			rider_ride_payments,
			rider_consumer_inbox,
			rider_dispatch_attempts,
			rider_cron_runs
		CASCADE
	`)
	return New(pool), func() { pool.Close() }
}
