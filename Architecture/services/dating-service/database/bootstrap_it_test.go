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

// Audit 2026-10-30 M4 (founder decision 31 Oct): rows the old app wrote with
// share_location_on_panic=true and no consent record are not opted in; the
// boot clears the flag once. A row with a recorded consent keeps it, and a
// second apply writes nothing (updated_at untouched).
func TestBootstrapIT_TrustedContactsWithoutConsentAreNotOptedIn(t *testing.T) {
	pool := bootstrapITPool(t)
	ctx := context.Background()
	var user, legacy, consented string
	if err := pool.QueryRow(ctx, `SELECT gen_random_uuid()::text, gen_random_uuid()::text, gen_random_uuid()::text`).
		Scan(&user, &legacy, &consented); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM dating_trusted_contacts WHERE user_id = $1`, user)
	})
	if _, err := pool.Exec(ctx, `
		INSERT INTO dating_trusted_contacts (user_id, contact_id, share_location_on_panic, location_consent_at, location_consent_version, location_consent_copy)
		VALUES ($1, $2, true, NULL, NULL, NULL), ($1, $3, true, now(), 'panic_location_v1', 'copy')`,
		user, legacy, consented); err != nil {
		t.Fatalf("seed: %v", err)
	}
	type row struct {
		share   bool
		updated string
	}
	read := func(contact string) row {
		t.Helper()
		var r row
		if err := pool.QueryRow(ctx, `SELECT share_location_on_panic, updated_at::text FROM dating_trusted_contacts
			WHERE user_id = $1 AND contact_id = $2`, user, contact).Scan(&r.share, &r.updated); err != nil {
			t.Fatalf("read %s: %v", contact, err)
		}
		return r
	}
	consentedBefore := read(consented)
	var afterFirst row
	for run := 1; run <= 2; run++ {
		if err := ForgetSchemaFingerprint(ctx, pool); err != nil {
			t.Fatalf("forget fingerprint run %d: %v", run, err)
		}
		if err := BootstrapSchema(ctx, pool); err != nil {
			t.Fatalf("bootstrap run %d: %v", run, err)
		}
		l := read(legacy)
		if l.share {
			t.Fatalf("run %d: a row with no consent record is still opted in", run)
		}
		if run == 1 {
			afterFirst = l
		} else if l.updated != afterFirst.updated {
			t.Fatalf("second apply rewrote the row (%s -> %s)", afterFirst.updated, l.updated)
		}
		if c := read(consented); !c.share || c.updated != consentedBefore.updated {
			t.Fatalf("run %d: consented row changed: %+v (was %+v)", run, c, consentedBefore)
		}
	}
}
