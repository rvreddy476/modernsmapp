package service

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

// Post screens parity (2026-10-18): the friends audience on the feed, and
// the details-screen fields and counts on every feed row.

// friendsFanoutStubs is fanoutStubs plus the author's following list.
func friendsFanoutStubs(t *testing.T, followers, following, connections, subscribers []uuid.UUID, failFollowing bool) (*Service, *recordingTimelines) {
	t.Helper()
	s, tl := fanoutStubs(t, followers, connections, subscribers, false)
	encode := func(w http.ResponseWriter, ids []uuid.UUID) {
		out := make([]string, 0, len(ids))
		for _, id := range ids {
			out = append(out, id.String())
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"data": out})
	}
	graph := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.Contains(r.URL.Path, "/followers/"):
			encode(w, followers)
		case strings.Contains(r.URL.Path, "/following/"):
			if failFollowing {
				w.WriteHeader(http.StatusBadGateway)
				return
			}
			encode(w, following)
		case strings.Contains(r.URL.Path, "/connections/"):
			encode(w, connections)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(graph.Close)
	s.graphURL = graph.URL
	return s, tl
}

func TestFanoutPost_FriendsReachesOnlyMutualFollows(t *testing.T) {
	author, mutual, oneWay, followedOnly, connection, subscriber, channel :=
		uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New()
	s, tl := friendsFanoutStubs(t,
		[]uuid.UUID{mutual, oneWay},       // follow the author
		[]uuid.UUID{mutual, followedOnly}, // the author follows
		[]uuid.UUID{connection}, []uuid.UUID{subscriber}, false)

	if err := s.FanoutPost(context.Background(), uuid.New(), author, time.Now(), "long_video", "friends", channel); err != nil {
		t.Fatal(err)
	}
	if got := tl.rows(mutual); got != 1 {
		t.Fatalf("mutual follow got %d rows, want 1", got)
	}
	for who, id := range map[string]uuid.UUID{"one-way follower": oneWay, "followed only": followedOnly,
		"connection": connection, "subscriber": subscriber} {
		if got := tl.rows(id); got != 0 {
			t.Errorf("%s got %d rows of a friends post, want 0", who, got)
		}
	}
	if got := tl.rows(author); got != 1 {
		t.Fatalf("author got %d own rows, want 1", got)
	}
}

// No following list, no rows for anyone else, and the event is retried.
func TestFanoutPost_FriendsFailsClosedWithoutTheFollowingList(t *testing.T) {
	author, mutual := uuid.New(), uuid.New()
	s, tl := friendsFanoutStubs(t, []uuid.UUID{mutual}, []uuid.UUID{mutual}, nil, nil, true)
	err := s.FanoutPost(context.Background(), uuid.New(), author, time.Now(), "post", "friends", uuid.Nil)
	if err == nil {
		t.Fatal("a friends fan-out without the following list reported success")
	}
	if got := tl.rows(mutual); got != 0 {
		t.Fatalf("follower got %d rows while the audience was unknown", got)
	}
}

func TestMutualRecipients(t *testing.T) {
	a, b, c := uuid.New(), uuid.New(), uuid.New()
	got := mutualRecipients([]uuid.UUID{a, b}, []uuid.UUID{b, c})
	if len(got) != 1 {
		t.Fatalf("got %v", got)
	}
	if _, ok := got[b]; !ok {
		t.Fatalf("got %v, want only %s", got, b)
	}
	if len(mutualRecipients(nil, []uuid.UUID{a})) != 0 || len(mutualRecipients([]uuid.UUID{a}, nil)) != 0 {
		t.Fatal("an empty side produced recipients")
	}
}

// A friends row is listable (post-service's batch decided for this viewer);
// the hydration tail must not drop it the way it drops unlisted.
func TestListableVisibility_Friends(t *testing.T) {
	for _, v := range []string{"friends", "FRIENDS", " friends "} {
		if !listableVisibility(v) {
			t.Errorf("%q is not listable", v)
		}
	}
	author, viewer := uuid.New(), uuid.New()
	out := applyListingVisibilityFilter(viewer, []HydratedPost{{ID: uuid.New(), AuthorID: author, Visibility: "friends"}})
	if len(out) != 1 {
		t.Fatal("the hydration tail dropped a friends post post-service allowed")
	}
}

// ── feed rows: the details-screen fields and four counts ────────────────

func carouselFeedRow() HydratedPost {
	link, place, loc := "https://example.com/sunday-market", "ChIJLbZ-NFv9DDkRQJY4FbcFcgM", "Banjara Hills, Hyderabad"
	lat, lng := 17.4126, 78.4387
	cover := uuid.MustParse("c4444444-4444-4444-8444-444444444444")
	username := "raghu"
	return HydratedPost{
		ID: uuid.MustParse("c1111111-1111-4111-8111-111111111111"), AuthorID: uuid.MustParse("c2222222-2222-4222-8222-222222222222"),
		Text: "Sunday market haul #food @call.b", Visibility: "friends", ContentType: "post", PostType: "image", AppOrigin: "postbook",
		ShareToPostbook: true, CoverMediaID: &cover, CreatedAt: "2026-10-18T12:00:00Z", UpdatedAt: "2026-10-18T12:00:00Z",
		Media: []HydratedMedia{
			{MediaID: uuid.MustParse("c3333333-3333-4333-8333-333333333333"), Kind: "image", Position: 0, AltText: "Stalls under string lights", Status: "ready", ProcessingStatus: "ready", ModerationStatus: "passed"},
			{MediaID: cover, Kind: "image", Position: 1, AltText: "A basket of mangoes", Status: "ready", ProcessingStatus: "ready", ModerationStatus: "passed"},
			{MediaID: uuid.MustParse("c5555555-5555-4555-8555-555555555555"), Kind: "image", Position: 2, AltDecorative: true, Status: "ready", ProcessingStatus: "ready", ModerationStatus: "passed"},
		},
		Counts:    json.RawMessage(`{"likes":128,"comments":14,"shares":6,"saves":21}`),
		ViewCount: 2048, Hashtags: json.RawMessage(`["food","market"]`), Mentions: json.RawMessage(`["call.b"]`),
		Title: "Sunday market", RemixSetting: "disallow", AllowDownload: true,
		LocationName: &loc, LocationLat: &lat, LocationLng: &lng,
		Language: "en", PaidPromotion: false, IsBranded: true, AlteredContent: false, AIGenerated: true,
		CommentAccess: "followers", LinkURL: &link, LocationPlaceID: &place,
		FeedContentType: "post", Author: Author{ID: uuid.MustParse("c2222222-2222-4222-8222-222222222222"), DisplayName: "Raghu", Username: &username},
		Reason: "following", ReasonText: "You follow Raghu",
	}
}

// Golden feed row for the Android lane:
//
//	UPDATE_CONTRACTS=1 go test ./internal/service -run TestPostScreensFeedRowContract
func TestPostScreensFeedRowContract(t *testing.T) {
	path := filepath.Join("testdata", "contracts", "posts", "feed_row_photo_carousel.json")
	got, err := json.MarshalIndent(carouselFeedRow(), "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	got = append(got, '\n')
	if os.Getenv("UPDATE_CONTRACTS") == "1" {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, got, 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read fixture: %v (UPDATE_CONTRACTS=1 to generate)", err)
	}
	if !bytes.Equal(bytes.ReplaceAll(want, []byte("\r\n"), []byte("\n")), got) {
		t.Fatalf("fixture is stale.\n--- want\n%s\n--- got\n%s", want, got)
	}
}

// post-service's own post fixture decodes into a feed row with every new
// field intact: nothing is dropped between the batch answer and the feed.
func TestHydratedPost_KeepsPostServiceDetailFields(t *testing.T) {
	path := filepath.Join("..", "..", "..", "post-service", "internal", "http", "testdata", "contracts", "posts", "post_detail.json")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Skipf("post-service fixture not in this checkout: %v", err)
	}
	var row HydratedPost
	if err := json.Unmarshal(raw, &row); err != nil {
		t.Fatal(err)
	}
	if row.Visibility != "friends" || !row.IsBranded || !row.AIGenerated || row.PaidPromotion || row.AlteredContent ||
		row.Language != "en" || row.CommentAccess != "followers" || row.RemixSetting != "disallow" ||
		row.LinkURL == nil || *row.LinkURL != "https://example.com/sunday-market" ||
		row.LocationPlaceID == nil || *row.LocationPlaceID != "ChIJLbZ-NFv9DDkRQJY4FbcFcgM" || len(row.Media) != 3 {
		t.Fatalf("row = %+v", row)
	}
	var counts map[string]int64
	if err := json.Unmarshal(row.Counts, &counts); err != nil {
		t.Fatal(err)
	}
	if counts["likes"] != 128 || counts["comments"] != 14 || counts["shares"] != 6 || counts["saves"] != 21 {
		t.Fatalf("counts = %v", counts)
	}
	out, _ := json.Marshal(row)
	for _, k := range []string{`"is_branded":true`, `"ai_generated":true`, `"paid_promotion":false`, `"altered_content":false`,
		`"link_url":"https://example.com/sunday-market"`, `"comment_access":"followers"`, `"language":"en"`, `"shares":6`, `"saves":21`} {
		if !strings.Contains(string(out), k) {
			t.Errorf("re-emitted row lacks %s", k)
		}
	}
}

// The disclosure switches are on every row, even false; a row without a
// link or place id gains no key for them.
func TestHydratedPost_DisclosureAlwaysEmitted(t *testing.T) {
	out, _ := json.Marshal(HydratedPost{ID: uuid.New()})
	for _, k := range []string{`"paid_promotion":false`, `"is_branded":false`, `"altered_content":false`, `"ai_generated":false`} {
		if !strings.Contains(string(out), k) {
			t.Errorf("row lacks %s", k)
		}
	}
	for _, k := range []string{`"link_url"`, `"location_place_id"`, `"comment_access"`} {
		if strings.Contains(string(out), k) {
			t.Errorf("an unset row carries %s", k)
		}
	}
}
