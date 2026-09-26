//go:build integration

package service

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/atpost/group-service/database"
	"github.com/atpost/group-service/internal/store"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

/*
The anonymous author's own comment on their own post is the post's
pseudonym on the wire — created and listed — and another member's comment
is still theirs. The legacy feed masks the author the same way. And a
private group's comments are for members only.

Run: GROUP_POSTGRES_DSN=…/group_it_test go test -tags integration ./internal/service/ -run AnonymousComments -p 1
*/
func TestAnonymousCommentsAndLegacyFeedAreMasked(t *testing.T) {
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
	if _, err := pool.Exec(ctx, `
		ALTER TABLE groups ADD COLUMN IF NOT EXISTS privacy_level TEXT NOT NULL DEFAULT 'public';
		ALTER TABLE groups ADD COLUMN IF NOT EXISTS status TEXT NOT NULL DEFAULT 'active';
		ALTER TABLE groups ADD COLUMN IF NOT EXISTS handle TEXT;
		ALTER TABLE group_members ADD COLUMN IF NOT EXISTS status TEXT NOT NULL DEFAULT 'active';
		ALTER TABLE group_members ADD COLUMN IF NOT EXISTS removal_reason TEXT;
		ALTER TABLE group_posts ADD COLUMN IF NOT EXISTS status VARCHAR(20) DEFAULT 'published';
		ALTER TABLE group_posts ADD COLUMN IF NOT EXISTS is_anonymous BOOLEAN NOT NULL DEFAULT FALSE;
		ALTER TABLE group_posts ADD COLUMN IF NOT EXISTS anon_alias UUID;
		ALTER TABLE group_post_comments ADD COLUMN IF NOT EXISTS is_anonymous BOOLEAN NOT NULL DEFAULT FALSE;
	`); err != nil {
		t.Fatal(err)
	}

	// No Redis: membership checks fall back to the database.
	svc := New(store.New(pool), nil, "", "", "", "")
	groupID, author, member, stranger := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	postID, alias := uuid.New(), uuid.New()
	if _, err := pool.Exec(ctx,
		`INSERT INTO groups (id, name, description, creator_id, visibility, privacy_level) VALUES ($1, $2, '', $3, 'private', 'private')`,
		groupID, "anon-comments-"+groupID.String()[:8], author); err != nil {
		t.Fatal(err)
	}
	for _, m := range []uuid.UUID{author, member} {
		if _, err := pool.Exec(ctx, `INSERT INTO group_members (group_id, user_id, role) VALUES ($1, $2, 'member')`, groupID, m); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO group_posts (id, group_id, author_id, content_type, body, status, is_anonymous, anon_alias)
		 VALUES ($1, $2, $3, 'text', 'secret', 'published', TRUE, $4)`,
		postID, groupID, author.String(), alias); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		pool.Exec(ctx, `DELETE FROM group_post_comments WHERE post_id = $1`, postID)
		pool.Exec(ctx, `DELETE FROM group_posts WHERE group_id = $1`, groupID)
		pool.Exec(ctx, `DELETE FROM group_members WHERE group_id = $1`, groupID)
		pool.Exec(ctx, `DELETE FROM groups WHERE id = $1`, groupID)
	})

	own, err := svc.AddGroupPostComment(ctx, author, groupID, postID, "it was me, replying", nil)
	if err != nil {
		t.Fatalf("author comments: %v", err)
	}
	other, err := svc.AddGroupPostComment(ctx, member, groupID, postID, "who is this?", nil)
	if err != nil {
		t.Fatalf("member comments: %v", err)
	}
	wire := func(c *store.GroupPostComment) string {
		b, err := json.Marshal(c)
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	if w := wire(own); strings.Contains(w, author.String()) || !strings.Contains(w, alias.String()) {
		t.Fatalf("the author's own comment names them on create: %s", w)
	}
	if w := wire(other); !strings.Contains(w, member.String()) {
		t.Fatalf("another member's comment lost its author: %s", w)
	}

	list, err := svc.ListGroupPostComments(ctx, member, groupID, postID, 20, 0)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(list)
	if strings.Contains(string(b), author.String()) {
		t.Fatalf("the comment list names the anonymous author: %s", b)
	}
	if !strings.Contains(string(b), alias.String()) || !strings.Contains(string(b), member.String()) {
		t.Fatalf("the comment list lost the alias or the member: %s", b)
	}

	if _, err := svc.ListGroupPostComments(ctx, stranger, groupID, postID, 20, 0); err == nil || !strings.Contains(err.Error(), "not a member") {
		t.Fatalf("a stranger read a private group's comments: err=%v", err)
	}

	// Legacy feed.
	legacy, err := svc.store.ListGroupPosts(ctx, groupID, 20, 0)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, p := range legacy {
		if p.AuthorID == author.String() {
			t.Fatal("the legacy feed names the anonymous author")
		}
		if p.AuthorID == alias.String() {
			found = true
		}
	}
	if !found {
		t.Fatal("the legacy feed did not carry the alias")
	}

	// Realtime/event payloads name the actor as the post knows them.
	post, _ := svc.store.GetGroupPostV2(ctx, postID)
	if svc.publicActorID(post, author) != alias || svc.publicActorID(post, member) != member {
		t.Fatal("publicActorID does not map the anonymous author to the alias (and only them)")
	}
}
