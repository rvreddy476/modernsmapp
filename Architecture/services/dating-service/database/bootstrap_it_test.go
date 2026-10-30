// Audit 2026-10-30 M4 against a real Postgres. TEST_PG_DSN on a "_test"
// database, CI only.
package database

import (
	"context"
	"errors"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/atpost/shared/store/migrationrunner"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

func bootstrapITPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("TEST_PG_DSN")
	if dsn == "" {
		t.Skip("TEST_PG_DSN not set; skipping dating bootstrap integration tests")
	}
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatalf("parse dsn: %v", err)
	}
	if !strings.HasSuffix(cfg.ConnConfig.Database, "_test") {
		t.Fatalf("refusing to run against database %q: name must end in _test", cfg.ConnConfig.Database)
	}
	cfg.MaxConns = 10
	pool, err := pgxpool.NewWithConfig(context.Background(), cfg)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(pool.Close)
	if err := BootstrapSchema(context.Background(), pool); err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	return pool
}

// An unchanged script takes no lock on dating_profiles: a boot completes
// while another session holds ACCESS SHARE (a long deck read) on it. A boot
// that does apply the script fails fast on lock_timeout instead of queueing
// every profile read and write behind its ALTER. The test holds the boot lock
// itself so no other package's boot runs into its reader.
func TestBootstrapIT_UnchangedScriptSkipsAndChangedOneTimesOut(t *testing.T) {
	pool := bootstrapITPool(t)
	ctx := context.Background()

	var fp string
	if err := pool.QueryRow(ctx, `SELECT sha256 FROM dating_schema_bootstrap WHERE id = 1`).Scan(&fp); err != nil {
		t.Fatalf("read fingerprint: %v", err)
	}
	if fp != SchemaFingerprint(SetupSQL) {
		t.Fatalf("recorded fingerprint %q is not the script's", fp)
	}

	err := migrationrunner.WithLock(ctx, pool, bootstrapLockService, func(ctx context.Context, conn *pgxpool.Conn) error {
		reader, err := pool.Begin(ctx)
		if err != nil {
			return err
		}
		defer func() { _ = reader.Rollback(ctx) }()
		if _, err := reader.Exec(ctx, `LOCK TABLE dating_profiles IN ACCESS SHARE MODE`); err != nil {
			return err
		}

		start := time.Now()
		if err := bootstrapLocked(ctx, conn); err != nil {
			t.Errorf("unchanged boot with a reader on dating_profiles: %v", err)
		}
		if d := time.Since(start); d > 3*time.Second {
			t.Errorf("unchanged boot took %v; it waited on a table lock", d)
		}

		// Applying the script (what a changed fingerprint does) times out.
		err = applySchema(ctx, conn, "m4-probe")
		var pgErr *pgconn.PgError
		if !errors.As(err, &pgErr) || pgErr.Code != "55P03" {
			t.Errorf("apply behind a reader: err = %v; want lock_not_available (55P03)", err)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	// The failed apply rolled back: the recorded fingerprint is untouched.
	if err := pool.QueryRow(ctx, `SELECT sha256 FROM dating_schema_bootstrap WHERE id = 1`).Scan(&fp); err != nil || fp != SchemaFingerprint(SetupSQL) {
		t.Fatalf("fingerprint after the failed apply = %q err=%v", fp, err)
	}
}

// A re-applied script does not re-add unchanged constraints or re-create the
// audit trigger, every tagged constraint ends up validated, and concurrent
// boots (server + worker binaries) all succeed.
func TestBootstrapIT_ReapplyKeepsConstraintsAndConcurrentBootsSucceed(t *testing.T) {
	pool := bootstrapITPool(t)
	ctx := context.Background()
	read := func() (con, trg uint32) {
		t.Helper()
		if err := pool.QueryRow(ctx, `
			SELECT
				(SELECT oid FROM pg_constraint WHERE conrelid = 'dating_profiles'::regclass AND conname = 'dating_profiles_status_chk'),
				(SELECT oid FROM pg_trigger WHERE tgrelid = 'dating_admin_audit'::regclass AND tgname = 'dating_admin_audit_no_update')
		`).Scan(&con, &trg); err != nil {
			t.Fatalf("read catalog: %v", err)
		}
		return
	}
	con1, trg1 := read()
	if err := ForgetSchemaFingerprint(ctx, pool); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	errs := make(chan error, 4)
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs <- BootstrapSchema(ctx, pool)
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("concurrent boot: %v", err)
		}
	}
	con2, trg2 := read()
	if con1 != con2 || trg1 != trg2 {
		t.Fatalf("re-apply re-created objects: constraint %d->%d trigger %d->%d", con1, con2, trg1, trg2)
	}
	var unvalidated int
	if err := pool.QueryRow(ctx, `
		SELECT COUNT(*)::int FROM pg_constraint
		WHERE contype = 'c' AND NOT convalidated
		  AND obj_description(oid, 'pg_constraint') LIKE 'setup.sql:%'`).Scan(&unvalidated); err != nil {
		t.Fatal(err)
	}
	if unvalidated != 0 {
		t.Fatalf("%d setup constraints left NOT VALID after boot", unvalidated)
	}
	// The status CHECK is enforced.
	_, err := pool.Exec(ctx, `INSERT INTO dating_profiles (user_id, profile_status) VALUES (gen_random_uuid(), 'bogus')`)
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != "23514" {
		t.Fatalf("bogus profile_status: err = %v; want check_violation", err)
	}
}
