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
	startMs  map[uuid.UUID]int       // post -> where in the sound it starts
	counted  map[uuid.UUID]int
	// sources are the posts a sound comes from, keyed by the sound's media
	// or by the post it names; primary is each post's own first video.
	sources    map[uuid.UUID][]postgres.SoundSourcePost
	sourcesErr error
	primary    map[uuid.UUID]uuid.UUID
	primaryErr error
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

func (f *fakeAudioStore) AttachAudioToPost(_ context.Context, postID, audioTrackID uuid.UUID, startMs int) (bool, error) {
	newToPost := f.attached[postID] != audioTrackID
	f.attached[postID] = audioTrackID
	if f.startMs == nil {
		f.startMs = map[uuid.UUID]int{}
	}
	f.startMs[postID] = startMs
	return newToPost, nil
}

func (f *fakeAudioStore) SoundSourcePosts(_ context.Context, sourcePostID, mediaID *uuid.UUID) ([]postgres.SoundSourcePost, error) {
	if f.sourcesErr != nil {
		return nil, f.sourcesErr
	}
	var out []postgres.SoundSourcePost
	if mediaID != nil {
		out = append(out, f.sources[*mediaID]...)
	}
	if sourcePostID != nil {
		out = append(out, f.sources[*sourcePostID]...)
	}
	return out, nil
}

func (f *fakeAudioStore) PrimaryVideoMediaID(_ context.Context, postID uuid.UUID) (*uuid.UUID, error) {
	if f.primaryErr != nil {
		return nil, f.primaryErr
	}
	if id, ok := f.primary[postID]; ok {
		return &id, nil
	}
	return nil, nil
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
	_, err := f.svc.AttachAudioToPost(context.Background(), actor, f.post, sound, 0)
	return err
}

// attachAt links with a start and returns what AttachAudioToPost answered.
func (f *audioFixture) attachAt(actor, sound uuid.UUID, startMs int) (*SoundLink, error) {
	return f.svc.AttachAudioToPost(context.Background(), actor, f.post, sound, startMs)
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

// The link carries where in the sound playback starts; a start outside the
// sound is stored as 0.
func TestAttachAudioStoresTheStart(t *testing.T) {
	for _, tc := range []struct {
		name            string
		startMs, length int
		want            int
	}{
		{"inside the sound", 12000, 28400, 12000},
		{"the first millisecond", 0, 28400, 0},
		{"the very end", 28400, 28400, 28400},
		{"past the end", 28401, 28400, 0},
		{"negative", -1, 28400, 0},
		{"a sound of unknown length", 500, 0, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newAudioFixture()
			sound := f.sound(&f.publicVideo, func(t *postgres.AudioTrack) { t.DurationMs = tc.length })
			link, err := f.attachAt(f.other, sound, tc.startMs)
			if err != nil || link == nil {
				t.Fatalf("link=%v err=%v", link, err)
			}
			if got := f.store.startMs[f.post]; got != tc.want || link.StartMs != tc.want || link.AudioTrackID != sound {
				t.Fatalf("stored start %d, answered %+v, want %d", got, link, tc.want)
			}
		})
	}
}

// Creator consent: a sound whose source reel turned reuse off is its
// author's alone. Both other settings permit it.
func TestAttachAudioNeedsTheSourceCreatorsConsent(t *testing.T) {
	source := uuid.New()
	build := func(setting string) (*audioFixture, uuid.UUID) {
		f := newAudioFixture()
		sound := f.sound(&f.publicVideo, func(t *postgres.AudioTrack) { t.SourcePostID = &source })
		f.store.sources = map[uuid.UUID][]postgres.SoundSourcePost{
			source: {{ID: source, AuthorID: f.author, RemixSetting: setting}},
		}
		return f, sound
	}

	f, sound := build("disallow")
	if err := f.attach(f.other, sound); !errors.Is(err, ErrSoundReuseNotAllowed) {
		t.Fatalf("a stranger on a sound whose source disallows reuse: got %v want %v", err, ErrSoundReuseNotAllowed)
	}
	f.wantNothingWritten(t, "reuse turned off")

	f, sound = build("disallow")
	if err := f.attach(f.author, sound); err != nil {
		t.Fatalf("the source reel's own author: %v", err)
	}
	if f.store.attached[f.post] != sound {
		t.Fatalf("attached %v, want %s", f.store.attached, sound)
	}

	for _, setting := range []string{"allow", "allow_audio_only", ""} {
		f, sound = build(setting)
		if err := f.attach(f.other, sound); err != nil {
			t.Fatalf("remix_setting %q: %v", setting, err)
		}
	}

	// A sound that names no post still answers to the reel carrying its media.
	g := newAudioFixture()
	unnamed := g.sound(&g.publicVideo, nil)
	g.store.sources = map[uuid.UUID][]postgres.SoundSourcePost{
		g.publicVideo: {{ID: uuid.New(), AuthorID: g.author, RemixSetting: "disallow"}},
	}
	if err := g.attach(g.other, unnamed); !errors.Is(err, ErrSoundReuseNotAllowed) {
		t.Fatalf("a sound with no source post, over a reel that disallows reuse: got %v", err)
	}
	g.wantNothingWritten(t, "reuse turned off, by media")

	// Consent that cannot be read is not consent.
	h, hs := build("allow")
	h.store.sourcesErr = errors.New("connection reset")
	if err := h.attach(h.other, hs); err == nil || errors.Is(err, ErrSoundReuseNotAllowed) {
		t.Fatalf("consent unreadable: got %v, want a fault", err)
	}
	h.wantNothingWritten(t, "consent unreadable")
}

// A post does not add its own video's sound: no link, no count, no error.
func TestAttachAudioSkipsThePostsOwnSound(t *testing.T) {
	f := newAudioFixture()
	sound := f.sound(&f.publicVideo, nil)
	f.store.primary = map[uuid.UUID]uuid.UUID{f.post: f.publicVideo}
	link, err := f.attachAt(f.author, sound, 0)
	if err != nil || link != nil {
		t.Fatalf("own primary video's sound: link=%v err=%v, want neither", link, err)
	}
	f.wantNothingWritten(t, "own primary video")

	g := newAudioFixture()
	named := g.sound(&g.publicVideo, func(t *postgres.AudioTrack) { t.SourcePostID = &g.post })
	link, err = g.attachAt(g.author, named, 0)
	if err != nil || link != nil {
		t.Fatalf("a sound that names the post as its source: link=%v err=%v, want neither", link, err)
	}
	g.wantNothingWritten(t, "own source post")

	// Another post's video is not its own.
	h := newAudioFixture()
	h.store.primary = map[uuid.UUID]uuid.UUID{h.post: uuid.New()}
	if link, err := h.attachAt(h.other, h.sound(&h.publicVideo, nil), 0); err != nil || link == nil {
		t.Fatalf("another video's sound: link=%v err=%v", link, err)
	}

	k := newAudioFixture()
	k.store.primaryErr = errors.New("connection reset")
	if _, err := k.attachAt(k.author, k.sound(&k.publicVideo, nil), 0); err == nil {
		t.Fatal("the post's own video could not be read, and the sound was linked")
	}
	k.wantNothingWritten(t, "own video unreadable")
}

// A use is a sound that is new to the post: linking the one it already
// plays again (a retried create) moves the start and counts nothing.
func TestAttachAudioCountsAUseOnce(t *testing.T) {
	f := newAudioFixture()
	sound := f.sound(&f.publicVideo, func(t *postgres.AudioTrack) { t.DurationMs = 9000 })
	for _, start := range []int{0, 4000} {
		if _, err := f.attachAt(f.other, sound, start); err != nil {
			t.Fatal(err)
		}
	}
	if f.store.counted[sound] != 1 || f.store.startMs[f.post] != 4000 {
		t.Fatalf("counted %d, start %d; want 1 use and the later start", f.store.counted[sound], f.store.startMs[f.post])
	}
}

// A reel draft's sound reaches the post published from it (drafts.go).
func TestKeepDraftSoundLinksWhatTheDraftChose(t *testing.T) {
	f := newAudioFixture()
	sound := f.sound(&f.publicVideo, func(t *postgres.AudioTrack) { t.DurationMs = 28400 })
	id := sound.String()
	post := &postgres.Post{ID: f.post, AuthorID: f.other}
	f.svc.keepDraftSound(context.Background(), post, &postgres.ReelDraft{ID: f.post, AudioTrackID: &id, AudioStartMs: 1500})
	if f.store.attached[f.post] != sound || f.store.startMs[f.post] != 1500 || f.store.counted[sound] != 1 {
		t.Fatalf("attached %v start %v counted %v", f.store.attached, f.store.startMs, f.store.counted)
	}
	if post.AudioTrackID == nil || *post.AudioTrackID != sound || post.AudioStartMs == nil || *post.AudioStartMs != 1500 {
		t.Fatalf("the published post answers audio_track_id=%v audio_start_ms=%v", post.AudioTrackID, post.AudioStartMs)
	}

	// No sound, an empty one and one that is not an id are all "no sound";
	// a refused one leaves the post as it was.
	g := newAudioFixture()
	empty, junk, refused := "", "not-a-sound", g.sound(&g.privateVideo, nil).String()
	for name, chosen := range map[string]*string{"none": nil, "empty": &empty, "not an id": &junk, "refused": &refused} {
		p := &postgres.Post{ID: g.post, AuthorID: g.other}
		g.svc.keepDraftSound(context.Background(), p, &postgres.ReelDraft{ID: g.post, AudioTrackID: chosen, AudioStartMs: 10})
		if p.AudioTrackID != nil || p.AudioStartMs != nil {
			t.Fatalf("%s: the post answers a sound: %v", name, p.AudioTrackID)
		}
		g.wantNothingWritten(t, name)
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
