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
The group media authority against a real Postgres (*_test only): who may
receive the bytes of a photo attached to a group post, and of a group's
cover. Run:
  GROUP_POSTGRES_DSN=…/group_it_test go test -tags integration ./internal/service/ -run MediaAccess -p 1
*/

type mediaAccessFixture struct {
	pool                                  *pgxpool.Pool
	svc                                   *Service
	publicGroup, privateGroup, deadGroup  uuid.UUID
	member, stranger, banned, uploader    uuid.UUID
	publicPhoto, privatePhoto, deadPhoto  uuid.UUID
	unpublishedPhoto, cover, unreferenced uuid.UUID
}

func newMediaAccessFixture(t *testing.T) *mediaAccessFixture {
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
	f := &mediaAccessFixture{
		pool: pool, svc: New(store.New(pool), nil, "", "", "", ""),
		publicGroup: uuid.New(), privateGroup: uuid.New(), deadGroup: uuid.New(),
		member: uuid.New(), stranger: uuid.New(), banned: uuid.New(), uploader: uuid.New(),
		publicPhoto: uuid.New(), privatePhoto: uuid.New(), deadPhoto: uuid.New(),
		unpublishedPhoto: uuid.New(), cover: uuid.New(), unreferenced: uuid.New(),
	}
	groups := []struct {
		id           uuid.UUID
		privacy, st  string
		cover        *uuid.UUID
	}{
		{f.publicGroup, "public", "active", &f.cover},
		{f.privateGroup, "private", "active", nil},
		{f.deadGroup, "public", "deleted", nil},
	}
	for _, g := range groups {
		if _, err := pool.Exec(ctx,
			`INSERT INTO groups (id, name, description, creator_id, visibility, privacy_level, status, cover_media_id)
			 VALUES ($1, $2, '', $3, CASE WHEN $4 = 'private' THEN 'private' ELSE 'public' END, $4, $5, $6)`,
			g.id, "media-access-"+g.id.String()[:8], f.uploader, g.privacy, g.st, g.cover); err != nil {
			t.Fatal(err)
		}
	}
	for _, m := range []struct {
		g, u   uuid.UUID
		status string
	}{
		{f.privateGroup, f.member, "active"}, {f.publicGroup, f.member, "active"},
		{f.publicGroup, f.banned, "banned"}, {f.privateGroup, f.banned, "banned"},
		{f.publicGroup, f.uploader, "active"}, {f.privateGroup, f.uploader, "active"},
	} {
		if _, err := pool.Exec(ctx,
			`INSERT INTO group_members (group_id, user_id, role, status) VALUES ($1, $2, 'member', $3)`, m.g, m.u, m.status); err != nil {
			t.Fatal(err)
		}
	}
	posts := []struct {
		g, photo uuid.UUID
		status   string
	}{
		{f.publicGroup, f.publicPhoto, "published"},
		{f.privateGroup, f.privatePhoto, "published"},
		{f.deadGroup, f.deadPhoto, "published"},
		{f.publicGroup, f.unpublishedPhoto, "pending_approval"},
	}
	for _, p := range posts {
		if _, err := pool.Exec(ctx,
			`INSERT INTO group_posts (id, group_id, author_id, content_type, body, status, attachments)
			 VALUES ($1, $2, $3, 'photo', 'pic', $4, jsonb_build_array($5::text))`,
			uuid.New(), p.g, f.uploader.String(), p.status, p.photo.String()); err != nil {
			t.Fatal(err)
		}
	}
	ids := []uuid.UUID{f.publicGroup, f.privateGroup, f.deadGroup}
	t.Cleanup(func() {
		pool.Exec(ctx, `DELETE FROM group_posts WHERE group_id = ANY($1)`, ids)
		pool.Exec(ctx, `DELETE FROM group_members WHERE group_id = ANY($1)`, ids)
		pool.Exec(ctx, `DELETE FROM groups WHERE id = ANY($1)`, ids)
	})
	return f
}

func TestMediaAccessFollowsGroupReadability(t *testing.T) {
	f := newMediaAccessFixture(t)
	ctx := context.Background()

	cases := []struct {
		name   string
		viewer uuid.UUID
		media  uuid.UUID
		allow  bool
		reason string
	}{
		{"member sees a private group's photo", f.member, f.privatePhoto, true, "group_post"},
		{"stranger does not see a private group's photo", f.stranger, f.privatePhoto, false, "not_a_member"},
		{"stranger sees a public group's photo", f.stranger, f.publicPhoto, true, "group_post"},
		{"banned viewer sees nothing, even public", f.banned, f.publicPhoto, false, "banned"},
		{"a deleted group's photo is gone", f.member, f.deadPhoto, false, "group_deleted"},
		{"an unpublished post's photo is not readable", f.member, f.unpublishedPhoto, false, "no_group_reference"},
		{"a public group's cover is readable", f.stranger, f.cover, true, "group_cover"},
		{"an asset no group references is not ours to allow", f.member, f.unreferenced, false, "no_group_reference"},
	}
	for _, c := range cases {
		res, err := f.svc.ViewerMayAccessMedia(ctx, c.viewer, c.media)
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if res.Allowed != c.allow || res.Reason != c.reason {
			t.Errorf("%s: allowed=%v reason=%q, want allowed=%v reason=%q", c.name, res.Allowed, res.Reason, c.allow, c.reason)
		}
	}

	// The batch answers every id asked, in one call.
	all := []uuid.UUID{f.publicPhoto, f.privatePhoto, f.deadPhoto, f.unreferenced}
	batch, err := f.svc.ViewerMayAccessMediaBatch(ctx, f.stranger, all)
	if err != nil {
		t.Fatal(err)
	}
	if len(batch) != len(all) {
		t.Fatalf("batch answered %d of %d", len(batch), len(all))
	}
	if !batch[f.publicPhoto].Allowed || batch[f.privatePhoto].Allowed || batch[f.deadPhoto].Allowed || batch[f.unreferenced].Allowed {
		t.Fatalf("batch verdicts wrong: %+v", batch)
	}
}
