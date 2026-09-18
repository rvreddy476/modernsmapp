//go:build integration

package postgres

// Migration 012, against a live PostgreSQL (payments_it_test): the embedded
// file seeds the application `mopedu` exactly once however often it runs,
// leaves an operator's later change alone, and extends the legacy mapping
// while keeping 011's branches.
//
// Everything runs in one transaction that is rolled back, so the shared
// registry is untouched. To prove the SEED (not a row left by an earlier
// apply) the existing `mopedu` row is deleted first, with foreign-key triggers
// off for this transaction only (session_replication_role, which needs the
// superuser the dev postgres container provides).
//
//	PAYMENTS_TEST_DSN=... go test -tags=integration ./internal/store/postgres/ -run Mopedu -v -count=1

import (
	"context"
	"io/fs"
	"testing"

	"github.com/atpost/payments-service/database"
)

const mopeduMigration = "migrations/012_mopedu_application.sql"

func TestMopeduApplicationMigrationSeedsOnceAndMapsLegacyRows(t *testing.T) {
	ctx := context.Background()
	sql, err := fs.ReadFile(database.Migrations, mopeduMigration)
	if err != nil {
		t.Fatalf("read embedded %s: %v", mopeduMigration, err)
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
	if _, err := tx.Exec(ctx, `DELETE FROM payments.applications WHERE key = 'mopedu'`); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, `SET LOCAL session_replication_role = origin`); err != nil {
		t.Fatal(err)
	}

	for run := 1; run <= 3; run++ {
		if _, err := tx.Exec(ctx, string(sql)); err != nil {
			t.Fatalf("run %d of %s: %v", run, mopeduMigration, err)
		}
		if n := scalar(`SELECT count(*)::text FROM payments.applications WHERE key = 'mopedu'`); n != "1" {
			t.Fatalf("after run %d: mopedu rows = %s, want 1", run, n)
		}
	}
	const row = `SELECT display_name || '|' || status || '|' || merchant_display_name || '|' ||
	                    array_to_string(ARRAY(SELECT unnest(enabled_methods) ORDER BY 1), ',') || '|' || settings::text
	               FROM payments.applications WHERE key = 'mopedu'`
	if got := scalar(row); got != "Mopedu|active|Momentum Merchant|card,upi|{}" {
		t.Fatalf("seeded mopedu = %q", got)
	}

	t.Run("a re-run leaves an operator's change alone", func(t *testing.T) {
		if _, err := tx.Exec(ctx, `UPDATE payments.applications SET status = 'disabled', display_name = 'Operator renamed' WHERE key = 'mopedu'`); err != nil {
			t.Fatal(err)
		}
		if _, err := tx.Exec(ctx, string(sql)); err != nil {
			t.Fatal(err)
		}
		if got := scalar(`SELECT display_name || '|' || status FROM payments.applications WHERE key = 'mopedu'`); got != "Operator renamed|disabled" {
			t.Fatalf("after re-run = %q, want the operator's change kept", got)
		}
	})

	t.Run("legacy mapping: rider-service and mopedu_ride map to mopedu, the rest unchanged", func(t *testing.T) {
		for _, tc := range []struct{ owner, ref, want string }{
			{"rider-service", "mopedu_ride", "mopedu"},
			{"rider-service", "food_order", "mopedu"}, // owner wins
			{"", "mopedu_ride", "mopedu"},
			{"legacy:mopedu_ride", "mopedu_ride", "mopedu"},
			{"commerce-service", "mopedu_ride", "mstore"},
			{"dating-service", "mopedu_ride", "dating"},
			{"dating-service", "dating_premium", "dating"},
			{"", "dating_premium", "dating"},
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

const mopeduSubscriptionMigration = "migrations/013_mopedu_subscription_reftype.sql"

// Migration 013 (mopedu_subscription, a captain's plan period): applied after
// 012, re-runnable, maps the new reference type to mopedu and keeps every
// branch 010–012 established. It seeds nothing: the registry row is 012's.
func TestMopeduSubscriptionMigrationExtendsLegacyMapping(t *testing.T) {
	ctx := context.Background()
	base, err := fs.ReadFile(database.Migrations, mopeduMigration)
	if err != nil {
		t.Fatalf("read embedded %s: %v", mopeduMigration, err)
	}
	sql, err := fs.ReadFile(database.Migrations, mopeduSubscriptionMigration)
	if err != nil {
		t.Fatalf("read embedded %s: %v", mopeduSubscriptionMigration, err)
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

	if _, err := tx.Exec(ctx, string(base)); err != nil {
		t.Fatalf("%s: %v", mopeduMigration, err)
	}
	apps := scalar(`SELECT count(*)::text FROM payments.applications`)
	for run := 1; run <= 3; run++ {
		if _, err := tx.Exec(ctx, string(sql)); err != nil {
			t.Fatalf("run %d of %s: %v", run, mopeduSubscriptionMigration, err)
		}
	}
	if got := scalar(`SELECT count(*)::text FROM payments.applications`); got != apps {
		t.Fatalf("013 changed the registry: %s rows before, %s after", apps, got)
	}

	for _, tc := range []struct{ owner, ref, want string }{
		{"rider-service", "mopedu_subscription", "mopedu"},
		{"rider-service", "mopedu_ride", "mopedu"},
		{"rider-service", "food_order", "mopedu"}, // owner wins
		{"", "mopedu_subscription", "mopedu"},
		{"", "mopedu_ride", "mopedu"},
		{"legacy:mopedu_subscription", "mopedu_subscription", "mopedu"},
		{"commerce-service", "mopedu_subscription", "mstore"},
		{"food-service", "mopedu_subscription", "feast"},
		{"dating-service", "mopedu_subscription", "dating"},
		{"", "dating_premium", "dating"},
		{"", "order", "mstore"},
		{"", "food_order", "feast"},
		{"unknown", "demo_ref", ""},
	} {
		got := scalar(`SELECT COALESCE(payments.legacy_application_for(NULLIF($1,''), $2), '')`, tc.owner, tc.ref)
		if got != tc.want {
			t.Errorf("legacy_application_for(%q, %q) = %q, want %q", tc.owner, tc.ref, got, tc.want)
		}
	}
}
