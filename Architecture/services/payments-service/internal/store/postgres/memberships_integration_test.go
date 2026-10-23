//go:build integration

package postgres

// Migration 016, against a live PostgreSQL (payments_it_test only): the
// embedded file seeds the application `memberships` exactly once however
// often it runs, leaves an operator's later change alone, and extends the
// legacy mapping with monetization-service / creator_membership while keeping
// every branch 010–015 established.
//
// Everything runs in one transaction that is rolled back, so the shared
// registry is untouched. To prove the SEED (not a row left by an earlier
// apply) the existing `memberships` row is deleted first, with foreign-key
// triggers off for this transaction only (session_replication_role, which
// needs the superuser the dev postgres container provides).
//
//	PAYMENTS_TEST_DSN=.../payments_it_test go test -tags=integration ./internal/store/postgres/ -run Memberships -v -count=1

import (
	"context"
	"io/fs"
	"testing"

	"github.com/atpost/payments-service/database"
)

const membershipsMigration = "migrations/016_memberships_application.sql"

func TestMembershipsApplicationMigrationSeedsOnceAndMapsLegacyRows(t *testing.T) {
	ctx := context.Background()
	requireTestDatabase(t)
	sql, err := fs.ReadFile(database.Migrations, membershipsMigration)
	if err != nil {
		t.Fatalf("read embedded %s: %v", membershipsMigration, err)
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
	if _, err := tx.Exec(ctx, `DELETE FROM payments.applications WHERE key = 'memberships'`); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, `SET LOCAL session_replication_role = origin`); err != nil {
		t.Fatal(err)
	}
	others := scalar(`SELECT count(*)::text FROM payments.applications`)

	for run := 1; run <= 3; run++ {
		if _, err := tx.Exec(ctx, string(sql)); err != nil {
			t.Fatalf("run %d of %s: %v", run, membershipsMigration, err)
		}
		if n := scalar(`SELECT count(*)::text FROM payments.applications WHERE key = 'memberships'`); n != "1" {
			t.Fatalf("after run %d: memberships rows = %s, want 1", run, n)
		}
	}
	if got := scalar(`SELECT (count(*) - 1)::text FROM payments.applications`); got != others {
		t.Fatalf("016 touched other registry rows: %s before, %s after", others, got)
	}
	const row = `SELECT display_name || '|' || status || '|' || merchant_display_name || '|' ||
	                    array_to_string(ARRAY(SELECT unnest(enabled_methods) ORDER BY 1), ',') || '|' || settings::text
	               FROM payments.applications WHERE key = 'memberships'`
	if got := scalar(row); got != "Memberships|active|Momentum Merchant|card,upi|{}" {
		t.Fatalf("seeded memberships = %q", got)
	}

	t.Run("a re-run leaves an operator's change alone", func(t *testing.T) {
		if _, err := tx.Exec(ctx, `UPDATE payments.applications SET status = 'disabled', display_name = 'Operator renamed' WHERE key = 'memberships'`); err != nil {
			t.Fatal(err)
		}
		if _, err := tx.Exec(ctx, string(sql)); err != nil {
			t.Fatal(err)
		}
		if got := scalar(`SELECT display_name || '|' || status FROM payments.applications WHERE key = 'memberships'`); got != "Operator renamed|disabled" {
			t.Fatalf("after re-run = %q, want the operator's change kept", got)
		}
	})

	t.Run("legacy mapping: monetization-service and creator_membership map to memberships, the rest unchanged", func(t *testing.T) {
		for _, tc := range []struct{ owner, ref, want string }{
			{"monetization-service", "creator_membership", "memberships"},
			{"monetization-service", "food_order", "memberships"}, // owner wins
			{"", "creator_membership", "memberships"},
			{"legacy:creator_membership", "creator_membership", "memberships"},
			{"commerce-service", "creator_membership", "mstore"},
			{"food-service", "creator_membership", "feast"},
			{"doorstep-service", "creator_membership", "doorstep"},
			// 010–015 unchanged.
			{"doorstep-service", "doorstep_booking", "doorstep"},
			{"", "doorstep_extras", "doorstep"},
			{"rider-service", "mopedu_ride", "mopedu"},
			{"", "mopedu_subscription", "mopedu"},
			{"", "dating_premium", "dating"},
			{"food-service", "order", "feast"},
			{"", "order", "mstore"},
			{"", "food_order", "feast"},
			{"unknown", "demo_ref", ""},
			{"", "memberships", ""}, // the application key is not a reference type
		} {
			got := scalar(`SELECT COALESCE(payments.legacy_application_for(NULLIF($1,''), $2), '')`, tc.owner, tc.ref)
			if got != tc.want {
				t.Errorf("legacy_application_for(%q, %q) = %q, want %q", tc.owner, tc.ref, got, tc.want)
			}
		}
	})
}
