// Command commerce-migrate applies commerce-service schema migrations as a
// ONE-SHOT JOB rather than at service boot.
//
// Amendment A7. The review's preference, and mine after writing it: a
// dedicated job is a far smaller blast radius than changing the shared
// migration runner's behaviour for every service on the platform. The
// serialisation primitive (migrationrunner.RunExclusive) is additive, so
// services that have not opted in are untouched.
//
// Two modes:
//
//	commerce-migrate            apply setup.sql + migrations/*.sql
//	commerce-migrate -gated     ALSO apply gated/*.sql
//
// The gated set validates constraints that were added NOT VALID and
// tightens money columns to NOT NULL. It must run only after every replica
// on the previous image is drained and the money comparison is clean; the
// SQL asserts those preconditions itself and refuses otherwise.
//
// Deployment shape: a Kubernetes Job with the same image, IRSA role and
// database credentials as the service, run as an argo/helm pre-sync hook.
// Service replicas set COMMERCE_SKIP_BOOT_MIGRATIONS=true so they never race
// this job or each other.
package main

import (
	"context"
	"flag"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"time"

	"github.com/atpost/commerce-service/database"
	"github.com/atpost/shared/o11y/logging"
	"github.com/atpost/shared/store/migrationrunner"
	"github.com/jackc/pgx/v5/pgxpool"
)

func main() {
	var (
		gated   = flag.Bool("gated", false, "also apply database/gated/*.sql (validation + tightening; requires a drained fleet)")
		timeout = flag.Duration("timeout", 30*time.Minute, "overall timeout")
	)
	flag.Parse()

	logging.Init(logging.Config{ServiceName: "commerce-migrate"})

	dsn := os.Getenv("POSTGRES_DSN")
	if dsn == "" {
		slog.Error("commerce-migrate: POSTGRES_DSN is required")
		os.Exit(1)
	}

	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()

	// A small pool: the runner needs one dedicated connection for the
	// advisory lock, and nothing else runs here.
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		slog.Error("commerce-migrate: bad DSN", "error", err)
		os.Exit(1)
	}
	cfg.MaxConns = 4
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		slog.Error("commerce-migrate: connect", "error", err)
		os.Exit(1)
	}
	defer pool.Close()

	if err := pool.Ping(ctx); err != nil {
		slog.Error("commerce-migrate: ping", "error", err)
		os.Exit(1)
	}

	// setup.sql is idempotent and must stay free of contract-stage DDL
	// (review §5.4): it runs BEFORE migrations, so a constraint placed here
	// would hit a mixed fleet before its migration had a chance to prepare
	// the data.
	if _, err := pool.Exec(ctx, database.SetupSQL); err != nil {
		slog.Error("commerce-migrate: apply setup.sql", "error", err)
		os.Exit(1)
	}
	slog.Info("commerce-migrate: base schema applied")

	if err := migrationrunner.RunExclusive(ctx, pool, "commerce-service", database.Migrations, "migrations"); err != nil {
		slog.Error("commerce-migrate: migrations failed", "error", err)
		os.Exit(1)
	}
	slog.Info("commerce-migrate: migrations applied")

	if !*gated {
		slog.Info("commerce-migrate: done (gated set NOT applied; pass -gated once the fleet is drained)")
		return
	}

	if err := runGated(ctx, pool); err != nil {
		slog.Error("commerce-migrate: gated migrations failed", "error", err)
		os.Exit(1)
	}
	slog.Info("commerce-migrate: gated migrations applied")
}

// runGated applies database/gated/*.sql under the same advisory lock, in the
// same "record it only if it applied" discipline as the ordinary set.
//
// It is recorded under a distinct service name so a gated file can never be
// mistaken for an ordinary one by a service booting with the old code path.
func runGated(ctx context.Context, pool *pgxpool.Pool) error {
	sub, err := fs.Sub(database.Gated, ".")
	if err != nil {
		return fmt.Errorf("gated fs: %w", err)
	}
	return migrationrunner.RunExclusive(ctx, pool, "commerce-service-gated", sub, "gated")
}
