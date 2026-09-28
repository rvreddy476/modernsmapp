//go:build integration

package service

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/atpost/post-service/database"
	"github.com/atpost/post-service/internal/store/postgres"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Creator Hub batch (2026-09-28): playback authorisation against a real
// Postgres. media-service asks /v1/internal/media-access(/batch) and
// /v1/internal/media-download-allowed before it hands out a byte; each must
// honour the private share list and the age gate exactly as the detail read
// does.
//
//	POSTGRES_DSN=postgres://…/post_it_test go test -tags integration ./internal/service/ -run HubMedia -v
//
// Refuses any database whose name does not end in _test.

type noRelationships struct{}

func (noRelationships) Following(context.Context, string) ([]string, error) { return nil, nil }
func (noRelationships) RelationshipBatch(_ context.Context, _ string, targets []string) (map[string]ViewerRelationship, error) {
	out := make(map[string]ViewerRelationship, len(targets))
	for _, id := range targets {
		out[id] = ViewerRelationship{}
	}
	return out, nil
}

func openHubMediaDB(t *testing.T) *pgxpool.Pool {
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
		`CREATE TABLE IF NOT EXISTS user_preferences (user_id UUID PRIMARY KEY)`,
		`CREATE TABLE IF NOT EXISTS channels (
			id UUID PRIMARY KEY DEFAULT gen_random_uuid(), user_id UUID NOT NULL REFERENCES users(id),
			handle TEXT NOT NULL UNIQUE, name TEXT NOT NULL, description TEXT NOT NULL DEFAULT '',
			category TEXT NOT NULL DEFAULT '', country TEXT NOT NULL DEFAULT '', language TEXT NOT NULL DEFAULT '',
			contact_email TEXT NOT NULL DEFAULT '', collab_status TEXT NOT NULL DEFAULT 'closed',
			content_schedule TEXT NOT NULL DEFAULT '', subscriber_count INTEGER NOT NULL DEFAULT 0,
			is_verified BOOLEAN NOT NULL DEFAULT FALSE, created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
			updated_at TIMESTAMPTZ NOT NULL DEFAULT now())`,
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
	return pool
}

func TestHubMediaAccessHonoursSharesAndAge(t *testing.T) {
	pool := openHubMediaDB(t)
	ctx := context.Background()
	author, shared, stranger, minor := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	for _, id := range []uuid.UUID{author, shared, stranger, minor} {
		if _, err := pool.Exec(ctx, `INSERT INTO users (id) VALUES ($1) ON CONFLICT DO NOTHING`, id); err != nil {
			t.Fatal(err)
		}
	}
	postID, mediaID := uuid.New(), uuid.New()
	if _, err := pool.Exec(ctx, `INSERT INTO media_assets (id, uploader_id, file_type, processing_status, moderation_status) VALUES ($1,$2,'video','ready','passed')`, mediaID, author); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO posts (id, author_id, text, visibility, content_type, review_status, allow_download, created_at, updated_at)
		VALUES ($1, $2, 'shared cut', 'private', 'long_video', 'approved', true, NOW(), NOW())`, postID, author); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO post_media (post_id, media_id, kind) VALUES ($1,$2,'video')`, postID, mediaID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		bg := context.Background()
		_, _ = pool.Exec(bg, `DELETE FROM post_private_shares WHERE post_id = $1`, postID)
		_, _ = pool.Exec(bg, `DELETE FROM post_media WHERE post_id = $1`, postID)
		_, _ = pool.Exec(bg, `DELETE FROM post_engagement_counts WHERE post_id = $1`, postID)
		_, _ = pool.Exec(bg, `DELETE FROM posts WHERE id = $1`, postID)
		_, _ = pool.Exec(bg, `DELETE FROM media_assets WHERE id = $1`, mediaID)
		for _, id := range []uuid.UUID{author, shared, stranger, minor} {
			_, _ = pool.Exec(bg, `DELETE FROM users WHERE id = $1`, id)
		}
	})

	store := postgres.New(pool)
	svc := New(store, nil, nil)
	svc.WithStoryAudience(NewStoryAudience(noRelationships{}))
	svc.now = func() time.Time { return hubNow }
	svc.SetBirthDateSource(&fakeBirthDates{dob: map[uuid.UUID]*time.Time{shared: dayp(1990, 1, 1), minor: dayp(2012, 1, 1)}})

	single := func(viewer uuid.UUID) bool {
		t.Helper()
		res, err := svc.ViewerMayAccessMedia(ctx, viewer, mediaID)
		if err != nil {
			t.Fatal(err)
		}
		return res.Allowed
	}
	batch := func(viewer uuid.UUID) bool {
		t.Helper()
		res, err := svc.ViewerMayAccessMediaBatch(ctx, viewer, []uuid.UUID{mediaID})
		if err != nil {
			t.Fatal(err)
		}
		return res[mediaID].Allowed
	}
	download := func(viewer uuid.UUID) bool {
		t.Helper()
		ok, err := svc.ViewerMayDownloadMedia(ctx, viewer, mediaID)
		if err != nil {
			t.Fatal(err)
		}
		return ok
	}

	// Private and shared with nobody: the owner only.
	for name, check := range map[string]func(uuid.UUID) bool{"single": single, "batch": batch, "download": download} {
		if check(shared) || check(stranger) {
			t.Fatalf("%s: a private post played for someone not on its list", name)
		}
		if !check(author) {
			t.Fatalf("%s: owner refused", name)
		}
	}
	// On the list: plays (and downloads, the post allows it); others still not.
	if _, err := store.ReplacePrivateShares(ctx, postID, author, []uuid.UUID{shared, minor}); err != nil {
		t.Fatal(err)
	}
	for name, check := range map[string]func(uuid.UUID) bool{"single": single, "batch": batch, "download": download} {
		if !check(shared) {
			t.Fatalf("%s: shared viewer refused", name)
		}
		if check(stranger) {
			t.Fatalf("%s: stranger played a shared private post", name)
		}
	}
	// 18+: the shared adult plays, the shared minor does not, the owner does.
	if _, err := pool.Exec(ctx, `UPDATE posts SET age_restricted = true WHERE id = $1`, postID); err != nil {
		t.Fatal(err)
	}
	for name, check := range map[string]func(uuid.UUID) bool{"single": single, "batch": batch, "download": download} {
		if !check(shared) {
			t.Fatalf("%s: shared adult refused an 18+ post", name)
		}
		if check(minor) {
			t.Fatalf("%s: a minor on the list played an 18+ post", name)
		}
		if !check(author) {
			t.Fatalf("%s: owner refused their own 18+ post", name)
		}
	}
	// The same asset on a second, public, not-downloadable post: the minor
	// may now watch it (through that post), but a download is judged on the
	// 18+ post that allows it, so it stays refused.
	openID := uuid.New()
	if _, err := pool.Exec(ctx, `
		INSERT INTO posts (id, author_id, text, visibility, content_type, review_status, allow_download, created_at, updated_at)
		VALUES ($1, $2, 'open cut', 'public', 'long_video', 'approved', false, NOW(), NOW())`, openID, author); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO post_media (post_id, media_id, kind) VALUES ($1,$2,'video')`, openID, mediaID); err != nil {
		t.Fatal(err)
	}
	if !single(minor) {
		t.Fatal("minor refused the asset on its public, unrestricted post")
	}
	if download(minor) {
		t.Fatal("a minor downloaded through the 18+ post")
	}
	if !download(shared) {
		t.Fatal("shared adult lost the download")
	}
	for _, stmt := range []string{`DELETE FROM post_media WHERE post_id = $1`, `DELETE FROM post_engagement_counts WHERE post_id = $1`, `DELETE FROM posts WHERE id = $1`} {
		if _, err := pool.Exec(ctx, stmt, openID); err != nil {
			t.Fatal(err)
		}
	}
	// Public and 18+: anyone adult; a stranger with no known age does not.
	if _, err := pool.Exec(ctx, `UPDATE posts SET visibility = 'public' WHERE id = $1`, postID); err != nil {
		t.Fatal(err)
	}
	if single(stranger) || batch(stranger) {
		t.Fatal("a viewer with no date of birth played an 18+ post")
	}
	// The page filter drops the 18+ post for the minor and keeps it for the
	// adult and the owner (by-author, recent, trending ... share it).
	p, err := store.GetPost(ctx, postID)
	if err != nil || p == nil {
		t.Fatalf("reload: %v", err)
	}
	for viewer, want := range map[uuid.UUID]int{minor: 0, shared: 1, author: 1} {
		v := viewer
		page, err := svc.attachMediaStateToDetails(ctx, []PostDetail{{Post: clonePost(p)}}, &v)
		if err != nil || len(page) != want {
			t.Fatalf("page for %s: %d rows want %d (%v)", viewer, len(page), want, err)
		}
	}
	anonPage, _ := svc.attachMediaStateToDetails(ctx, []PostDetail{{Post: clonePost(p)}}, nil)
	if len(anonPage) != 0 {
		t.Fatal("anonymous page kept an 18+ post")
	}
}

func clonePost(p *postgres.Post) *postgres.Post {
	cp := *p
	cp.Media = append([]postgres.PostMedia{}, p.Media...)
	return &cp
}

// The direct read (GET /v1/posts/:id) and the related-post card run the
// share and age gates. Only the refusals are driven here: they return before
// the Scylla counts the success path reads.
func TestHubDirectReadAndRelatedCardGates(t *testing.T) {
	pool := openHubMediaDB(t)
	ctx := context.Background()
	author, shared, stranger, minor := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	for _, id := range []uuid.UUID{author, shared, stranger, minor} {
		if _, err := pool.Exec(ctx, `INSERT INTO users (id) VALUES ($1) ON CONFLICT DO NOTHING`, id); err != nil {
			t.Fatal(err)
		}
	}
	mainID, relatedID := uuid.New(), uuid.New()
	for _, row := range []struct {
		id         uuid.UUID
		visibility string
	}{{mainID, "public"}, {relatedID, "private"}} {
		if _, err := pool.Exec(ctx, `
			INSERT INTO posts (id, author_id, text, title, visibility, content_type, review_status, created_at, updated_at)
			VALUES ($1, $2, 'body', 'Related one', $3, 'long_video', 'approved', NOW(), NOW())`, row.id, author, row.visibility); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := pool.Exec(ctx, `UPDATE posts SET related_post_id = $2 WHERE id = $1`, mainID, relatedID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		bg := context.Background()
		_, _ = pool.Exec(bg, `DELETE FROM post_private_shares WHERE post_id = ANY($1)`, []uuid.UUID{mainID, relatedID})
		_, _ = pool.Exec(bg, `DELETE FROM post_engagement_counts WHERE post_id = ANY($1)`, []uuid.UUID{mainID, relatedID})
		_, _ = pool.Exec(bg, `DELETE FROM posts WHERE id = ANY($1)`, []uuid.UUID{mainID, relatedID})
		for _, id := range []uuid.UUID{author, shared, stranger, minor} {
			_, _ = pool.Exec(bg, `DELETE FROM users WHERE id = $1`, id)
		}
	})
	store := postgres.New(pool)
	svc := New(store, nil, nil)
	svc.now = func() time.Time { return hubNow }
	svc.SetBirthDateSource(&fakeBirthDates{dob: map[uuid.UUID]*time.Time{shared: dayp(1990, 1, 1), minor: dayp(2012, 1, 1)}})

	// Private, not shared: the direct read is the plain 404 (nil, nil).
	if d, err := svc.GetPost(ctx, relatedID, &stranger); d != nil || err != nil {
		t.Fatalf("stranger read a private post: %v %v", d, err)
	}
	main, _ := store.GetPost(ctx, mainID)
	if card := svc.relatedPostCard(ctx, main, &shared); card != nil {
		t.Fatal("related card named a private post to someone not on its list")
	}
	if _, err := store.ReplacePrivateShares(ctx, relatedID, author, []uuid.UUID{shared, minor}); err != nil {
		t.Fatal(err)
	}
	card := svc.relatedPostCard(ctx, main, &shared)
	if card == nil || card.ID != relatedID || card.Title != "Related one" {
		t.Fatalf("shared viewer's related card: %+v", card)
	}
	if svc.relatedPostCard(ctx, main, &stranger) != nil {
		t.Fatal("stranger got the related card once the post was shared with someone else")
	}
	// 18+: the refusals come back from the direct read with their codes, and
	// the card is withheld from whoever may not open the post.
	if _, err := pool.Exec(ctx, `UPDATE posts SET age_restricted = true WHERE id = ANY($1)`, []uuid.UUID{mainID, relatedID}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.GetPost(ctx, mainID, nil); !errors.Is(err, ErrAgeSignIn) {
		t.Fatalf("anonymous on 18+: %v", err)
	}
	if _, err := svc.GetPost(ctx, mainID, &minor); !errors.Is(err, ErrAgeRestricted) {
		t.Fatalf("minor on 18+: %v", err)
	}
	if _, err := svc.GetPost(ctx, mainID, &stranger); !errors.Is(err, ErrAgeUnverified) {
		t.Fatalf("unknown age on 18+: %v", err)
	}
	if d, err := svc.GetPost(ctx, relatedID, &stranger); d != nil || err != nil {
		t.Fatalf("private + 18+ for a stranger must stay the plain 404, not an age code: %v %v", d, err)
	}
	if svc.relatedPostCard(ctx, main, &minor) != nil {
		t.Fatal("minor on the list got an 18+ related card")
	}
	if svc.relatedPostCard(ctx, main, &shared) == nil {
		t.Fatal("adult on the list lost the related card")
	}
	// The id-only reads (GET /v1/videos/:id, the comments reads) share
	// singlePostRead: the same 404 first, then the same age refusals.
	for _, tc := range []struct {
		post   uuid.UUID
		viewer *uuid.UUID
		want   error
	}{
		{relatedID, &stranger, ErrPostNotVisible},
		{relatedID, &minor, ErrAgeRestricted},
		{mainID, nil, ErrAgeSignIn},
		{mainID, &stranger, ErrAgeUnverified},
		{relatedID, &shared, nil},
		{relatedID, &author, nil},
	} {
		if err := svc.singlePostRead(ctx, tc.post, tc.viewer); !errors.Is(err, tc.want) {
			t.Fatalf("singlePostRead(%s, %v): %v want %v", tc.post, tc.viewer, err, tc.want)
		}
		if _, _, err := svc.ListCommentsSortedPG(ctx, tc.post, tc.viewer, "", 5, "top"); !errors.Is(err, tc.want) {
			t.Fatalf("comments(%s, %v): %v want %v", tc.post, tc.viewer, err, tc.want)
		}
	}
}
