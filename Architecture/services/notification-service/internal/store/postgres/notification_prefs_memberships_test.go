package postgres

import (
	"io/fs"
	"strings"
	"testing"

	"github.com/atpost/notification-service/database"
)

// Paid memberships (migration 018): the two toggles are read, written and
// defaulted in the one shared column order, and the migration adds both
// columns defaulting on.

func notifPrefsColumnIndex(t *testing.T, col string) int {
	t.Helper()
	for i, c := range strings.Split(notifPrefsColumns, ",") {
		if strings.TrimSpace(c) == col {
			return i
		}
	}
	t.Fatalf("%s is not in notifPrefsColumns", col)
	return -1
}

func TestNotifPrefsColumnsCarryMemberships(t *testing.T) {
	var p NotificationPreferences
	scan := scanNotifPrefs(&p)
	cols := strings.Split(notifPrefsColumns, ",")
	if len(scan) != len(cols) || len(notifPrefsValues(&p)) != len(cols) {
		t.Fatalf("columns %d, scan %d, values %d", len(cols), len(scan), len(notifPrefsValues(&p)))
	}
	for col, field := range map[string]*bool{"push_memberships": &p.PushMemberships, "inapp_memberships": &p.InappMemberships} {
		i := notifPrefsColumnIndex(t, col)
		if got, ok := scan[i].(*bool); !ok || got != field {
			t.Errorf("%s scans into the wrong field", col)
		}
		*field = true
		if v, ok := notifPrefsValues(&p)[i].(bool); !ok || !v {
			t.Errorf("%s binds the wrong value", col)
		}
		*field = false
		if !strings.Contains(notifPrefsUpsertSQL, col+" = $") {
			t.Errorf("upsert does not update %s", col)
		}
	}
	d := DefaultNotificationPreferences("u")
	if !d.PushMemberships || !d.InappMemberships {
		t.Fatal("memberships toggles must default on")
	}
}

func TestMigration018AddsMembershipsColumnsDefaultOn(t *testing.T) {
	raw, err := fs.ReadFile(database.Migrations, "migrations/018_memberships_preference.sql")
	if err != nil {
		t.Fatal(err)
	}
	sql := string(raw)
	for _, col := range []string{"push_memberships", "inapp_memberships"} {
		want := "ADD COLUMN IF NOT EXISTS " + col
		i := strings.Index(sql, want)
		if i < 0 {
			t.Fatalf("migration 018 does not add %s", col)
		}
		line := sql[i:]
		if j := strings.IndexByte(line, ';'); j >= 0 {
			line = line[:j]
		}
		if !strings.Contains(line, "BOOLEAN NOT NULL DEFAULT TRUE") {
			t.Errorf("%s: %q, want BOOLEAN NOT NULL DEFAULT TRUE", col, line)
		}
	}
}
