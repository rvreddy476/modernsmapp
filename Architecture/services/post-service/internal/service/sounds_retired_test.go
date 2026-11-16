package service

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/atpost/post-service/internal/store/postgres"
	"github.com/google/uuid"
)

// A retired sound (media-service migration 032: its author deleted the
// source video) keeps playing on the posts that carry it, for whoever reads
// those posts, and is found nowhere else.

func retiredFixture() (*soundFixture, uuid.UUID) {
	f := newSoundFixture()
	retired := uuid.New()
	f.store.tracks[retired] = &postgres.AudioTrack{ID: retired, Title: "Original sound - Asha", Artist: "Asha",
		DurationMs: 12000, Status: "retired", IsPublic: true, CreatorUserID: &f.owner}
	return f, retired
}

func TestRetiredSoundStillPlaysOnThePostsThatCarryIt(t *testing.T) {
	for _, viewer := range []string{"stranger", "signed out"} {
		f, retired := retiredFixture()
		var who *uuid.UUID
		if viewer == "stranger" {
			who = &f.stranger
		}
		start := 900
		page := []*PostDetail{{Post: &postgres.Post{ID: uuid.New(), AuthorID: f.stranger, ContentType: "flick",
			AudioTrackID: &retired, AudioStartMs: &start}}}
		f.svc.attachSounds(context.Background(), who, page)
		got := page[0].Sound
		if got == nil || got.ID != retired || !got.Retired || got.StartMs != 900 {
			t.Fatalf("%s: sound = %+v, want the retired sound, marked retired", viewer, got)
		}
		// No media to ask: the post being read is the authority.
		for _, ask := range f.asked {
			if len(ask.media) != 0 {
				t.Fatalf("%s: an audience question was asked for a retired sound: %v", viewer, ask)
			}
		}
		body, _ := json.Marshal(got)
		if !strings.Contains(string(body), `"retired":true`) {
			t.Fatalf("%s: wire = %s", viewer, body)
		}
	}
	// Every other sound carries no retired field.
	f := newSoundFixture()
	page := f.page()
	f.svc.attachSounds(context.Background(), &f.stranger, page)
	if body, _ := json.Marshal(page[0].Sound); strings.Contains(string(body), "retired") {
		t.Fatalf("an ordinary sound says retired: %s", body)
	}
}

// No sound page, no reuse: soundForViewer says not found; "use this sound"
// says why.
func TestRetiredSoundIsNotReusable(t *testing.T) {
	f, retired := retiredFixture()
	if _, err := f.svc.soundForViewer(context.Background(), &f.owner, retired); !errors.Is(err, ErrSoundNotFound) {
		t.Fatalf("soundForViewer(retired) = %v, want ErrSoundNotFound", err)
	}
	if !errors.Is(errSoundRetired, ErrSoundNotFound) {
		t.Fatal("errSoundRetired must read as not found on the sound page")
	}
	if playableSound(f.store.tracks[retired]) || reusableSound(f.store.tracks[retired], &f.owner) {
		t.Fatal("a retired sound is playable/reusable for a new post")
	}
}
