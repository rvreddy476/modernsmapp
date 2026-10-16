//go:build integration

package service

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
)

// Library sounds against a real Postgres (2026-10-15): a row media-service
// materialised from its curated library (library_id set; no media, no
// source reel, no creator) is attached by any author, heard by every
// viewer, counted, and its sound page lists the reels that play it.
// Migration 064 is under test: the projection reads library_id.
//
//	POSTGRES_DSN=postgres://…/post_it_test go test -tags integration ./internal/service/ -run LibrarySoundsIT -v

func TestLibrarySoundsITAttachHearAndList(t *testing.T) {
	f := newSoundsITFixture(t)
	ctx := context.Background()

	sound, libraryID := uuid.New(), uuid.New()
	// Where media-service has migrated this database too, library_id is a
	// foreign key to its audio_library: give it the row it points at.
	var hasLibrary bool
	if err := f.pool.QueryRow(ctx, `SELECT to_regclass('audio_library') IS NOT NULL`).Scan(&hasLibrary); err != nil {
		t.Fatal(err)
	}
	if hasLibrary {
		f.set(t, `INSERT INTO audio_library (id, title, artist, duration_ms, audio_url, source)
			VALUES ($1, 'Monsoon Groove', 'Momentum Library', 30000, 'library/monsoon.m4a', 'licensed')`, libraryID)
	}
	f.set(t, `
		INSERT INTO audio_tracks (id, library_id, title, artist, duration_ms, status, is_original, is_public)
		VALUES ($1, $2, 'Monsoon Groove', 'Momentum Library', 30000, 'ready', FALSE, TRUE)`,
		sound, libraryID)
	t.Cleanup(func() {
		bg := context.Background()
		_, _ = f.pool.Exec(bg, `UPDATE posts SET audio_track_id = NULL WHERE audio_track_id = $1`, sound)
		_, _ = f.pool.Exec(bg, `DELETE FROM audio_tracks WHERE id = $1`, sound)
		if hasLibrary {
			_, _ = f.pool.Exec(bg, `DELETE FROM audio_library WHERE id = $1`, libraryID)
		}
	})

	track, err := f.store.GetAudioTrack(ctx, sound)
	if err != nil || !track.IsLibrarySound() || *track.LibraryID != libraryID || track.MediaID != nil {
		t.Fatalf("read the library sound: %+v, %v", track, err)
	}

	// Any author: the stranger, on a reel of theirs; and the fixture's
	// author, on theirs. Neither may hear any particular video for this.
	newer, older := f.reel(t, time.Hour, nil), f.reel(t, 2*time.Hour, nil)
	for _, c := range []struct {
		author, post uuid.UUID
		start        int
	}{{f.stranger, newer, 1_500}, {f.stranger, older, 0}, {f.author, f.post, 0}} {
		link, err := f.svc.AttachAudioToPost(ctx, c.author, c.post, sound, c.start)
		if err != nil || link == nil || link.AudioTrackID != sound || link.StartMs != c.start {
			t.Fatalf("attach by %s to %s: %+v, %v", c.author, c.post, link, err)
		}
		if got, _ := f.link(t, c.post); got == nil || *got != sound {
			t.Fatalf("post %s plays %v, want %s", c.post, got, sound)
		}
	}
	if use, usage := f.counters(t, sound); use != 3 || usage != 3 {
		t.Fatalf("counters use=%d usage=%d, want 3 and 3", use, usage)
	}
	// A retried attach is not another use.
	if _, err := f.svc.AttachAudioToPost(ctx, f.stranger, newer, sound, 1_500); err != nil {
		t.Fatal(err)
	}
	if use, _ := f.counters(t, sound); use != 3 {
		t.Fatalf("a retried attach counted: use=%d", use)
	}

	// Heard by everyone, signed out included.
	for name, viewer := range map[string]*uuid.UUID{"signed out": nil, "a stranger": &f.stranger, "the viewer": &f.viewer} {
		s := f.soundOn(t, newer, viewer)
		if s == nil || s.ID != sound || s.StartMs != 1_500 || s.LibraryID == nil || *s.LibraryID != libraryID ||
			s.CreatorUserID != nil || s.SourcePostID != nil {
			t.Fatalf("%s: sound on the reel = %+v", name, s)
		}
	}

	// The sound page lists the reels that play it, newest first; there is no
	// origin reel to show.
	if listed := f.listed(t, sound); !listed[newer] || !listed[older] {
		t.Fatalf("ListPostsBySound = %v, want both reels", listed)
	}
	for name, viewer := range map[string]*uuid.UUID{"signed out": nil, "a signed-in viewer": &f.viewer} {
		page, _, err := f.svc.PostsBySound(ctx, viewer, sound, 0, "")
		if err != nil || page.Sound == nil || page.Sound.ID != sound || page.Origin != nil {
			t.Fatalf("%s: by-sound page %+v, %v", name, page, err)
		}
		pos := map[uuid.UUID]int{}
		for i, it := range page.Items {
			pos[it.ID] = i + 1
			if it.Sound == nil || it.Sound.ID != sound {
				t.Fatalf("%s: item %s carries sound %+v", name, it.ID, it.Sound)
			}
		}
		if pos[newer] == 0 || pos[older] == 0 || pos[newer] > pos[older] {
			t.Fatalf("%s: items %v, want the newer reel before the older", name, pos)
		}
	}

	// Withdrawn: heard by nobody, attached by nobody, listed for nobody.
	f.set(t, `UPDATE audio_tracks SET status = 'deleted' WHERE id = $1`, sound)
	if s := f.soundOn(t, newer, nil); s != nil {
		t.Fatalf("a withdrawn library sound is still heard: %+v", s)
	}
	third := f.reel(t, 3*time.Hour, nil)
	if _, err := f.svc.AttachAudioToPost(ctx, f.stranger, third, sound, 0); err == nil {
		t.Fatal("a withdrawn library sound was attached")
	}
	if _, _, err := f.svc.PostsBySound(ctx, nil, sound, 0, ""); err == nil {
		t.Fatal("a withdrawn library sound still has a page")
	}
}
