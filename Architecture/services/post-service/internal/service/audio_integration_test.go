//go:build integration

package service

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/atpost/post-service/internal/store/postgres"
)

// Attaching a sound, against a real Postgres (2026-09-29): the one
// audio_tracks table carries the rows of two services, so the read is proven
// on both shapes, and the rule is proven with the real media-access decision
// rather than a fake of it. Migration 058 is itself under test: this
// database was created by post-service alone, where media_id was NOT NULL
// and the owner's columns did not exist.
//
//	POSTGRES_DSN=postgres://…/post_it_test go test -tags integration ./internal/service/ -run AudioIT -v
//
// Refuses any database whose name does not end in _test (openHubMediaDB).

type audioITFixture struct {
	*anonMediaFixture
	store    *postgres.Store
	stranger uuid.UUID
	// extracted: a sound media-service made from the fixture's video (no
	// media_id). uploaded: a row in post-service's older shape (media_id,
	// no source). orphan: neither. processing: not ready yet.
	extracted, uploaded, orphan, processing uuid.UUID
	target                                  uuid.UUID // the stranger's own post
}

func newAudioITFixture(t *testing.T) *audioITFixture {
	t.Helper()
	f := &audioITFixture{
		anonMediaFixture: newAnonMediaFixture(t),
		stranger:         uuid.New(),
		extracted:        uuid.New(), uploaded: uuid.New(), orphan: uuid.New(), processing: uuid.New(),
		target: uuid.New(),
	}
	f.store = postgres.New(f.pool)
	f.set(t, `INSERT INTO users (id) VALUES ($1) ON CONFLICT DO NOTHING`, f.stranger)
	f.set(t, `INSERT INTO audio_tracks (id, title, source_media_id, status) VALUES ($1, 'Extracted take', $2, 'ready')`, f.extracted, f.media)
	f.set(t, `INSERT INTO audio_tracks (id, title, artist, media_id, status) VALUES ($1, 'Uploaded take', 'Asha', $2, 'ready')`, f.uploaded, f.media)
	f.set(t, `INSERT INTO audio_tracks (id, title, status) VALUES ($1, 'Lost take', 'ready')`, f.orphan)
	f.set(t, `INSERT INTO audio_tracks (id, title, source_media_id) VALUES ($1, 'Early take', $2)`, f.processing, f.media)
	f.set(t, `
		INSERT INTO posts (id, author_id, text, visibility, content_type, review_status, created_at, updated_at)
		VALUES ($1, $2, 'a reel that plays a sound', 'public', 'flick', 'approved', NOW(), NOW())`, f.target, f.stranger)
	t.Cleanup(func() {
		bg := context.Background()
		_, _ = f.pool.Exec(bg, `UPDATE posts SET audio_track_id = NULL WHERE audio_track_id = ANY($1)`, f.sounds())
		_, _ = f.pool.Exec(bg, `DELETE FROM post_engagement_counts WHERE post_id = $1`, f.target)
		_, _ = f.pool.Exec(bg, `DELETE FROM posts WHERE id = $1`, f.target)
		_, _ = f.pool.Exec(bg, `DELETE FROM audio_tracks WHERE id = ANY($1)`, f.sounds())
		_, _ = f.pool.Exec(bg, `DELETE FROM users WHERE id = $1`, f.stranger)
	})
	return f
}

func (f *audioITFixture) sounds() []uuid.UUID {
	return []uuid.UUID{f.extracted, f.uploaded, f.orphan, f.processing}
}

// playing reads the sound the stranger's post is linked to.
func (f *audioITFixture) playing(t *testing.T) *uuid.UUID {
	t.Helper()
	var id *uuid.UUID
	if err := f.pool.QueryRow(context.Background(), `SELECT audio_track_id FROM posts WHERE id = $1`, f.target).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

func (f *audioITFixture) uses(t *testing.T, sound uuid.UUID) int {
	t.Helper()
	var n int
	if err := f.pool.QueryRow(context.Background(), `SELECT use_count FROM audio_tracks WHERE id = $1`, sound).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestAudioITReadsBothShapesOfTheSharedTable(t *testing.T) {
	f := newAudioITFixture(t)
	ctx := context.Background()

	got, err := f.store.GetAudioTrack(ctx, f.extracted)
	if err != nil {
		t.Fatalf("a sound media-service wrote: %v", err)
	}
	if got.MediaID == nil || *got.MediaID != f.media || got.Status != "ready" || got.Title != "Extracted take" || !got.IsPublic || got.Artist != "" {
		t.Fatalf("extracted sound read as %+v", got)
	}

	got, err = f.store.GetAudioTrack(ctx, f.uploaded)
	if err != nil {
		t.Fatalf("a sound in the older shape: %v", err)
	}
	if got.MediaID == nil || *got.MediaID != f.media || got.Artist != "Asha" {
		t.Fatalf("uploaded sound read as %+v", got)
	}

	got, err = f.store.GetAudioTrack(ctx, f.orphan)
	if err != nil || got.MediaID != nil {
		t.Fatalf("a sound with no media read as %+v (%v), want no media", got, err)
	}

	// The column default is the owner's: a row that names no status is not ready.
	got, err = f.store.GetAudioTrack(ctx, f.processing)
	if err != nil || got.Status != "processing" {
		t.Fatalf("a sound with no status read as %+v (%v), want processing", got, err)
	}
}

func TestAudioITAttachFollowsTheSourceVideosAudience(t *testing.T) {
	f := newAudioITFixture(t)
	ctx := context.Background()
	attach := func(sound uuid.UUID) error { return f.svc.AttachAudioToPost(ctx, f.stranger, f.target, sound) }

	// The video is public: anyone may play its sound on their own post.
	if err := attach(f.extracted); err != nil {
		t.Fatalf("a public video's sound: %v", err)
	}
	if got := f.playing(t); got == nil || *got != f.extracted {
		t.Fatalf("post plays %v, want %s", got, f.extracted)
	}
	if n := f.uses(t, f.extracted); n != 1 {
		t.Fatalf("use_count = %d, want 1", n)
	}

	// Made private, it is the author's alone; the link already made stays.
	f.set(t, `UPDATE posts SET visibility = 'private' WHERE id = $1`, f.post)
	f.set(t, `UPDATE posts SET audio_track_id = NULL WHERE id = $1`, f.target)
	for name, sound := range map[string]uuid.UUID{"extracted": f.extracted, "uploaded": f.uploaded} {
		if err := attach(sound); !errors.Is(err, ErrAudioTrackNotFound) {
			t.Fatalf("%s sound of a private video, by a stranger: got %v want %v", name, err, ErrAudioTrackNotFound)
		}
	}
	if got := f.playing(t); got != nil {
		t.Fatalf("a refused sound was linked: %v", got)
	}
	if n := f.uses(t, f.extracted); n != 1 {
		t.Fatalf("a refused use was counted: use_count = %d", n)
	}
	if err := f.svc.AttachAudioToPost(ctx, f.author, f.post, f.extracted); err != nil {
		t.Fatalf("the author on their own private video's sound: %v", err)
	}

	f.set(t, `UPDATE posts SET visibility = 'public', audio_track_id = NULL WHERE id = $1`, f.post)
	if err := attach(f.orphan); !errors.Is(err, ErrAudioTrackNotFound) {
		t.Fatalf("a sound with no media: got %v want %v", err, ErrAudioTrackNotFound)
	}
	if err := attach(f.processing); !errors.Is(err, ErrAudioTrackNotReady) {
		t.Fatalf("a sound still processing: got %v want %v", err, ErrAudioTrackNotReady)
	}
	if err := attach(uuid.New()); !errors.Is(err, ErrAudioTrackNotFound) {
		t.Fatalf("no such sound: got %v want %v", err, ErrAudioTrackNotFound)
	}
}
