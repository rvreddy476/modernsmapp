//go:build integration

package claimsync

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/atpost/post-service/database"
	"github.com/atpost/post-service/internal/mediaclaims"
	"github.com/atpost/post-service/internal/store/postgres"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// The backfill / repair pass (cmd/media-claims) on the real schema, with
// media-service faked: a pre-protocol post cover is claimed, a deleted reel
// draft's claims are released, and a claim whose post was purged is found
// through media_claims and released.
//
//	POSTGRES_DSN=postgres://…/post_it_test go test -tags integration ./internal/claimsync/ -run MediaClaimsIT -v
//
// Refuses any database whose name does not end in _test.

func openDB(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("POSTGRES_DSN")
	if dsn == "" {
		t.Skip("POSTGRES_DSN not set")
	}
	cfg, err := pgx.ParseConfig(dsn)
	if err != nil {
		t.Fatalf("POSTGRES_DSN does not parse: %v", err)
	}
	if db := cfg.Database; db == "app" || !strings.HasSuffix(db, "_test") {
		t.Fatalf("refusing to run against database %q: the name must end in _test", db)
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	for _, ddl := range []string{
		`CREATE TABLE IF NOT EXISTS users (id UUID PRIMARY KEY)`,
		`CREATE TABLE IF NOT EXISTS media_assets (
			id UUID PRIMARY KEY, uploader_id UUID NOT NULL, file_type TEXT NOT NULL,
			processing_status TEXT NOT NULL, moderation_status TEXT NOT NULL DEFAULT 'pending',
			duration_seconds INTEGER, created_at TIMESTAMPTZ NOT NULL DEFAULT NOW())`,
	} {
		if _, err := pool.Exec(ctx, ddl); err != nil {
			t.Fatal(err)
		}
	}
	if err := postgres.BootstrapSchema(ctx, pool, database.SetupSQL, database.Migrations); err != nil {
		t.Fatalf("bootstrap schema: %v", err)
	}
	if _, err := pool.Exec(ctx, `CREATE TABLE IF NOT EXISTS media_claims (
		media_id UUID NOT NULL, claimant TEXT NOT NULL, claim_kind TEXT NOT NULL,
		claim_ref TEXT NOT NULL, created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
		PRIMARY KEY (media_id, claimant, claim_kind, claim_ref))`); err != nil {
		t.Fatal(err)
	}
	return pool
}

func TestMediaClaimsIT_BackfillRepairAndOrphans(t *testing.T) {
	pool := openDB(t)
	ctx := context.Background()
	author, cover, draftMedia := uuid.New(), uuid.New(), uuid.New()
	post, draft, purged := uuid.New(), uuid.New(), uuid.New()
	if _, err := pool.Exec(ctx, `
		INSERT INTO posts (id, author_id, text, visibility, content_type, review_status, cover_media_id, created_at, updated_at)
		VALUES ($1, $2, 'old', 'public', 'post', 'approved', $3, NOW(), NOW())`, post, author, cover); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO reel_drafts (id, author_id, media_id, status) VALUES ($1, $2, $3, 'deleted')`,
		draft, author, draftMedia); err != nil {
		t.Fatal(err)
	}
	// The purged post's claim, as media-service holds it.
	if _, err := pool.Exec(ctx, `INSERT INTO media_claims (media_id, claimant, claim_kind, claim_ref) VALUES ($1, $2, $3, $4)`,
		uuid.New(), mediaclaims.Claimant, mediaclaims.KindPostCover, purged.String()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		bg := context.Background()
		// The posts insert trigger made an engagement counts row, whose
		// foreign key refuses the post's delete until it goes first.
		_, _ = pool.Exec(bg, `DELETE FROM post_engagement_counts WHERE post_id = $1`, post)
		_, _ = pool.Exec(bg, `DELETE FROM posts WHERE id = $1`, post)
		_, _ = pool.Exec(bg, `DELETE FROM reel_drafts WHERE id = $1`, draft)
		_, _ = pool.Exec(bg, `DELETE FROM media_claims WHERE claim_ref = $1`, purged.String())
	})

	var log []string
	c := &fakeClaims{held: map[key]map[uuid.UUID]bool{}, missing: map[uuid.UUID]bool{}, log: &log}
	c.held[key{mediaclaims.KindReelDraft, draft}] = map[uuid.UUID]bool{draftMedia: true}
	c.held[key{mediaclaims.KindPostCover, purged}] = map[uuid.UUID]bool{uuid.New(): true}

	// Dry run first: nothing sent.
	if _, err := Run(ctx, postgres.New(pool), c, false); err != nil {
		t.Fatal(err)
	}
	if len(log) != 0 {
		t.Fatalf("the dry run sent %v", log)
	}

	rep, err := Run(ctx, postgres.New(pool), c, true)
	if err != nil {
		t.Fatal(err)
	}
	if rep.OrphanScan != "done" {
		t.Fatalf("orphan scan: %s", rep.OrphanScan)
	}
	if got := c.ids(mediaclaims.KindPostCover, post); got != cover.String() {
		t.Fatalf("the pre-protocol cover was not backfilled: %q", got)
	}
	if got := c.ids(mediaclaims.KindReelDraft, draft); got != "" {
		t.Fatalf("the deleted draft kept its claim: %q", got)
	}
	if got := c.ids(mediaclaims.KindPostCover, purged); got != "" {
		t.Fatalf("the purged post's orphan claim survived: %q", got)
	}
}

// Composer draft covers come out of the JSONB payload: a live draft's cover
// is backfilled, a deleted one's released, under the post_draft_cover kind.
func TestMediaClaimsIT_BackfillPostDraftCovers(t *testing.T) {
	pool := openDB(t)
	ctx := context.Background()
	author, cover := uuid.New(), uuid.New()
	live, deleted := uuid.New(), uuid.New()
	payload := `{"text":"t","cover_media_id":"` + cover.String() + `"}`
	for id, status := range map[uuid.UUID]string{live: "draft", deleted: "deleted"} {
		if _, err := pool.Exec(ctx, `INSERT INTO post_drafts (id, author_id, payload, status) VALUES ($1, $2, $3::jsonb, $4)`,
			id, author, payload, status); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM post_drafts WHERE id = ANY($1)`, []uuid.UUID{live, deleted})
	})
	var log []string
	c := &fakeClaims{held: map[key]map[uuid.UUID]bool{}, missing: map[uuid.UUID]bool{}, log: &log}
	c.held[key{mediaclaims.KindPostDraftCover, deleted}] = map[uuid.UUID]bool{cover: true}

	if _, err := Run(ctx, postgres.New(pool), c, true); err != nil {
		t.Fatal(err)
	}
	if got := c.ids(mediaclaims.KindPostDraftCover, live); got != cover.String() {
		t.Fatalf("the saved draft's cover was not backfilled: %q", got)
	}
	if got := c.ids(mediaclaims.KindPostDraftCover, deleted); got != "" {
		t.Fatalf("the deleted draft kept its claim: %q", got)
	}
}

// Series and playlist covers come out of their own tables: a series'
// cover_media_id and a playlist's cover_url serve path are backfilled under
// their kinds; a playlist cover that is not a serve path holds nothing.
func TestMediaClaimsIT_BackfillSeriesAndPlaylistCovers(t *testing.T) {
	pool := openDB(t)
	ctx := context.Background()
	creator, cover := uuid.New(), uuid.New()
	if _, err := pool.Exec(ctx, `INSERT INTO users (id) VALUES ($1) ON CONFLICT DO NOTHING`, creator); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO media_assets (id, uploader_id, file_type, processing_status) VALUES ($1, $2, 'image', 'ready')`,
		cover, creator); err != nil {
		t.Fatal(err)
	}
	series, playlist, odd := uuid.New(), uuid.New(), uuid.New()
	if _, err := pool.Exec(ctx, `INSERT INTO video_series (id, creator_id, title, cover_media_id) VALUES ($1, $2, 's', $3)`,
		series, creator, cover); err != nil {
		t.Fatal(err)
	}
	for id, url := range map[uuid.UUID]string{playlist: "/v1/media/" + cover.String() + "/serve", odd: "not-a-serve-path"} {
		if _, err := pool.Exec(ctx, `INSERT INTO playlists (id, creator_id, title, cover_url) VALUES ($1, $2, 'p', $3)`,
			id, creator, url); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() {
		bg := context.Background()
		_, _ = pool.Exec(bg, `DELETE FROM playlists WHERE id = ANY($1)`, []uuid.UUID{playlist, odd})
		_, _ = pool.Exec(bg, `DELETE FROM video_series WHERE id = $1`, series)
		_, _ = pool.Exec(bg, `DELETE FROM media_assets WHERE id = $1`, cover)
	})
	var log []string
	c := &fakeClaims{held: map[key]map[uuid.UUID]bool{}, missing: map[uuid.UUID]bool{}, log: &log}
	c.held[key{mediaclaims.KindPlaylistCover, odd}] = map[uuid.UUID]bool{cover: true}

	if _, err := Run(ctx, postgres.New(pool), c, true); err != nil {
		t.Fatal(err)
	}
	if got := c.ids(mediaclaims.KindVideoSeriesCover, series); got != cover.String() {
		t.Fatalf("the series cover was not backfilled: %q", got)
	}
	if got := c.ids(mediaclaims.KindPlaylistCover, playlist); got != cover.String() {
		t.Fatalf("the playlist cover was not backfilled: %q", got)
	}
	if got := c.ids(mediaclaims.KindPlaylistCover, odd); got != "" {
		t.Fatalf("a playlist cover that is not a serve path kept its claim: %q", got)
	}
}
