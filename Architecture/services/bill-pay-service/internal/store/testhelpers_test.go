package store

import (
	"context"
	"os"
	"testing"

	"github.com/atpost/bill-pay-service/database"
	"github.com/jackc/pgx/v5/pgxpool"
)

// billpayTestStore returns a *Store backed by TEST_PG_DSN, applying the
// billpay schema first. Skips if TEST_PG_DSN is unset (CI may run unit-only).
func billpayTestStore(t *testing.T) (*Store, func()) {
	t.Helper()
	dsn := os.Getenv("TEST_PG_DSN")
	if dsn == "" {
		t.Skip("TEST_PG_DSN not set; skipping billpay store integration tests")
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
	return New(pool), func() { pool.Close() }
}
