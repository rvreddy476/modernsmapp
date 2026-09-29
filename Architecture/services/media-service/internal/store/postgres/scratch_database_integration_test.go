//go:build integration

package postgres

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

// requireScratchDatabase refuses to run a seeding suite against anything
// but a database whose name ends in _test. It used to live in
// public_post_media_integration_test.go; that file went with the public
// poster rework (2026-09-29), and the audio-track, transcode-lease and
// copyright suites all still need the guard.
func requireScratchDatabase(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	var name string
	if err := pool.QueryRow(context.Background(), `SELECT current_database()`).Scan(&name); err != nil {
		t.Fatalf("current_database: %v", err)
	}
	if !strings.HasSuffix(name, "_test") {
		t.Fatalf("refusing to seed rows into %q: name a scratch database ending in _test "+
			"(POSTGRES_DSN=%q)", name, os.Getenv("POSTGRES_DSN"))
	}
}
