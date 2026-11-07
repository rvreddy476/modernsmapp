// DB-backed proof of migration 017: a group whose type is private or hidden
// but whose privacy_level was left public (rows from before G-L1) is made
// private, as CreateGroup now makes it, and a second run changes nothing.
// Requires TEST_PG_DSN on a "_test" database (group_it_test); skipped when
// unset. CI only.
package store

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/atpost/group-service/database"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestMigration017_PrivateGroupTypesArePrivate(t *testing.T) {
	dsn := os.Getenv("TEST_PG_DSN")
	if dsn == "" {
		t.Skip("TEST_PG_DSN not set; skipping group-service migration 017 integration test")
	}
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil || !strings.HasSuffix(cfg.ConnConfig.Database, "_test") {
		t.Fatal("TEST_PG_DSN must parse and name a database ending in _test")
	}
	ctx := context.Background()
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	if _, err := pool.Exec(ctx, database.SetupSQL); err != nil {
		t.Fatal(err)
	}
	// The columns the migration reads, as 001 and 004 add them. The full
	// chain is not applied here (002 alters post-service's posts).
	if _, err := pool.Exec(ctx, `
		ALTER TABLE groups ADD COLUMN IF NOT EXISTS privacy_level TEXT NOT NULL DEFAULT 'public';
		ALTER TABLE groups ADD COLUMN IF NOT EXISTS join_mode TEXT NOT NULL DEFAULT 'open';
		ALTER TABLE groups ADD COLUMN IF NOT EXISTS group_type TEXT NOT NULL DEFAULT 'public';
	`); err != nil {
		t.Fatal(err)
	}
	migration, err := database.Migrations.ReadFile("migrations/017_private_group_types_are_private.sql")
	if err != nil {
		t.Fatal(err)
	}

	type row struct {
		groupType, privacy, visibility, joinMode string
	}
	seed := map[string]row{
		"hidden, left public":       {"hidden", "public", "public", "open"},
		"private, left restricted":  {"private", "restricted", "public", "request"},
		"private, already private":  {"private", "private", "private", "invite_only"},
		"public stays public":       {"public", "public", "public", "open"},
		"study stays restricted":    {"study", "restricted", "public", "request"},
		"hidden, visibility behind": {"hidden", "private", "public", "invite_only"},
	}
	ids := map[string]uuid.UUID{}
	for name, r := range seed {
		var id uuid.UUID
		if err := pool.QueryRow(ctx,
			`INSERT INTO groups (name, creator_id, group_type, privacy_level, visibility, join_mode)
			 VALUES ($1, $2, $3, $4, $5, $6) RETURNING id`,
			"m017 "+name, uuid.New(), r.groupType, r.privacy, r.visibility, r.joinMode).Scan(&id); err != nil {
			t.Fatalf("seed %s: %v", name, err)
		}
		ids[name] = id
	}
	t.Cleanup(func() {
		for _, id := range ids {
			_, _ = pool.Exec(context.Background(), `DELETE FROM groups WHERE id = $1`, id)
		}
	})

	read := func(id uuid.UUID) row {
		var r row
		if err := pool.QueryRow(ctx,
			`SELECT group_type, privacy_level, visibility, join_mode FROM groups WHERE id = $1`, id).
			Scan(&r.groupType, &r.privacy, &r.visibility, &r.joinMode); err != nil {
			t.Fatal(err)
		}
		return r
	}

	for run := 1; run <= 2; run++ {
		tag, err := pool.Exec(ctx, string(migration))
		if err != nil {
			t.Fatalf("run %d: %v", run, err)
		}
		if run == 2 && tag.RowsAffected() != 0 {
			t.Fatalf("second run changed %d rows; the migration is not idempotent", tag.RowsAffected())
		}
		for name, before := range seed {
			got := read(ids[name])
			want := before
			if before.groupType == "private" || before.groupType == "hidden" {
				want = row{before.groupType, "private", "private", "invite_only"}
			}
			if got != want {
				t.Fatalf("run %d, %s: %+v, want %+v", run, name, got, want)
			}
		}
	}
}
