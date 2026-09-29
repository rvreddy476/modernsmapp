package service

import (
	"context"
	"errors"
	"testing"

	"github.com/atpost/post-service/internal/store/postgres"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Attaching a sound to a post (audio.go): a post may play a sound its author
// may hear. The store and the audience decision are faked; the rule is real.

type fakeAudioStore struct {
	tracks   map[uuid.UUID]*postgres.AudioTrack
	loadErr  error
	attached map[uuid.UUID]uuid.UUID // post -> sound
	counted  map[uuid.UUID]int
}

func (f *fakeAudioStore) GetAudioTrack(_ context.Context, id uuid.UUID) (*postgres.AudioTrack, error) {
	if f.loadErr != nil {
		return nil, f.loadErr
	}
	t, ok := f.tracks[id]
	if !ok {
		return nil, pgx.ErrNoRows
	}
	cp := *t
	return &cp, nil
}

func (f *fakeAudioStore) AttachAudioToPost(_ context.Context, postID, audioTrackID uuid.UUID) error {
	f.attached[postID] = audioTrackID
	return nil
}

func (f *fakeAudioStore) IncrementAudioUseCount(_ context.Context, id uuid.UUID) error {
	f.counted[id]++
	return nil
}

type audioFixture struct {
	svc    *Service
	store  *fakeAudioStore
	asked  [][2]uuid.UUID // (author, media) the audience was asked about
	author uuid.UUID
	other  uuid.UUID
	post   uuid.UUID
	// publicVideo: anyone may watch. privateVideo: only `author`.
	publicVideo, privateVideo uuid.UUID
	audienceErr               error
}

func newAudioFixture() *audioFixture {
	f := &audioFixture{
		author: uuid.New(), other: uuid.New(), post: uuid.New(),
		publicVideo: uuid.New(), privateVideo: uuid.New(),
		store: &fakeAudioStore{tracks: map[uuid.UUID]*postgres.AudioTrack{}, attached: map[uuid.UUID]uuid.UUID{}, counted: map[uuid.UUID]int{}},
	}
	f.svc = &Service{
		audioTracks: f.store,
		audioAudience: func(_ context.Context, authorID, mediaID uuid.UUID) (bool, error) {
			f.asked = append(f.asked, [2]uuid.UUID{authorID, mediaID})
			if f.audienceErr != nil {
				return false, f.audienceErr
			}
			return mediaID == f.publicVideo || (mediaID == f.privateVideo && authorID == f.author), nil
		},
	}
	return f
}

func (f *audioFixture) sound(media *uuid.UUID, mutate func(*postgres.AudioTrack)) uuid.UUID {
	t := &postgres.AudioTrack{ID: uuid.New(), Title: "Take", MediaID: media, Status: "ready", IsPublic: true}
	if mutate != nil {
		mutate(t)
	}
	f.store.tracks[t.ID] = t
	return t.ID
}

func (f *audioFixture) attach(actor, sound uuid.UUID) error {
	return f.svc.AttachAudioToPost(context.Background(), actor, f.post, sound)
}

func (f *audioFixture) wantNothingWritten(t *testing.T, what string) {
	t.Helper()
	if len(f.store.attached) != 0 || len(f.store.counted) != 0 {
		t.Fatalf("%s: a refused sound was attached or counted: %v %v", what, f.store.attached, f.store.counted)
	}
}

func TestAttachAudioLinksASoundTheAuthorMayHear(t *testing.T) {
	f := newAudioFixture()
	sound := f.sound(&f.publicVideo, nil)
	if err := f.attach(f.other, sound); err != nil {
		t.Fatalf("a public video's sound: %v", err)
	}
	if f.store.attached[f.post] != sound || f.store.counted[sound] != 1 {
		t.Fatalf("attached %v counted %v", f.store.attached, f.store.counted)
	}
	if len(f.asked) != 1 || f.asked[0] != [2]uuid.UUID{f.other, f.publicVideo} {
		t.Fatalf("audience asked about %v, want the author and the sound's media", f.asked)
	}

	own := f.sound(&f.privateVideo, nil)
	if err := f.attach(f.author, own); err != nil {
		t.Fatalf("the author's own private video's sound: %v", err)
	}
	if f.store.attached[f.post] != own {
		t.Fatalf("attached %v, want %s", f.store.attached, own)
	}
}

func TestAttachAudioRefusesWhatTheAuthorMayNotHear(t *testing.T) {
	mine := uuid.New()
	for _, tc := range []struct {
		name  string
		build func(f *audioFixture) (actor, sound uuid.UUID)
		want  error
	}{
		{"a private video's sound, by a stranger", func(f *audioFixture) (uuid.UUID, uuid.UUID) { return f.other, f.sound(&f.privateVideo, nil) }, ErrAudioTrackNotFound},
		{"no such sound", func(f *audioFixture) (uuid.UUID, uuid.UUID) { return f.author, uuid.New() }, ErrAudioTrackNotFound},
		{"a sound with no media", func(f *audioFixture) (uuid.UUID, uuid.UUID) { return f.author, f.sound(nil, nil) }, ErrAudioTrackNotFound},
		{"a sound whose media is the nil id", func(f *audioFixture) (uuid.UUID, uuid.UUID) {
			nilID := uuid.Nil
			return f.author, f.sound(&nilID, nil)
		}, ErrAudioTrackNotFound},
		{"still processing", func(f *audioFixture) (uuid.UUID, uuid.UUID) {
			return f.author, f.sound(&f.publicVideo, func(t *postgres.AudioTrack) { t.Status = "processing" })
		}, ErrAudioTrackNotReady},
		{"refused by review", func(f *audioFixture) (uuid.UUID, uuid.UUID) {
			return f.author, f.sound(&f.publicVideo, func(t *postgres.AudioTrack) { t.Status = "rejected" })
		}, ErrAudioTrackNotReady},
		{"no status at all", func(f *audioFixture) (uuid.UUID, uuid.UUID) {
			return f.author, f.sound(&f.publicVideo, func(t *postgres.AudioTrack) { t.Status = "" })
		}, ErrAudioTrackNotReady},
		{"private to another creator", func(f *audioFixture) (uuid.UUID, uuid.UUID) {
			return f.other, f.sound(&f.publicVideo, func(t *postgres.AudioTrack) { t.IsPublic, t.CreatorUserID = false, &mine })
		}, ErrAudioTrackPrivate},
		{"private with no creator recorded", func(f *audioFixture) (uuid.UUID, uuid.UUID) {
			return f.other, f.sound(&f.publicVideo, func(t *postgres.AudioTrack) { t.IsPublic = false })
		}, ErrAudioTrackPrivate},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newAudioFixture()
			actor, sound := tc.build(f)
			if err := f.attach(actor, sound); !errors.Is(err, tc.want) {
				t.Fatalf("got %v want %v", err, tc.want)
			}
			f.wantNothingWritten(t, tc.name)
		})
	}
}

// A private sound is its creator's, and still only over media they may hear.
func TestAttachAudioPrivateSoundIsItsCreators(t *testing.T) {
	f := newAudioFixture()
	sound := f.sound(&f.privateVideo, func(t *postgres.AudioTrack) { t.IsPublic, t.CreatorUserID = false, &f.author })
	if err := f.attach(f.author, sound); err != nil {
		t.Fatalf("the creator: %v", err)
	}

	g := newAudioFixture()
	theirs := g.sound(&g.privateVideo, func(t *postgres.AudioTrack) { t.IsPublic, t.CreatorUserID = false, &g.other })
	if err := g.attach(g.other, theirs); !errors.Is(err, ErrAudioTrackNotFound) {
		t.Fatalf("the creator of a sound over media they may not hear: got %v want %v", err, ErrAudioTrackNotFound)
	}
	g.wantNothingWritten(t, "creator without the media")
}

// A refusal must not tell a sound that exists from one that does not.
func TestAttachAudioDenialIsTheMissingAnswer(t *testing.T) {
	f := newAudioFixture()
	denied := f.attach(f.other, f.sound(&f.privateVideo, nil))
	missing := f.attach(f.other, uuid.New())
	if denied == nil || missing == nil || denied.Error() != missing.Error() {
		t.Fatalf("denied %q, missing %q: the two must read the same", denied, missing)
	}
}

// Fail closed: a decision that cannot be made refuses the attach.
func TestAttachAudioUnresolvedRefuses(t *testing.T) {
	f := newAudioFixture()
	f.audienceErr = errors.New("media access store unavailable")
	err := f.attach(f.author, f.sound(&f.publicVideo, nil))
	if err == nil || errors.Is(err, ErrAudioTrackNotFound) {
		t.Fatalf("audience down: got %v, want a fault that is not a denial", err)
	}
	f.wantNothingWritten(t, "audience down")

	g := newAudioFixture()
	g.store.loadErr = errors.New("connection reset")
	if err := g.attach(g.author, uuid.New()); err == nil || errors.Is(err, ErrAudioTrackNotFound) {
		t.Fatalf("store down: got %v, want a fault that is not a denial", err)
	}
	if len(g.asked) != 0 {
		t.Fatalf("the audience was asked about a sound that was never loaded: %v", g.asked)
	}
}
