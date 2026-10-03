package database

import (
	"io/fs"
	"strings"
	"testing"
)

func TestRequireTestDatabase(t *testing.T) {
	cases := []struct {
		dsn string
		ok  bool
	}{
		{"postgres://u:p@127.0.0.1:5432/doorstep_it_test?sslmode=disable", true},
		{"host=127.0.0.1 user=u password=p dbname=doorstep_it_test sslmode=disable", true},
		{"postgres://u:p@127.0.0.1:5432/app?sslmode=disable", false},
		{"postgres://u:p@127.0.0.1:5432/rider_it_test?sslmode=disable", false},
		{"postgres://u:p@127.0.0.1:5432/doorstep_it_test_old?sslmode=disable", false},
		{"postgres://u:p@127.0.0.1:5432/doorstep?sslmode=disable", false},
	}
	for _, c := range cases {
		err := RequireTestDatabase(c.dsn)
		if (err == nil) != c.ok {
			t.Errorf("RequireTestDatabase(%q) err=%v, want ok=%v", c.dsn, err, c.ok)
		}
	}
}

// The migrations are embedded and the first one creates the whole schema,
// including the calendar exclusion constraint later lanes rely on.
func TestMigrationsEmbedded(t *testing.T) {
	names, err := fs.Glob(Migrations, "migrations/*.sql")
	if err != nil || len(names) == 0 {
		t.Fatalf("no embedded migrations: %v", err)
	}
	body, err := fs.ReadFile(Migrations, "migrations/001_doorstep_schema.sql")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"CREATE EXTENSION IF NOT EXISTS btree_gist",
		"CREATE EXTENSION IF NOT EXISTS postgis",
		"EXCLUDE USING gist (pro_id WITH =, during WITH &&) WHERE (active)",
		"doorstep.outbox_events",
		"doorstep.identity_role_intents",
	} {
		if !strings.Contains(string(body), want) {
			t.Errorf("001 does not contain %q", want)
		}
	}
}
