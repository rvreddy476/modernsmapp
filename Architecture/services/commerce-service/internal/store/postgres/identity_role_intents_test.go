package postgres

import (
	"strings"
	"testing"

	"github.com/atpost/commerce-service/database"
	"github.com/atpost/shared/identityroles"
)

// TestMigrationMatchesSharedSchema guards the one copy Go cannot import.
//
// shared/identityroles.SchemaSQL("") is the single definition of the intent
// queue; migration 032 is a hand-written copy of it, because commerce applies
// DDL through a numbered migration runner that reads .sql files. If someone
// changes the shared definition and forgets the migration — or the reverse —
// the worker's queries stop matching the table and role grants stop being
// delivered, silently. This fails first instead.
func TestMigrationMatchesSharedSchema(t *testing.T) {
	raw, err := database.Migrations.ReadFile("migrations/032_identity_role_intents.sql")
	if err != nil {
		t.Fatalf("read migration: %v", err)
	}
	got := normalizeSQL(string(raw))
	want := normalizeSQL(identityroles.SchemaSQL(""))
	if !strings.Contains(got, want) {
		t.Fatalf("migration 032 has drifted from identityroles.SchemaSQL(\"\").\n"+
			"shared wants:\n%s\n\nmigration has:\n%s", want, got)
	}
}

// normalizeSQL strips SQL comments and collapses whitespace so the comparison
// is about the DDL, not about how the two files are laid out.
func normalizeSQL(s string) string {
	var b strings.Builder
	for _, line := range strings.Split(s, "\n") {
		if i := strings.Index(line, "--"); i >= 0 {
			line = line[:i]
		}
		if t := strings.TrimSpace(line); t != "" {
			b.WriteString(t)
			b.WriteString(" ")
		}
	}
	return strings.Join(strings.Fields(b.String()), " ")
}
