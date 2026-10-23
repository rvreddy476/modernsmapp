package service

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/atpost/post-service/internal/store/postgres"
	"github.com/google/uuid"
)

// Members-only reads (members_read.go). These pin the one rule every list
// shares and the redacted row's wire shape clients draw the lock from.

// membersFake answers the membership question in place of
// monetization-service and counts how often it was asked.
type membersFake struct {
	member map[uuid.UUID]map[uuid.UUID]bool // viewer -> tier -> member
	err    error
	asks   int
}

func (f *membersFake) svc() *Service {
	s := &Service{}
	s.offlineEntitlement = func(_ context.Context, viewer uuid.UUID, p *postgres.Post) (bool, error) {
		f.asks++
		if f.err != nil {
			return false, f.err
		}
		return f.member[viewer][*p.TierRequiredID], nil
	}
	return s
}

func strPtr(s string) *string { return &s }

func gatedRow(author, tier uuid.UUID) *postgres.Post {
	lat, lng, year := 17.385, 78.4867, 2026
	cover, sound, start := uuid.New(), uuid.New(), 1500
	return &postgres.Post{
		ID: uuid.New(), AuthorID: author, TierRequiredID: &tier,
		Text: "the paid body #secret @ravi", RichText: json.RawMessage(`{"blocks":["paid"]}`),
		Media: []postgres.PostMedia{
			{MediaID: uuid.New(), Kind: "audio", DurationMs: 90000}, // not a video: never the duration
			{MediaID: uuid.New(), Kind: "video", DurationMs: 61000},
			{MediaID: uuid.New(), Kind: "video", DurationMs: 30000},
		},
		Poll:  &postgres.PollData{Question: "paid poll"},
		Title: "Episode 1", ContentType: "long_video", Visibility: "public",
		// Derived from the body: cleared on a redacted row.
		Hashtags: []string{"secret"}, MentionUsernames: []string{"ravi"}, Mentions: []uuid.UUID{uuid.New()},
		Tags: []string{"paid-tag"}, LinkURL: strPtr("https://example.com/paid"), TaggedUserIDs: []uuid.UUID{uuid.New()},
		Action:       &postgres.ActionView{ID: uuid.New(), Label: "apply_now", LabelText: "Apply now", Domain: "example.com", Href: "/l/x"},
		LocationName: strPtr("Hyderabad"), LocationLat: &lat, LocationLng: &lng, LocationPlaceID: strPtr("place-1"),
		RecordingLocation: "Studio 4",
		// Kept on a redacted row.
		CoverMediaID: &cover, AudioTrackID: &sound, AudioStartMs: &start,
		Genres: []string{"drama"}, ReleaseYear: &year, ProductionHouse: strPtr("Momentum Films"),
	}
}

func openRow(author uuid.UUID) *postgres.Post {
	return &postgres.Post{ID: uuid.New(), AuthorID: author, Text: "open body", Title: "Open", ContentType: "post", Visibility: "public",
		Hashtags: []string{"open"}, LinkURL: strPtr("https://example.com/open")}
}

// isRedacted is the pinned redacted row: body and everything derived from
// it gone, the preview fields kept, the longest video's duration carried.
func isRedacted(p *postgres.Post) bool {
	cleared := p.BodyRedacted && p.Text == "" && p.RichText == nil && p.Media == nil && p.Poll == nil &&
		p.Hashtags == nil && p.MentionUsernames == nil && p.Mentions == nil && p.Tags == nil && p.TaggedUserIDs == nil && p.LinkURL == nil && p.Action == nil &&
		p.LocationName == nil && p.LocationLat == nil && p.LocationLng == nil && p.LocationPlaceID == nil && p.RecordingLocation == ""
	kept := p.Title == "Episode 1" && p.CoverMediaID != nil && p.ContentType == "long_video" && p.TierRequiredID != nil &&
		p.AudioTrackID != nil && p.AudioStartMs != nil && len(p.Genres) == 1 && p.ReleaseYear != nil && p.ProductionHouse != nil &&
		p.DurationMs == 61000
	return cleared && kept
}

func isFull(p *postgres.Post) bool {
	if p.BodyRedacted || p.Text == "" || p.DurationMs != 0 || p.Hashtags == nil || p.LinkURL == nil {
		return false
	}
	return p.TierRequiredID == nil || (p.RichText != nil && p.Media != nil && p.Poll != nil &&
		p.MentionUsernames != nil && p.Tags != nil && p.TaggedUserIDs != nil && p.LocationName != nil && p.RecordingLocation != "" && p.Action != nil)
}

// The rule, row by row, for every kind of viewer — and one membership
// question per distinct (creator, tier) on the page, never one per row.
func TestMembersRedactionRule(t *testing.T) {
	authorA, authorB := uuid.New(), uuid.New()
	tierA, tierB := uuid.New(), uuid.New()
	memberA, stranger := uuid.New(), uuid.New()

	cases := []struct {
		name     string
		viewer   *uuid.UUID
		wantFull [4]bool // a1, a2, b, open
		maxAsks  int
	}{
		{"signed out", nil, [4]bool{false, false, false, true}, 0},
		{"stranger", &stranger, [4]bool{false, false, false, true}, 2},
		{"member of A's tier", &memberA, [4]bool{true, true, false, true}, 2},
		{"author A", &authorA, [4]bool{true, true, false, true}, 1},
		{"author B", &authorB, [4]bool{false, false, true, true}, 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := &membersFake{member: map[uuid.UUID]map[uuid.UUID]bool{memberA: {tierA: true}}}
			s := f.svc()
			a1, a2, b, open := gatedRow(authorA, tierA), gatedRow(authorA, tierA), gatedRow(authorB, tierB), openRow(authorA)
			rows := []*postgres.Post{a1, a2, b, open, nil}
			s.redactForViewer(context.Background(), tc.viewer, rows)
			for i, p := range []*postgres.Post{a1, a2, b, open} {
				if tc.wantFull[i] && !isFull(p) {
					t.Errorf("row %d redacted, want full: %+v", i, p)
				}
				if !tc.wantFull[i] && !isRedacted(p) {
					t.Errorf("row %d full, want redacted: %+v", i, p)
				}
				// tier_required_id stays on every gated row for everyone.
				if p.TierRequiredID == nil && i < 3 {
					t.Errorf("row %d lost tier_required_id", i)
				}
			}
			if f.asks > tc.maxAsks {
				t.Errorf("asked monetization %d times for 2 distinct (creator, tier) pairs; want at most %d", f.asks, tc.maxAsks)
			}
		})
	}
}

// A lookup that fails redacts: a paid body never leaks because
// monetization-service was down.
func TestMembersRedactionFailsClosed(t *testing.T) {
	author, tier, viewer := uuid.New(), uuid.New(), uuid.New()
	f := &membersFake{err: errors.New("monetization unreachable"), member: map[uuid.UUID]map[uuid.UUID]bool{viewer: {tier: true}}}
	s := f.svc()
	p := gatedRow(author, tier)
	s.redactForViewer(context.Background(), &viewer, []*postgres.Post{p})
	if !isRedacted(p) {
		t.Fatalf("a failed lookup served the full body: %+v", p)
	}
}

// The real entitlement path (CheckEntitlement against monetization-service),
// end to end over HTTP: a definite yes opens the row; a 5xx, an unreachable
// service and an unset URL all redact.
func TestMembersRedactionOverTheEntitlementService(t *testing.T) {
	author, tier, member, other := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	mon := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/monetization/entitlements" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		ok := r.Header.Get("X-User-Id") == member.String() &&
			r.URL.Query().Get("creator_id") == author.String() &&
			r.URL.Query().Get("tier_id") == tier.String()
		_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"allowed": ok, "reason": "test"}})
	}))
	defer mon.Close()
	broken := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer broken.Close()
	down := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	downURL := down.URL
	down.Close()

	cases := []struct {
		name   string
		url    string
		viewer uuid.UUID
		full   bool
	}{
		{"member", mon.URL, member, true},
		{"non-member", mon.URL, other, false},
		{"monetization 502", broken.URL, member, false},
		{"monetization unreachable", downURL, member, false},
		{"monetization URL unset", "", member, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := &Service{monetizationServiceURL: tc.url, httpClient: http.DefaultClient}
			p := gatedRow(author, tier)
			s.redactForViewer(context.Background(), &tc.viewer, []*postgres.Post{p})
			if tc.full && !isFull(p) {
				t.Fatalf("want full, got %+v", p)
			}
			if !tc.full && !isRedacted(p) {
				t.Fatalf("want redacted, got %+v", p)
			}
		})
	}
}

// gatedDetail is a gated row as the direct read answers it: chapters (here
// as if parsed from the description), credits and the sound on the detail.
func gatedDetail(author, tier uuid.UUID) *PostDetail {
	credits := []CreditView{{ID: uuid.New(), Name: "A. Director", Role: "director"}}
	return &PostDetail{
		Post:     gatedRow(author, tier),
		Chapters: []ChapterRef{{StartMs: 0, Title: "Cold open"}, {StartMs: 60000, Title: "The paid reveal"}},
		Credits:  &credits,
		Sound:    &PostSound{},
	}
}

// The pinned wire shape. A redacted row: tier_required_id, title, cover,
// sound, genres / release_year / production_house and duration_ms kept;
// body_redacted: true; text empty; media, rich_text, poll, hashtags,
// mentions, tags, link_url and every location key absent; chapters and
// credits empty arrays. A member's row: everything, tier_required_id kept,
// no body_redacted and no duration_ms key at all.
func TestRedactedRowWireShape(t *testing.T) {
	author, tier, member, stranger := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	f := &membersFake{member: map[uuid.UUID]map[uuid.UUID]bool{member: {tier: true}}}
	s := f.svc()
	wire := func(viewer uuid.UUID) map[string]any {
		t.Helper()
		d := gatedDetail(author, tier)
		s.RedactDetailForViewer(context.Background(), &viewer, d)
		raw, err := json.Marshal(d)
		if err != nil {
			t.Fatal(err)
		}
		var m map[string]any
		if err := json.Unmarshal(raw, &m); err != nil {
			t.Fatal(err)
		}
		return m
	}
	cleared := []string{"media", "rich_text", "poll", "hashtags", "mentions", "mention_user_ids", "tags", "tagged_user_ids", "link_url", "action",
		"location_name", "location_lat", "location_lng", "location_place_id", "recording_location"}
	kept := []string{"tier_required_id", "title", "cover_media_id", "content_type", "author_id",
		"audio_track_id", "audio_start_ms", "sound", "genres", "release_year", "production_house"}

	red := wire(stranger)
	if red["tier_required_id"] != tier.String() || red["body_redacted"] != true || red["title"] != "Episode 1" {
		t.Fatalf("redacted row: tier/body_redacted/title wrong: %v", red)
	}
	if red["text"] != "" {
		t.Fatalf("redacted row carries text %q", red["text"])
	}
	if red["duration_ms"] != float64(61000) {
		t.Fatalf("redacted row duration_ms = %v, want the longest video's 61000", red["duration_ms"])
	}
	for _, k := range cleared {
		if v, ok := red[k]; ok {
			t.Errorf("redacted row carries %q: %v", k, v)
		}
	}
	for _, k := range []string{"chapters", "credits"} {
		if list, ok := red[k].([]any); !ok || len(list) != 0 {
			t.Errorf("redacted row %q = %v, want an empty array", k, red[k])
		}
	}
	for _, k := range kept {
		if _, ok := red[k]; !ok {
			t.Errorf("redacted row lost %q", k)
		}
	}

	full := wire(member)
	if full["tier_required_id"] != tier.String() || full["text"] != "the paid body #secret @ravi" {
		t.Fatalf("member row: %v", full)
	}
	for _, k := range []string{"body_redacted", "duration_ms"} {
		if v, ok := full[k]; ok {
			t.Errorf("member row carries %q: %v", k, v)
		}
	}
	for _, k := range append(append([]string{}, cleared...), kept...) {
		if _, ok := full[k]; !ok {
			t.Errorf("member row lost %q", k)
		}
	}
	for k, n := range map[string]int{"chapters": 2, "credits": 1} {
		if list, ok := full[k].([]any); !ok || len(list) != n {
			t.Errorf("member row %q = %v, want %d entries", k, full[k], n)
		}
	}
}

// Every row family a list answers, through the helper that family calls.
// The inventory test (members_read_inventory_test.go) proves each list
// reaches one of these; this proves each of them applies the whole rule,
// the detail's chapters and credits included where the family has them.
func TestMembersRedactionEveryRowFamily(t *testing.T) {
	author, tier, member, stranger := uuid.New(), uuid.New(), uuid.New(), uuid.New()

	type family struct {
		name string
		// detail: the family answers details, so chapters and credits are
		// checked too (the thread answers plain rows).
		detail bool
		// run hands the family a gated detail for this viewer and returns
		// it as the family answers it.
		run func(t *testing.T, s *Service, viewer *uuid.UUID, d *PostDetail) *PostDetail
	}
	batchOf := func(s *Service, d *PostDetail) {
		s.batchPostsSeam = func(context.Context, []uuid.UUID, *uuid.UUID) (map[uuid.UUID]*PostDetail, error) {
			return map[uuid.UUID]*PostDetail{d.Post.ID: d}, nil
		}
	}
	families := []family{
		{"batch: POST /v1/posts/batch (every feed-service surface), playlists, watch later", true, func(t *testing.T, s *Service, viewer *uuid.UUID, d *PostDetail) *PostDetail {
			batchOf(s, d)
			out, err := s.GetPostsByIDs(context.Background(), []uuid.UUID{d.Post.ID}, viewer)
			if err != nil {
				t.Fatal(err)
			}
			return out[d.Post.ID]
		}},
		{"continue watching / history (through the batch read)", true, func(t *testing.T, s *Service, viewer *uuid.UUID, d *PostDetail) *PostDetail {
			batchOf(s, d)
			byID, err := s.GetPostsByIDs(context.Background(), []uuid.UUID{d.Post.ID}, viewer)
			if err != nil {
				t.Fatal(err)
			}
			items := attachContinueWatchingPosts([]postgres.WatchProgress{{PostID: d.Post.ID}}, byID)
			if len(items) != 1 {
				t.Fatalf("items = %d", len(items))
			}
			return items[0].Post
		}},
		{"detail pages: by-author, recent, hashtag, trending, bookmarks, live recordings, sound page", true, func(_ *testing.T, s *Service, viewer *uuid.UUID, d *PostDetail) *PostDetail {
			page := []PostDetail{*d}
			s.redactDetailsForViewer(context.Background(), viewer, page)
			return &page[0]
		}},
		{"detail pointers: appearances", true, func(_ *testing.T, s *Service, viewer *uuid.UUID, d *PostDetail) *PostDetail {
			page := []*PostDetail{d, nil}
			s.redactDetailPtrsForViewer(context.Background(), viewer, page)
			return page[0]
		}},
		{"plain rows: thread", false, func(_ *testing.T, s *Service, viewer *uuid.UUID, d *PostDetail) *PostDetail {
			rows := []postgres.Post{*d.Post}
			s.redactPostRowsForViewer(context.Background(), viewer, rows)
			return &PostDetail{Post: &rows[0]}
		}},
		{"direct read: GET /v1/posts/:postId", true, func(_ *testing.T, s *Service, viewer *uuid.UUID, d *PostDetail) *PostDetail {
			s.RedactDetailForViewer(context.Background(), viewer, d)
			return d
		}},
	}
	viewers := []struct {
		name   string
		viewer *uuid.UUID
		full   bool
	}{
		{"signed out", nil, false},
		{"non-member", &stranger, false},
		{"member", &member, true},
		{"author", &author, true},
	}
	for _, fam := range families {
		for _, v := range viewers {
			t.Run(fam.name+"/"+v.name, func(t *testing.T) {
				f := &membersFake{member: map[uuid.UUID]map[uuid.UUID]bool{member: {tier: true}}}
				got := fam.run(t, f.svc(), v.viewer, gatedDetail(author, tier))
				if v.full && !isFull(got.Post) {
					t.Fatalf("want the full row, got %+v", got.Post)
				}
				if !v.full && !isRedacted(got.Post) {
					t.Fatalf("want the redacted row, got %+v", got.Post)
				}
				if got.Post.TierRequiredID == nil || *got.Post.TierRequiredID != tier {
					t.Fatalf("tier_required_id lost: %+v", got.Post)
				}
				if !fam.detail {
					return
				}
				wantChapters, wantCredits := 2, 1
				if !v.full {
					wantChapters, wantCredits = 0, 0
				}
				if got.Chapters == nil || len(got.Chapters) != wantChapters {
					t.Fatalf("chapters = %v, want %d (an array either way)", got.Chapters, wantChapters)
				}
				if got.Credits == nil || len(*got.Credits) != wantCredits {
					t.Fatalf("credits = %v, want %d (an array either way)", got.Credits, wantCredits)
				}
				if got.Sound == nil {
					t.Fatal("the sound reference was dropped; it is public catalogue data")
				}
			})
		}
	}
}

// The comment thread's gate: author and members pass; a non-member, a
// signed-out viewer and a failed lookup are ErrMembersOnly; an open post is
// never gated.
func TestCommentsMembersGate(t *testing.T) {
	author, tier, member, stranger := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	gated, open := uuid.New(), uuid.New()
	newSvc := func(err error) *Service {
		f := &membersFake{err: err, member: map[uuid.UUID]map[uuid.UUID]bool{member: {tier: true}}}
		s := f.svc()
		s.postGateSeam = func(_ context.Context, id uuid.UUID) (uuid.UUID, *uuid.UUID, error) {
			if id == gated {
				return author, &tier, nil
			}
			return author, nil, nil
		}
		return s
	}
	for _, tc := range []struct {
		name   string
		post   uuid.UUID
		viewer *uuid.UUID
		err    error
		want   error
	}{
		{"author", gated, &author, nil, nil},
		{"member", gated, &member, nil, nil},
		{"non-member", gated, &stranger, nil, ErrMembersOnly},
		{"signed out", gated, nil, nil, ErrMembersOnly},
		{"lookup failed", gated, &member, errors.New("down"), ErrMembersOnly},
		{"open post, non-member", open, &stranger, nil, nil},
		{"open post, signed out", open, nil, nil, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := newSvc(tc.err).postMembersGate(context.Background(), tc.post, tc.viewer); !errors.Is(got, tc.want) && got != tc.want {
				t.Fatalf("got %v want %v", got, tc.want)
			}
		})
	}
	// The gate's own lookup failing is an error, never an open thread.
	s := &Service{postGateSeam: func(context.Context, uuid.UUID) (uuid.UUID, *uuid.UUID, error) {
		return uuid.Nil, nil, errors.New("db down")
	}}
	if err := s.postMembersGate(context.Background(), gated, &member); err == nil {
		t.Fatal("an unresolved gate opened the thread")
	}
}

// PostCreated carries the gate (a post gated before its scheduled publish
// announces itself gated, so search never indexes it publicly); an open
// post's event has no tier_required_id key at all.
func TestPostCreatedCarriesTheMembersGate(t *testing.T) {
	svc := &Service{}
	tier := uuid.New()
	gated := svc.buildPostCreatedPayload(context.Background(),
		&postgres.Post{ID: uuid.New(), AuthorID: uuid.New(), ContentType: "post", Visibility: "public", ReviewStatus: "approved", TierRequiredID: &tier},
		nil, 0, 0, 1)
	if gated.TierRequiredID != tier.String() {
		t.Fatalf("tier_required_id = %q, want %s", gated.TierRequiredID, tier)
	}
	open := svc.buildPostCreatedPayload(context.Background(),
		&postgres.Post{ID: uuid.New(), AuthorID: uuid.New(), ContentType: "post", Visibility: "public", ReviewStatus: "approved"},
		nil, 0, 0, 1)
	raw, _ := json.Marshal(open)
	if open.TierRequiredID != "" || strings.Contains(string(raw), "tier_required_id") {
		t.Fatalf("an open post's PostCreated carries a gate: %s", raw)
	}
}

// GET /v1/videos/:videoId on a members-only video: a non-member (signed in
// or not) gets the metadata without playback_url or storage_video_url; a
// member keeps playback_url (storage stays the owner's alone, as before);
// the author keeps both; an unresolved gate or a failed lookup drops
// playback_url.
func TestVideoDetailOmitsPlaybackForNonMembers(t *testing.T) {
	author, tier, member, stranger := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	postID := uuid.New()
	newSvc := func(gateErr, lookupErr error) *Service {
		f := &membersFake{err: lookupErr, member: map[uuid.UUID]map[uuid.UUID]bool{member: {tier: true}}}
		s := f.svc()
		s.authoringOwners = &fakeAuthoringStore{authorID: author, meta: &postgres.VideoMetadata{
			PostID: postID, DurationSeconds: 61,
			StorageVideoURL: strPtr("s3://private/original.mp4"),
			PlaybackURL:     strPtr("/v1/media/x/hls/master.m3u8"),
		}}
		s.readGate = func(context.Context, uuid.UUID, *uuid.UUID) error { return nil }
		s.postGateSeam = func(context.Context, uuid.UUID) (uuid.UUID, *uuid.UUID, error) {
			if gateErr != nil {
				return uuid.Nil, nil, gateErr
			}
			return author, &tier, nil
		}
		return s
	}
	for _, tc := range []struct {
		name               string
		caller             *uuid.UUID
		gateErr, lookupErr error
		playback, storage  bool
	}{
		{"signed out", nil, nil, nil, false, false},
		{"non-member", &stranger, nil, nil, false, false},
		{"member", &member, nil, nil, true, false},
		{"author", &author, nil, nil, true, true},
		{"gate unresolved", &member, errors.New("db down"), nil, false, false},
		{"membership lookup failed", &member, nil, errors.New("monetization down"), false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			vm, err := newSvc(tc.gateErr, tc.lookupErr).GetVideoDetailForCaller(context.Background(), postID, tc.caller)
			if err != nil {
				t.Fatal(err)
			}
			if (vm.PlaybackURL != nil) != tc.playback || (vm.StorageVideoURL != nil) != tc.storage {
				t.Fatalf("playback_url=%v storage_video_url=%v; want playback %v storage %v", vm.PlaybackURL, vm.StorageVideoURL, tc.playback, tc.storage)
			}
			if vm.DurationSeconds != 61 || vm.PostID != postID {
				t.Fatalf("the rest of the metadata changed: %+v", vm)
			}
		})
	}
}
