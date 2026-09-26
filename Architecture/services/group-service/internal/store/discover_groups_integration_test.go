//go:build integration

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

/*
DiscoverGroupsForUser against a real Postgres (*_test only). The regression
that matters: a viewer who belongs to nothing must get back an eligible
public group they are not a member of — the scan mismatch made every row
fail, so an empty result would have hidden the bug.

Run: GROUP_POSTGRES_DSN=…/group_it_test go test -tags integration ./internal/store/ -run DiscoverGroups -p 1
*/

type discoverFixture struct {
	pool   *pgxpool.Pool
	store  *Store
	viewer uuid.UUID
	owner  uuid.UUID
	groups []uuid.UUID
}

func newDiscoverFixture(t *testing.T) *discoverFixture {
	t.Helper()
	dsn := os.Getenv("GROUP_POSTGRES_DSN")
	if dsn == "" {
		t.Fatal("GROUP_POSTGRES_DSN is required")
	}
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(cfg.ConnConfig.Database, "_test") {
		t.Fatalf("refusing to run against database %q: integration tests only run against a *_test database", cfg.ConnConfig.Database)
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
	// Columns the discovery query reads that an older scratch schema lacks,
	// plus graph-service's connections table, which lives in the same `app`
	// database in every deployment but not in this scratch one.
	if _, err := pool.Exec(ctx, `
		ALTER TABLE groups ADD COLUMN IF NOT EXISTS privacy_level TEXT NOT NULL DEFAULT 'public';
		ALTER TABLE groups ADD COLUMN IF NOT EXISTS status TEXT NOT NULL DEFAULT 'active';
		ALTER TABLE groups ADD COLUMN IF NOT EXISTS handle TEXT;
		ALTER TABLE groups ADD COLUMN IF NOT EXISTS allow_anonymous_posts BOOLEAN NOT NULL DEFAULT FALSE;
		ALTER TABLE group_members ADD COLUMN IF NOT EXISTS status TEXT NOT NULL DEFAULT 'active';
		CREATE TABLE IF NOT EXISTS connections (
			user_a UUID NOT NULL, user_b UUID NOT NULL, created_at TIMESTAMPTZ DEFAULT NOW(),
			PRIMARY KEY (user_a, user_b)
		);
	`); err != nil {
		t.Fatal(err)
	}
	f := &discoverFixture{pool: pool, store: New(pool), viewer: uuid.New(), owner: uuid.New()}
	t.Cleanup(func() {
		pool.Exec(ctx, `DELETE FROM group_members WHERE group_id = ANY($1)`, f.groups)
		pool.Exec(ctx, `DELETE FROM groups WHERE id = ANY($1)`, f.groups)
	})
	return f
}

func (f *discoverFixture) group(t *testing.T, name, privacy, status string, anonymous bool) uuid.UUID {
	t.Helper()
	id := uuid.New()
	if _, err := f.pool.Exec(context.Background(),
		`INSERT INTO groups (id, name, description, creator_id, visibility, privacy_level, status, allow_anonymous_posts, member_count)
		 VALUES ($1, $2, '', $3, CASE WHEN $4 = 'private' THEN 'private' ELSE 'public' END, $4, $5, $6, 3)`,
		id, name+"-"+id.String()[:8], f.owner, privacy, status, anonymous); err != nil {
		t.Fatal(err)
	}
	f.groups = append(f.groups, id)
	return id
}

func TestDiscoverGroupsReturnsAnEligibleNonMemberGroup(t *testing.T) {
	f := newDiscoverFixture(t)
	ctx := context.Background()

	eligible := f.group(t, "discover-eligible", "public", "active", true)
	private := f.group(t, "discover-private", "private", "active", false)
	deleted := f.group(t, "discover-deleted", "public", "deleted", false)
	joined := f.group(t, "discover-joined", "public", "active", false)
	if _, err := f.pool.Exec(ctx,
		`INSERT INTO group_members (group_id, user_id, role) VALUES ($1, $2, 'member')`, joined, f.viewer); err != nil {
		t.Fatal(err)
	}

	got, err := f.store.DiscoverGroupsForUser(ctx, f.viewer, "", 50, 0)
	if err != nil {
		t.Fatalf("DiscoverGroupsForUser: %v", err)
	}
	byID := map[uuid.UUID]DiscoverScoredGroup{}
	for _, g := range got {
		byID[g.ID] = g
	}
	e, ok := byID[eligible]
	if !ok {
		t.Fatalf("the eligible public group is missing from discovery (got %d rows)", len(got))
	}
	if !e.AllowAnonymousPosts {
		t.Fatal("allow_anonymous_posts was selected but not carried into the result — the column is scanned into the wrong field")
	}
	if e.Score <= 0 {
		t.Fatalf("score = %d, want > 0 (member_count 3 alone scores)", e.Score)
	}
	for name, id := range map[string]uuid.UUID{"private": private, "deleted": deleted, "already joined": joined} {
		if _, present := byID[id]; present {
			t.Errorf("a %s group surfaced in discovery", name)
		}
	}
}
