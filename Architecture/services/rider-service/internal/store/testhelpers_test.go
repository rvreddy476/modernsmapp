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
	return New(pool), func() { pool.Close() }
}
