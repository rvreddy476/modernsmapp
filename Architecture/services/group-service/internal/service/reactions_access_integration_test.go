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
Access rules for reactions, through the real service and a real Postgres
(*_test only): a non-member of a private group, a banned member, a post from
another group, and an unlisted reaction are all refused — and a refusal
leaves spark_count and the rows exactly as they were.

Run: GROUP_POSTGRES_DSN=…/group_it_test go test -tags integration ./internal/service/ -run ReactionAccess -p 1
*/

type reactionAccessFixture struct {
	pool          *pgxpool.Pool
	svc           *Service
	privateGroup  uuid.UUID
	otherGroup    uuid.UUID
	member        uuid.UUID
	banned        uuid.UUID
	stranger      uuid.UUID
	privatePostID uuid.UUID
	publicPostID  uuid.UUID
}

func newReactionAccessFixture(t *testing.T) *reactionAccessFixture {
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
		ALTER TABLE group_posts ADD COLUMN IF NOT EXISTS status VARCHAR(20) DEFAULT 'published';
	`); err != nil {
		t.Fatal(err)
	}
	m016, err := database.Migrations.ReadFile("migrations/016_group_post_reactions.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, string(m016)); err != nil {
		t.Fatalf("migration 016: %v", err)
	}

	f := &reactionAccessFixture{
		pool:          pool,
		svc:           New(store.New(pool), nil, "", "", "", ""),
		privateGroup:  uuid.New(),
		otherGroup:    uuid.New(),
		member:        uuid.New(),
		banned:        uuid.New(),
		stranger:      uuid.New(),
		privatePostID: uuid.New(),
		publicPostID:  uuid.New(),
	}
	for _, g := range []struct {
		id  uuid.UUID
		vis string
	}{{f.privateGroup, "private"}, {f.otherGroup, "public"}} {
		if _, err := pool.Exec(ctx,
			`INSERT INTO groups (id, name, description, creator_id, visibility, privacy_level)
			 VALUES ($1, $2, '', $3, $4, $4)`,
			g.id, "react-access-"+g.id.String()[:8], f.member, g.vis); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO group_members (group_id, user_id, role) VALUES ($1, $2, 'member')`,
		f.privateGroup, f.member); err != nil {
		t.Fatal(err)
	}
	// A ban is group_members.status = 'banned' (what store.CheckBanned reads).
	// It lives in the PUBLIC group so the ban is what refuses, not the
	// private group's membership rule.
	if _, err := pool.Exec(ctx,
		`INSERT INTO group_members (group_id, user_id, role, status) VALUES ($1, $2, 'member', 'banned')`,
		f.otherGroup, f.banned); err != nil {
		t.Fatal(err)
	}
	for _, p := range []struct {
		id, group uuid.UUID
		body      string
	}{{f.privatePostID, f.privateGroup, "private post"}, {f.publicPostID, f.otherGroup, "public post"}} {
		if _, err := pool.Exec(ctx,
			`INSERT INTO group_posts (id, group_id, author_id, content_type, body, status)
			 VALUES ($1, $2, $3, 'text', $4, 'published')`,
			p.id, p.group, f.member.String(), p.body); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() {
		pool.Exec(ctx, `DELETE FROM group_post_sparks WHERE post_id IN ($1, $2)`, f.privatePostID, f.publicPostID)
		pool.Exec(ctx, `DELETE FROM member_stats WHERE group_id IN ($1, $2)`, f.privateGroup, f.otherGroup)
		pool.Exec(ctx, `DELETE FROM group_posts WHERE group_id IN ($1, $2)`, f.privateGroup, f.otherGroup)
		pool.Exec(ctx, `DELETE FROM group_members WHERE group_id IN ($1, $2)`, f.privateGroup, f.otherGroup)
		pool.Exec(ctx, `DELETE FROM groups WHERE id IN ($1, $2)`, f.privateGroup, f.otherGroup)
	})
	return f
}

// snapshot sums spark_count and reaction rows over both fixture posts.
func (f *reactionAccessFixture) snapshot(t *testing.T) (sparkCount, rows int) {
	t.Helper()
	ctx := context.Background()
	if err := f.pool.QueryRow(ctx,
		`SELECT COALESCE(SUM(spark_count), 0) FROM group_posts WHERE id IN ($1, $2)`,
		f.privatePostID, f.publicPostID).Scan(&sparkCount); err != nil {
		t.Fatal(err)
	}
	if err := f.pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM group_post_sparks WHERE post_id IN ($1, $2)`,
		f.privatePostID, f.publicPostID).Scan(&rows); err != nil {
		t.Fatal(err)
	}
	return
}

func TestReactionAccessRefusalsMutateNothing(t *testing.T) {
	f := newReactionAccessFixture(t)
	ctx := context.Background()

	// A member's reaction lands, so the refusals below are against a post
	// that has real state to disturb.
	st, err := f.svc.SetGroupPostReaction(ctx, f.member, f.privateGroup, f.privatePostID, "smile")
	if err != nil {
		t.Fatalf("member reacts: %v", err)
	}
	if st.Reaction == nil || *st.Reaction != "smile" || st.SparkCount != 1 || st.ReactionCounts["smile"] != 1 || !st.ViewerSparked {
		t.Fatalf("member state = %+v", st)
	}
	before, rowsBefore := f.snapshot(t)

	cases := []struct {
		name      string
		actor     uuid.UUID
		group     uuid.UUID
		post      uuid.UUID
		reaction  string
		wantWords string // substring handleServiceError maps: forbidden→403, not a member/not found→404, invalid→422
	}{
		{"stranger in a private group", f.stranger, f.privateGroup, f.privatePostID, "like", "not a member"},
		{"banned member of a public group", f.banned, f.otherGroup, f.publicPostID, "like", "forbidden"},
		{"post reached through another group", f.member, f.otherGroup, f.privatePostID, "like", "not found"},
		{"unlisted reaction", f.member, f.privateGroup, f.privatePostID, "heart", "invalid"},
		{"unknown group", f.member, uuid.New(), f.privatePostID, "like", "not found"},
	}
	for _, c := range cases {
		_, err := f.svc.SetGroupPostReaction(ctx, c.actor, c.group, c.post, c.reaction)
		if err == nil || !strings.Contains(err.Error(), c.wantWords) {
			t.Errorf("%s: set err=%v, want containing %q", c.name, err, c.wantWords)
		}
		if c.reaction != "heart" { // remove has no reaction argument
			_, err = f.svc.RemoveGroupPostReaction(ctx, c.actor, c.group, c.post)
			if err == nil || !strings.Contains(err.Error(), c.wantWords) {
				t.Errorf("%s: remove err=%v, want containing %q", c.name, err, c.wantWords)
			}
		}
		// Legacy spark goes through the same gate now.
		err = f.svc.SparkGroupPost(ctx, c.actor, c.group, c.post, false)
		if err == nil {
			t.Errorf("%s: legacy spark succeeded — the gate is not applied to /spark", c.name)
		}
	}
	after, rowsAfter := f.snapshot(t)
	if after != before || rowsAfter != rowsBefore {
		t.Fatalf("a refused request mutated state: spark_count %d→%d rows %d→%d", before, after, rowsBefore, rowsAfter)
	}

	// Reload persistence: what the member sees after all that is still smile.
	p, err := f.svc.GetGroupPostV2(ctx, f.member, f.privateGroup, f.privatePostID)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if p.ViewerReaction == nil || *p.ViewerReaction != "smile" || p.ReactionCounts["smile"] != 1 || p.SparkCount != 1 {
		t.Fatalf("reloaded post = viewer_reaction %v counts %v spark_count %d", p.ViewerReaction, p.ReactionCounts, p.SparkCount)
	}

	// Removal answers with the read-back state.
	st, err = f.svc.RemoveGroupPostReaction(ctx, f.member, f.privateGroup, f.privatePostID)
	if err != nil {
		t.Fatal(err)
	}
	if st.Reaction != nil || st.SparkCount != 0 || len(st.ReactionCounts) != 0 || st.ViewerSparked {
		t.Fatalf("state after remove = %+v", st)
	}
}
