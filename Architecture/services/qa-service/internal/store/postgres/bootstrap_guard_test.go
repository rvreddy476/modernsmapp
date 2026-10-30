package postgres

import (
	"io/fs"
	"regexp"
	"strings"
	"testing"

	"github.com/atpost/qa-service/database"
	"github.com/atpost/shared/outbox"
)

// Audit 2026-10-30 M5: setup.sql may run at boot. Statements that take a
// heavy table lock unconditionally, or scan a live table on every apply,
// must not come back.
func TestSetupSQLHasNoUnguardedHeavyDDL(t *testing.T) {
	sql := database.SetupSQL
	for _, banned := range []string{"DROP TRIGGER IF EXISTS", "DROP CONSTRAINT IF EXISTS"} {
		if strings.Contains(sql, banned) {
			t.Errorf("setup.sql runs %q; guard it with a catalog check (qa_ensure_check_constraint)", banned)
		}
	}
	// A top-level ALTER TABLE ... ADD CONSTRAINT re-validates every row under
	// ACCESS EXCLUSIVE; constraints go through qa_ensure_check_constraint.
	if m := regexp.MustCompile(`(?mi)^\s*ALTER TABLE[^;]*\bADD CONSTRAINT\b`).FindAllString(sql, -1); len(m) > 0 {
		t.Errorf("setup.sql adds constraints unguarded: %v", m)
	}
	// Columns on the hot tables go through qa_ensure_column.
	if m := regexp.MustCompile(`(?mi)^\s*ALTER TABLE\s+(questions|answers|topics|answer_comments)\b[^;]*ADD COLUMN`).FindAllString(sql, -1); len(m) > 0 {
		t.Errorf("setup.sql adds columns to a hot table unguarded: %v", m)
	}
	// A top-level write scans and row-locks a live table on every apply.
	if m := regexp.MustCompile(`(?mi)^\s*(UPDATE|DELETE FROM|INSERT INTO)\s+\w+`).FindAllString(sql, -1); len(m) > 0 {
		t.Errorf("setup.sql has top-level writes: %v", m)
	}
	if regexp.MustCompile(`(?mi)^\s*CREATE TRIGGER`).MatchString(sql) {
		t.Errorf("setup.sql has an unguarded CREATE TRIGGER")
	}
	if regexp.MustCompile(`(?mi)SET NOT NULL`).MatchString(sql) {
		t.Errorf("setup.sql sets NOT NULL (ACCESS EXCLUSIVE + scan)")
	}
	if regexp.MustCompile(`(?mi)CREATE INDEX CONCURRENTLY`).MatchString(sql) {
		t.Errorf("CONCURRENTLY cannot run in the boot transaction; list it in database.ConcurrentIndexes")
	}
	if !strings.Contains(sql, "SELECT qa_ensure_check_constraint('questions', 'questions_status_check'") {
		t.Errorf("questions_status_check is not created through qa_ensure_check_constraint")
	}
	if !strings.Contains(sql, "CHECK (%s) NOT VALID") {
		t.Errorf("qa_ensure_check_constraint must add constraints NOT VALID")
	}
	if !strings.Contains(sql, "WHERE attrelid = p_table AND attname = p_column AND NOT attisdropped") {
		t.Errorf("qa_ensure_column must check the catalog before ALTER TABLE")
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

func TestValidateConstraintSQLQuotesTheName(t *testing.T) {
	got := ValidateConstraintSQL("questions", `odd"name`)
	if got != `ALTER TABLE questions VALIDATE CONSTRAINT "odd""name"` {
		t.Fatalf("got %q", got)
	}
}

func TestConcurrentIndexesAreValid(t *testing.T) {
	if err := ValidateConcurrentIndexes(database.ConcurrentIndexes); err != nil {
		t.Fatal(err)
	}
	for _, bad := range [][]database.ConcurrentIndex{
		{{Name: "x", SQL: "CREATE INDEX x ON questions (id)"}},
		{{Name: "x", SQL: "CREATE INDEX CONCURRENTLY IF NOT EXISTS y ON questions (id)"}},
		{{Name: "x", SQL: "CREATE INDEX CONCURRENTLY IF NOT EXISTS x ON questions (id); DROP TABLE questions"}},
		{{Name: "Bad-Name", SQL: "CREATE INDEX CONCURRENTLY IF NOT EXISTS Bad-Name ON questions (id)"}},
		{
			{Name: "x", SQL: "CREATE INDEX CONCURRENTLY IF NOT EXISTS x ON questions (id)"},
			{Name: "x", SQL: "CREATE INDEX CONCURRENTLY IF NOT EXISTS x ON questions (id)"},
		},
	} {
		if ValidateConcurrentIndexes(bad) == nil {
			t.Errorf("accepted %+v", bad)
		}
	}
}

// The search queries must use the indexed expressions verbatim.
func TestSearchIndexesMatchTheQueryExpressions(t *testing.T) {
	unaliased := func(expr string) string { return strings.ReplaceAll(expr, "%[1]s.", "") }
	byName := map[string]string{}
	for _, ix := range database.ConcurrentIndexes {
		byName[ix.Name] = ix.SQL
	}
	if !strings.Contains(byName["idx_questions_search_tsv"], "(("+unaliased(database.SearchDocumentSQL)+"))") {
		t.Errorf("idx_questions_search_tsv does not index SearchDocumentSQL")
	}
	if !strings.Contains(byName["idx_questions_title_tsv"], "(("+unaliased(database.TitleDocumentSQL)+"))") {
		t.Errorf("idx_questions_title_tsv does not index TitleDocumentSQL")
	}
	if !strings.Contains(byName["idx_question_follows_question"], "question_follows (question_id)") {
		t.Errorf("question_follows has no question_id index (M7)")
	}
}

// The outbox table has every claim column shared/outbox leases with, and
// the migrations are numbered and non-empty.
func TestMigrations(t *testing.T) {
	names, err := fs.Glob(database.Migrations, "migrations/*.sql")
	if err != nil || len(names) == 0 {
		t.Fatalf("no migrations embedded: %v", err)
	}
	for _, n := range names {
		if !regexp.MustCompile(`^migrations/\d{3}_[a-z0-9_]+\.sql$`).MatchString(n) {
			t.Errorf("migration %q is not NNN_name.sql", n)
		}
	}
	body, err := fs.ReadFile(database.Migrations, "migrations/001_qa_outbox_events.sql")
	if err != nil {
		t.Fatal(err)
	}
	for _, col := range append([]string{"event_type", "partition_key", "payload", "published_at"}, outbox.ClaimColumns...) {
		if !strings.Contains(string(body), "    "+col+" ") {
			t.Errorf("qa.outbox_events lacks %s", col)
		}
	}
	if !strings.Contains(string(body), "CREATE TABLE IF NOT EXISTS qa.outbox_events") {
		t.Errorf("the outbox is not qa.outbox_events")
	}
}
