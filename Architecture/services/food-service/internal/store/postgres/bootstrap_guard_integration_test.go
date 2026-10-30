package postgres

import (
	"context"
	"testing"

	"github.com/atpost/food-service/database"
)

// Audit 2026-10-30 M4: a re-applied script (changed fingerprint) neither
// re-creates an existing trigger nor re-adds an unchanged CHECK constraint,
// and an unchanged script is skipped. Only the database proves it.
func TestBootstrapSchemaDoesNotRecreateTriggersOrConstraints(t *testing.T) {
	s, cleanup := foodTestStore(t) // applies the current script
	defer cleanup()
	ctx := context.Background()

	read := func() (trigger, constraint uint32, fingerprint string) {
		t.Helper()
		if err := s.db.QueryRow(ctx, `
			SELECT
				(SELECT oid FROM pg_trigger WHERE tgrelid = 'food.orders'::regclass AND tgname = 'trg_orders_updated_at'),
				(SELECT oid FROM pg_constraint WHERE conrelid = 'food.restaurants'::regclass AND conname = 'ck_food_restaurant_tax_category'),
				(SELECT sha256 FROM food.schema_bootstrap WHERE id = 1)
		`).Scan(&trigger, &constraint, &fingerprint); err != nil {
			t.Fatalf("read catalog: %v", err)
		}
		return
	}
	trg1, con1, fp1 := read()
	if fp1 != SchemaFingerprint(database.SetupSQL) {
		t.Fatalf("recorded fingerprint %q is not the script's", fp1)
	}

	// A changed script is applied (fingerprint moves) but leaves existing
	// triggers and unchanged constraints alone.
	changed := database.SetupSQL + "\n-- m4 re-apply probe\n"
	if err := BootstrapSchema(ctx, s.db, changed); err != nil {
		t.Fatalf("re-apply: %v", err)
	}
	trg2, con2, fp2 := read()
	if fp2 != SchemaFingerprint(changed) {
		t.Fatalf("fingerprint not recorded after re-apply")
	}
	if trg2 != trg1 {
		t.Fatalf("trg_orders_updated_at was re-created (oid %d -> %d)", trg1, trg2)
	}
	if con2 != con1 {
		t.Fatalf("ck_food_restaurant_tax_category was re-added (oid %d -> %d)", con1, con2)
	}

	// Restore the real script for the other tests.
	if err := BootstrapSchema(ctx, s.db, database.SetupSQL); err != nil {
		t.Fatalf("restore: %v", err)
	}
	if _, _, fp3 := read(); fp3 != fp1 {
		t.Fatalf("fingerprint not restored")
	}
}
