//go:build integration

// Post screens parity (2026-10-18) on the real schema: migration 066's
// friends visibility, link / ai / place columns and their CHECKs, the
// widened post_drafts kinds, the new columns through insert / read / edit,
// and the engagement-count read behind counts.shares / counts.saves.
//
//	POST_SCREENS_POSTGRES_DSN=postgres://…/post_screens_it_test go test -tags integration ./internal/store/postgres/ -run PostScreens -v
//
// The database name must end in _test.

package postgres_test

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/atpost/post-service/database"
	store "github.com/atpost/post-service/internal/store/postgres"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func openPostScreensDB(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("POST_SCREENS_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("POST_SCREENS_POSTGRES_DSN is required")
	}
	name := dsn[strings.LastIndex(dsn, "/")+1:]
	if i := strings.Index(name, "?"); i >= 0 {
		name = name[:i]
	}
	if !strings.HasSuffix(name, "_test") {
		t.Fatalf("refusing to run against a database whose name does not end in _test")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	if _, err := pool.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS media_assets (
			id UUID PRIMARY KEY, uploader_id UUID NOT NULL, file_type TEXT NOT NULL, processing_status TEXT NOT NULL,
			moderation_status TEXT NOT NULL DEFAULT 'pending', duration_seconds INTEGER, created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
		)`); err != nil {
		t.Fatal(err)
	}
	for _, ddl := range []string{
		`ALTER TABLE media_assets ADD COLUMN IF NOT EXISTS duration_ms INT`,
		`ALTER TABLE media_assets ADD COLUMN IF NOT EXISTS alt_text TEXT`,
		`ALTER TABLE media_assets ADD COLUMN IF NOT EXISTS alt_decorative BOOLEAN NOT NULL DEFAULT FALSE`,
	} {
		if _, err := pool.Exec(ctx, ddl); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.BootstrapSchema(ctx, pool, database.SetupSQL, database.Migrations); err != nil {
		t.Fatalf("bootstrap real schema: %v", err)
	}
	return pool
}

func TestPostScreens_MigrationConstraints(t *testing.T) {
	pool := openPostScreensDB(t)
	ctx := context.Background()
	insert := func(vis string, link *string, place *string) error {
		_, err := pool.Exec(ctx, `
			INSERT INTO posts (id, author_id, text, visibility, content_type, link_url, location_place_id, created_at, updated_at)
			VALUES ($1, $2, 'parity', $3, 'post', $4, $5, NOW(), NOW())`, uuid.New(), uuid.New(), vis, link, place)
		return err
	}
	s := func(v string) *string { return &v }
	if err := insert("friends", s("https://example.com"), s("ChIJ-x")); err != nil {
		t.Fatalf("friends + https link + place id refused: %v", err)
	}
	for name, err := range map[string]error{
		"unknown visibility": insert("mutuals", nil, nil),
		"http link":          insert("public", s("http://example.com"), nil),
		"javascript link":    insert("public", s("javascript:alert(1)"), nil),
		"501-char link":      insert("public", s("https://e.com/"+strings.Repeat("a", 487)), nil),
		"empty place id":     insert("public", nil, s("")),
	} {
		if err == nil {
			t.Errorf("%s: the CHECK let it in", name)
		}
	}
	var aiDefault bool
	id := uuid.New()
	if _, err := pool.Exec(ctx, `INSERT INTO posts (id, author_id, text, visibility, content_type, created_at, updated_at) VALUES ($1, $2, 'x', 'public', 'post', NOW(), NOW())`, id, uuid.New()); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT ai_generated FROM posts WHERE id = $1`, id).Scan(&aiDefault); err != nil || aiDefault {
		t.Fatalf("ai_generated default = %v, %v; want false", aiDefault, err)
	}
	for _, kind := range []string{"post", "poll", "article", "reel", "video"} {
		if _, err := pool.Exec(ctx, `INSERT INTO post_drafts (author_id, post_type, payload) VALUES ($1, $2, '{}')`, uuid.New(), kind); err != nil {
			t.Errorf("post_drafts kind %q refused: %v", kind, err)
		}
	}
	if _, err := pool.Exec(ctx, `INSERT INTO post_drafts (author_id, post_type, payload) VALUES ($1, 'image', '{}')`, uuid.New()); err == nil {
		t.Error("post_drafts accepted a kind outside the list")
	}
}

func TestPostScreens_ColumnsRoundTripAndEdit(t *testing.T) {
	pool := openPostScreensDB(t)
	ctx := context.Background()
	s := store.New(pool)
	link, place, loc := "https://example.com/market", "ChIJLbZ-NFv9DDkRQJY4FbcFcgM", "Banjara Hills"
	p := &store.Post{
		ID: uuid.New(), AuthorID: uuid.New(), Text: "parity", Visibility: "friends", ContentType: "post", PostType: "image",
		AppOrigin: "postbook", ReviewStatus: "approved", Language: "en", RemixSetting: "disallow", CommentAccess: "followers",
		IsBranded: true, AIGenerated: true, LinkURL: &link, LocationName: &loc, LocationPlaceID: &place,
		AllowDownload: true, CreatedAt: time.Now(),
	}
	if err := s.CreatePostWithEvent(ctx, p, "", nil); err != nil {
		t.Fatalf("create: %v", err)
	}
	got, err := s.GetPost(ctx, p.ID)
	if err != nil || got == nil {
		t.Fatalf("get: %v", err)
	}
	if got.Visibility != "friends" || !got.IsBranded || !got.AIGenerated || got.LinkURL == nil || *got.LinkURL != link ||
		got.LocationPlaceID == nil || *got.LocationPlaceID != place {
		t.Fatalf("read back = %+v", got)
	}

	no := false
	other := "https://example.com/other"
	after, err := s.UpdatePostFields(ctx, p.ID, p.AuthorID, store.PostEditPatch{LinkURL: &other, IsBranded: &no, AIGenerated: &no})
	if err != nil {
		t.Fatalf("edit: %v", err)
	}
	if after.IsBranded || after.AIGenerated || after.LinkURL == nil || *after.LinkURL != other {
		t.Fatalf("after edit = %+v", after)
	}
	var changes json.RawMessage
	if err := pool.QueryRow(ctx, `SELECT changes FROM post_edit_audit WHERE post_id = $1 ORDER BY created_at DESC LIMIT 1`, p.ID).Scan(&changes); err != nil {
		t.Fatalf("audit: %v", err)
	}
	for _, k := range []string{`"link_url"`, `"is_branded"`, `"ai_generated"`} {
		if !strings.Contains(string(changes), k) {
			t.Errorf("audit %s lacks %s", changes, k)
		}
	}
	cleared, err := s.UpdatePostFields(ctx, p.ID, p.AuthorID, store.PostEditPatch{ClearLinkURL: true})
	if err != nil || cleared.LinkURL != nil {
		t.Fatalf("clear: %+v %v", cleared, err)
	}
}

func TestPostScreens_EngagementCountRows(t *testing.T) {
	pool := openPostScreensDB(t)
	ctx := context.Background()
	s := store.New(pool)
	withRow, without := uuid.New(), uuid.New()
	if _, err := pool.Exec(ctx, `INSERT INTO posts (id, author_id, text, visibility, content_type, created_at, updated_at)
		VALUES ($1, $2, 'counts', 'public', 'post', NOW(), NOW())`, withRow, uuid.New()); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO post_engagement_counts (post_id, comment_count, share_count, bookmark_count)
		VALUES ($1, 3, 4, 5)
		ON CONFLICT (post_id) DO UPDATE SET comment_count = 3, share_count = 4, bookmark_count = 5`, withRow); err != nil {
		t.Fatal(err)
	}
	rows, err := s.GetEngagementCountRows(ctx, []uuid.UUID{withRow, without})
	if err != nil {
		t.Fatal(err)
	}
	if rows[withRow] != (store.EngagementCountRow{Comments: 3, Shares: 4, Saves: 5}) {
		t.Fatalf("row = %+v", rows[withRow])
	}
	if _, ok := rows[without]; ok {
		t.Fatal("a post with no row produced one")
	}
}
