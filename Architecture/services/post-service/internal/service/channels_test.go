package service

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/atpost/post-service/internal/store/postgres"
	"github.com/google/uuid"
)

// Tube channels (2026-09-05): the contract's validation rules, the one-per-
// account / handle-taken conflicts, and the founder's long-video gate.

func TestNormalizeChannelHandle(t *testing.T) {
	cases := []struct {
		in   string
		want string
		ok   bool
	}{
		{"abc", "abc", true},
		{"Call.B_Studio", "call.b_studio", true},
		{"@CallB", "callb", true},
		{"  spaced  ", "spaced", true},
		{"a1_b2.c3", "a1_b2.c3", true},
		{strings.Repeat("x", 30), strings.Repeat("x", 30), true},
		{"ab", "", false},                    // too short
		{strings.Repeat("x", 31), "", false}, // too long
		{"_abc", "", false},                  // must start alnum
		{"abc_", "", false},                  // must end alnum
		{".abc", "", false},                  // must start alnum
		{"abc.", "", false},                  // must end alnum
		{"ab..c", "", false},                 // no ".."
		{"call b", "", false},                // no spaces
		{"call-b", "", false},                // no dashes
		{"cállb", "", false},                 // ascii only
		{"", "", false},
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			got, err := NormalizeChannelHandle(tc.in)
			if tc.ok && (err != nil || got != tc.want) {
				t.Fatalf("got %q, %v; want %q", got, err, tc.want)
			}
			if !tc.ok && !errors.Is(err, ErrInvalidHandle) {
				t.Fatalf("got %q, %v; want ErrInvalidHandle", got, err)
			}
		})
	}
}

func TestNormalizeChannelNameAndAbout(t *testing.T) {
	if _, err := NormalizeChannelName("  ab "); !errors.Is(err, ErrInvalidChannelName) {
		t.Fatalf("2-rune name accepted: %v", err)
	}
	if got, err := NormalizeChannelName("  Call B Studio "); err != nil || got != "Call B Studio" {
		t.Fatalf("name not trimmed: %q %v", got, err)
	}
	// Runes, not bytes: 40 Devanagari characters are a valid name.
	if _, err := NormalizeChannelName(strings.Repeat("क", 40)); err != nil {
		t.Fatalf("40-rune non-latin name refused: %v", err)
	}
	if _, err := NormalizeChannelName(strings.Repeat("x", 41)); !errors.Is(err, ErrInvalidChannelName) {
		t.Fatalf("41-rune name accepted: %v", err)
	}
	if got, err := NormalizeChannelAbout(""); err != nil || got != "" {
		t.Fatalf("empty about must be fine: %q %v", got, err)
	}
	if _, err := NormalizeChannelAbout(strings.Repeat("क", 200)); err != nil {
		t.Fatalf("200-rune about refused: %v", err)
	}
	if _, err := NormalizeChannelAbout(strings.Repeat("x", 201)); !errors.Is(err, ErrInvalidChannelAbout) {
		t.Fatalf("201-rune about accepted: %v", err)
	}
}

func TestSlugifyHandleAlwaysValid(t *testing.T) {
	cases := map[string]string{
		"Call B":                      "call.b",
		"call_b":                      "call_b",
		"  Raghu Varan!! ":            "raghu.varan",
		"__lead":                      "lead",
		"a":                           "a.channel",
		"":                            "creator",
		"héllo wörld":                 "h.llo.w.rld",
		strings.Repeat("longname", 6): strings.Repeat("longname", 6)[:28],
	}
	for in, want := range cases {
		got := SlugifyHandle(in)
		if got != want {
			t.Errorf("SlugifyHandle(%q) = %q, want %q", in, got, want)
		}
		if _, err := NormalizeChannelHandle(got); err != nil {
			t.Errorf("SlugifyHandle(%q) = %q is not a valid handle: %v", in, got, err)
		}
	}
}

// fakeChannelStore is an in-memory channelStore.
type fakeChannelStore struct {
	byUser   map[uuid.UUID]*postgres.Channel
	byHandle map[string]*postgres.Channel
	videos   map[uuid.UUID]int
}

func newFakeChannelStore() *fakeChannelStore {
	return &fakeChannelStore{
		byUser:   map[uuid.UUID]*postgres.Channel{},
		byHandle: map[string]*postgres.Channel{},
		videos:   map[uuid.UUID]int{},
	}
}

func (f *fakeChannelStore) CreateChannel(_ context.Context, ch *postgres.Channel) error {
	if _, ok := f.byHandle[ch.Handle]; ok {
		return postgres.ErrHandleTaken
	}
	if _, ok := f.byUser[ch.UserID]; ok {
		return postgres.ErrChannelExists
	}
	ch.ID = uuid.New()
	ch.CreatedAt = time.Now()
	ch.UpdatedAt = ch.CreatedAt
	c := *ch
	f.byUser[c.UserID] = &c
	f.byHandle[c.Handle] = &c
	return nil
}

func (f *fakeChannelStore) UpdateChannel(_ context.Context, userID uuid.UUID, patch postgres.ChannelPatch) (*postgres.Channel, error) {
	ch, ok := f.byUser[userID]
	if !ok {
		return nil, postgres.ErrChannelNotFound
	}
	if patch.Handle != nil && *patch.Handle != ch.Handle {
		if _, taken := f.byHandle[*patch.Handle]; taken {
			return nil, postgres.ErrHandleTaken
		}
		delete(f.byHandle, ch.Handle)
		ch.Handle = *patch.Handle
		f.byHandle[ch.Handle] = ch
	}
	if patch.Name != nil {
		ch.Name = *patch.Name
	}
	if patch.About != nil {
		ch.About = *patch.About
	}
	if patch.ClearAvatar {
		ch.AvatarMediaID = nil
	} else if patch.AvatarMediaID != nil {
		ch.AvatarMediaID = patch.AvatarMediaID
	}
	ch.UpdatedAt = time.Now()
	c := *ch
	return &c, nil
}

func (f *fakeChannelStore) GetChannelByUserID(_ context.Context, userID uuid.UUID) (*postgres.Channel, error) {
	if ch, ok := f.byUser[userID]; ok {
		c := *ch
		return &c, nil
	}
	return nil, nil
}

func (f *fakeChannelStore) GetChannelByHandle(_ context.Context, handle string) (*postgres.Channel, error) {
	if ch, ok := f.byHandle[handle]; ok {
		c := *ch
		return &c, nil
	}
	return nil, nil
}

func (f *fakeChannelStore) ChannelHandleExists(_ context.Context, handle string) (bool, error) {
	_, ok := f.byHandle[handle]
	return ok, nil
}

func (f *fakeChannelStore) GetChannelsByUserIDs(_ context.Context, ids []uuid.UUID) (map[uuid.UUID]*postgres.Channel, error) {
	out := map[uuid.UUID]*postgres.Channel{}
	for _, id := range ids {
		if ch, ok := f.byUser[id]; ok {
			c := *ch
			out[id] = &c
		}
	}
	return out, nil
}

func (f *fakeChannelStore) CountChannelVideos(_ context.Context, id uuid.UUID) (int, error) {
	return f.videos[id], nil
}

func (f *fakeChannelStore) CountChannelVideosBatch(_ context.Context, ids []uuid.UUID) (map[uuid.UUID]int, error) {
	out := map[uuid.UUID]int{}
	for _, id := range ids {
		out[id] = f.videos[id]
	}
	return out, nil
}

// SearchChannels applies the matcher and returns the rows UNORDERED, so the
// service's sort is what the tests exercise.
func (f *fakeChannelStore) SearchChannels(_ context.Context, q string, limit int) ([]postgres.ChannelSearchHit, error) {
	var out []postgres.ChannelSearchHit
	for _, ch := range f.byHandle {
		if ok, _ := ChannelMatches(q, ch.Handle, ch.Name); ok {
			out = append(out, postgres.ChannelSearchHit{Channel: *ch, VideoCount: f.videos[ch.UserID]})
		}
	}
	// Deliberately in map (random) order; over-return so the service's
	// own limit is exercised too.
	_ = limit
	return out, nil
}

func newChannelTestService(store channelStore) *Service {
	return &Service{channels: store, httpClient: &http.Client{Timeout: time.Second}}
}

func TestNormalizeChannelQuery(t *testing.T) {
	cases := []struct {
		in   string
		want string
		ok   bool
	}{
		{"Call", "call", true},
		{"  @Call.B  ", "call.b", true},
		{"@", "", false},
		{"   ", "", false},
		{"", "", false},
		{"c", "c", true},
	}
	for _, tc := range cases {
		got, err := NormalizeChannelQuery(tc.in)
		if tc.ok && (err != nil || got != tc.want) {
			t.Errorf("%q: got %q, %v; want %q", tc.in, got, err, tc.want)
		}
		if !tc.ok && !errors.Is(err, ErrEmptyChannelQuery) {
			t.Errorf("%q: got %q, %v; want ErrEmptyChannelQuery", tc.in, got, err)
		}
	}
	if ClampChannelSearchLimit(0) != DefaultChannelSearchLimit || ClampChannelSearchLimit(-3) != DefaultChannelSearchLimit {
		t.Fatal("zero/negative limit must fall back to the default")
	}
	if ClampChannelSearchLimit(500) != MaxChannelSearchLimit || ClampChannelSearchLimit(7) != 7 {
		t.Fatal("limit must be clamped to the max and otherwise honoured")
	}
}

func TestChannelMatches(t *testing.T) {
	cases := []struct {
		q, handle, name       string
		matches, handlePrefix bool
	}{
		{"call", "call.userb", "Call B Studio", true, true},
		{"call", "userb", "Call B Studio", true, false},        // name substring only
		{"studio", "call.userb", "Call B Studio", true, false}, // name substring, not a handle prefix
		{"userb", "call.userb", "Call B Studio", false, false}, // handle substring is NOT a match
		{"b st", "call.userb", "Call B Studio", true, false},   // spaces inside the name are fine
		{"zzz", "call.userb", "Call B Studio", false, false},
		{"", "call.userb", "Call B Studio", false, false},
	}
	for _, tc := range cases {
		m, p := ChannelMatches(tc.q, tc.handle, tc.name)
		if m != tc.matches || p != tc.handlePrefix {
			t.Errorf("ChannelMatches(%q, %q, %q) = %v,%v want %v,%v", tc.q, tc.handle, tc.name, m, p, tc.matches, tc.handlePrefix)
		}
	}
}

func TestSearchChannelsOrdersPrefixThenVideoCount(t *testing.T) {
	store := newFakeChannelStore()
	mk := func(handle, name string, videos int) uuid.UUID {
		id := uuid.New()
		if err := store.CreateChannel(context.Background(), &postgres.Channel{UserID: id, Handle: handle, Name: name}); err != nil {
			t.Fatal(err)
		}
		store.videos[id] = videos
		return id
	}
	mk("callb", "B", 1)                // handle prefix, 1 video
	mk("calla", "A", 5)                // handle prefix, 5 videos -> first
	mk("callc", "C", 5)                // handle prefix, 5 videos -> after calla by handle
	mk("studio", "The Call Room", 100) // name-only match -> after every prefix match
	mk("zeta", "Unrelated", 9)         // no match

	svc := newChannelTestService(store)
	views, err := svc.SearchChannels(context.Background(), uuid.Nil, " @CALL ", 0)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, v := range views {
		got = append(got, v.Handle)
	}
	want := []string{"calla", "callc", "callb", "studio"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("order = %v, want %v", got, want)
	}
	if views[0].VideoCount != 5 || views[3].VideoCount != 100 {
		t.Fatalf("video_count not carried from the search row: %+v", views)
	}

	// The limit is honoured after ranking.
	views, err = svc.SearchChannels(context.Background(), uuid.Nil, "call", 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(views) != 2 || views[0].Handle != "calla" || views[1].Handle != "callc" {
		t.Fatalf("limited page = %+v", views)
	}

	// A blank query is refused, never a full table scan.
	if _, err := svc.SearchChannels(context.Background(), uuid.Nil, " @ ", 10); !errors.Is(err, ErrEmptyChannelQuery) {
		t.Fatalf("blank query: got %v", err)
	}
}

func TestCreateChannelValidatesAndConflicts(t *testing.T) {
	ctx := context.Background()
	store := newFakeChannelStore()
	svc := newChannelTestService(store)
	owner, other := uuid.New(), uuid.New()
	store.videos[owner] = 2

	// Validation, in the order the contract names the codes.
	if _, err := svc.CreateChannel(ctx, owner, CreateChannelInput{Name: "ab", Handle: "callb"}); !errors.Is(err, ErrInvalidChannelName) {
		t.Fatalf("short name: %v", err)
	}
	if _, err := svc.CreateChannel(ctx, owner, CreateChannelInput{Name: "Call B Studio", Handle: "call b"}); !errors.Is(err, ErrInvalidHandle) {
		t.Fatalf("bad handle: %v", err)
	}
	if _, err := svc.CreateChannel(ctx, owner, CreateChannelInput{Name: "Call B Studio", Handle: "callb", About: strings.Repeat("x", 201)}); !errors.Is(err, ErrInvalidChannelAbout) {
		t.Fatalf("long about: %v", err)
	}

	// Create: handle is stored lowercased, video_count comes from the posts.
	view, err := svc.CreateChannel(ctx, owner, CreateChannelInput{Name: " Call B Studio ", Handle: "Call.B", About: " hi "})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if view.UserID != owner || view.Name != "Call B Studio" || view.Handle != "call.b" || view.About != "hi" || view.VideoCount != 2 || view.AvatarURL != nil {
		t.Fatalf("unexpected view: %+v", view)
	}

	// One per account.
	if _, err := svc.CreateChannel(ctx, owner, CreateChannelInput{Name: "Second", Handle: "second"}); !errors.Is(err, postgres.ErrChannelExists) {
		t.Fatalf("duplicate: %v", err)
	}
	// Handle uniqueness is case-insensitive because handles are lowercased.
	if _, err := svc.CreateChannel(ctx, other, CreateChannelInput{Name: "Other", Handle: "CALL.B"}); !errors.Is(err, postgres.ErrHandleTaken) {
		t.Fatalf("taken handle: %v", err)
	}

	// Me / by ref / by user id / patch.
	me, err := svc.GetMyChannel(ctx, owner)
	if err != nil || me.Handle != "call.b" {
		t.Fatalf("me: %+v %v", me, err)
	}
	if _, err := svc.GetMyChannel(ctx, other); !errors.Is(err, ErrChannelNotFound) {
		t.Fatalf("no channel: %v", err)
	}
	if got, err := svc.GetChannelByRef(ctx, other, "@Call.B"); err != nil || got.UserID != owner {
		t.Fatalf("by handle: %+v %v", got, err)
	}
	if got, err := svc.GetChannelByRef(ctx, other, owner.String()); err != nil || got.Handle != "call.b" {
		t.Fatalf("by user id: %+v %v", got, err)
	}
	if _, err := svc.GetChannelByRef(ctx, other, "nobody"); !errors.Is(err, ErrChannelNotFound) {
		t.Fatalf("unknown ref: %v", err)
	}
	newName := "Call B Films"
	if got, err := svc.UpdateMyChannel(ctx, owner, UpdateChannelInput{Name: &newName}); err != nil || got.Name != newName || got.Handle != "call.b" {
		t.Fatalf("patch name: %+v %v", got, err)
	}
	if _, err := svc.UpdateMyChannel(ctx, other, UpdateChannelInput{Name: &newName}); !errors.Is(err, ErrChannelNotFound) {
		t.Fatalf("patch without channel: %v", err)
	}
}

func TestChannelHandleAvailabilitySuggestsFreeVariant(t *testing.T) {
	ctx := context.Background()
	store := newFakeChannelStore()
	svc := newChannelTestService(store)
	owner := uuid.New()
	if _, err := svc.CreateChannel(ctx, owner, CreateChannelInput{Name: "Call B Studio", Handle: "call.b"}); err != nil {
		t.Fatal(err)
	}

	available, suggestion, err := svc.ChannelHandleAvailability(ctx, uuid.New(), "Call.B", "")
	if err != nil || available || suggestion != "call.b1" {
		t.Fatalf("taken: available=%v suggestion=%q err=%v", available, suggestion, err)
	}
	available, suggestion, err = svc.ChannelHandleAvailability(ctx, uuid.New(), "fresh.one", "")
	if err != nil || !available || suggestion != "fresh.one" {
		t.Fatalf("free: available=%v suggestion=%q err=%v", available, suggestion, err)
	}
	// The owner's own handle is theirs.
	available, suggestion, err = svc.ChannelHandleAvailability(ctx, owner, "call.b", "")
	if err != nil || !available || suggestion != "call.b" {
		t.Fatalf("own handle: available=%v suggestion=%q err=%v", available, suggestion, err)
	}
	// Invalid input still yields a usable suggestion.
	available, suggestion, err = svc.ChannelHandleAvailability(ctx, uuid.New(), "Call B!", "")
	if err != nil || available || suggestion != "call.b1" {
		t.Fatalf("invalid: available=%v suggestion=%q err=%v", available, suggestion, err)
	}
	// No handle: derived from the seed (no profile service configured).
	_, suggestion, err = svc.ChannelHandleAvailability(ctx, uuid.New(), "", "Raghu Varan")
	if err != nil || suggestion != "raghu.varan" {
		t.Fatalf("seeded: suggestion=%q err=%v", suggestion, err)
	}
}

// The founder's gate: a long video needs a channel; reels/flicks never do.
func TestGateVideoBehindChannel(t *testing.T) {
	ctx := context.Background()
	store := newFakeChannelStore()
	svc := newChannelTestService(store)
	withChannel, without := uuid.New(), uuid.New()
	if _, err := svc.CreateChannel(ctx, withChannel, CreateChannelInput{Name: "Call B Studio", Handle: "call.b"}); err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name        string
		author      uuid.UUID
		contentType string
		wantErr     error
	}{
		{"long_video without channel", without, "long_video", ErrChannelRequired},
		{"legacy video without channel", without, "video", ErrChannelRequired},
		{"long_video with channel", withChannel, "long_video", nil},
		{"flick without channel is not gated", without, "flick", nil},
		{"post without channel is not gated", without, "post", nil},
		{"poll without channel is not gated", without, "poll", nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := svc.gateVideoBehindChannel(ctx, tc.author, tc.contentType)
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("got %v want %v", err, tc.wantErr)
			}
		})
	}

	// No store at all fails closed.
	if err := (&Service{}).gateVideoBehindChannel(ctx, withChannel, "long_video"); !errors.Is(err, ErrChannelRequired) {
		t.Fatalf("nil store must fail closed: %v", err)
	}
}

// End to end through CreatePost: the gate fires before anything is written
// (and before any other store is touched — this Service has no pgStore).
func TestCreatePostLongVideoRequiresChannel(t *testing.T) {
	svc := newChannelTestService(newFakeChannelStore())
	for _, ct := range []string{"long_video", "video"} {
		_, err := svc.CreatePost(context.Background(), &CreatePostInput{
			AuthorID:    uuid.New(),
			Text:        "my first video",
			Title:       "My first video",
			Visibility:  "public",
			ContentType: ct,
			MediaIDs:    []uuid.UUID{uuid.New()},
		})
		if !errors.Is(err, ErrChannelRequired) {
			t.Fatalf("%s: got %v want ErrChannelRequired", ct, err)
		}
	}
}

func TestAttachChannelRefsOnlyOnLongVideos(t *testing.T) {
	ctx := context.Background()
	store := newFakeChannelStore()
	svc := newChannelTestService(store)
	owner, nobody := uuid.New(), uuid.New()
	if _, err := svc.CreateChannel(ctx, owner, CreateChannelInput{Name: "Call B Studio", Handle: "call.b"}); err != nil {
		t.Fatal(err)
	}
	details := []*PostDetail{
		{Post: &postgres.Post{ID: uuid.New(), AuthorID: owner, ContentType: "long_video"}},
		{Post: &postgres.Post{ID: uuid.New(), AuthorID: owner, ContentType: "flick"}},
		{Post: &postgres.Post{ID: uuid.New(), AuthorID: nobody, ContentType: "long_video"}},
	}
	svc.attachChannelRefs(ctx, uuid.New(), details)
	if details[0].Channel == nil || details[0].Channel.Handle != "call.b" || details[0].Channel.Name != "Call B Studio" || details[0].Channel.UserID != owner {
		t.Fatalf("long video by channel owner: %+v", details[0].Channel)
	}
	if details[1].Channel != nil {
		t.Fatalf("flick must not carry a channel: %+v", details[1].Channel)
	}
	if details[2].Channel != nil {
		t.Fatalf("author without channel must not carry one: %+v", details[2].Channel)
	}
}
