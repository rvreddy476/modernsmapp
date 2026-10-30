package postgres

import (
	"regexp"
	"strings"
	"testing"

	"github.com/atpost/food-service/database"
)

// Audit 2026-10-30 M4: setup.sql runs at boot. Statements that take a heavy
// table lock unconditionally must not come back.
func TestSetupSQLHasNoUnguardedHeavyDDL(t *testing.T) {
	sql := database.SetupSQL
	for _, banned := range []string{"DROP TRIGGER IF EXISTS", "DROP CONSTRAINT IF EXISTS"} {
		if strings.Contains(sql, banned) {
			t.Errorf("setup.sql runs %q on every boot; guard it with a catalog check (food.ensure_updated_at_trigger / food.ensure_check_constraint)", banned)
		}
	}
	// A top-level UPDATE or DELETE scans and row-locks a live table on every
	// boot; one-off backfills belong in a DO block behind an EXISTS guard.
	if m := regexp.MustCompile(`(?m)^(UPDATE|DELETE FROM) food\.\w+`).FindAllString(sql, -1); len(m) > 0 {
		t.Errorf("setup.sql has unguarded top-level writes: %v", m)
	}
	// Every updated_at trigger is created through the guard.
	if n := strings.Count(sql, "SELECT food.ensure_updated_at_trigger("); n < 20 {
		t.Errorf("only %d guarded updated_at triggers; expected the 20 setup.sql had", n)
	}
	if regexp.MustCompile(`(?m)^CREATE TRIGGER`).MatchString(sql) {
		t.Errorf("setup.sql has an unguarded CREATE TRIGGER")
	}
}

func TestSchemaFingerprintTracksTheScript(t *testing.T) {
	a := SchemaFingerprint(database.SetupSQL)
	if a != SchemaFingerprint(database.SetupSQL) || len(a) != 64 {
		t.Fatalf("fingerprint is not a stable sha256: %q", a)
	}
	if a == SchemaFingerprint(database.SetupSQL+"\n-- changed") {
		t.Fatal("a changed script has the same fingerprint, so it would never be applied")
	}
	if BootstrapLockTimeout == "" || BootstrapLockTimeout == "0" {
		t.Fatal("the boot transaction must have a lock_timeout")
	}
}
