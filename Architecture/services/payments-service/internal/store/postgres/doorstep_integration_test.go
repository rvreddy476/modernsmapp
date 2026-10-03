//go:build integration

package postgres

// Migration 015, against a live PostgreSQL (payments_it_test only): the
// embedded file seeds the application `doorstep` exactly once however often it
// runs, leaves an operator's later change alone, and extends the legacy
// mapping with doorstep-service / doorstep_booking / doorstep_extras while
// keeping every branch 010–013 established.
//
// Everything runs in one transaction that is rolled back, so the shared
// registry is untouched. To prove the SEED (not a row left by an earlier
// apply) the existing `doorstep` row is deleted first, with foreign-key
// triggers off for this transaction only (session_replication_role, which
// needs the superuser the dev postgres container provides).
//
//	PAYMENTS_TEST_DSN=.../payments_it_test go test -tags=integration ./internal/store/postgres/ -run Doorstep -v -count=1

import (
	"context"
	"io/fs"
	"strings"
	"testing"

	"github.com/atpost/payments-service/database"
)

const doorstepMigration = "migrations/015_doorstep_application.sql"

func TestDoorstepApplicationMigrationSeedsOnceAndMapsLegacyRows(t *testing.T) {
	ctx := context.Background()
	requireTestDatabase(t)
	sql, err := fs.ReadFile(database.Migrations, doorstepMigration)
	if err != nil {
		t.Fatalf("read embedded %s: %v", doorstepMigration, err)
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
	if _, err := tx.Exec(ctx, `DELETE FROM payments.applications WHERE key = 'doorstep'`); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, `SET LOCAL session_replication_role = origin`); err != nil {
		t.Fatal(err)
	}
	others := scalar(`SELECT count(*)::text FROM payments.applications`)

	for run := 1; run <= 3; run++ {
		if _, err := tx.Exec(ctx, string(sql)); err != nil {
			t.Fatalf("run %d of %s: %v", run, doorstepMigration, err)
		}
		if n := scalar(`SELECT count(*)::text FROM payments.applications WHERE key = 'doorstep'`); n != "1" {
			t.Fatalf("after run %d: doorstep rows = %s, want 1", run, n)
		}
	}
	if got := scalar(`SELECT (count(*) - 1)::text FROM payments.applications`); got != others {
		t.Fatalf("015 touched other registry rows: %s before, %s after", others, got)
	}
	const row = `SELECT display_name || '|' || status || '|' || merchant_display_name || '|' ||
	                    array_to_string(ARRAY(SELECT unnest(enabled_methods) ORDER BY 1), ',') || '|' || settings::text
	               FROM payments.applications WHERE key = 'doorstep'`
	if got := scalar(row); got != "Doorstep|active|Momentum Merchant|card,upi|{}" {
		t.Fatalf("seeded doorstep = %q", got)
	}

	t.Run("a re-run leaves an operator's change alone", func(t *testing.T) {
		if _, err := tx.Exec(ctx, `UPDATE payments.applications SET status = 'disabled', display_name = 'Operator renamed' WHERE key = 'doorstep'`); err != nil {
			t.Fatal(err)
		}
		if _, err := tx.Exec(ctx, string(sql)); err != nil {
			t.Fatal(err)
		}
		if got := scalar(`SELECT display_name || '|' || status FROM payments.applications WHERE key = 'doorstep'`); got != "Operator renamed|disabled" {
			t.Fatalf("after re-run = %q, want the operator's change kept", got)
		}
	})

	t.Run("legacy mapping: doorstep-service and both reference types map to doorstep, the rest unchanged", func(t *testing.T) {
		for _, tc := range []struct{ owner, ref, want string }{
			{"doorstep-service", "doorstep_booking", "doorstep"},
			{"doorstep-service", "doorstep_extras", "doorstep"},
			{"doorstep-service", "food_order", "doorstep"}, // owner wins
			{"", "doorstep_booking", "doorstep"},
			{"", "doorstep_extras", "doorstep"},
			{"legacy:doorstep_booking", "doorstep_booking", "doorstep"},
			{"legacy:doorstep_extras", "doorstep_extras", "doorstep"},
			{"commerce-service", "doorstep_booking", "mstore"},
			{"food-service", "doorstep_extras", "feast"},
			{"dating-service", "doorstep_booking", "dating"},
			{"rider-service", "doorstep_extras", "mopedu"},
			// 010–013 unchanged.
			{"rider-service", "mopedu_ride", "mopedu"},
			{"", "mopedu_ride", "mopedu"},
			{"", "mopedu_subscription", "mopedu"},
			{"", "dating_premium", "dating"},
			{"food-service", "order", "feast"},
			{"", "order", "mstore"},
			{"", "food_order", "feast"},
			{"unknown", "demo_ref", ""},
			{"", "doorstep", ""}, // the application key is not a reference type
		} {
			got := scalar(`SELECT COALESCE(payments.legacy_application_for(NULLIF($1,''), $2), '')`, tc.owner, tc.ref)
			if got != tc.want {
				t.Errorf("legacy_application_for(%q, %q) = %q, want %q", tc.owner, tc.ref, got, tc.want)
			}
		}
	})
}

// requireTestDatabase refuses to run against anything but a *_test database:
// this test deletes and re-seeds a registry row (inside a rolled-back
// transaction, but on the real schema).
func requireTestDatabase(t *testing.T) {
	t.Helper()
	var name string
	if err := testPool.QueryRow(context.Background(), `SELECT current_database()`).Scan(&name); err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(name, "_test") {
		t.Fatalf("refusing to run against database %q: PAYMENTS_TEST_DSN must name a *_test database", name)
	}
}
