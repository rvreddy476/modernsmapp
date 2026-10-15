package service

import (
	"context"
	"errors"
	"testing"

	"github.com/atpost/post-service/internal/store/postgres"
	"github.com/google/uuid"
)

// Library sounds (2026-10-15): a curated library sound that media-service
// materialised as an audio_tracks row (library_id set, no media, no source
// reel, no creator). It is the platform's, public, and heard by everyone, so
// every gate that otherwise asks the sound's media admits it without asking.

func librarySound(id uuid.UUID, mutate func(*postgres.AudioTrack)) *postgres.AudioTrack {
	lib := uuid.New()
	t := &postgres.AudioTrack{ID: id, Title: "Monsoon Groove", Artist: "Momentum Library", DurationMs: 30_000,
		Status: "ready", IsPublic: true, LibraryID: &lib}
	if mutate != nil {
		mutate(t)
	}
	return t
}

func TestAttachAudioAcceptsALibrarySoundForAnyAuthor(t *testing.T) {
	f := newAudioFixture()
	id := uuid.New()
	f.store.tracks[id] = librarySound(id, nil)

	for _, author := range []uuid.UUID{f.author, f.other, uuid.New()} {
		f.post = uuid.New()
		link, err := f.attachAt(author, id, 4_000)
		if err != nil {
			t.Fatalf("author %s: a library sound was refused: %v", author, err)
		}
		if link == nil || link.AudioTrackID != id || link.StartMs != 4_000 {
			t.Fatalf("author %s: link = %+v", author, link)
		}
		if f.store.attached[f.post] != id {
			t.Fatalf("author %s: attached %v", author, f.store.attached)
		}
	}
	if f.store.counted[id] != 3 {
		t.Fatalf("counted %d uses, want 3 (one per post)", f.store.counted[id])
	}
	if len(f.asked) != 0 {
		t.Fatalf("the media audience was asked about a library sound: %v", f.asked)
	}
}

func TestAttachAudioLibrarySoundStillNeedsToBeReady(t *testing.T) {
	f := newAudioFixture()
	withdrawn := uuid.New()
	f.store.tracks[withdrawn] = librarySound(withdrawn, func(t *postgres.AudioTrack) { t.Status = "deleted" })
	if err := f.attach(f.other, withdrawn); !errors.Is(err, ErrAudioTrackNotReady) {
		t.Fatalf("a withdrawn library sound: %v, want ErrAudioTrackNotReady", err)
	}
	f.wantNothingWritten(t, "withdrawn library sound")

	// A row that is neither a library sound nor names media is still refused.
	orphan := f.sound(nil, nil)
	if err := f.attach(f.other, orphan); !errors.Is(err, ErrAudioTrackNotFound) {
		t.Fatalf("a sound with no media and no library row: %v, want ErrAudioTrackNotFound", err)
	}
	f.wantNothingWritten(t, "orphan sound")

	// The nil UUID is not a library id.
	nilLib := f.sound(nil, func(t *postgres.AudioTrack) { z := uuid.Nil; t.LibraryID = &z })
	if err := f.attach(f.other, nilLib); !errors.Is(err, ErrAudioTrackNotFound) {
		t.Fatalf("library_id = nil UUID: %v, want ErrAudioTrackNotFound", err)
	}
	f.wantNothingWritten(t, "nil library id")
}

func TestAttachSoundsHearsALibrarySoundForEveryViewer(t *testing.T) {
	for name, viewer := range map[string]func(f *soundFixture) *uuid.UUID{
		"signed out":         func(*soundFixture) *uuid.UUID { return nil },
		"a signed-in viewer": func(f *soundFixture) *uuid.UUID { return &f.stranger },
	} {
		t.Run(name, func(t *testing.T) {
			f := newSoundFixture()
			id := uuid.New()
			f.store.tracks[id] = librarySound(id, nil)
			start := 2_500
			page := []*PostDetail{{Post: &postgres.Post{ID: uuid.New(), AudioTrackID: &id, AudioStartMs: &start}}}
			f.svc.attachSounds(context.Background(), viewer(f), page)
			got := page[0].Sound
			if got == nil || got.ID != id || got.StartMs != 2_500 || got.LibraryID == nil ||
				*got.LibraryID != *f.store.tracks[id].LibraryID || got.CreatorUserID != nil || got.SourcePostID != nil {
				t.Fatalf("sound = %+v", got)
			}
			if len(f.asked) != 0 {
				t.Fatalf("a page of only library sounds asked the audience: %+v", f.asked)
			}
		})
	}
}

// On a mixed page the media sounds are decided as before, and the library
// sound is heard even when that decision cannot be made.
func TestAttachSoundsLibrarySoundSurvivesAnUnresolvedAudience(t *testing.T) {
	f := newSoundFixture()
	id := uuid.New()
	f.store.tracks[id] = librarySound(id, nil)
	page := []*PostDetail{
		{Post: &postgres.Post{ID: uuid.New(), AudioTrackID: &f.publicSound}},
		{Post: &postgres.Post{ID: uuid.New(), AudioTrackID: &id}},
	}
	f.svc.attachSounds(context.Background(), &f.stranger, page)
	if page[0].Sound == nil || page[1].Sound == nil {
		t.Fatalf("mixed page: %+v / %+v", page[0].Sound, page[1].Sound)
	}
	if len(f.asked) != 1 || len(f.asked[0].media) != 1 || f.asked[0].media[0] != f.publicVideo {
		t.Fatalf("asked %+v, want the public video alone", f.asked)
	}

	f = newSoundFixture()
	f.store.tracks[id] = librarySound(id, nil)
	f.audienceErr = errors.New("media access store unavailable")
	page = []*PostDetail{
		{Post: &postgres.Post{ID: uuid.New(), AudioTrackID: &f.publicSound}},
		{Post: &postgres.Post{ID: uuid.New(), AudioTrackID: &id}},
	}
	f.svc.attachSounds(context.Background(), &f.stranger, page)
	if page[0].Sound != nil {
		t.Fatalf("an undecided media sound was attached: %+v", page[0].Sound)
	}
	if page[1].Sound == nil || page[1].Sound.ID != id {
		t.Fatalf("the library sound was dropped with the undecided one: %+v", page[1].Sound)
	}

	// A withdrawn library sound is heard by nobody.
	f = newSoundFixture()
	f.store.tracks[id] = librarySound(id, func(t *postgres.AudioTrack) { t.Status = "deleted" })
	page = []*PostDetail{{Post: &postgres.Post{ID: uuid.New(), AudioTrackID: &id}}}
	f.svc.attachSounds(context.Background(), nil, page)
	if page[0].Sound != nil {
		t.Fatalf("a withdrawn library sound was attached: %+v", page[0].Sound)
	}
}

func TestSoundForViewerAdmitsALibrarySound(t *testing.T) {
	f := newSoundFixture()
	id := uuid.New()
	f.store.tracks[id] = librarySound(id, nil)
	for _, viewer := range []*uuid.UUID{nil, &f.stranger} {
		got, err := f.svc.soundForViewer(context.Background(), viewer, id)
		if err != nil || got == nil || got.ID != id {
			t.Fatalf("viewer %v: %+v, %v", viewer, got, err)
		}
	}
	if len(f.asked) != 0 {
		t.Fatalf("the audience was asked about a library sound: %+v", f.asked)
	}

	// The by-sound page answers it (the listing itself is the store's).
	page, _, err := f.svc.PostsBySound(context.Background(), nil, id, 10, "")
	if err != nil || page == nil || page.Sound == nil || page.Sound.ID != id || page.Origin != nil {
		t.Fatalf("by-sound page: %+v, %v", page, err)
	}

	withdrawn := uuid.New()
	f.store.tracks[withdrawn] = librarySound(withdrawn, func(t *postgres.AudioTrack) { t.Status = "deleted" })
	if _, err := f.svc.soundForViewer(context.Background(), &f.stranger, withdrawn); !errors.Is(err, ErrSoundNotFound) {
		t.Fatalf("a withdrawn library sound: %v, want ErrSoundNotFound", err)
	}
}
