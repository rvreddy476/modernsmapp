//go:build integration

package service

import (
	"context"
	"testing"
	"time"

	"github.com/atpost/post-service/internal/store/postgres"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Signed-out playback and the P-8 media-gate gaps against a real Postgres
// (2026-09-29): the SQL behind PostIDsByMediaID / GetPost / GetPostsByIDs /
// GetMediaAccessFacts / AnyHidden carries publish_at, deleted_at,
// age_restricted, tier_required_id and post_hidden_authors to the rule
// that media_access_gate_test.go proves over in-memory rows.
//
//	POSTGRES_DSN=postgres://…/post_it_test go test -tags integration ./internal/service/ -run AnonymousMedia -v
//
// Refuses any database whose name does not end in _test (openHubMediaDB).

type anonMediaFixture struct {
	pool   *pgxpool.Pool
	svc    *Service
	author uuid.UUID
	viewer uuid.UUID
	media  uuid.UUID
	post   uuid.UUID
}

func newAnonMediaFixture(t *testing.T) *anonMediaFixture {
	t.Helper()
	pool := openHubMediaDB(t)
	ctx := context.Background()
	f := &anonMediaFixture{pool: pool, author: uuid.New(), viewer: uuid.New(), media: uuid.New(), post: uuid.New()}
	for _, id := range []uuid.UUID{f.author, f.viewer} {
		if _, err := pool.Exec(ctx, `INSERT INTO users (id) VALUES ($1) ON CONFLICT DO NOTHING`, id); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := pool.Exec(ctx, `INSERT INTO media_assets (id, uploader_id, file_type, processing_status, moderation_status) VALUES ($1,$2,'video','ready','passed')`, f.media, f.author); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO posts (id, author_id, text, visibility, content_type, review_status, created_at, updated_at)
		VALUES ($1, $2, 'signed-out proof', 'public', 'long_video', 'approved', NOW(), NOW())`, f.post, f.author); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO post_media (post_id, media_id, kind) VALUES ($1,$2,'video')`, f.post, f.media); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		bg := context.Background()
		_, _ = pool.Exec(bg, `DELETE FROM post_hidden_authors WHERE user_id = $1`, f.author)
		_, _ = pool.Exec(bg, `DELETE FROM post_media WHERE post_id = $1`, f.post)
		_, _ = pool.Exec(bg, `DELETE FROM post_engagement_counts WHERE post_id = $1`, f.post)
		_, _ = pool.Exec(bg, `DELETE FROM posts WHERE id = $1`, f.post)
		_, _ = pool.Exec(bg, `DELETE FROM media_assets WHERE id = $1`, f.media)
		for _, id := range []uuid.UUID{f.author, f.viewer} {
			_, _ = pool.Exec(bg, `DELETE FROM users WHERE id = $1`, id)
		}
	})
	store := postgres.New(pool)
	f.svc = New(store, nil, nil)
	f.svc.WithStoryAudience(NewStoryAudience(noRelationships{}))
	f.svc.now = func() time.Time { return hubNow }
	f.svc.SetBirthDateSource(&fakeBirthDates{dob: map[uuid.UUID]*time.Time{f.viewer: dayp(1990, 1, 1)}})
	return f
}

func (f *anonMediaFixture) set(t *testing.T, sql string, args ...any) {
	t.Helper()
	if _, err := f.pool.Exec(context.Background(), sql, args...); err != nil {
		t.Fatal(err)
	}
}

// plays runs the single and batch paths and insists they agree.
func (f *anonMediaFixture) plays(t *testing.T, viewer uuid.UUID) bool {
	t.Helper()
	ctx := context.Background()
	single, err := f.svc.ViewerMayAccessMedia(ctx, viewer, f.media)
	if err != nil {
		t.Fatal(err)
	}
	batch, err := f.svc.ViewerMayAccessMediaBatch(ctx, viewer, []uuid.UUID{f.media})
	if err != nil {
		t.Fatal(err)
	}
	if single.Allowed != batch[f.media].Allowed {
		t.Fatalf("single (%v %s) and batch (%v %s) disagree", single.Allowed, single.Reason, batch[f.media].Allowed, batch[f.media].Reason)
	}
	return single.Allowed
}

func TestAnonymousMediaAgainstPostgres(t *testing.T) {
	f := newAnonMediaFixture(t)
	post := func(col string, val any) { f.set(t, `UPDATE posts SET `+col+` = $1 WHERE id = $2`, val, f.post) }

	if !f.plays(t, uuid.Nil) {
		t.Fatal("anonymous refused a public, approved, live post")
	}
	if !f.plays(t, f.viewer) || !f.plays(t, f.author) {
		t.Fatal("signed-in viewer or owner refused the public post")
	}

	// Each clause of the signed-out rule, from the row.
	for _, tc := range []struct {
		name  string
		apply func()
		reset func()
	}{
		{"unlisted", func() { post("visibility", "unlisted") }, func() { post("visibility", "public") }},
		{"followers", func() { post("visibility", "followers") }, func() { post("visibility", "public") }},
		{"private", func() { post("visibility", "private") }, func() { post("visibility", "public") }},
		{"pending review", func() { post("review_status", "pending") }, func() { post("review_status", "approved") }},
		{"scheduled", func() { post("publish_at", hubNow.Add(24*time.Hour)) }, func() { post("publish_at", nil) }},
		{"age-restricted", func() { post("age_restricted", true) }, func() { post("age_restricted", false) }},
		{"members-only", func() { post("tier_required_id", uuid.New()) }, func() { post("tier_required_id", nil) }},
		{"soft-deleted", func() { post("deleted_at", hubNow) }, func() { post("deleted_at", nil) }},
		{"hidden author", func() {
			f.set(t, `INSERT INTO post_hidden_authors (user_id, reason) VALUES ($1, 'deactivated') ON CONFLICT DO NOTHING`, f.author)
		}, func() { f.set(t, `DELETE FROM post_hidden_authors WHERE user_id = $1`, f.author) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tc.apply()
			defer tc.reset()
			if f.plays(t, uuid.Nil) {
				t.Fatalf("anonymous played a %s post", tc.name)
			}
			if !f.plays(t, f.author) {
				t.Fatalf("owner refused their own %s post", tc.name)
			}
		})
	}
	if !f.plays(t, uuid.Nil) {
		t.Fatal("public post did not come back after the resets")
	}
}

// P-8 (a) for a SIGNED-IN viewer, from the row: a scheduled post and a hidden
// author refuse a stranger who could otherwise play the public post.
func TestSignedInMediaGapsAgainstPostgres(t *testing.T) {
	f := newAnonMediaFixture(t)
	f.set(t, `UPDATE posts SET publish_at = $1 WHERE id = $2`, hubNow.Add(24*time.Hour), f.post)
	if f.plays(t, f.viewer) {
		t.Fatal("a viewer played a scheduled post before its time")
	}
	f.set(t, `UPDATE posts SET publish_at = NULL WHERE id = $1`, f.post)
	if !f.plays(t, f.viewer) {
		t.Fatal("the published post did not open")
	}
	f.set(t, `INSERT INTO post_hidden_authors (user_id, reason) VALUES ($1, 'pending_deletion') ON CONFLICT DO NOTHING`, f.author)
	if f.plays(t, f.viewer) {
		t.Fatal("a viewer played a hidden author's post")
	}
	if !f.plays(t, f.author) {
		t.Fatal("the hidden author lost their own post")
	}
	// A signed-in stranger never downloads a hidden author's post either.
	if ok, err := f.svc.ViewerMayDownloadMedia(context.Background(), f.viewer, f.media); err != nil || ok {
		t.Fatalf("download of a hidden author's post: %v %v", ok, err)
	}
}

// P-8 (d): the cached-body revalidation reads publish_at from the row.
func TestPostAccessStateCarriesPublishAt(t *testing.T) {
	f := newAnonMediaFixture(t)
	store := postgres.New(f.pool)
	state, err := store.GetPostAccessState(context.Background(), f.post)
	if err != nil || state == nil {
		t.Fatalf("state: %+v %v", state, err)
	}
	if state.PublishAt != nil {
		t.Fatalf("live post reported publish_at %v", state.PublishAt)
	}
	at := hubNow.Add(24 * time.Hour)
	f.set(t, `UPDATE posts SET publish_at = $1 WHERE id = $2`, at, f.post)
	state, err = store.GetPostAccessState(context.Background(), f.post)
	if err != nil || state == nil || state.PublishAt == nil || !state.PublishAt.Equal(at) {
		t.Fatalf("rescheduled post reported %+v %v", state, err)
	}
	cached := &postgres.Post{ID: f.post, AuthorID: f.author, Visibility: "public", ReviewStatus: "approved"}
	applyPostAccessState(cached, state)
	stranger := f.viewer
	if !hiddenFromViewer(cached, &stranger) {
		t.Fatal("a rescheduled post read as live through the cached body")
	}
}
