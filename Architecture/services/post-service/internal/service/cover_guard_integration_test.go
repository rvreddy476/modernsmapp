//go:build integration

package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"

	"github.com/atpost/post-service/internal/store/postgres"
)

// The cover on the two WRITE paths, against a real Postgres (2026-09-29):
// CreatePost (POST /v1/posts and every draft publish) and SetCoverFrame
// (POST /v1/videos/:id/cover-frame) stored cover_media_id as sent. Both now
// ask cover_guard.go first.
//
//	POSTGRES_DSN=postgres://…/post_it_test go test -tags integration ./internal/service/ -run CoverWrite -v
//
// Refuses any database whose name does not end in _test (openHubMediaDB).

type coverWriteFixture struct {
	*anonMediaFixture
	stranger      uuid.UUID
	ownImage      uuid.UUID
	ownOtherVideo uuid.UUID
	theirImage    uuid.UUID
	theirVideo    uuid.UUID
	created       []uuid.UUID
}

func newCoverWriteFixture(t *testing.T) *coverWriteFixture {
	t.Helper()
	f := &coverWriteFixture{
		anonMediaFixture: newAnonMediaFixture(t),
		stranger:         uuid.New(),
		ownImage:         uuid.New(), ownOtherVideo: uuid.New(),
		theirImage: uuid.New(), theirVideo: uuid.New(),
	}
	// CreatePost touches the cache after the insert; a client that cannot
	// connect fails those calls without reaching any Redis.
	rdb := redis.NewClient(&redis.Options{
		Addr: "127.0.0.1:1", DialTimeout: 50 * time.Millisecond, MaxRetries: -1,
	})
	t.Cleanup(func() { _ = rdb.Close() })
	f.svc = New(postgres.New(f.pool), nil, rdb)

	f.set(t, `INSERT INTO users (id) VALUES ($1) ON CONFLICT DO NOTHING`, f.stranger)
	for _, m := range []struct {
		id, uploader uuid.UUID
		kind         string
	}{
		{f.ownImage, f.author, "image"}, {f.ownOtherVideo, f.author, "video"},
		{f.theirImage, f.stranger, "image"}, {f.theirVideo, f.stranger, "video"},
	} {
		f.set(t, `INSERT INTO media_assets (id, uploader_id, file_type, processing_status, moderation_status) VALUES ($1,$2,$3,'ready','passed')`, m.id, m.uploader, m.kind)
	}
	t.Cleanup(func() {
		bg := context.Background()
		for _, id := range f.created {
			for _, q := range []string{
				`DELETE FROM post_outbox_events WHERE aggregate_id = $1`,
				`DELETE FROM video_metadata WHERE post_id = $1`,
				`DELETE FROM post_media WHERE post_id = $1`,
				`DELETE FROM post_engagement_counts WHERE post_id = $1`,
				`DELETE FROM posts WHERE id = $1`,
			} {
				_, _ = f.pool.Exec(bg, q, id)
			}
		}
		_, _ = f.pool.Exec(bg, `UPDATE posts SET cover_media_id = NULL WHERE id = $1`, f.post)
		_, _ = f.pool.Exec(bg, `DELETE FROM video_metadata WHERE post_id = $1`, f.post)
		_, _ = f.pool.Exec(bg, `DELETE FROM media_assets WHERE id = ANY($1)`,
			[]uuid.UUID{f.ownImage, f.ownOtherVideo, f.theirImage, f.theirVideo})
		_, _ = f.pool.Exec(bg, `DELETE FROM users WHERE id = $1`, f.stranger)
	})
	return f
}

// coverOf reads the stored cover straight from the row.
func (f *coverWriteFixture) coverOf(t *testing.T, post uuid.UUID) *uuid.UUID {
	t.Helper()
	var id *uuid.UUID
	if err := f.pool.QueryRow(context.Background(), `SELECT cover_media_id FROM posts WHERE id = $1`, post).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

func (f *coverWriteFixture) postExists(t *testing.T, id uuid.UUID) bool {
	t.Helper()
	var n int
	if err := f.pool.QueryRow(context.Background(), `SELECT COUNT(*) FROM posts WHERE id = $1`, id).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n > 0
}

// create runs CreatePost as the author with one cover; the post id is fixed
// up front so a refusal can be shown to have written nothing.
func (f *coverWriteFixture) create(t *testing.T, contentType, postType string, media []uuid.UUID, cover uuid.UUID) (uuid.UUID, error) {
	t.Helper()
	id := uuid.New()
	f.created = append(f.created, id)
	_, err := f.svc.CreatePost(context.Background(), &CreatePostInput{
		PostID: &id, AuthorID: f.author, Text: "cover write proof " + id.String()[:8],
		Visibility: "public", VisibilityExplicit: true,
		ContentType: contentType, PostType: postType,
		MediaIDs: media, CoverMediaID: &cover,
	})
	return id, err
}

func assertCoverRefused(t *testing.T, err, want error) {
	t.Helper()
	var coverErr *CoverMediaError
	if !errors.As(err, &coverErr) {
		t.Fatalf("want a refused cover (%v), got %v", want, err)
	}
	if !errors.Is(err, want) {
		t.Fatalf("want %v, got %v", want, err)
	}
}

func TestCoverWriteCreateRefusesAStrangersAsset(t *testing.T) {
	f := newCoverWriteFixture(t)
	for _, tc := range []struct {
		name  string
		cover uuid.UUID
		want  error
	}{
		{"a stranger's image", f.theirImage, ErrMediaNotOwned},
		{"a stranger's video", f.theirVideo, ErrMediaNotOwned},
		{"no such asset", uuid.New(), ErrMediaNotFound},
		{"own video the post does not attach", f.ownOtherVideo, ErrMediaTypeMismatch},
	} {
		t.Run(tc.name, func(t *testing.T) {
			id, err := f.create(t, "post", "image", []uuid.UUID{f.ownImage}, tc.cover)
			assertCoverRefused(t, err, tc.want)
			if f.postExists(t, id) {
				t.Fatal("a refused cover still created the post")
			}
		})
	}
}

func TestCoverWriteCreateAcceptsTheAuthorsOwnUpload(t *testing.T) {
	f := newCoverWriteFixture(t)

	// The studios upload the chosen frame as the author's own image.
	id, err := f.create(t, "flick", "video", []uuid.UUID{f.ownOtherVideo}, f.ownImage)
	if err != nil {
		t.Fatalf("own image as cover: %v", err)
	}
	if got := f.coverOf(t, id); got == nil || *got != f.ownImage {
		t.Fatalf("stored cover = %v, want %s", got, f.ownImage)
	}

	// A reel draft whose cover came from the frame picker names the video.
	id, err = f.create(t, "flick", "video", []uuid.UUID{f.ownOtherVideo}, f.ownOtherVideo)
	if err != nil {
		t.Fatalf("the post's own video as cover: %v", err)
	}
	if got := f.coverOf(t, id); got == nil || *got != f.ownOtherVideo {
		t.Fatalf("stored cover = %v, want %s", got, f.ownOtherVideo)
	}
}

func TestCoverWriteCoverFrameRefusesAStrangersAsset(t *testing.T) {
	f := newCoverWriteFixture(t)
	ctx := context.Background()
	f.set(t, `UPDATE posts SET cover_media_id = $1 WHERE id = $2`, f.ownImage, f.post)
	thumb := "https://example.invalid/frame.jpg"

	for _, tc := range []struct {
		name  string
		cover uuid.UUID
		want  error
	}{
		{"a stranger's image", f.theirImage, ErrMediaNotOwned},
		{"a stranger's video", f.theirVideo, ErrMediaNotOwned},
		{"no such asset", uuid.New(), ErrMediaNotFound},
		{"own video the post does not attach", f.ownOtherVideo, ErrMediaTypeMismatch},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cover := tc.cover
			err := f.svc.SetCoverFrame(ctx, f.post, f.author, &cover, &thumb)
			assertCoverRefused(t, err, tc.want)
			if got := f.coverOf(t, f.post); got == nil || *got != f.ownImage {
				t.Fatalf("a refused cover changed the row: %v", got)
			}
			var n int
			if err := f.pool.QueryRow(ctx, `SELECT COUNT(*) FROM video_metadata WHERE post_id = $1 AND thumbnail_url = $2`, f.post, thumb).Scan(&n); err != nil {
				t.Fatal(err)
			}
			if n != 0 {
				t.Fatal("a refused cover still wrote the thumbnail")
			}
		})
	}

	// Somebody else's post is refused before the cover is looked at.
	own := f.ownImage
	if err := f.svc.SetCoverFrame(ctx, f.post, f.stranger, &own, nil); err == nil || err.Error() != "unauthorized" {
		t.Fatalf("a stranger setting the cover: %v", err)
	}
}

func TestCoverWriteCoverFrameAcceptsTheAuthorsOwnUpload(t *testing.T) {
	f := newCoverWriteFixture(t)
	ctx := context.Background()

	own := f.ownImage
	if err := f.svc.SetCoverFrame(ctx, f.post, f.author, &own, nil); err != nil {
		t.Fatalf("own image as cover: %v", err)
	}
	if got := f.coverOf(t, f.post); got == nil || *got != f.ownImage {
		t.Fatalf("stored cover = %v, want %s", got, f.ownImage)
	}

	// The frame picker: the post's own video, the picture in thumbnail_url.
	video := f.media
	if err := f.svc.SetCoverFrame(ctx, f.post, f.author, &video, nil); err != nil {
		t.Fatalf("the post's own video as cover: %v", err)
	}
	if got := f.coverOf(t, f.post); got == nil || *got != f.media {
		t.Fatalf("stored cover = %v, want %s", got, f.media)
	}
}

func TestCoverWritePostMediaIDs(t *testing.T) {
	f := newCoverWriteFixture(t)
	ids, err := postgres.New(f.pool).PostMediaIDs(context.Background(), f.post)
	if err != nil || len(ids) != 1 || ids[0] != f.media {
		t.Fatalf("PostMediaIDs = %v (%v), want [%s]", ids, err, f.media)
	}
	ids, err = postgres.New(f.pool).PostMediaIDs(context.Background(), uuid.New())
	if err != nil || len(ids) != 0 {
		t.Fatalf("PostMediaIDs of no post = %v (%v)", ids, err)
	}
}
