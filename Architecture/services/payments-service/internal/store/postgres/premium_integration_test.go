//go:build integration

package postgres

// Migration 019, against a live PostgreSQL (payments_it_test only): the
// embedded file seeds the application `premium` exactly once however often
// it runs, leaves an operator's later change alone, and maps
// monetization-service by reference type (platform_premium → premium, the rest
// → memberships) while keeping every earlier branch. One rolled-back
// transaction, as the 016 test.
//
//	PAYMENTS_TEST_DSN=.../payments_it_test go test -tags=integration ./internal/store/postgres/ -run Premium -v -count=1

import (
	"context"
	"io/fs"
	"testing"

	"github.com/atpost/payments-service/database"
)

const premiumMigration = "migrations/019_premium_application.sql"

func TestPremiumApplicationMigrationSeedsOnceAndMapsLegacyRows(t *testing.T) {
	ctx := context.Background()
	requireTestDatabase(t)
	sql, err := fs.ReadFile(database.Migrations, premiumMigration)
	if err != nil {
		t.Fatalf("read embedded %s: %v", premiumMigration, err)
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
	if _, err := tx.Exec(ctx, `DELETE FROM payments.applications WHERE key = 'premium'`); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, `SET LOCAL session_replication_role = origin`); err != nil {
		t.Fatal(err)
	}
	for run := 1; run <= 3; run++ {
		if _, err := tx.Exec(ctx, string(sql)); err != nil {
			t.Fatalf("run %d of %s: %v", run, premiumMigration, err)
		}
		if n := scalar(`SELECT count(*)::text FROM payments.applications WHERE key = 'premium'`); n != "1" {
			t.Fatalf("after run %d: premium rows = %s, want 1", run, n)
		}
	}
	const row = `SELECT display_name || '|' || status || '|' || merchant_display_name || '|' ||
	                    array_to_string(ARRAY(SELECT unnest(enabled_methods) ORDER BY 1), ',')
	               FROM payments.applications WHERE key = 'premium'`
	if got := scalar(row); got != "Momentum Premium|active|Momentum Merchant|card,upi" {
		t.Fatalf("seeded premium = %q", got)
	}
	if _, err := tx.Exec(ctx, `UPDATE payments.applications SET status = 'disabled' WHERE key = 'premium'`); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, string(sql)); err != nil {
		t.Fatal(err)
	}
	if got := scalar(`SELECT status FROM payments.applications WHERE key = 'premium'`); got != "disabled" {
		t.Fatalf("a re-run overwrote the operator's change: %q", got)
	}
	for _, tc := range []struct{ owner, ref, want string }{
		{"monetization-service", "platform_premium", "premium"},
		{"monetization-service", "creator_membership", "memberships"},
		{"monetization-service", "food_order", "memberships"},
		{"", "platform_premium", "premium"},
		{"", "creator_membership", "memberships"},
		{"commerce-service", "platform_premium", "mstore"},
		{"doorstep-service", "doorstep_booking", "doorstep"},
		{"", "dating_premium", "dating"},
		{"", "order", "mstore"},
		{"unknown", "demo_ref", ""},
		{"", "premium", ""},
	} {
		got := scalar(`SELECT COALESCE(payments.legacy_application_for(NULLIF($1,''), $2), '')`, tc.owner, tc.ref)
		if got != tc.want {
			t.Errorf("legacy_application_for(%q, %q) = %q, want %q", tc.owner, tc.ref, got, tc.want)
		}
	}
}
