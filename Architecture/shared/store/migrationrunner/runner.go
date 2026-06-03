// Package migrationrunner applies SQL migration files in order on service boot,
// tracking applied filenames in a `schema_migrations` table so each migration
// runs exactly once per environment. Solves the recurring "fresh pod boots
// with stale schema" drift caused by setup.sql lagging behind migrations/.
//
// Pattern (per service):
//
//	//go:embed migrations/*.sql
//	var Migrations embed.FS
//
//	// On boot:
//	if err := migrationrunner.Run(ctx, dbPool, "post-service", database.Migrations, "migrations"); err != nil {
//	    log.Fatal(err)
//	}
//
// Each migration file is wrapped in its own transaction. A failure aborts the
// run; the service should fail to start so the operator notices.
//
// BASELINE mode: when SCHEMA_MIGRATIONS_BASELINE is "true", the runner marks
// every migration as applied without executing it. Use once when adopting this
// package against an existing database whose schema was applied out-of-band.
package migrationrunner

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"sort"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Run applies any migration files in `fsys` under `subdir` (e.g. "migrations")
// that haven't been recorded in `schema_migrations` yet. Filename ordering is
// lexical (the existing `001_…`, `002_…` convention sorts correctly).
//
// `service` is recorded alongside the filename for cross-service auditing —
// the same `schema_migrations` table holds rows from every service that
// shares the database, distinguished by service name.
func Run(ctx context.Context, db *pgxpool.Pool, service string, fsys fs.FS, subdir string) error {
	if db == nil {
		return fmt.Errorf("migrationrunner: nil db pool")
	}
	if _, err := db.Exec(ctx, schemaMigrationsDDL); err != nil {
		return fmt.Errorf("migrationrunner: ensure schema_migrations: %w", err)
	}

	names, err := listMigrationFiles(fsys, subdir)
	if err != nil {
		return err
	}

	applied, err := loadApplied(ctx, db, service)
	if err != nil {
		return fmt.Errorf("migrationrunner: load applied: %w", err)
	}

	baseline := strings.EqualFold(os.Getenv("SCHEMA_MIGRATIONS_BASELINE"), "true")

	for _, name := range names {
		if applied[name] {
			continue
		}
		path := subdir + "/" + name

		if baseline {
			if err := markApplied(ctx, db, service, name); err != nil {
				return fmt.Errorf("migrationrunner: baseline-mark %s: %w", name, err)
			}
			slog.Info("migration baselined (not executed)", "service", service, "migration", name)
			continue
		}

		body, err := fs.ReadFile(fsys, path)
		if err != nil {
			return fmt.Errorf("migrationrunner: read %s: %w", path, err)
		}
		if strings.TrimSpace(string(body)) == "" {
			// Empty file — record as applied and move on.
			if err := markApplied(ctx, db, service, name); err != nil {
				return fmt.Errorf("migrationrunner: mark empty %s: %w", name, err)
			}
			continue
		}

		// Each migration runs in its own transaction. On failure we abort
		// the whole boot — the operator must inspect.
		tx, err := db.Begin(ctx)
		if err != nil {
			return fmt.Errorf("migrationrunner: begin tx for %s: %w", name, err)
		}
		if _, err := tx.Exec(ctx, string(body)); err != nil {
			_ = tx.Rollback(ctx)
			return fmt.Errorf("migrationrunner: apply %s: %w", name, err)
		}
		if _, err := tx.Exec(ctx,
			`INSERT INTO schema_migrations (service, filename) VALUES ($1, $2) ON CONFLICT DO NOTHING`,
			service, name); err != nil {
			_ = tx.Rollback(ctx)
			return fmt.Errorf("migrationrunner: record %s: %w", name, err)
		}
		if err := tx.Commit(ctx); err != nil {
			return fmt.Errorf("migrationrunner: commit %s: %w", name, err)
		}
		slog.Info("migration applied", "service", service, "migration", name)
	}
	return nil
}

// listMigrationFiles returns the lex-sorted set of .sql files in `subdir` of
// `fsys`. Pulled out of Run so it's testable in isolation — the rest of Run
// needs a live pgxpool.Pool.
func listMigrationFiles(fsys fs.FS, subdir string) ([]string, error) {
	entries, err := fs.ReadDir(fsys, subdir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("migrationrunner: read %s: %w", subdir, err)
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".sql") {
			continue
		}
		names = append(names, e.Name())
	}
	sort.Strings(names)
	return names, nil
}

func loadApplied(ctx context.Context, db *pgxpool.Pool, service string) (map[string]bool, error) {
	rows, err := db.Query(ctx,
		`SELECT filename FROM schema_migrations WHERE service = $1`, service)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]bool{}
	for rows.Next() {
		var f string
		if err := rows.Scan(&f); err != nil {
			return nil, err
		}
		out[f] = true
	}
	return out, rows.Err()
}

func markApplied(ctx context.Context, db *pgxpool.Pool, service, name string) error {
	_, err := db.Exec(ctx,
		`INSERT INTO schema_migrations (service, filename) VALUES ($1, $2) ON CONFLICT DO NOTHING`,
		service, name)
	return err
}

const schemaMigrationsDDL = `
CREATE TABLE IF NOT EXISTS schema_migrations (
    service    TEXT        NOT NULL,
    filename   TEXT        NOT NULL,
    applied_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (service, filename)
);
`
