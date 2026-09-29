package service

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/atpost/post-service/internal/store/postgres"
	"github.com/google/uuid"
)

// Original sounds (sounds.go): `sound` on a post per viewer, "use this
// sound", and the client that asks media-service to make one. The stores,
// the audience decision and media-service are faked; the rules are real.
// The routes are driven end to end in internal/http/sounds_routes_test.go,
// the SQL in sounds_integration_test.go.

type fakeSoundStore struct {
	tracks    map[uuid.UUID]*postgres.AudioTrack
	tracksErr error
	reads     int // GetAudioTracksByIDs calls
	posts     map[uuid.UUID]*postgres.Post
	sources   map[uuid.UUID][]postgres.SoundSourcePost
}

func (f *fakeSoundStore) GetAudioTracksByIDs(_ context.Context, ids []uuid.UUID) (map[uuid.UUID]*postgres.AudioTrack, error) {
	f.reads++
	if f.tracksErr != nil {
		return nil, f.tracksErr
	}
	out := map[uuid.UUID]*postgres.AudioTrack{}
	for _, id := range ids {
		if t, ok := f.tracks[id]; ok {
			cp := *t
			out[id] = &cp
		}
	}
	return out, nil
}

func (f *fakeSoundStore) SoundSourcePosts(_ context.Context, sourcePostID, mediaID *uuid.UUID) ([]postgres.SoundSourcePost, error) {
	var out []postgres.SoundSourcePost
	if sourcePostID != nil {
		out = append(out, f.sources[*sourcePostID]...)
	}
	if mediaID != nil {
		out = append(out, f.sources[*mediaID]...)
	}
	return out, nil
}

func (f *fakeSoundStore) GetPost(_ context.Context, id uuid.UUID) (*postgres.Post, error) {
	if p, ok := f.posts[id]; ok {
		cp := *p
		cp.Media = append([]postgres.PostMedia(nil), p.Media...)
		return &cp, nil
	}
	return nil, nil
}

func (f *fakeSoundStore) ListPostsBySound(context.Context, uuid.UUID, *uuid.UUID, int, string) ([]postgres.Post, string, error) {
	return nil, "", nil
}

type soundFixture struct {
	svc   *Service
	store *fakeSoundStore
	// asked is every audience question: the viewer, and the media asked about.
	asked       []soundAsk
	audienceErr error
	// owner uploaded privateVideo; publicVideo plays for everyone.
	owner, stranger           uuid.UUID
	publicVideo, privateVideo uuid.UUID
	// publicSound and privateSound are taken from those two videos;
	// processing is a sound that is not ready yet.
	publicSound, privateSound, processing uuid.UUID
}

type soundAsk struct {
	viewer uuid.UUID
	media  []uuid.UUID
}

func newSoundFixture() *soundFixture {
	f := &soundFixture{
		owner: uuid.New(), stranger: uuid.New(),
		publicVideo: uuid.New(), privateVideo: uuid.New(),
		publicSound: uuid.New(), privateSound: uuid.New(), processing: uuid.New(),
		store: &fakeSoundStore{tracks: map[uuid.UUID]*postgres.AudioTrack{}, posts: map[uuid.UUID]*postgres.Post{},
			sources: map[uuid.UUID][]postgres.SoundSourcePost{}},
	}
	source := uuid.New()
	f.store.tracks[f.publicSound] = &postgres.AudioTrack{ID: f.publicSound, Title: "Original sound - Asha", Artist: "Asha",
		DurationMs: 28400, MediaID: &f.publicVideo, SourcePostID: &source, Status: "ready", UseCount: 3, IsPublic: true, CreatorUserID: &f.owner}
	f.store.tracks[f.privateSound] = &postgres.AudioTrack{ID: f.privateSound, Title: "Kept", DurationMs: 9000,
		MediaID: &f.privateVideo, Status: "ready", IsPublic: true, CreatorUserID: &f.owner}
	f.store.tracks[f.processing] = &postgres.AudioTrack{ID: f.processing, Title: "Early", MediaID: &f.publicVideo, Status: "processing", IsPublic: true}
	f.svc = &Service{
		soundReads: f.store,
		soundAudience: func(_ context.Context, viewer uuid.UUID, media []uuid.UUID) (map[uuid.UUID]bool, error) {
			f.asked = append(f.asked, soundAsk{viewer, append([]uuid.UUID(nil), media...)})
			if f.audienceErr != nil {
				return nil, f.audienceErr
			}
			out := map[uuid.UUID]bool{}
			for _, m := range media {
				out[m] = m == f.publicVideo || (m == f.privateVideo && viewer == f.owner)
			}
			return out, nil
		},
	}
	return f
}

// page is one row per sound, plus one that plays none and one whose sound
// row is gone.
func (f *soundFixture) page() []*PostDetail {
	start := 1500
	row := func(sound *uuid.UUID, startMs *int) *PostDetail {
		return &PostDetail{Post: &postgres.Post{ID: uuid.New(), AuthorID: f.stranger, ContentType: "flick", AudioTrackID: sound, AudioStartMs: startMs}}
	}
	missing := uuid.New()
	return []*PostDetail{
		row(&f.publicSound, &start), row(&f.privateSound, nil), row(nil, nil), row(&f.processing, nil), row(&missing, nil),
	}
}

func TestAttachSoundsIsDecidedPerViewer(t *testing.T) {
	for _, tc := range []struct {
		name        string
		viewer      func(f *soundFixture) *uuid.UUID
		asks        func(f *soundFixture) uuid.UUID
		wantPrivate bool
	}{
		{"a signed-in stranger", func(f *soundFixture) *uuid.UUID { return &f.stranger }, func(f *soundFixture) uuid.UUID { return f.stranger }, false},
		{"the owner of the private video", func(f *soundFixture) *uuid.UUID { return &f.owner }, func(f *soundFixture) uuid.UUID { return f.owner }, true},
		{"signed out", func(*soundFixture) *uuid.UUID { return nil }, func(*soundFixture) uuid.UUID { return uuid.Nil }, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newSoundFixture()
			page := f.page()
			f.svc.attachSounds(context.Background(), tc.viewer(f), page)

			got := page[0].Sound
			if got == nil {
				t.Fatal("a public video's sound was not attached")
			}
			want := f.store.tracks[f.publicSound]
			if got.ID != f.publicSound || got.Title != want.Title || got.Artist != "Asha" || got.DurationMs != 28400 ||
				got.StartMs != 1500 || got.UseCount != 3 || got.SourcePostID == nil || *got.SourcePostID != *want.SourcePostID ||
				got.CreatorUserID == nil || *got.CreatorUserID != f.owner {
				t.Fatalf("sound = %+v", got)
			}
			if (page[1].Sound != nil) != tc.wantPrivate {
				t.Fatalf("a private video's sound attached = %v, want %v", page[1].Sound != nil, tc.wantPrivate)
			}
			for i, what := range map[int]string{2: "a post that plays no sound", 3: "a sound still processing", 4: "a sound whose row is gone"} {
				if page[i].Sound != nil {
					t.Fatalf("%s carries a sound: %+v", what, page[i].Sound)
				}
			}
			// The id is not a secret and stays, whoever asks.
			if page[1].AudioTrackID == nil || *page[1].AudioTrackID != f.privateSound {
				t.Fatalf("audio_track_id was removed with the sound: %v", page[1].AudioTrackID)
			}
			// One read and one decision for the page, asked as this viewer.
			if f.store.reads != 1 || len(f.asked) != 1 || f.asked[0].viewer != tc.asks(f) {
				t.Fatalf("reads=%d asked=%+v", f.store.reads, f.asked)
			}
			if len(f.asked[0].media) != 2 {
				t.Fatalf("asked about %v, want the two videos once each", f.asked[0].media)
			}
		})
	}
}

// A decision that cannot be made omits the sound; the read is not failed.
func TestAttachSoundsUnresolvedOmitsTheSound(t *testing.T) {
	for name, breakIt := range map[string]func(f *soundFixture){
		"the audience cannot be decided": func(f *soundFixture) { f.audienceErr = errors.New("media access store unavailable") },
		"the sounds cannot be read":      func(f *soundFixture) { f.store.tracksErr = errors.New("connection reset") },
		"there is no store":              func(f *soundFixture) { f.svc.soundReads = nil },
	} {
		t.Run(name, func(t *testing.T) {
			f := newSoundFixture()
			breakIt(f)
			page := f.page()
			f.svc.attachSounds(context.Background(), &f.owner, page)
			for i, d := range page {
				if d.Sound != nil {
					t.Fatalf("row %d carries a sound nobody decided: %+v", i, d.Sound)
				}
			}
			if page[0].AudioTrackID == nil || page[0].AudioStartMs == nil {
				t.Fatal("the post lost its own audio fields")
			}
		})
	}
}

// A sound that arrived with the row is not a decision for this viewer.
func TestAttachSoundsReplacesWhatArrivedWithTheRow(t *testing.T) {
	f := newSoundFixture()
	page := f.page()
	for _, d := range page {
		d.Sound = &PostSound{ID: uuid.New(), Title: "from somewhere else"}
	}
	f.svc.attachSounds(context.Background(), &f.stranger, page)
	if page[0].Sound == nil || page[0].Sound.ID != f.publicSound {
		t.Fatalf("row 0 = %+v", page[0].Sound)
	}
	for _, i := range []int{1, 2, 3, 4} {
		if page[i].Sound != nil {
			t.Fatalf("row %d kept a sound it arrived with: %+v", i, page[i].Sound)
		}
	}
}

// The start on the wire is the post's, held inside the sound.
func TestAttachSoundsStartIsInsideTheSound(t *testing.T) {
	f := newSoundFixture()
	late := 99000
	page := []*PostDetail{{Post: &postgres.Post{ID: uuid.New(), AudioTrackID: &f.publicSound, AudioStartMs: &late}}}
	f.svc.attachSounds(context.Background(), nil, page)
	if page[0].Sound == nil || page[0].Sound.StartMs != 0 {
		t.Fatalf("sound = %+v, want start 0 for a start past the end", page[0].Sound)
	}
}

// What the cache holds is the post (post_cache.go). `sound` lives on the
// detail, so a cached body cannot carry one viewer's sound to another.
func TestCachedPostBodyCarriesNoSound(t *testing.T) {
	f := newSoundFixture()
	start := 0
	detail := &PostDetail{Post: &postgres.Post{ID: uuid.New(), AudioTrackID: &f.publicSound, AudioStartMs: &start}}
	f.svc.attachSounds(context.Background(), &f.stranger, []*PostDetail{detail})
	if detail.Sound == nil {
		t.Fatal("the fixture attached no sound")
	}

	cached, err := json.Marshal(detail.Post) // exactly what getCachedPostBody stores
	if err != nil {
		t.Fatal(err)
	}
	var body map[string]json.RawMessage
	if err := json.Unmarshal(cached, &body); err != nil {
		t.Fatal(err)
	}
	if _, ok := body["sound"]; ok {
		t.Fatalf("the cached body carries a sound: %s", cached)
	}
	for _, key := range []string{"audio_track_id", "audio_start_ms"} {
		if _, ok := body[key]; !ok {
			t.Fatalf("the cached body lost %s: %s", key, cached)
		}
	}
	var fromCache postgres.Post
	if err := json.Unmarshal(cached, &fromCache); err != nil {
		t.Fatal(err)
	}
	if fromCache.AudioTrackID == nil || *fromCache.AudioTrackID != f.publicSound || fromCache.AudioStartMs == nil {
		t.Fatalf("a body read back from the cache lost its link: %+v %v", fromCache.AudioTrackID, fromCache.AudioStartMs)
	}

	// And no field of the post can ever be the sound.
	postType := reflect.TypeOf(postgres.Post{})
	for i := 0; i < postType.NumField(); i++ {
		if tag := strings.Split(postType.Field(i).Tag.Get("json"), ",")[0]; tag == "sound" {
			t.Fatalf("postgres.Post.%s is on the wire as `sound`: it would be cached", postType.Field(i).Name)
		}
	}

	wire, _ := json.Marshal(detail)
	var answer map[string]json.RawMessage
	_ = json.Unmarshal(wire, &answer)
	if _, ok := answer["sound"]; !ok {
		t.Fatalf("the detail does not carry the sound: %s", wire)
	}
}

// A post with no added sound has neither field on the wire; one with a
// sound has both, the start included when it is 0.
func TestPostAudioFieldsOnTheWire(t *testing.T) {
	plain, _ := json.Marshal(&postgres.Post{ID: uuid.New()})
	for _, key := range []string{"audio_track_id", "audio_start_ms", "sound"} {
		if strings.Contains(string(plain), `"`+key+`"`) {
			t.Fatalf("a post that plays no sound carries %s: %s", key, plain)
		}
	}
	zero, sound := 0, uuid.New()
	with, _ := json.Marshal(&postgres.Post{ID: uuid.New(), AudioTrackID: &sound, AudioStartMs: &zero})
	if !strings.Contains(string(with), `"audio_track_id":"`+sound.String()+`"`) || !strings.Contains(string(with), `"audio_start_ms":0`) {
		t.Fatalf("a post that plays a sound: %s", with)
	}
}

// ---- "use this sound" ----

type fakeSoundMaker struct {
	mu    sync.Mutex
	calls []fakeSoundCall
	err   error
	sound *MediaSound
}

type fakeSoundCall struct {
	media uuid.UUID
	in    SoundRequest
}

func (f *fakeSoundMaker) EnsureSound(_ context.Context, mediaID uuid.UUID, in SoundRequest) (*MediaSound, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, fakeSoundCall{mediaID, in})
	if f.err != nil {
		return nil, f.err
	}
	if f.sound != nil {
		return f.sound, nil
	}
	return &MediaSound{ID: uuid.New(), Title: in.Title, Artist: in.Artist, DurationMs: 28400, Status: "ready",
		SourcePostID: &in.SourcePostID, CreatorUserID: &in.CreatorUserID}, nil
}

type fakeMediaStates map[uuid.UUID]postgres.MediaOwnership

func (f fakeMediaStates) BatchGetMediaOwnership(_ context.Context, ids []uuid.UUID) (map[uuid.UUID]postgres.MediaOwnership, error) {
	out := map[uuid.UUID]postgres.MediaOwnership{}
	for _, id := range ids {
		if m, ok := f[id]; ok {
			out[id] = m
		}
	}
	return out, nil
}

type fakeSoundChannels struct {
	channelStore
	names map[uuid.UUID]string
}

func (f fakeSoundChannels) GetChannelByUserID(_ context.Context, userID uuid.UUID) (*postgres.Channel, error) {
	if name, ok := f.names[userID]; ok {
		return &postgres.Channel{UserID: userID, Name: name}, nil
	}
	return nil, nil
}

type useSoundFixture struct {
	*soundFixture
	maker  *fakeSoundMaker
	states fakeMediaStates
	reel   uuid.UUID // the owner's public reel, over publicVideo
	hidden map[uuid.UUID]bool
}

func newUseSoundFixture() *useSoundFixture {
	f := &useSoundFixture{soundFixture: newSoundFixture(), maker: &fakeSoundMaker{}, reel: uuid.New(), hidden: map[uuid.UUID]bool{}}
	f.states = fakeMediaStates{f.publicVideo: {UploaderID: f.owner, Kind: "video", ProcessingStatus: "ready", ModerationStatus: "passed", DurationMs: 28400}}
	f.store.posts[f.reel] = &postgres.Post{ID: f.reel, AuthorID: f.owner, ContentType: "flick", Visibility: "public", ReviewStatus: "approved",
		RemixSetting: "allow", Media: []postgres.PostMedia{{MediaID: f.publicVideo, Kind: "video"}}}
	f.svc.soundMaker = f.maker
	f.svc.mediaStates = f.states
	f.svc.channels = fakeSoundChannels{names: map[uuid.UUID]string{f.owner: "Asha"}}
	f.svc.readGate = func(_ context.Context, postID uuid.UUID, _ *uuid.UUID) error {
		if f.hidden[postID] || f.store.posts[postID] == nil {
			return ErrPostNotVisible
		}
		return nil
	}
	return f
}

func (f *useSoundFixture) use(viewer uuid.UUID) (*PostSound, error) {
	return f.svc.UseSound(context.Background(), viewer, f.reel)
}

func TestUseSoundAsksMediaServiceForTheReelsOwnSound(t *testing.T) {
	f := newUseSoundFixture()
	got, err := f.use(f.stranger)
	if err != nil {
		t.Fatal(err)
	}
	if len(f.maker.calls) != 1 {
		t.Fatalf("media-service was asked %d times", len(f.maker.calls))
	}
	call := f.maker.calls[0]
	want := SoundRequest{Title: "Original sound - Asha", Artist: "Asha", SourcePostID: f.reel, CreatorUserID: f.owner}
	if call.media != f.publicVideo || call.in != want {
		t.Fatalf("asked for media %s with %+v, want %s with %+v", call.media, call.in, f.publicVideo, want)
	}
	if got.Title != want.Title || got.Artist != "Asha" || got.StartMs != 0 || got.DurationMs != 28400 ||
		got.SourcePostID == nil || *got.SourcePostID != f.reel || got.CreatorUserID == nil || *got.CreatorUserID != f.owner {
		t.Fatalf("sound = %+v", got)
	}
}

func TestUseSoundRefusals(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(f *useSoundFixture)
		viewer func(f *useSoundFixture) uuid.UUID
		check  func(t *testing.T, err error)
	}{
		{"a post the viewer may not open", func(f *useSoundFixture) { f.hidden[f.reel] = true }, nil,
			func(t *testing.T, err error) {
				if !errors.Is(err, ErrPostNotVisible) {
					t.Fatalf("got %v", err)
				}
			}},
		{"still processing, for a stranger", func(f *useSoundFixture) {
			f.states[f.publicVideo] = postgres.MediaOwnership{Kind: "video", ProcessingStatus: "processing", ModerationStatus: "pending"}
		}, nil, func(t *testing.T, err error) {
			if !errors.Is(err, ErrPostNotVisible) {
				t.Fatalf("got %v", err)
			}
		}},
		{"still processing, for its author", func(f *useSoundFixture) {
			f.states[f.publicVideo] = postgres.MediaOwnership{Kind: "video", ProcessingStatus: "processing", ModerationStatus: "pending"}
		}, func(f *useSoundFixture) uuid.UUID { return f.owner }, wantRefusal(SoundCodeNotReady)},
		{"a long video", func(f *useSoundFixture) { f.store.posts[f.reel].ContentType = "long_video" }, nil, wantRefusal(SoundCodeNotAReel)},
		{"a text post", func(f *useSoundFixture) { f.store.posts[f.reel].ContentType = "post" }, nil, wantRefusal(SoundCodeNotAReel)},
		{"a reel with no video", func(f *useSoundFixture) {
			f.store.posts[f.reel].Media = []postgres.PostMedia{{MediaID: f.publicVideo, Kind: "image"}}
		}, nil,
			wantRefusal(SoundCodeNotAReel)},
		{"longer than five minutes", func(f *useSoundFixture) {
			m := f.states[f.publicVideo]
			m.DurationMs = MaxSoundSourceMs + 1
			f.states[f.publicVideo] = m
		}, nil, wantRefusal(SoundCodeTooLong)},
		{"reuse turned off, for a stranger", func(f *useSoundFixture) { f.store.posts[f.reel].RemixSetting = "disallow" }, nil,
			func(t *testing.T, err error) {
				if !errors.Is(err, ErrSoundReuseNotAllowed) {
					t.Fatalf("got %v", err)
				}
			}},
		{"over the hourly limit", func(f *useSoundFixture) {
			f.svc.soundLimiter = func(context.Context, uuid.UUID) bool { return false }
		}, nil, func(t *testing.T, err error) {
			if !errors.Is(err, ErrSoundRateLimited) {
				t.Fatalf("got %v", err)
			}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newUseSoundFixture()
			tc.change(f)
			viewer := f.stranger
			if tc.viewer != nil {
				viewer = tc.viewer(f)
			}
			got, err := f.use(viewer)
			if got != nil || err == nil {
				t.Fatalf("sound=%+v err=%v, want a refusal", got, err)
			}
			tc.check(t, err)
			if len(f.maker.calls) != 0 {
				t.Fatalf("media-service was asked for a refused sound: %+v", f.maker.calls)
			}
		})
	}
}

func wantRefusal(code string) func(t *testing.T, err error) {
	return func(t *testing.T, err error) {
		t.Helper()
		var refusal *SoundRefusal
		if !errors.As(err, &refusal) || refusal.Code != code {
			t.Fatalf("got %v, want the refusal %s", err, code)
		}
	}
}

// Exactly five minutes is a sound source; the author may always use their
// own reel's sound, reuse turned off or not.
func TestUseSoundAdmits(t *testing.T) {
	f := newUseSoundFixture()
	m := f.states[f.publicVideo]
	m.DurationMs = MaxSoundSourceMs
	f.states[f.publicVideo] = m
	if _, err := f.use(f.stranger); err != nil {
		t.Fatalf("a video of exactly five minutes: %v", err)
	}

	g := newUseSoundFixture()
	g.store.posts[g.reel].RemixSetting = "disallow"
	if _, err := g.use(g.owner); err != nil {
		t.Fatalf("the author, with reuse turned off: %v", err)
	}
	if len(g.maker.calls) != 1 || g.maker.calls[0].in.CreatorUserID != g.owner {
		t.Fatalf("calls = %+v", g.maker.calls)
	}

	h := newUseSoundFixture()
	h.store.posts[h.reel].ContentType = "reel" // the legacy spelling
	if _, err := h.use(h.stranger); err != nil {
		t.Fatalf("content_type reel: %v", err)
	}
}

// A reel that already plays an added sound answers that sound.
func TestUseSoundReturnsTheSoundTheReelAlreadyPlays(t *testing.T) {
	f := newUseSoundFixture()
	start := 4000
	f.store.posts[f.reel].AudioTrackID, f.store.posts[f.reel].AudioStartMs = &f.publicSound, &start
	got, err := f.use(f.stranger)
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != f.publicSound || got.StartMs != 0 || got.UseCount != 3 {
		t.Fatalf("sound = %+v, want the attached sound from its start", got)
	}
	if len(f.maker.calls) != 0 {
		t.Fatalf("media-service was asked although the reel already plays a sound: %+v", f.maker.calls)
	}

	// Its source turned reuse off: nobody but that source's author.
	source := *f.store.tracks[f.publicSound].SourcePostID
	f.store.sources[source] = []postgres.SoundSourcePost{{ID: source, AuthorID: f.owner, RemixSetting: "disallow"}}
	if _, err := f.use(f.stranger); !errors.Is(err, ErrSoundReuseNotAllowed) {
		t.Fatalf("a stranger, source reuse off: got %v", err)
	}
	if _, err := f.use(f.owner); err != nil {
		t.Fatalf("the source's author: %v", err)
	}
	if len(f.maker.calls) != 0 {
		t.Fatalf("media-service was asked: %+v", f.maker.calls)
	}
}

// A sound the viewer may not hear is not theirs to reuse: what they get is
// the reel's own audio. One that cannot be decided is a fault.
func TestUseSoundFallsBackToTheReelsOwnAudio(t *testing.T) {
	f := newUseSoundFixture()
	f.store.posts[f.reel].AudioTrackID = &f.privateSound
	got, err := f.use(f.stranger)
	if err != nil {
		t.Fatal(err)
	}
	if len(f.maker.calls) != 1 || got.ID == f.privateSound {
		t.Fatalf("sound=%+v calls=%+v, want the reel's own sound made", got, f.maker.calls)
	}

	g := newUseSoundFixture()
	g.store.posts[g.reel].AudioTrackID = &g.publicSound
	g.audienceErr = errors.New("media access store unavailable")
	if _, err := g.use(g.stranger); !errors.Is(err, ErrSoundUnavailable) {
		t.Fatalf("audience down: got %v, want %v", err, ErrSoundUnavailable)
	}
	if len(g.maker.calls) != 0 {
		t.Fatalf("an undecided sound was replaced by a new one: %+v", g.maker.calls)
	}
}

func TestUseSoundPassesOnWhatMediaServiceSays(t *testing.T) {
	f := newUseSoundFixture()
	f.maker.err = &SoundRefusal{Code: "NOT_A_VIDEO"}
	_, err := f.use(f.stranger)
	wantRefusal("NOT_A_VIDEO")(t, err)

	g := newUseSoundFixture()
	g.maker.err = errors.Join(ErrSoundUnavailable, errors.New("status 502"))
	if _, err := g.use(g.stranger); !errors.Is(err, ErrSoundUnavailable) {
		t.Fatalf("got %v", err)
	}

	h := newUseSoundFixture()
	h.maker.sound = &MediaSound{ID: uuid.New(), Status: "processing"}
	_, err = h.use(h.stranger)
	wantRefusal(SoundCodeNotReady)(t, err)

	k := newUseSoundFixture()
	k.maker.sound = &MediaSound{}
	if _, err := k.use(k.stranger); !errors.Is(err, ErrSoundUnavailable) {
		t.Fatalf("a sound with no id: got %v", err)
	}
}

func TestSoundNames(t *testing.T) {
	long := strings.Repeat("క", 200) // runes, not bytes
	for _, tc := range []struct{ name, wantTitle, wantArtist string }{
		{"Asha", "Original sound - Asha", "Asha"},
		{"  Asha  ", "Original sound - Asha", "Asha"},
		{"", "Original sound", ""},
		{"   ", "Original sound", ""},
		{long, "Original sound - " + strings.Repeat("క", 80), strings.Repeat("క", 80)},
	} {
		title, artist := soundNames(tc.name)
		if title != tc.wantTitle || artist != tc.wantArtist {
			t.Fatalf("soundNames(%q) = %q, %q", tc.name, title, artist)
		}
		if n := len([]rune(title)); n > 120 {
			t.Fatalf("title is %d runes", n)
		}
	}
	// No channel and no profile service: the sound is plain.
	f := newUseSoundFixture()
	f.svc.channels = fakeSoundChannels{}
	if _, err := f.use(f.stranger); err != nil {
		t.Fatal(err)
	}
	if in := f.maker.calls[0].in; in.Title != "Original sound" || in.Artist != "" {
		t.Fatalf("request = %+v", in)
	}
}

// ---- the client ----

type soundServer struct {
	mu      sync.Mutex
	status  int
	body    string
	method  string
	path    string
	headers http.Header
	got     []byte
}

func (s *soundServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.method, s.path, s.headers = r.Method, r.URL.Path, r.Header.Clone()
	s.got, _ = io.ReadAll(r.Body)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(s.status)
	_, _ = io.WriteString(w, s.body)
}

func TestHTTPSoundSourceRequest(t *testing.T) {
	media, post, author, sound := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	fake := &soundServer{status: http.StatusOK, body: `{"data":{"id":"` + sound.String() + `","title":"Original sound - Asha","artist":"Asha",
		"duration_ms":28400,"status":"ready","is_original":true,"usage_count":3,"source_media_id":"` + media.String() + `",
		"source_post_id":"` + post.String() + `","source_reel_id":"` + post.String() + `","creator_user_id":"` + author.String() + `",
		"created_at":"2026-09-29T10:00:00Z"}}`}
	srv := httptest.NewServer(fake)
	defer srv.Close()
	svc := &Service{mediaServiceURL: srv.URL + "/", internalServiceKey: "internal-test-key", httpClient: &http.Client{Timeout: time.Second}}

	got, err := httpSoundSource{svc: svc}.EnsureSound(context.Background(), media,
		SoundRequest{Title: "Original sound - Asha", Artist: "Asha", SourcePostID: post, CreatorUserID: author})
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != sound || got.DurationMs != 28400 || got.UsageCount != 3 || got.Status != "ready" ||
		got.SourcePostID == nil || *got.SourcePostID != post || got.CreatorUserID == nil || *got.CreatorUserID != author {
		t.Fatalf("sound = %+v", got)
	}
	if fake.method != http.MethodPost || fake.path != "/v1/media/internal/"+media.String()+"/sound" {
		t.Fatalf("%s %s", fake.method, fake.path)
	}
	if fake.headers.Get("X-Internal-Service-Key") != "internal-test-key" || fake.headers.Get("Content-Type") != "application/json" ||
		fake.headers.Get("Accept") != "application/json" {
		t.Fatalf("headers = %v", fake.headers)
	}
	// A trusted service caller: never a viewer.
	for _, h := range []string{"X-User-Id", "Authorization", "Cookie"} {
		if v := fake.headers.Get(h); v != "" {
			t.Fatalf("the request carried %s: %q", h, v)
		}
	}
	var body map[string]any
	if err := json.Unmarshal(fake.got, &body); err != nil {
		t.Fatal(err)
	}
	want := map[string]any{"title": "Original sound - Asha", "artist": "Asha", "source_post_id": post.String(), "creator_user_id": author.String()}
	if !reflect.DeepEqual(body, want) {
		t.Fatalf("body = %s, want %v", fake.got, want)
	}
}

func TestHTTPSoundSourceAnswers(t *testing.T) {
	media := uuid.New()
	for _, tc := range []struct {
		name   string
		status int
		body   string
		check  func(t *testing.T, err error)
	}{
		{"a refusal passes through by its code", 422, `{"error":{"code":"TOO_LONG","message":"ffprobe says 301.2 s"}}`, wantRefusal("TOO_LONG")},
		{"not a video", 422, `{"error":{"code":"NOT_A_VIDEO","message":"x"}}`, wantRefusal("NOT_A_VIDEO")},
		{"a refusal with no code", 422, `{"error":{"message":"x"}}`, wantUnavailable},
		{"a refusal whose code is not a code", 422, `{"error":{"code":"<script>","message":"x"}}`, wantUnavailable},
		{"media-service fault", 503, `{"error":{"code":"STORAGE","message":"s3://bucket/key timed out"}}`, wantUnavailable},
		{"media-service crash", 500, `oops`, wantUnavailable},
		{"media missing", 404, `{"error":{"code":"NOT_FOUND","message":"x"}}`, wantUnavailable},
		{"the key was refused", 401, `{"error":{"code":"UNAUTHORIZED","message":"x"}}`, wantUnavailable},
		{"200 with no sound", 200, `{"data":null}`, wantUnavailable},
		{"200 that is not JSON", 200, `<html>`, wantUnavailable},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(&soundServer{status: tc.status, body: tc.body})
			defer srv.Close()
			svc := &Service{mediaServiceURL: srv.URL}
			got, err := httpSoundSource{svc: svc}.EnsureSound(context.Background(), media, SoundRequest{})
			if got != nil || err == nil {
				t.Fatalf("sound=%+v err=%v", got, err)
			}
			tc.check(t, err)
			// Another service's words never reach the caller's message.
			var refusal *SoundRefusal
			if errors.As(err, &refusal) && strings.Contains(refusal.Message(), "ffprobe") {
				t.Fatalf("the refusal carries media-service's text: %q", refusal.Message())
			}
		})
	}

	t.Run("unreachable", func(t *testing.T) {
		srv := httptest.NewServer(&soundServer{status: 200})
		url := srv.URL
		srv.Close()
		_, err := httpSoundSource{svc: &Service{mediaServiceURL: url}}.EnsureSound(context.Background(), media, SoundRequest{})
		wantUnavailable(t, err)
	})
	t.Run("not configured", func(t *testing.T) {
		_, err := httpSoundSource{svc: &Service{}}.EnsureSound(context.Background(), media, SoundRequest{})
		wantUnavailable(t, err)
	})
}

func wantUnavailable(t *testing.T, err error) {
	t.Helper()
	if !errors.Is(err, ErrSoundUnavailable) {
		t.Fatalf("got %v, want %v", err, ErrSoundUnavailable)
	}
}

func TestNormalizeSoundPostsLimit(t *testing.T) {
	for in, want := range map[int]int{0: 24, -3: 24, 1: 1, 24: 24, 50: 50, 51: 50, 5000: 50} {
		if got := NormalizeSoundPostsLimit(in); got != want {
			t.Fatalf("limit %d -> %d, want %d", in, got, want)
		}
	}
}

func TestValidSoundPostsCursor(t *testing.T) {
	good := time.Date(2026, 9, 29, 10, 0, 0, 123456000, time.UTC).Format(time.RFC3339Nano) + "_" + uuid.NewString()
	if !ValidSoundPostsCursor(good) {
		t.Fatalf("%q was refused", good)
	}
	for _, bad := range []string{"x", "2026-09-29T10:00:00Z", uuid.NewString(), "2026-09-29T10:00:00Z_not-a-uuid", "yesterday_" + uuid.NewString()} {
		if ValidSoundPostsCursor(bad) {
			t.Fatalf("%q was accepted", bad)
		}
	}
}

// ---- where `sound` is attached ----

// The page filter every listing shares attaches the sound too.
func TestListingsAttachTheSound(t *testing.T) {
	f := newSoundFixture()
	f.svc.mediaStates = fakeMediaStates{}
	start := 1500
	rows := []PostDetail{
		{Post: &postgres.Post{ID: uuid.New(), AuthorID: f.stranger, AudioTrackID: &f.publicSound, AudioStartMs: &start}},
		{Post: &postgres.Post{ID: uuid.New(), AuthorID: f.stranger, AudioTrackID: &f.privateSound}},
		{Post: &postgres.Post{ID: uuid.New(), AuthorID: f.stranger}},
	}
	out, err := f.svc.attachMediaStateToDetails(context.Background(), rows, nil)
	if err != nil || len(out) != 3 {
		t.Fatalf("%d rows, %v", len(out), err)
	}
	if out[0].Sound == nil || out[0].Sound.ID != f.publicSound || out[0].Sound.StartMs != 1500 {
		t.Fatalf("row 0 sound = %+v", out[0].Sound)
	}
	if out[1].Sound != nil || out[2].Sound != nil {
		t.Fatalf("rows 1 and 2 carry sounds: %+v %+v", out[1].Sound, out[2].Sound)
	}
	if len(f.asked) != 1 || f.asked[0].viewer != uuid.Nil {
		t.Fatalf("asked = %+v, want one question as the signed-out viewer", f.asked)
	}

	// An audience that cannot be decided does not fail the page.
	f.audienceErr = errors.New("media access store unavailable")
	out, err = f.svc.attachMediaStateToDetails(context.Background(), rows, &f.owner)
	if err != nil || len(out) != 3 || out[0].Sound != nil {
		t.Fatalf("rows=%d err=%v sound=%+v", len(out), err, out[0].Sound)
	}
}

// The two reads that need Scylla and cannot run here are held to the rule
// by their source: GetPost attaches the sound after the cache read, and
// GetPostsByIDs attaches it to the page it returns.
func TestPostReadsAttachTheSound(t *testing.T) {
	src, err := os.ReadFile("post.go")
	if err != nil {
		t.Fatal(err)
	}
	body := func(signature string) string {
		t.Helper()
		start := strings.Index(string(src), signature)
		if start < 0 {
			t.Fatalf("post.go has no %q", signature)
		}
		rest := string(src)[start:]
		end := strings.Index(rest, "\n}\n")
		if end < 0 {
			t.Fatalf("%q has no end", signature)
		}
		return rest[:end]
	}

	get := body("func (s *Service) GetPost(ctx context.Context, id uuid.UUID, viewerID *uuid.UUID) (*PostDetail, error) {")
	cache, attach := strings.Index(get, "s.getCachedPostBody(ctx, id)"), strings.Index(get, "s.attachSounds(ctx, viewerID, []*PostDetail{detail})")
	if cache < 0 || attach < 0 || attach < cache {
		t.Fatalf("GetPost must attach the sound, after the cache read (cache at %d, attach at %d)", cache, attach)
	}
	if last := strings.LastIndex(get, "return detail, nil"); last < attach {
		t.Fatal("GetPost returns the detail before the sound is attached")
	}

	batch := body("func (s *Service) GetPostsByIDs(ctx context.Context, ids []uuid.UUID, viewerID *uuid.UUID) (map[uuid.UUID]*PostDetail, error) {")
	attach = strings.Index(batch, "s.attachSounds(ctx, viewerID, page)")
	if attach < 0 || strings.LastIndex(batch, "return result, nil") < attach {
		t.Fatal("GetPostsByIDs must attach the sound to the page it returns")
	}
	// Nothing on these reads writes a sound into the cache.
	cacheSrc, err := os.ReadFile("post_cache.go")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(cacheSrc), "attachSounds") || strings.Contains(string(cacheSrc), "PostSound") {
		t.Fatal("post_cache.go handles sounds: a cached body holds nothing per viewer")
	}
}
