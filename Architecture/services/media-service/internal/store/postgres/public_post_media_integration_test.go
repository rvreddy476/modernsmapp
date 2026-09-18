//go:build integration

package postgres

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Open Graph posters for shared links (2026-09-18) — the predicate itself.
//
// delivery/public_poster_test.go proves the gate only ever consults this for
// an anonymous still request. What it cannot prove is WHICH posts count as
// public, because that is one SQL EXISTS over posts / post_media /
// media_assets. This suite is that half, and it is the reason the rule can be
// trusted to be narrower than post-service's own post visibility (which admits
// unlisted and a blank visibility).
//
// Run with:
//
//	POSTGRES_DSN=postgres://…/app_test go test -tags integration ./internal/store/postgres/ -run PublicPostMedia -v
//
// It requires the media-service and post-service schemas, including
// post-service migration 042 (posts.publish_at).

// requireScratchDatabase refuses to run against anything but a scratch
// database. These tests INSERT posts, and a post seeded into a real database
// is a post somebody can see.
func requireScratchDatabase(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	var name string
	if err := pool.QueryRow(context.Background(), `SELECT current_database()`).Scan(&name); err != nil {
		t.Fatalf("current_database: %v", err)
	}
	if !strings.HasSuffix(name, "_test") {
		t.Fatalf("refusing to seed posts into %q: name a scratch database ending in _test "+
			"(POSTGRES_DSN=%q)", name, os.Getenv("POSTGRES_DSN"))
	}
}

// seedPosterMedia inserts a ready, moderation-passed asset — the state a
// poster is normally in.
func seedPosterMedia(t *testing.T, pool *pgxpool.Pool, moderation string) uuid.UUID {
	t.Helper()
	id := uuid.New()
	_, err := pool.Exec(context.Background(), `
		INSERT INTO media_assets (id, uploader_id, file_type, media_subtype, mime_type,
		    file_size_bytes, storage_bucket, storage_key, processing_status, moderation_status,
		    created_at, updated_at)
		VALUES ($1, $2, 'video', 'general', 'video/mp4', 100, 'media', $3, 'ready', $4, NOW(), NOW())`,
		id, uuid.New(), "test/"+id.String(), moderation)
	if err != nil {
		t.Fatalf("seed media: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM media_assets WHERE id = $1`, id)
	})
	return id
}

type postShape struct {
	visibility   string
	reviewStatus string
	scheduled    bool
	deleted      bool
	// cover attaches the asset as the post's cover frame instead of through
	// post_media — the shape a Tube video's poster actually has.
	cover bool
}

func seedPostFor(t *testing.T, pool *pgxpool.Pool, mediaID uuid.UUID, shape postShape) uuid.UUID {
	t.Helper()
	ctx := context.Background()
	postID := uuid.New()
	var cover any
	if shape.cover {
		cover = mediaID
	}
	_, err := pool.Exec(ctx, `
		INSERT INTO posts (id, author_id, text, visibility, content_type, review_status,
		    cover_media_id, publish_at, deleted_at, created_at, updated_at)
		VALUES ($1, $2, 'x', $3, 'video', $4, $5,
		    CASE WHEN $6 THEN NOW() + INTERVAL '1 day' END,
		    CASE WHEN $7 THEN NOW() END,
		    NOW(), NOW())`,
		postID, uuid.New(), shape.visibility, shape.reviewStatus, cover, shape.scheduled, shape.deleted)
	if err != nil {
		t.Fatalf("seed post: %v", err)
	}
	if !shape.cover {
		if _, err := pool.Exec(ctx,
			`INSERT INTO post_media (post_id, media_id, kind) VALUES ($1,$2,'video')`, postID, mediaID); err != nil {
			t.Fatalf("seed post_media: %v", err)
		}
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM post_media WHERE post_id = $1`, postID)
		_, _ = pool.Exec(ctx, `DELETE FROM posts WHERE id = $1`, postID)
	})
	return postID
}

func TestPublicPostMedia_PublicPostIsPublic(t *testing.T) {
	pool := testPool(t)
	requireScratchDatabase(t, pool)
	store := New(pool)

	for _, attachment := range []struct {
		name  string
		cover bool
	}{{"attached", false}, {"cover_frame", true}} {
		t.Run(attachment.name, func(t *testing.T) {
			mediaID := seedPosterMedia(t, pool, "passed")
			seedPostFor(t, pool, mediaID, postShape{visibility: "public", reviewStatus: "approved", cover: attachment.cover})

			public, err := store.MediaIsOnPublicPost(context.Background(), mediaID.String())
			if err != nil {
				t.Fatalf("lookup: %v", err)
			}
			if !public {
				t.Fatal("a public, approved, live post's asset was not recognised as public")
			}
		})
	}
}

// Everything short of genuinely public. "unlisted" and "" head the list on
// purpose: post-service's own rule treats both as visible to a signed-in
// viewer, and delegating to it — which is what setting allowAnonymous on the
// post authorizer would have done — would have handed link-only posts to
// search engines.
func TestPublicPostMedia_NarrowerThanPostVisibility(t *testing.T) {
	pool := testPool(t)
	requireScratchDatabase(t, pool)
	store := New(pool)

	cases := []struct {
		name  string
		shape postShape
	}{
		{"unlisted", postShape{visibility: "unlisted", reviewStatus: "approved"}},
		{"blank_visibility", postShape{visibility: "", reviewStatus: "approved"}},
		{"followers", postShape{visibility: "followers", reviewStatus: "approved"}},
		{"close_friends", postShape{visibility: "close_friends", reviewStatus: "approved"}},
		{"private", postShape{visibility: "private", reviewStatus: "approved"}},
		{"pending_review", postShape{visibility: "public", reviewStatus: "pending"}},
		{"flagged", postShape{visibility: "public", reviewStatus: "flagged"}},
		{"rejected", postShape{visibility: "public", reviewStatus: "rejected"}},
		{"scheduled", postShape{visibility: "public", reviewStatus: "approved", scheduled: true}},
		{"soft_deleted", postShape{visibility: "public", reviewStatus: "approved", deleted: true}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			mediaID := seedPosterMedia(t, pool, "passed")
			seedPostFor(t, pool, mediaID, tc.shape)

			public, err := store.MediaIsOnPublicPost(context.Background(), mediaID.String())
			if err != nil {
				t.Fatalf("lookup: %v", err)
			}
			if public {
				t.Fatalf("a %s post's poster was offered to anonymous callers", tc.name)
			}
		})
	}
}

// The canonical media gate is not waived by the post being public.
func TestPublicPostMedia_ModerationIsNotWaivedByAPublicPost(t *testing.T) {
	pool := testPool(t)
	requireScratchDatabase(t, pool)
	store := New(pool)

	for _, moderation := range []string{"pending", "manual_review", "rejected", "failed", ""} {
		t.Run("moderation_"+moderation, func(t *testing.T) {
			mediaID := seedPosterMedia(t, pool, moderation)
			seedPostFor(t, pool, mediaID, postShape{visibility: "public", reviewStatus: "approved"})

			public, err := store.MediaIsOnPublicPost(context.Background(), mediaID.String())
			if err != nil {
				t.Fatalf("lookup: %v", err)
			}
			if public {
				t.Fatalf("an asset with moderation_status=%q was offered to anonymous callers", moderation)
			}
		})
	}
}

// An asset nothing references has no audience, so it has no public poster
// either — the same rule the post authority applies.
func TestPublicPostMedia_UnreferencedAssetIsNotPublic(t *testing.T) {
	pool := testPool(t)
	requireScratchDatabase(t, pool)
	store := New(pool)

	mediaID := seedPosterMedia(t, pool, "passed")
	public, err := store.MediaIsOnPublicPost(context.Background(), mediaID.String())
	if err != nil {
		t.Fatalf("lookup: %v", err)
	}
	if public {
		t.Fatal("an unreferenced asset was treated as a public post's poster")
	}
}

// One public post is enough, and one private post is not a veto — an asset
// reshared into a private post keeps the public poster it already had.
func TestPublicPostMedia_PublicPostAmongPrivateOnesStillCounts(t *testing.T) {
	pool := testPool(t)
	requireScratchDatabase(t, pool)
	store := New(pool)

	mediaID := seedPosterMedia(t, pool, "passed")
	seedPostFor(t, pool, mediaID, postShape{visibility: "private", reviewStatus: "approved"})
	seedPostFor(t, pool, mediaID, postShape{visibility: "public", reviewStatus: "approved"})

	public, err := store.MediaIsOnPublicPost(context.Background(), mediaID.String())
	if err != nil {
		t.Fatalf("lookup: %v", err)
	}
	if !public {
		t.Fatal("a public post's poster was refused because the asset is also in a private post")
	}
}

func TestPublicPostMedia_MalformedIDIsADenialNotAnError(t *testing.T) {
	pool := testPool(t)
	requireScratchDatabase(t, pool)
	store := New(pool)

	public, err := store.MediaIsOnPublicPost(context.Background(), "not-a-uuid")
	if err != nil {
		t.Fatalf("malformed id should not error: %v", err)
	}
	if public {
		t.Fatal("malformed id was treated as public")
	}
}
