//go:build integration

package store

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/atpost/group-service/database"
)

/*
	Regression for GET /v1/groups/my answering 500.

	Observed 2026-09-25 18:17Z on dev for one account: /my → 500 in 1 ms
	while GET /:groupId, /members, /media and /feed/v2 on that account's
	groups all answered 200. The query itself ran fine in psql and returned
	three rows. The failure was in the scan: one of the three groups had a
	NULL handle (a row written straight to the table, not through
	CreateGroup, which always writes a value), and Group.Handle is a plain
	string, which pgx refuses to fill from NULL. Every member of that one
	group lost their whole list.

	This file reproduces exactly that shape against a scratch database and
	pins the other list invariants the endpoint promises: banned and removed
	memberships stay out, deleted groups stay out, an account with nothing
	gets an empty slice, and the pagination contract is what the sidebar
	will be told it is.

	Run: GROUP_POSTGRES_DSN=postgres://…/group_it_test go test -tags integration ./internal/store/ -run ListGroupsByUser
*/

type listFixture struct {
	pool   *pgxpool.Pool
	store  *Store
	viewer uuid.UUID
	groups []uuid.UUID
}

func newListFixture(t *testing.T) (*listFixture, func()) {
	t.Helper()
	dsn := os.Getenv("GROUP_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("GROUP_POSTGRES_DSN is not set")
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
	if _, err := pool.Exec(ctx, database.SetupSQL); err != nil {
		pool.Close()
		t.Fatal(err)
	}
	/*
		Bring the scratch schema forward to what groupColumns selects.

		The real boot path (BootstrapSchema: SetupSQL, then every embedded
		migration) cannot run here: migration 002 references post-service's
		`posts` table, which exists only in the shared application database.
		So, like group_post_viewer_flags_integration_test.go, the columns
		later migrations added are patched in directly. Every statement is a
		no-op on an up-to-date database. Types mirror the live schema.
	*/
	if _, err := pool.Exec(ctx, `
		ALTER TABLE groups ADD COLUMN IF NOT EXISTS handle TEXT;
		ALTER TABLE groups ADD COLUMN IF NOT EXISTS category TEXT NOT NULL DEFAULT '';
		ALTER TABLE groups ADD COLUMN IF NOT EXISTS privacy_level TEXT NOT NULL DEFAULT 'public';
		ALTER TABLE groups ADD COLUMN IF NOT EXISTS join_mode TEXT NOT NULL DEFAULT 'open';
		ALTER TABLE groups ADD COLUMN IF NOT EXISTS who_can_post TEXT NOT NULL DEFAULT 'all_members';
		ALTER TABLE groups ADD COLUMN IF NOT EXISTS who_can_invite TEXT NOT NULL DEFAULT 'all_members';
		ALTER TABLE groups ADD COLUMN IF NOT EXISTS location TEXT NOT NULL DEFAULT '';
		ALTER TABLE groups ADD COLUMN IF NOT EXISTS language TEXT NOT NULL DEFAULT '';
		ALTER TABLE groups ADD COLUMN IF NOT EXISTS status TEXT NOT NULL DEFAULT 'active';
		ALTER TABLE groups ADD COLUMN IF NOT EXISTS deleted_at TIMESTAMPTZ;
		ALTER TABLE groups ADD COLUMN IF NOT EXISTS pending_request_count INTEGER NOT NULL DEFAULT 0;
		ALTER TABLE groups ADD COLUMN IF NOT EXISTS group_type TEXT NOT NULL DEFAULT 'public';
		ALTER TABLE groups ADD COLUMN IF NOT EXISTS max_members INTEGER NOT NULL DEFAULT 0;
		ALTER TABLE groups ADD COLUMN IF NOT EXISTS join_questions JSONB NOT NULL DEFAULT '[]';
		ALTER TABLE groups ADD COLUMN IF NOT EXISTS topic_tags TEXT[] NOT NULL DEFAULT '{}';
		ALTER TABLE groups ADD COLUMN IF NOT EXISTS comment_permission TEXT NOT NULL DEFAULT 'all_members';
		ALTER TABLE groups ADD COLUMN IF NOT EXISTS member_list_visible BOOLEAN NOT NULL DEFAULT TRUE;
		ALTER TABLE groups ADD COLUMN IF NOT EXISTS link_sharing BOOLEAN NOT NULL DEFAULT TRUE;
		ALTER TABLE groups ADD COLUMN IF NOT EXISTS is_mature BOOLEAN NOT NULL DEFAULT FALSE;
		ALTER TABLE groups ADD COLUMN IF NOT EXISTS allow_anonymous_posts BOOLEAN NOT NULL DEFAULT FALSE;
		ALTER TABLE group_members ADD COLUMN IF NOT EXISTS status TEXT NOT NULL DEFAULT 'active';
	`); err != nil {
		pool.Close()
		t.Fatal(err)
	}
	f := &listFixture{pool: pool, store: New(pool), viewer: uuid.New()}
	cleanup := func() {
		for _, id := range f.groups {
			_, _ = pool.Exec(ctx, `DELETE FROM group_members WHERE group_id = $1`, id)
			_, _ = pool.Exec(ctx, `DELETE FROM groups WHERE id = $1`, id)
		}
		pool.Close()
	}
	return f, cleanup
}

// newGroup inserts a group the way a direct SQL seed would — handle
// nullable — and records it for cleanup. handle == nil writes NULL.
func (f *listFixture) newGroup(t *testing.T, name string, handle *string, status string) uuid.UUID {
	t.Helper()
	id := uuid.New()
	_, err := f.pool.Exec(context.Background(), `
		INSERT INTO groups (id, name, description, creator_id, visibility, handle, status)
		VALUES ($1, $2, '', $3, 'public', $4, $5)`,
		id, name, f.viewer, handle, status)
	if err != nil {
		t.Fatalf("insert group %s: %v", name, err)
	}
	f.groups = append(f.groups, id)
	return id
}

func (f *listFixture) join(t *testing.T, groupID uuid.UUID, status string) {
	t.Helper()
	_, err := f.pool.Exec(context.Background(), `
		INSERT INTO group_members (group_id, user_id, role, status) VALUES ($1, $2, 'member', $3)`,
		groupID, f.viewer, status)
	if err != nil {
		t.Fatalf("insert membership: %v", err)
	}
}

func str(s string) *string { return &s }

// The reproduction. Before the COALESCE in groupColumns this fails with
// "can't scan into dest[13]: cannot scan NULL into *string" — the exact
// error GET /v1/groups/my turned into a 500.
func TestListGroupsByUserSurvivesANullHandle(t *testing.T) {
	f, cleanup := newListFixture(t)
	defer cleanup()
	ctx := context.Background()

	withHandle := f.newGroup(t, "has-handle-"+f.viewer.String()[:8], str("h-"+f.viewer.String()[:8]), "active")
	f.join(t, withHandle, "active")
	noHandle := f.newGroup(t, "no-handle-"+f.viewer.String()[:8], nil, "active")
	f.join(t, noHandle, "active")

	got, err := f.store.ListGroupsByUser(ctx, f.viewer, 20, 0)
	if err != nil {
		t.Fatalf("ListGroupsByUser failed on a NULL handle — this is the GET /v1/groups/my 500: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d groups, want 2 (a NULL handle must not drop the row either)", len(got))
	}
	for _, g := range got {
		if g.ID == noHandle && g.Handle != "" {
			t.Fatalf("NULL handle scanned as %q, want empty string", g.Handle)
		}
	}

	// The single-row path scans the same projection and must agree.
	one, err := f.store.GetGroupByID(ctx, noHandle)
	if err != nil {
		t.Fatalf("GetGroupByID on a NULL-handle group: %v", err)
	}
	if one == nil || one.Handle != "" {
		t.Fatalf("GetGroupByID: got %+v, want the group with an empty handle", one)
	}
}

// Acceptance 3: what must NOT come back.
func TestListGroupsByUserExcludesBannedRemovedAndDeleted(t *testing.T) {
	f, cleanup := newListFixture(t)
	defer cleanup()
	ctx := context.Background()

	visible := f.newGroup(t, "visible-"+f.viewer.String()[:8], str("v-"+f.viewer.String()[:8]), "active")
	f.join(t, visible, "active")
	banned := f.newGroup(t, "banned-"+f.viewer.String()[:8], str("b-"+f.viewer.String()[:8]), "active")
	f.join(t, banned, "banned")
	removed := f.newGroup(t, "removed-"+f.viewer.String()[:8], str("r-"+f.viewer.String()[:8]), "active")
	f.join(t, removed, "removed")
	deleted := f.newGroup(t, "deleted-"+f.viewer.String()[:8], str("d-"+f.viewer.String()[:8]), "deleted")
	f.join(t, deleted, "active")

	got, err := f.store.ListGroupsByUser(ctx, f.viewer, 20, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].ID != visible {
		ids := make([]string, 0, len(got))
		for _, g := range got {
			ids = append(ids, g.Name)
		}
		t.Fatalf("want only the active membership of an active group, got %v", ids)
	}
}

// Acceptance 2: an account with nothing is an empty list, not an error.
func TestListGroupsByUserEmptyAccount(t *testing.T) {
	f, cleanup := newListFixture(t)
	defer cleanup()
	got, err := f.store.ListGroupsByUser(context.Background(), uuid.New(), 20, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("got %d groups for a fresh account, want 0", len(got))
	}
}

// Acceptance 5: the pagination contract, as it actually behaves. limit
// defaults to 20 and is capped at 100 — but a limit ABOVE 100 falls back
// to 20, it is not clamped to 100. There is no total and no has_more: the
// only way a client knows there is another page is to ask for it, or to
// treat a full page as "maybe more".
func TestListGroupsByUserPaginationContract(t *testing.T) {
	f, cleanup := newListFixture(t)
	defer cleanup()
	ctx := context.Background()
	const n = 23
	for i := 0; i < n; i++ {
		id := f.newGroup(t, "page-"+uuid.New().String()[:8], str("p-"+uuid.New().String()[:8]), "active")
		f.join(t, id, "active")
	}

	first, err := f.store.ListGroupsByUser(ctx, f.viewer, 0, 0) // 0 → default
	if err != nil {
		t.Fatal(err)
	}
	if len(first) != 20 {
		t.Fatalf("default page size: got %d, want 20", len(first))
	}
	second, err := f.store.ListGroupsByUser(ctx, f.viewer, 20, 20)
	if err != nil {
		t.Fatal(err)
	}
	if len(second) != n-20 {
		t.Fatalf("second page: got %d, want %d", len(second), n-20)
	}
	over, err := f.store.ListGroupsByUser(ctx, f.viewer, 101, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(over) != 20 {
		t.Fatalf("limit=101 falls back to the default of 20, got %d — if this changed, update the web contract note", len(over))
	}
	all, err := f.store.ListGroupsByUser(ctx, f.viewer, 100, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != n {
		t.Fatalf("limit=100 should return all %d, got %d", n, len(all))
	}
}
