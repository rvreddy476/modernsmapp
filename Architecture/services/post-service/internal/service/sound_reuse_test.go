package service

import (
	"errors"
	"testing"

	"github.com/atpost/post-service/internal/store/postgres"
	"github.com/google/uuid"
)

// Reels audit 2026-10-30 (HIGH 2): a library sound withdrawn for new uses,
// switched off, or past its licence keeps audio_tracks.status 'ready' so
// existing posts keep playing it. It must not go on a new post: neither by
// attach (create, draft publish) nor by "use this sound".

var soundReuseBlocks = []string{postgres.SoundReuseWithdrawn, postgres.SoundReuseInactive, postgres.SoundReuseLicenseExpired}

func wantSoundUnavailable(t *testing.T, what string, err error, reason string) {
	t.Helper()
	var unavailable *SoundUnavailableError
	if !errors.Is(err, ErrSoundNotAvailable) || !errors.As(err, &unavailable) || unavailable.Reason != reason {
		t.Fatalf("%s: got %v, want ErrSoundNotAvailable (%s)", what, err, reason)
	}
}

func TestAttachAudioRefusesALibrarySoundNoLongerAvailable(t *testing.T) {
	for _, block := range soundReuseBlocks {
		f := newAudioFixture()
		id := uuid.New()
		f.store.tracks[id] = librarySound(id, nil)
		f.store.reuseBlock = map[uuid.UUID]string{*f.store.tracks[id].LibraryID: block}
		wantSoundUnavailable(t, "attach "+block, f.attach(f.other, id), block)
		f.wantNothingWritten(t, block)
	}

	// The library row cannot be read: refused, not assumed licensed.
	f := newAudioFixture()
	id := uuid.New()
	f.store.tracks[id] = librarySound(id, nil)
	f.store.reuseErr = errors.New("connection reset")
	if err := f.attach(f.other, id); !errors.Is(err, ErrSoundUnavailable) {
		t.Fatalf("library row unreadable: %v, want ErrSoundUnavailable", err)
	}
	f.wantNothingWritten(t, "library row unreadable")
}

// An extracted sound has no library row: nothing is asked about it, so an
// unreadable library never refuses one.
func TestAttachAudioDoesNotAskTheLibraryAboutAnExtractedSound(t *testing.T) {
	f := newAudioFixture()
	sound := f.sound(&f.publicVideo, nil)
	f.store.reuseErr = errors.New("audio_library unreadable")
	if err := f.attach(f.other, sound); err != nil {
		t.Fatalf("an extracted sound: %v", err)
	}
}

func TestUseSoundRefusesALibrarySoundNoLongerAvailable(t *testing.T) {
	for _, block := range soundReuseBlocks {
		f := newUseSoundFixture()
		id := uuid.New()
		f.store.tracks[id] = librarySound(id, nil)
		f.store.posts[f.reel].AudioTrackID = &id
		f.store.reuseBlock = map[uuid.UUID]string{*f.store.tracks[id].LibraryID: block}
		got, err := f.use(f.stranger)
		wantSoundUnavailable(t, "use "+block, err, block)
		if got != nil {
			t.Fatalf("%s: answered %+v", block, got)
		}
		// Refused, not swapped for the reel's own audio.
		if len(f.maker.calls) != 0 {
			t.Fatalf("%s: media-service was asked to make a sound instead: %+v", block, f.maker.calls)
		}
	}

	// Still available: the reel's library sound is handed out.
	f := newUseSoundFixture()
	id := uuid.New()
	f.store.tracks[id] = librarySound(id, nil)
	f.store.posts[f.reel].AudioTrackID = &id
	if got, err := f.use(f.stranger); err != nil || got == nil || got.ID != id {
		t.Fatalf("an available library sound: %+v, %v", got, err)
	}

	// Unreadable library row: a fault, never the sound.
	g := newUseSoundFixture()
	g.store.tracks[id] = librarySound(id, nil)
	g.store.posts[g.reel].AudioTrackID = &id
	g.store.reuseErr = errors.New("connection reset")
	if _, err := g.use(g.stranger); !errors.Is(err, ErrSoundUnavailable) {
		t.Fatalf("library row unreadable: %v, want ErrSoundUnavailable", err)
	}
}

// media-service's own verdict on the sound it makes or returns is obeyed;
// an older media-service that does not send it is read as available.
func TestUseSoundObeysMediaServiceAvailability(t *testing.T) {
	no, yes := false, true
	f := newUseSoundFixture()
	f.maker.sound = &MediaSound{ID: uuid.New(), Status: "ready", AvailableForNewPosts: &no, UnavailableReason: "withdrawn"}
	_, err := f.use(f.stranger)
	wantSoundUnavailable(t, "media-service says withdrawn", err, "withdrawn")

	f = newUseSoundFixture()
	f.maker.sound = &MediaSound{ID: uuid.New(), Status: "ready", AvailableForNewPosts: &no}
	_, err = f.use(f.stranger)
	wantSoundUnavailable(t, "media-service says no without a reason", err, "unavailable")

	for _, avail := range []*bool{&yes, nil} {
		f = newUseSoundFixture()
		f.maker.sound = &MediaSound{ID: uuid.New(), Status: "ready", AvailableForNewPosts: avail}
		if got, err := f.use(f.stranger); err != nil || got == nil {
			t.Fatalf("available=%v: %+v, %v", avail, got, err)
		}
	}
}
