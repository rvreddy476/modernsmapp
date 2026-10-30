//go:build integration

package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/atpost/post-service/internal/store/postgres"
	"github.com/google/uuid"
)

// The new-use rule of a library sound against a real Postgres (reels audit
// 2026-10-30): LibrarySoundReuseBlock reads media-service's audio_library
// row the way media-service does, and AttachAudioToPost refuses a sound
// withdrawn, switched off or out of licence while its status stays 'ready'.
//
//	POSTGRES_DSN=postgres://…/post_it_test go test -tags integration ./internal/service/ -run SoundReuseIT -v
//
// post_it_test has no audio_library (media-service's table): the test
// gives it the columns read here for its own run and drops it after.
func TestSoundReuseITLibraryRowDecides(t *testing.T) {
	f := newSoundsITFixture(t)
	ctx := context.Background()

	var hasLibrary bool
	if err := f.pool.QueryRow(ctx, `SELECT to_regclass('audio_library') IS NOT NULL`).Scan(&hasLibrary); err != nil {
		t.Fatal(err)
	}
	// No table: no library row, read as media-service reads a missing row.
	if !hasLibrary {
		if block, err := f.store.LibrarySoundReuseBlock(ctx, uuid.New()); err != nil || block != "" {
			t.Fatalf("no audio_library table: %q, %v", block, err)
		}
		f.set(t, `CREATE TABLE audio_library (
			id UUID PRIMARY KEY, title TEXT NOT NULL DEFAULT '', artist TEXT NOT NULL DEFAULT '',
			duration_ms INT NOT NULL DEFAULT 0, audio_url TEXT NOT NULL DEFAULT '', source TEXT NOT NULL DEFAULT 'licensed',
			is_active BOOLEAN NOT NULL DEFAULT TRUE, withdrawn_at TIMESTAMPTZ, license_expires_at TIMESTAMPTZ)`)
		t.Cleanup(func() { _, _ = f.pool.Exec(context.Background(), `DROP TABLE IF EXISTS audio_library`) })
	}

	past, future := time.Now().Add(-time.Hour), time.Now().Add(24*time.Hour)
	for _, c := range []struct {
		name      string
		active    bool
		withdrawn *time.Time
		expires   *time.Time
		want      string
	}{
		{"active, licensed", true, nil, &future, ""},
		{"active, no expiry", true, nil, nil, ""},
		{"withdrawn", false, &past, nil, postgres.SoundReuseWithdrawn},
		{"switched off", false, nil, nil, postgres.SoundReuseInactive},
		{"licence lapsed, still active", true, nil, &past, postgres.SoundReuseLicenseExpired},
	} {
		lib, sound := uuid.New(), uuid.New()
		f.set(t, `INSERT INTO audio_library (id, title, artist, duration_ms, audio_url, source, is_active, withdrawn_at, license_expires_at)
			VALUES ($1, 'Monsoon Groove', 'Momentum Library', 30000, 'library/monsoon.m4a', 'licensed', $2, $3, $4)`,
			lib, c.active, c.withdrawn, c.expires)
		f.set(t, `INSERT INTO audio_tracks (id, library_id, title, artist, duration_ms, status, is_original, is_public)
			VALUES ($1, $2, 'Monsoon Groove', 'Momentum Library', 30000, 'ready', FALSE, TRUE)`, sound, lib)
		t.Cleanup(func() {
			bg := context.Background()
			_, _ = f.pool.Exec(bg, `UPDATE posts SET audio_track_id = NULL WHERE audio_track_id = $1`, sound)
			_, _ = f.pool.Exec(bg, `DELETE FROM audio_tracks WHERE id = $1`, sound)
			_, _ = f.pool.Exec(bg, `DELETE FROM audio_library WHERE id = $1`, lib)
		})

		block, err := f.store.LibrarySoundReuseBlock(ctx, lib)
		if err != nil || block != c.want {
			t.Fatalf("%s: block %q, %v; want %q", c.name, block, err, c.want)
		}
		post := f.reel(t, time.Hour, nil)
		_, err = f.svc.AttachAudioToPost(ctx, f.stranger, post, sound, 0)
		linked, _ := f.link(t, post)
		if c.want == "" {
			if err != nil || linked == nil || *linked != sound {
				t.Fatalf("%s: attach %v, linked %v", c.name, err, linked)
			}
			continue
		}
		var unavailable *SoundUnavailableError
		if !errors.As(err, &unavailable) || unavailable.Reason != c.want {
			t.Fatalf("%s: attach %v, want SoundUnavailableError(%s)", c.name, err, c.want)
		}
		if linked != nil {
			t.Fatalf("%s: a sound no longer available was linked", c.name)
		}
		// The status is untouched: posts that already play it keep it.
		if tr, err := f.store.GetAudioTrack(ctx, sound); err != nil || tr.Status != "ready" {
			t.Fatalf("%s: track %+v, %v", c.name, tr, err)
		}
	}
}
