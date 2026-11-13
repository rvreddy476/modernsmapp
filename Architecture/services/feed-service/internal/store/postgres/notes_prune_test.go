package postgres

import (
	"os"
	"strings"
	"testing"
)

// The prune DELETE (service/notes_prune.go): bounded, and only the two
// kinds of row it exists for. The behaviour against a real database is
// CI's; these pin the SQL.
func TestPruneEchoNotesSQL(t *testing.T) {
	sql := strings.Join(strings.Fields(pruneEchoNotesSQL), " ")
	for _, want := range []string{
		"DELETE FROM echo_notes WHERE post_id IN (",
		"WHERE created_at < $1 OR (deleted AND deleted_at < $2)",
		"LIMIT $3)",
	} {
		if !strings.Contains(sql, want) {
			t.Errorf("prune SQL lacks %q:\n%s", want, sql)
		}
	}
}

// A redelivered PostDeleted must not restart the window, and a restore
// clears it.
func TestSetNoteDeletedKeepsTheFirstDeleteTime(t *testing.T) {
	raw, err := os.ReadFile("notes.go")
	if err != nil {
		t.Fatal(err)
	}
	src := strings.Join(strings.Fields(string(raw)), " ")
	if !strings.Contains(src, "deleted_at = CASE WHEN $2 THEN COALESCE(deleted_at, NOW()) ELSE NULL END") {
		t.Fatal("SetNoteDeleted does not keep the first delete time")
	}
}
