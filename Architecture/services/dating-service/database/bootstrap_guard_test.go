package database

import (
	"regexp"
	"strings"
	"testing"
)

// Audit 2026-10-30 M4: setup.sql runs at boot. Statements that take a heavy
// table lock unconditionally, or scan and row-lock a live table on every
// boot, must not come back.
func TestSetupSQLHasNoUnguardedHeavyDDL(t *testing.T) {
	sql := SetupSQL
	for _, banned := range []string{"DROP TRIGGER IF EXISTS", "DROP CONSTRAINT IF EXISTS"} {
		if strings.Contains(sql, banned) {
			t.Errorf("setup.sql runs %q on every boot; guard it with a catalog check (dating_ensure_check_constraint / pg_trigger)", banned)
		}
	}
	// A top-level write scans and row-locks a live table on every boot;
	// one-off backfills belong in a DO block behind an EXISTS guard.
	if m := regexp.MustCompile(`(?m)^(UPDATE|DELETE FROM|INSERT INTO) dating_\w+`).FindAllString(sql, -1); len(m) > 0 {
		t.Errorf("setup.sql has unguarded top-level writes: %v", m)
	}
	if regexp.MustCompile(`(?m)^CREATE TRIGGER`).MatchString(sql) {
		t.Errorf("setup.sql has an unguarded CREATE TRIGGER")
	}
	if regexp.MustCompile(`(?m)^ALTER TABLE \w+ ALTER COLUMN \w+ SET NOT NULL`).MatchString(sql) {
		t.Errorf("setup.sql sets NOT NULL unconditionally (ACCESS EXCLUSIVE + scan every boot)")
	}
	// Every dating_profiles CHECK the audit named goes through the guard.
	for _, name := range []string{
		"dating_profiles_status_chk", "dating_profiles_prior_status_chk",
		"dating_profiles_dob_source_chk", "dating_profiles_first_name_source_chk",
	} {
		if !strings.Contains(sql, "SELECT dating_ensure_check_constraint('dating_profiles', '"+name+"'") {
			t.Errorf("%s is not created through dating_ensure_check_constraint", name)
		}
	}
	// The guard adds NOT VALID; validation happens after the commit.
	if !strings.Contains(sql, "CHECK (%s) NOT VALID") {
		t.Errorf("dating_ensure_check_constraint must add constraints NOT VALID")
	}
}

func TestSchemaFingerprintTracksTheScript(t *testing.T) {
	a := SchemaFingerprint(SetupSQL)
	if a != SchemaFingerprint(SetupSQL) || len(a) != 64 {
		t.Fatalf("fingerprint is not a stable sha256: %q", a)
	}
	if a == SchemaFingerprint(SetupSQL+"\n-- changed") {
		t.Fatal("a changed script has the same fingerprint, so it would never be applied")
	}
	if BootstrapLockTimeout == "" || BootstrapLockTimeout == "0" {
		t.Fatal("the boot transaction must have a lock_timeout")
	}
}

func TestValidateConstraintSQLQuotesTheName(t *testing.T) {
	got := ValidateConstraintSQL("dating_profiles", `odd"name`)
	if got != `ALTER TABLE dating_profiles VALIDATE CONSTRAINT "odd""name"` {
		t.Fatalf("got %q", got)
	}
}
