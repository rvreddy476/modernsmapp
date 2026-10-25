//go:build integration

package postgres

// A restart must not revert a migration.
//
// BootstrapSchema runs setup.sql on every boot, before the migrations, and
// the gated contract files are never re-run by a boot. So whatever setup.sql
// restates is put back over the gated half on every pod start. That is how
// gated 1001 (no more approval_status='live') held only until the next
// restart: setup.sql dropped and re-added the CHECK with 'live' in it.
//
// This boots a scratch database, applies the gated files a launch applies,
// snapshots the catalogue, boots again, and requires the catalogue to be
// byte-for-byte what it was.
//
//	COMMERCE_TEST_DSN=.../commerce_it_test go test -tags=integration ./internal/store/postgres/ -run RestartDoesNotRevert -v

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/atpost/commerce-service/database"
	"github.com/atpost/commerce-service/internal/testdsn"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func restartScratchPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("COMMERCE_TEST_DSN")
	if dsn == "" {
		t.Skip("COMMERCE_TEST_DSN not set")
	}
	ctx := context.Background()
	admin, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	scratch := "commerce_restart_" + strings.ReplaceAll(uuid.NewString()[:8], "-", "") + "_test"
	if _, err := admin.Exec(ctx, `CREATE DATABASE `+scratch); err != nil {
		t.Fatalf("creating the scratch database: %v", err)
	}
	admin.Close()
	scratchDSN := testdsn.SwapDatabase(dsn, scratch)
	testdsn.Refuse(scratchDSN)
	pool, err := pgxpool.New(ctx, scratchDSN)
	if err != nil {
		t.Fatalf("connect to scratch: %v", err)
	}
	t.Cleanup(func() {
		pool.Close()
		if cleanup, err := pgxpool.New(ctx, dsn); err == nil {
			_, _ = cleanup.Exec(ctx, `DROP DATABASE IF EXISTS `+scratch+` WITH (FORCE)`)
			cleanup.Close()
		}
	})
	return pool
}

// catalogue is every schema fact a boot could silently change: constraint
// definitions, trigger definitions, function bodies, column type /
// nullability / default, and index definitions, in a stable order.
func catalogue(t *testing.T, pool *pgxpool.Pool) []string {
	t.Helper()
	rows, err := pool.Query(context.Background(), `
		SELECT 'constraint ' || conrelid::regclass || ' ' || conname || ' ' || pg_get_constraintdef(oid) || ' validated=' || convalidated
		  FROM pg_constraint WHERE connamespace = 'public'::regnamespace
		UNION ALL
		SELECT 'trigger ' || tgrelid::regclass || ' ' || tgname || ' ' || pg_get_triggerdef(oid)
		  FROM pg_trigger WHERE NOT tgisinternal
		UNION ALL
		SELECT 'function ' || proname || ' ' || md5(prosrc)
		  FROM pg_proc WHERE pronamespace = 'public'::regnamespace
		UNION ALL
		SELECT 'column ' || table_name || '.' || column_name || ' ' || data_type || ' null=' || is_nullable || ' default=' || COALESCE(column_default, '')
		  FROM information_schema.columns WHERE table_schema = 'public'
		UNION ALL
		SELECT 'index ' || indexdef FROM pg_indexes WHERE schemaname = 'public'
		ORDER BY 1`)
	if err != nil {
		t.Fatalf("catalogue: %v", err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			t.Fatal(err)
		}
		out = append(out, s)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestRestartDoesNotRevertAGatedMigration(t *testing.T) {
	pool := restartScratchPool(t)
	ctx := context.Background()

	if err := BootstrapSchema(ctx, pool, database.SetupSQL, database.Migrations); err != nil {
		t.Fatalf("first boot: %v", err)
	}
	// The gated files a launch runs after the fleet is drained, whose
	// preconditions hold on an empty database. 999/1000/1002 refuse here by
	// design (backfill and cutover preconditions), so they are not applied.
	for _, name := range []string{"998_contract_triggers_and_fences.sql", "1001_approval_status_tighten.sql"} {
		body, err := database.Gated.ReadFile("gated/" + name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := pool.Exec(ctx, string(body)); err != nil {
			t.Fatalf("gated %s: %v", name, err)
		}
	}
	liveRefused := func(when string) {
		t.Helper()
		var def string
		if err := pool.QueryRow(ctx, `SELECT pg_get_constraintdef(oid) FROM pg_constraint
			WHERE conrelid = 'products'::regclass AND conname = 'products_approval_status_check'`).Scan(&def); err != nil {
			t.Fatalf("%s: reading the CHECK: %v", when, err)
		}
		if strings.Contains(def, "'live'") {
			t.Fatalf("%s: products_approval_status_check permits 'live' again; gated 1001 was reverted:\n%s", when, def)
		}
		if !strings.Contains(def, "'changes_requested'") {
			t.Fatalf("%s: products_approval_status_check refuses 'changes_requested':\n%s", when, def)
		}
	}
	liveRefused("after gated 1001")
	before := catalogue(t, pool)

	// The restart.
	if err := BootstrapSchema(ctx, pool, database.SetupSQL, database.Migrations); err != nil {
		t.Fatalf("second boot: %v", err)
	}
	liveRefused("after a restart")
	after := catalogue(t, pool)

	b := map[string]bool{}
	for _, s := range before {
		b[s] = true
	}
	a := map[string]bool{}
	for _, s := range after {
		a[s] = true
	}
	for _, s := range before {
		if !a[s] {
			t.Errorf("a restart changed or removed: %s", s)
		}
	}
	for _, s := range after {
		if !b[s] {
			t.Errorf("a restart added: %s", s)
		}
	}
}
