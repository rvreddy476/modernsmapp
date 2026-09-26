//go:build integration

package service

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/atpost/group-service/database"
	"github.com/atpost/group-service/internal/store"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

/*
Who may learn an anonymous author, against a real Postgres (*_test only):
the creator, an admin and a moderator — each answer audited; a member is
refused and no audit row appears; a non-anonymous post reveals nothing new
and writes no audit.

Run: GROUP_POSTGRES_DSN=…/group_it_test go test -tags integration ./internal/service/ -run RevealAuthor -p 1
*/
func TestRevealAuthorRolesAndAudit(t *testing.T) {
	dsn := os.Getenv("GROUP_POSTGRES_DSN")
	if dsn == "" {
		t.Fatal("GROUP_POSTGRES_DSN is required")
	}
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(cfg.ConnConfig.Database, "_test") {
		t.Fatalf("refusing to run against database %q", cfg.ConnConfig.Database)
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
	m012, err := database.Migrations.ReadFile("migrations/012_admin_report_review.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, string(m012)); err != nil {
		t.Fatalf("migration 012: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		ALTER TABLE groups ADD COLUMN IF NOT EXISTS privacy_level TEXT NOT NULL DEFAULT 'public';
		ALTER TABLE groups ADD COLUMN IF NOT EXISTS status TEXT NOT NULL DEFAULT 'active';
		ALTER TABLE groups ADD COLUMN IF NOT EXISTS handle TEXT;
		ALTER TABLE group_members ADD COLUMN IF NOT EXISTS status TEXT NOT NULL DEFAULT 'active';
		ALTER TABLE group_members ADD COLUMN IF NOT EXISTS removal_reason TEXT;
		ALTER TABLE group_post_comments ADD COLUMN IF NOT EXISTS is_anonymous BOOLEAN NOT NULL DEFAULT FALSE;
		ALTER TABLE group_posts ADD COLUMN IF NOT EXISTS status VARCHAR(20) DEFAULT 'published';
		ALTER TABLE group_posts ADD COLUMN IF NOT EXISTS is_anonymous BOOLEAN NOT NULL DEFAULT FALSE;
		ALTER TABLE group_posts ADD COLUMN IF NOT EXISTS anon_alias UUID;
	`); err != nil {
		t.Fatal(err)
	}

	svc := New(store.New(pool), nil, "", "", "", "")
	groupID, creator, admin, mod, member, author := uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New()
	anonPost, plainPost := uuid.New(), uuid.New()
	if _, err := pool.Exec(ctx,
		`INSERT INTO groups (id, name, description, creator_id, visibility) VALUES ($1, $2, '', $3, 'public')`,
		groupID, "reveal-"+groupID.String()[:8], creator); err != nil {
		t.Fatal(err)
	}
	for _, m := range []struct {
		u    uuid.UUID
		role string
	}{{creator, "admin"}, {admin, "admin"}, {mod, "moderator"}, {member, "member"}, {author, "member"}} {
		if _, err := pool.Exec(ctx, `INSERT INTO group_members (group_id, user_id, role) VALUES ($1, $2, $3)`, groupID, m.u, m.role); err != nil {
			t.Fatal(err)
		}
	}
	alias := uuid.New()
	if _, err := pool.Exec(ctx,
		`INSERT INTO group_posts (id, group_id, author_id, content_type, body, status, is_anonymous, anon_alias)
		 VALUES ($1, $2, $3, 'text', 'secret', 'published', TRUE, $4),
		        ($5, $2, $3, 'text', 'open', 'published', FALSE, NULL)`,
		anonPost, groupID, author.String(), alias, plainPost); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		pool.Exec(ctx, `DELETE FROM group_admin_audit WHERE target_id IN ($1, $2)`, anonPost, plainPost)
		pool.Exec(ctx, `DELETE FROM group_posts WHERE group_id = $1`, groupID)
		pool.Exec(ctx, `DELETE FROM group_members WHERE group_id = $1`, groupID)
		pool.Exec(ctx, `DELETE FROM groups WHERE id = $1`, groupID)
	})

	audits := func() int {
		n, err := svc.store.CountAuthorReveals(ctx, anonPost)
		if err != nil {
			t.Fatal(err)
		}
		return n
	}

	// The post's own wire shape hides the author from everyone, admins included.
	p, err := svc.GetGroupPostV2(ctx, admin, groupID, anonPost)
	if err != nil {
		t.Fatal(err)
	}
	if wire, _ := p.MarshalJSON(); strings.Contains(string(wire), author.String()) {
		t.Fatal("the post JSON carries the real author")
	}

	for i, actor := range []uuid.UUID{creator, admin, mod} {
		out, err := svc.RevealPostAuthor(ctx, actor, groupID, anonPost)
		if err != nil {
			t.Fatalf("actor %d: %v", i, err)
		}
		if out.AuthorID != author.String() || !out.IsAnonymous || out.RevealedAt == nil {
			t.Fatalf("actor %d: %+v", i, out)
		}
		if audits() != i+1 {
			t.Fatalf("actor %d: %d audit rows, want %d", i, audits(), i+1)
		}
	}

	if _, err := svc.RevealPostAuthor(ctx, member, groupID, anonPost); err == nil || !strings.Contains(err.Error(), "forbidden") {
		t.Fatalf("member reveal: err=%v, want forbidden", err)
	}
	if _, err := svc.RevealPostAuthor(ctx, uuid.New(), groupID, anonPost); err == nil || !strings.Contains(err.Error(), "forbidden") {
		t.Fatalf("stranger reveal: err=%v, want forbidden", err)
	}
	if audits() != 3 {
		t.Fatalf("a refused reveal wrote an audit row (%d)", audits())
	}

	out, err := svc.RevealPostAuthor(ctx, admin, groupID, plainPost)
	if err != nil || out.IsAnonymous || out.AuthorID != author.String() || out.RevealedAt != nil {
		t.Fatalf("plain post: %+v %v", out, err)
	}
	if n, _ := svc.store.CountAuthorReveals(ctx, plainPost); n != 0 {
		t.Fatal("a non-anonymous post reveal was audited")
	}
}
