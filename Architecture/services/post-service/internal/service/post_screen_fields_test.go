package service

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/atpost/post-service/internal/store/postgres"
	"github.com/atpost/post-service/internal/store/scylla"
	"github.com/google/uuid"
)

// Post screens parity (2026-10-18): the details screen's fields and the
// friends audience. Every guard below has a test that failed before it
// existed (see the lane report for the mutation checks).

// ── link_url ────────────────────────────────────────────────────────────

func TestNormalizeLinkURL(t *testing.T) {
	ok := []struct{ in, want string }{
		{"https://example.com", "https://example.com"},
		{"  https://example.com/a?b=c#d  ", "https://example.com/a?b=c#d"},
		{"HTTPS://Example.com/x", "HTTPS://Example.com/x"},
		{"https://example.com:8443/p", "https://example.com:8443/p"},
	}
	for _, tc := range ok {
		got, err := NormalizeLinkURL(tc.in)
		if err != nil || got == nil || *got != tc.want {
			t.Errorf("NormalizeLinkURL(%q) = %v, %v; want %q", tc.in, got, err, tc.want)
		}
	}
	if got, err := NormalizeLinkURL("   "); err != nil || got != nil {
		t.Errorf("blank link = %v, %v; want nil, nil (no link)", got, err)
	}
	bad := []string{
		"http://example.com",
		"javascript:alert(1)",
		"JavaScript://example.com/%0aalert(1)",
		"data:text/html;base64,PHNjcmlwdD4=",
		"ftp://example.com",
		"//example.com",
		"example.com",
		"https:example.com",
		"https://",
		"https://user:pw@example.com",
		"https://bank.example@evil.example",
		"https://exa mple.com",
		"https://example.com/\x00",
		"https://example.com/" + strings.Repeat("a", MaxLinkURLLength),
	}
	for _, in := range bad {
		if got, err := NormalizeLinkURL(in); !errors.Is(err, ErrInvalidLinkURL) {
			t.Errorf("NormalizeLinkURL(%q) = %v, %v; want ErrInvalidLinkURL", in, got, err)
		}
	}
	// Exactly the cap is accepted.
	exact := "https://example.com/" + strings.Repeat("a", MaxLinkURLLength-len("https://example.com/"))
	if _, err := NormalizeLinkURL(exact); err != nil {
		t.Errorf("a %d-character link was refused: %v", len(exact), err)
	}
}

// ── location_place_id ──────────────────────────────────────────────────

func TestNormalizeLocationPlaceID(t *testing.T) {
	s := func(v string) *string { return &v }
	if got, err := NormalizeLocationPlaceID(nil); err != nil || got != nil {
		t.Fatalf("nil = %v, %v", got, err)
	}
	if got, err := NormalizeLocationPlaceID(s("  ")); err != nil || got != nil {
		t.Fatalf("blank = %v, %v", got, err)
	}
	if got, err := NormalizeLocationPlaceID(s(" ChIJLbZ-NFv9DDkRQJY4FbcFcgM ")); err != nil || got == nil || *got != "ChIJLbZ-NFv9DDkRQJY4FbcFcgM" {
		t.Fatalf("google place id = %v, %v", got, err)
	}
	for _, in := range []string{"<script>", "a b", "x;drop", strings.Repeat("a", MaxLocationPlaceIDLength+1)} {
		if _, err := NormalizeLocationPlaceID(s(in)); !errors.Is(err, ErrInvalidLocationPlaceID) {
			t.Errorf("%q: err %v, want ErrInvalidLocationPlaceID", in, err)
		}
	}
}

// ── carousel cover ─────────────────────────────────────────────────────

func TestResolvePostCover(t *testing.T) {
	a, b, c, stranger := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	media := []uuid.UUID{a, b, c}
	idx := func(i int) *int { return &i }

	if got, err := ResolvePostCover("image", media, &b, nil); err != nil || got == nil || *got != b {
		t.Fatalf("a cover on the carousel: %v %v", got, err)
	}
	if _, err := ResolvePostCover("image", media, &stranger, nil); !errors.Is(err, ErrInvalidCover) {
		t.Fatalf("a photo post's cover outside its media: %v, want ErrInvalidCover", err)
	}
	if _, err := ResolvePostCover("IMAGE", media, &stranger, nil); !errors.Is(err, ErrInvalidCover) {
		t.Fatalf("post_type is case-insensitive: %v", err)
	}
	if got, err := ResolvePostCover("image", media, nil, idx(2)); err != nil || got == nil || *got != c {
		t.Fatalf("cover_index 2: %v %v", got, err)
	}
	if got, err := ResolvePostCover("image", media, &a, idx(0)); err != nil || *got != a {
		t.Fatalf("agreeing cover_media_id + cover_index: %v %v", got, err)
	}
	for name, tc := range map[string]struct {
		cover *uuid.UUID
		i     *int
	}{
		"index past the end":    {nil, idx(3)},
		"negative index":        {nil, idx(-1)},
		"index and id disagree": {&a, idx(1)},
	} {
		if _, err := ResolvePostCover("image", media, tc.cover, tc.i); !errors.Is(err, ErrInvalidCover) {
			t.Errorf("%s: %v, want ErrInvalidCover", name, err)
		}
	}
	if _, err := ResolvePostCover("video", media, nil, idx(0)); !errors.Is(err, ErrInvalidCover) {
		t.Fatalf("cover_index on a non-photo post: %v, want ErrInvalidCover", err)
	}
	// Other kinds keep their own rule: a video's cover is an uploaded frame.
	if got, err := ResolvePostCover("video", media, &stranger, nil); err != nil || *got != stranger {
		t.Fatalf("a video's frame cover: %v %v", got, err)
	}
	if got, err := ResolvePostCover("image", media, nil, nil); err != nil || got != nil {
		t.Fatalf("no cover: %v %v", got, err)
	}
}

// ── create refuses each bad field before anything is read or written ─────

func TestCreatePost_RefusesBadDetailFields(t *testing.T) {
	media, stranger := uuid.New(), uuid.New()
	place := "bad place"
	idx := 4
	base := func() *CreatePostInput {
		return &CreatePostInput{AuthorID: uuid.New(), Text: "hello", Visibility: "public",
			PostType: "image", MediaIDs: []uuid.UUID{media}}
	}
	cases := map[string]struct {
		mut  func(*CreatePostInput)
		want error
	}{
		"cover not on the carousel": {func(in *CreatePostInput) { in.CoverMediaID = &stranger }, ErrInvalidCover},
		"cover_index out of range":  {func(in *CreatePostInput) { in.CoverIndex = &idx }, ErrInvalidCover},
		"remix_setting":             {func(in *CreatePostInput) { in.RemixSetting = "deny" }, ErrInvalidRemixSetting},
		"comment_access":            {func(in *CreatePostInput) { in.CommentAccess = "friends_only" }, ErrInvalidCommentAccess},
		"link_url javascript":       {func(in *CreatePostInput) { in.LinkURL = "javascript:alert(1)" }, ErrInvalidLinkURL},
		"link_url http":             {func(in *CreatePostInput) { in.LinkURL = "http://example.com" }, ErrInvalidLinkURL},
		"location_place_id":         {func(in *CreatePostInput) { in.LocationPlaceID = &place }, ErrInvalidLocationPlaceID},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			in := base()
			tc.mut(in)
			// A bare Service: reaching any store would panic, so a pass
			// proves the refusal comes first.
			_, err := (&Service{}).CreatePost(context.Background(), in)
			if !errors.Is(err, tc.want) {
				t.Fatalf("err = %v, want %v", err, tc.want)
			}
		})
	}
}

func TestNormalizeCreateEnum_DefaultsAndLowercases(t *testing.T) {
	if v, err := normalizeCreateEnum("", PostRemixSettings, "allow", ErrInvalidRemixSetting); err != nil || v != "allow" {
		t.Fatalf("empty = %q %v", v, err)
	}
	if v, err := normalizeCreateEnum(" Allow_Audio_Only ", PostRemixSettings, "allow", ErrInvalidRemixSetting); err != nil || v != "allow_audio_only" {
		t.Fatalf("mixed case = %q %v", v, err)
	}
	if v, err := normalizeCreateEnum("NOBODY", PostCommentAccesses, "everyone", ErrInvalidCommentAccess); err != nil || v != "nobody" {
		t.Fatalf("nobody = %q %v", v, err)
	}
}

// ── friends audience ─────────────────────────────────────────────────────

// friendsGraph answers /v1/internal/graph/can (everyone may view everyone)
// and /v1/graph/relationship from edges["viewer|author"] = {follows,
// followed_by}; a pair listed in broken answers 502.
type friendsGraph struct {
	mu     sync.Mutex
	edges  map[string][2]bool
	broken map[string]bool
	asked  map[string]int
}

func newFriendsGraph(t *testing.T) (*friendsGraph, *httptest.Server) {
	t.Helper()
	g := &friendsGraph{edges: map[string][2]bool{}, broken: map[string]bool{}, asked: map[string]int{}}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/internal/graph/can":
			var req canRequest
			_ = json.NewDecoder(r.Body).Decode(&req)
			data := map[string]bool{}
			for _, id := range req.TargetIDs {
				data[id] = true
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"data": data})
		case "/v1/graph/relationship":
			key := r.URL.Query().Get("user_id") + "|" + r.URL.Query().Get("other_id")
			g.mu.Lock()
			g.asked[key]++
			e, broken := g.edges[key], g.broken[key]
			g.mu.Unlock()
			if broken {
				w.WriteHeader(http.StatusBadGateway)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"follows": e[0], "followed_by": e[1]}})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)
	return g, srv
}

func (g *friendsGraph) set(viewer, author uuid.UUID, follows, followedBy bool) {
	g.edges[viewer.String()+"|"+author.String()] = [2]bool{follows, followedBy}
}

func TestBatchVisibilityAllowsEdges_FriendsNeedsBothEdges(t *testing.T) {
	cases := []struct {
		vis   string
		edges followEdges
		want  bool
	}{
		{"friends", followEdges{true, true}, true},
		{"FRIENDS", followEdges{true, true}, true},
		{"friends", followEdges{true, false}, false},
		{"friends", followEdges{false, true}, false},
		{"friends", followEdges{}, false},
		{"followers", followEdges{true, false}, true},
		{"followers", followEdges{false, true}, false},
		{"public", followEdges{}, true},
		{"private", followEdges{true, true}, false},
	}
	for _, tc := range cases {
		if got := batchVisibilityAllowsEdges(tc.vis, tc.edges); got != tc.want {
			t.Errorf("batchVisibilityAllowsEdges(%q, %+v) = %v, want %v", tc.vis, tc.edges, got, tc.want)
		}
	}
	// The old two-argument form never admits a friends post on one edge.
	if batchVisibilityAllows("friends", true) {
		t.Error("batchVisibilityAllows let a one-way follower read a friends post")
	}
}

func TestFollowEdgesOfRestrictedPosts_FriendsAndFollowers(t *testing.T) {
	g, srv := newFriendsGraph(t)
	viewer, mutual, oneWay, broken, public := uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New()
	g.set(viewer, mutual, true, true)
	g.set(viewer, oneWay, true, false)
	g.broken[viewer.String()+"|"+broken.String()] = true
	s := newGatedService(srv.URL)
	posts := []postgres.Post{
		{AuthorID: mutual, Visibility: "friends"},
		{AuthorID: mutual, Visibility: "followers"}, // same author: one lookup
		{AuthorID: oneWay, Visibility: "friends"},
		{AuthorID: broken, Visibility: "friends"},
		{AuthorID: public, Visibility: "public"},
		{AuthorID: viewer, Visibility: "friends"},
	}
	got := s.followEdgesOfRestrictedPosts(context.Background(), posts, &viewer)
	if e := got[mutual]; !e.follows || !e.followedBy {
		t.Errorf("mutual author edges = %+v", e)
	}
	if e := got[oneWay]; !e.follows || e.followedBy {
		t.Errorf("one-way author edges = %+v", e)
	}
	if _, ok := got[broken]; ok {
		t.Error("a failed lookup produced edges; it must fail closed")
	}
	if g.asked[viewer.String()+"|"+mutual.String()] != 1 {
		t.Errorf("mutual author looked up %d times, want 1", g.asked[viewer.String()+"|"+mutual.String()])
	}
	if g.asked[viewer.String()+"|"+public.String()] != 0 || g.asked[viewer.String()+"|"+viewer.String()] != 0 {
		t.Error("looked up a public author or the viewer themself")
	}
	if len(s.followEdgesOfRestrictedPosts(context.Background(), posts, nil)) != 0 {
		t.Error("an anonymous viewer has edges")
	}
}

func TestViewerMayViewPost_FriendsIsMutualOnly(t *testing.T) {
	g, srv := newFriendsGraph(t)
	author, mutual, follower, followedOnly, broken := uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New()
	g.set(mutual, author, true, true)
	g.set(follower, author, true, false)
	g.set(followedOnly, author, false, true)
	g.broken[broken.String()+"|"+author.String()] = true
	s := newGatedService(srv.URL)
	post := &postgres.Post{ID: uuid.New(), AuthorID: author, Visibility: "friends"}
	ctx := context.Background()
	if !s.viewerMayViewPost(ctx, post, &mutual) {
		t.Error("a mutual follow was refused a friends post")
	}
	for name, v := range map[string]*uuid.UUID{"one-way follower": &follower, "followed only": &followedOnly,
		"graph outage": &broken, "anonymous": nil} {
		if s.viewerMayViewPost(ctx, post, v) {
			t.Errorf("%s read a friends post", name)
		}
	}
	if !s.viewerMayViewPost(ctx, post, &author) {
		t.Error("the author was refused their own friends post")
	}
}

func TestEvaluatePostMediaVisibility_Friends(t *testing.T) {
	viewer := uuid.New()
	p := &postgres.Post{ID: uuid.New(), AuthorID: uuid.New(), Visibility: "friends", ReviewStatus: "approved"}
	in := func(f, fb bool) postMediaInputs {
		return postMediaInputs{rel: ViewerRelationship{Follows: f, FollowedBy: fb}, authorVisible: true}
	}
	if !evaluatePostMediaVisibility(viewer, p, in(true, true)) {
		t.Error("a mutual follow cannot play a friends post's media")
	}
	if evaluatePostMediaVisibility(viewer, p, in(true, false)) || evaluatePostMediaVisibility(viewer, p, in(false, true)) {
		t.Error("one edge plays a friends post's media")
	}
}

func TestCanViewThread_FriendsIsMutualOnly(t *testing.T) {
	g, srv := newFriendsGraph(t)
	author, mutual, follower := uuid.New(), uuid.New(), uuid.New()
	g.set(mutual, author, true, true)
	g.set(follower, author, true, false)
	s := newGatedService(srv.URL)
	root := &postgres.Post{ID: uuid.New(), AuthorID: author, Visibility: "friends"}
	if !s.canViewThread(context.Background(), root, &mutual) {
		t.Error("a mutual follow cannot read a friends thread")
	}
	if s.canViewThread(context.Background(), root, &follower) {
		t.Error("a one-way follower read a friends thread")
	}
}

func TestRepostableVisibility(t *testing.T) {
	for v, want := range map[string]bool{"public": true, "followers": true, "unlisted": true, "private": false, "friends": false, "Friends": false} {
		if got := repostableVisibility(v); got != want {
			t.Errorf("repostableVisibility(%q) = %v, want %v", v, got, want)
		}
	}
}

func TestEditAndCreateAcceptFriends(t *testing.T) {
	if !editVisibilityAllowed("friends") || !createVisibilityAllowed(" Friends ") {
		t.Fatal("friends is not an owner-settable visibility")
	}
	if createVisibilityAllowed("staged") || createVisibilityAllowed("close_friends") {
		t.Fatal("a pipeline / retired value is owner-settable")
	}
}

// ── comment_access ───────────────────────────────────────────────────────

func TestCommentAccessAllows(t *testing.T) {
	g, srv := newFriendsGraph(t)
	author, follower, stranger, broken := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	g.set(follower, author, true, false)
	g.broken[broken.String()+"|"+author.String()] = true
	s := newGatedService(srv.URL)
	ctx := context.Background()
	post := func(access string) *postgres.Post {
		return &postgres.Post{ID: uuid.New(), AuthorID: author, CommentAccess: access}
	}
	for _, access := range []string{"", "everyone", "EVERYONE"} {
		if err := s.commentAccessAllows(ctx, post(access), stranger); err != nil {
			t.Errorf("%q refused a stranger: %v", access, err)
		}
	}
	if err := s.commentAccessAllows(ctx, post("followers"), follower); err != nil {
		t.Errorf("followers refused a follower: %v", err)
	}
	if err := s.commentAccessAllows(ctx, post("followers"), author); err != nil {
		t.Errorf("followers refused the author: %v", err)
	}
	if err := s.commentAccessAllows(ctx, post("followers"), stranger); !errors.Is(err, ErrCommentsFollowersOnly) {
		t.Errorf("followers let a stranger comment: %v", err)
	}
	if err := s.commentAccessAllows(ctx, post("followers"), broken); !errors.Is(err, ErrCommentsFollowersOnly) {
		t.Errorf("followers on a graph outage: %v, want fail closed", err)
	}
	for _, access := range []string{"nobody", "something-new"} {
		for _, who := range []uuid.UUID{author, follower, stranger} {
			if err := s.commentAccessAllows(ctx, post(access), who); !errors.Is(err, ErrCommentsDisabled) {
				t.Errorf("%q let %s comment: %v", access, who, err)
			}
		}
	}
}

// Every comment write asks comment_access: top-level, reply and reel
// comment. The three need a database to run end to end, so this pins the
// call in each one's body (same technique as viewer_eligibility_guard_test).
func TestEveryCommentWriteEnforcesCommentAccess(t *testing.T) {
	for file, funcs := range map[string][]string{
		"post.go":            {"CreateCommentPG", "CreateReply"},
		"reel_engagement.go": {"AddReelComment"},
	} {
		src, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		for _, fn := range funcs {
			body := funcBody(string(src), fn)
			if body == "" {
				t.Fatalf("%s: func %s not found", file, fn)
			}
			if !strings.Contains(body, "s.commentAccessAllows(ctx, post,") {
				t.Errorf("%s.%s does not enforce comment_access", file, fn)
			}
		}
	}
}

// funcBody returns the source of method fn on *Service, up to the next
// top-level declaration.
func funcBody(src, fn string) string {
	start := strings.Index(src, ") "+fn+"(ctx context.Context")
	if start < 0 {
		return ""
	}
	rest := src[start:]
	if end := strings.Index(rest, "\nfunc "); end >= 0 {
		return rest[:end]
	}
	return rest
}

// ── counts.shares / counts.saves ─────────────────────────────────────────

func TestOverlayEngagementRows_SharesAndSaves(t *testing.T) {
	a, b := uuid.New(), uuid.New()
	byPost := map[uuid.UUID]*scylla.Counts{a: {Likes: 7, Comments: 99}}
	overlayEngagementRows([]uuid.UUID{a, b}, byPost, map[uuid.UUID]postgres.EngagementCountRow{
		a: {Comments: 3, Shares: 4, Saves: 5},
	})
	if got := *byPost[a]; got != (scylla.Counts{Likes: 7, Comments: 3, Shares: 4, Saves: 5}) {
		t.Fatalf("a = %+v", got)
	}
	if got := byPost[b]; got == nil || *got != (scylla.Counts{}) {
		t.Fatalf("b (no row) = %+v, want zeros", got)
	}
	raw, _ := json.Marshal(byPost[a])
	if string(raw) != `{"likes":7,"comments":3,"shares":4,"saves":5}` {
		t.Fatalf("wire = %s", raw)
	}
}

func TestProfileGridAllows(t *testing.T) {
	yes := func() bool { return true }
	no := func() bool { return false }
	mustNotAsk := func() bool { t.Error("asked the graph for a post that does not need it"); return true }
	cases := []struct {
		vis             string
		follows, mutual func() bool
		want            bool
	}{
		{"public", mustNotAsk, mustNotAsk, true},
		{"unlisted", mustNotAsk, mustNotAsk, true},
		{"followers", yes, mustNotAsk, true},
		{"followers", no, mustNotAsk, false},
		{"friends", mustNotAsk, yes, true},
		{"friends", yes, no, false},
		{"FRIENDS", mustNotAsk, no, false},
		{"private", mustNotAsk, mustNotAsk, false},
		{"close_friends", mustNotAsk, mustNotAsk, false},
	}
	for _, tc := range cases {
		if got := profileGridAllows(tc.vis, tc.follows, tc.mutual); got != tc.want {
			t.Errorf("profileGridAllows(%q) = %v, want %v", tc.vis, got, tc.want)
		}
	}
}

func TestShareRestricted(t *testing.T) {
	cases := []struct {
		vis, share string
		want       bool
	}{
		{"public", "repost", false},
		{"private", "external", true},
		{"followers", "repost", true},
		{"followers", "external", false},
		{"friends", "repost", true},
		{"friends", "quote", true},
		{"friends", "external", false},
	}
	for _, tc := range cases {
		if got := shareRestricted(tc.vis, tc.share); got != tc.want {
			t.Errorf("shareRestricted(%q, %q) = %v, want %v", tc.vis, tc.share, got, tc.want)
		}
	}
}
