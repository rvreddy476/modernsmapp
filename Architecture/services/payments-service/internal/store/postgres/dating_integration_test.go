//go:build integration

package postgres

// Migration 011, against a live PostgreSQL (payments_it_test): the embedded
// file seeds the application `dating` exactly once however often it runs,
// leaves an operator's later change alone, and extends the legacy mapping.
//
// Everything runs in one transaction that is rolled back, so the shared
// registry is untouched. To prove the SEED (not a row left by an earlier
// apply) the existing `dating` row is deleted first, with foreign-key triggers
// off for this transaction only (session_replication_role, which needs the
// superuser the dev postgres container provides).
//
//	PAYMENTS_TEST_DSN=... go test -tags=integration ./internal/store/postgres/ -run Dating -v -count=1

import (
	"context"
	"io/fs"
	"testing"

	"github.com/atpost/payments-service/database"
)

const datingMigration = "migrations/011_dating_application.sql"

func TestDatingApplicationMigrationSeedsOnceAndMapsLegacyRows(t *testing.T) {
	ctx := context.Background()
	sql, err := fs.ReadFile(database.Migrations, datingMigration)
	if err != nil {
		t.Fatalf("read embedded %s: %v", datingMigration, err)
	}

	tx, err := testPool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	scalar := func(query string, args ...any) string {
		t.Helper()
		var v string
		if err := tx.QueryRow(ctx, query, args...).Scan(&v); err != nil {
			t.Fatalf("%s: %v", query, err)
		}
		return v
	}

	if _, err := tx.Exec(ctx, `SET LOCAL session_replication_role = replica`); err != nil {
		t.Fatalf("disable FK triggers for this transaction (needs superuser): %v", err)
	}
	if _, err := tx.Exec(ctx, `DELETE FROM payments.applications WHERE key = 'dating'`); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, `SET LOCAL session_replication_role = origin`); err != nil {
		t.Fatal(err)
	}

	for run := 1; run <= 3; run++ {
		if _, err := tx.Exec(ctx, string(sql)); err != nil {
			t.Fatalf("run %d of %s: %v", run, datingMigration, err)
		}
		if n := scalar(`SELECT count(*)::text FROM payments.applications WHERE key = 'dating'`); n != "1" {
			t.Fatalf("after run %d: dating rows = %s, want 1", run, n)
		}
	}
	const row = `SELECT display_name || '|' || status || '|' || merchant_display_name || '|' ||
	                    array_to_string(ARRAY(SELECT unnest(enabled_methods) ORDER BY 1), ',') || '|' || settings::text
	               FROM payments.applications WHERE key = 'dating'`
	if got := scalar(row); got != "Momentum Dating|active|Momentum Merchant|card,upi|{}" {
		t.Fatalf("seeded dating = %q", got)
	}

	t.Run("a re-run leaves an operator's change alone", func(t *testing.T) {
		if _, err := tx.Exec(ctx, `UPDATE payments.applications SET status = 'disabled', display_name = 'Operator renamed' WHERE key = 'dating'`); err != nil {
			t.Fatal(err)
		}
		if _, err := tx.Exec(ctx, string(sql)); err != nil {
			t.Fatal(err)
		}
		if got := scalar(`SELECT display_name || '|' || status FROM payments.applications WHERE key = 'dating'`); got != "Operator renamed|disabled" {
			t.Fatalf("after re-run = %q, want the operator's change kept", got)
		}
	})

	t.Run("legacy mapping: dating-service and dating_premium map to dating, the rest unchanged", func(t *testing.T) {
		for _, tc := range []struct{ owner, ref, want string }{
			{"dating-service", "dating_premium", "dating"},
			{"dating-service", "food_order", "dating"}, // owner wins
			{"", "dating_premium", "dating"},
			{"legacy:dating_premium", "dating_premium", "dating"},
			{"commerce-service", "dating_premium", "mstore"},
			{"food-service", "order", "feast"},
			{"", "order", "mstore"},
			{"", "food_order", "feast"},
			{"unknown", "demo_ref", ""},
		} {
			got := scalar(`SELECT COALESCE(payments.legacy_application_for(NULLIF($1,''), $2), '')`, tc.owner, tc.ref)
			if got != tc.want {
				t.Errorf("legacy_application_for(%q, %q) = %q, want %q", tc.owner, tc.ref, got, tc.want)
			}
		}
	})
}
