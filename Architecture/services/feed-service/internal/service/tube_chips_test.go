package service

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/atpost/feed-service/internal/store/scylla"
	"github.com/google/uuid"
)

// MTube chips and sort (2026-09-27, tube_chips.go / tube_page.go).
//
// Each chip is a viewer-relative filter over the rows the surface would
// have produced anyway, applied before the page is cut so a chip can never
// return a short page while the cursor advances past rows it should have
// kept. `popular` reorders the assembled page by the hydrated view count.
// The tests drive the real GetLongVideoFilteredPage / GetRelatedVideosWithChip
// over the fake timeline from muted_author_window_test.go, the fake
// feedback store, and one stub standing in for post-service, graph-service,
// trust-safety, identity-profile and analytics-service.

// tubeFixture is that stub plus the state a test sets on it.
type tubeFixture struct {
	svc    *Service
	viewer uuid.UUID
	tl     *fakeReelTimeline

	mu sync.Mutex
	// posts is what post-service's viewer-scoped batch answers with.
	posts map[uuid.UUID]HydratedPost
	// history is GET /v1/videos/history, most recent first.
	history []HydratedPost
	// following is graph-service's one-way follow list for the viewer.
	following []uuid.UUID
	// views is analytics-service's display count per post.
	views map[uuid.UUID]int64
	// byAuthor / recent feed the related surface's collection sources and
	// the Tube discovery fill.
	byAuthor []HydratedPost
	recent   []HydratedPost
	// historyErr makes the history read fail, to pin fail-closed.
	historyErr bool
}

func newTubeFixture(t *testing.T) *tubeFixture {
	t.Helper()
	t.Setenv("INTERNAL_SERVICE_KEY", "test-internal")
	f := &tubeFixture{
		viewer: uuid.New(),
		tl:     &fakeReelTimeline{},
		posts:  map[uuid.UUID]HydratedPost{},
		views:  map[uuid.UUID]int64{},
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/v1/internal/graph/blocked-and-muted":
			_ = json.NewEncoder(w).Encode(map[string]any{"user_ids": []string{}})
		case r.URL.Path == "/v1/internal/graph/can":
			var body struct {
				TargetIDs []string `json:"target_ids"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			allowed := map[string]bool{}
			for _, id := range body.TargetIDs {
				allowed[id] = true
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"data": allowed})
		case r.URL.Path == "/v1/internal/keyword-filters":
			_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"keywords": []string{}}})
		case r.URL.Path == "/v1/profiles/batch":
			_ = json.NewEncoder(w).Encode(map[string]any{})
		case strings.HasPrefix(r.URL.Path, "/v1/graph/following/"):
			ids := make([]string, 0, len(f.following))
			for _, id := range f.following {
				ids = append(ids, id.String())
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"data": ids})
		case r.URL.Path == "/v1/videos/history":
			if f.historyErr {
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
			if r.Header.Get("X-User-Id") != f.viewer.String() {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			items := make([]map[string]any, 0, len(f.history))
			for _, p := range f.history {
				items = append(items, map[string]any{"post_id": p.ID.String(), "post": p})
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"data": items})
		case r.URL.Path == "/v1/analytics/internal/content-views":
			var body struct {
				ContentIDs []string `json:"content_ids"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			out := map[string]any{}
			for _, id := range body.ContentIDs {
				pid, _ := uuid.Parse(id)
				out[id] = map[string]any{"views_display": f.views[pid]}
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"data": out})
		case r.URL.Path == "/v1/posts/batch":
			var body struct {
				IDs []string `json:"ids"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			out := map[string]HydratedPost{}
			for _, id := range body.IDs {
				pid, _ := uuid.Parse(id)
				if p, ok := f.posts[pid]; ok {
					out[id] = p
				}
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"data": out})
		case strings.HasPrefix(r.URL.Path, "/v1/posts/by-author/"):
			_ = json.NewEncoder(w).Encode(map[string]any{"data": f.byAuthor})
		case r.URL.Path == "/v1/posts/recent":
			_ = json.NewEncoder(w).Encode(map[string]any{"data": f.recent})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)

	f.svc = New(nil, nil, nil)
	f.svc.postServiceURL = srv.URL
	f.svc.graphURL = srv.URL
	f.svc.trustSafetyURL = srv.URL
	f.svc.profileServiceURL = srv.URL
	f.svc.mediaServiceURL = srv.URL
	f.svc.analyticsServiceURL = srv.URL
	f.svc.windows = f.tl
	f.svc.feedback = newFakeFeedbackStore()
	return f
}

// video registers a long video by `author`, published at `at`, in
// `category`, on both the timeline (in call order, so callers add newest
// first) and post-service's batch.
func (f *tubeFixture) video(author uuid.UUID, at time.Time, category string) HydratedPost {
	f.mu.Lock()
	defer f.mu.Unlock()
	p := HydratedPost{
		ID: uuid.New(), AuthorID: author, ContentType: "long_video",
		Category: category, CreatedAt: at.UTC().Format(time.RFC3339Nano),
	}
	f.posts[p.ID] = p
	f.tl.rows = append(f.tl.rows, scylla.FeedItem{
		PostID: p.ID, AuthorID: author, ContentType: "long_video",
		CreatedAt: at, CursorToken: "tok-" + p.ID.String(),
	})
	return p
}

// post registers a post with post-service only (no timeline row): a seed
// or a collection-source row for the related surface.
func (f *tubeFixture) post(author uuid.UUID, at time.Time, category string) HydratedPost {
	f.mu.Lock()
	defer f.mu.Unlock()
	p := HydratedPost{
		ID: uuid.New(), AuthorID: author, ContentType: "long_video",
		Category: category, CreatedAt: at.UTC().Format(time.RFC3339Nano),
	}
	f.posts[p.ID] = p
	return p
}

func idsOf(posts []HydratedPost) []uuid.UUID {
	out := make([]uuid.UUID, 0, len(posts))
	for _, p := range posts {
		out = append(out, p.ID)
	}
	return out
}

func sameIDs(got []HydratedPost, want ...HydratedPost) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i].ID != want[i].ID {
			return false
		}
	}
	return true
}

// ─── /v1/feed/videos ────────────────────────────────────────────────────

func TestTubeChip_FreshDropsRowsOlderThanSevenDays(t *testing.T) {
	f := newTubeFixture(t)
	author := uuid.New()
	now := time.Now()
	fresh := f.video(author, now.Add(-time.Hour), "")
	old := f.video(author, now.Add(-10*24*time.Hour), "")
	alsoFresh := f.video(author, now.Add(-6*24*time.Hour), "")

	page, _, err := f.svc.GetLongVideoFilteredPage(context.Background(), f.viewer, 10, "", TubeFilter{Chip: TubeChipFresh}, false, false)
	if err != nil {
		t.Fatalf("GetLongVideoFilteredPage: %v", err)
	}
	if !sameIDs(page, fresh, alsoFresh) {
		t.Fatalf("fresh page = %v, want [%s %s] and not the 10-day-old %s", idsOf(page), fresh.ID, alsoFresh.ID, old.ID)
	}
}

func TestTubeChip_SeenReturnsOnlyWatchHistory(t *testing.T) {
	f := newTubeFixture(t)
	author := uuid.New()
	now := time.Now()
	unseen1 := f.video(author, now, "")
	seen := f.video(author, now.Add(-time.Minute), "")
	unseen2 := f.video(author, now.Add(-2*time.Minute), "")
	f.history = []HydratedPost{seen}

	page, _, err := f.svc.GetLongVideoFilteredPage(context.Background(), f.viewer, 10, "", TubeFilter{Chip: TubeChipSeen}, false, false)
	if err != nil {
		t.Fatalf("GetLongVideoFilteredPage: %v", err)
	}
	if !sameIDs(page, seen) {
		t.Fatalf("seen page = %v, want exactly the watched %s (not %s, %s)", idsOf(page), seen.ID, unseen1.ID, unseen2.ID)
	}
}

func TestTubeChip_NewToYouDropsFollowedAndWatchedAuthors(t *testing.T) {
	f := newTubeFixture(t)
	followed, watched, stranger := uuid.New(), uuid.New(), uuid.New()
	now := time.Now()
	f.video(followed, now, "")
	watchedVideo := f.video(watched, now.Add(-time.Minute), "")
	s1 := f.video(stranger, now.Add(-2*time.Minute), "")
	// A second video by the watched author, one the viewer has NOT seen:
	// still excluded, because the chip is about authors, not posts.
	f.video(watched, now.Add(-3*time.Minute), "")
	s2 := f.video(stranger, now.Add(-4*time.Minute), "")
	f.following = []uuid.UUID{followed}
	f.history = []HydratedPost{watchedVideo}

	page, _, err := f.svc.GetLongVideoFilteredPage(context.Background(), f.viewer, 10, "", TubeFilter{Chip: TubeChipNewToYou}, false, false)
	if err != nil {
		t.Fatalf("GetLongVideoFilteredPage: %v", err)
	}
	if !sameIDs(page, s1, s2) {
		t.Fatalf("new_to_you page = %v, want only the stranger's [%s %s]", idsOf(page), s1.ID, s2.ID)
	}
	for _, p := range page {
		if p.AuthorID == followed || p.AuthorID == watched {
			t.Fatalf("post %s by a followed/watched author reached the new_to_you page", p.ID)
		}
	}
}

func TestTubeChip_HistoryFailureFailsClosed(t *testing.T) {
	f := newTubeFixture(t)
	f.video(uuid.New(), time.Now(), "")
	f.historyErr = true
	for _, chip := range []string{TubeChipSeen, TubeChipNewToYou} {
		if page, _, err := f.svc.GetLongVideoFilteredPage(context.Background(), f.viewer, 10, "", TubeFilter{Chip: chip}, false, false); err == nil {
			t.Fatalf("chip %s served %d rows with the watch history unresolved; want an error", chip, len(page))
		}
	}
	// fresh needs nothing from post-service beyond hydration.
	if _, _, err := f.svc.GetLongVideoFilteredPage(context.Background(), f.viewer, 10, "", TubeFilter{Chip: TubeChipFresh}, false, false); err != nil {
		t.Fatalf("fresh must not depend on the history read: %v", err)
	}
}

func TestTubeSort_PopularOrdersByHydratedViewCount(t *testing.T) {
	f := newTubeFixture(t)
	author := uuid.New()
	now := time.Now()
	a := f.video(author, now, "")
	b := f.video(author, now.Add(-time.Minute), "")
	c := f.video(author, now.Add(-2*time.Minute), "")
	f.views[a.ID], f.views[b.ID], f.views[c.ID] = 5, 50, 10

	page, _, err := f.svc.GetLongVideoFilteredPage(context.Background(), f.viewer, 10, "", TubeFilter{Sort: TubeSortPopular}, false, false)
	if err != nil {
		t.Fatalf("GetLongVideoFilteredPage: %v", err)
	}
	if !sameIDs(page, b, c, a) {
		t.Fatalf("popular page = %v, want [b c a] by views 50/10/5", idsOf(page))
	}
	if page[0].ViewCount != 50 || page[1].ViewCount != 10 || page[2].ViewCount != 5 {
		t.Fatalf("view counts on the page = %d %d %d, want 50 10 5", page[0].ViewCount, page[1].ViewCount, page[2].ViewCount)
	}

	// recent leaves the surface's own order alone.
	page, _, err = f.svc.GetLongVideoFilteredPage(context.Background(), f.viewer, 10, "", TubeFilter{Sort: TubeSortRecent, Chip: TubeChipFresh}, false, false)
	if err != nil {
		t.Fatalf("recent: %v", err)
	}
	if !sameIDs(page, a, b, c) {
		t.Fatalf("recent page = %v, want the timeline order [a b c]", idsOf(page))
	}
}

func TestTubeChip_ComposesWithCategory(t *testing.T) {
	f := newTubeFixture(t)
	author := uuid.New()
	now := time.Now()
	musicFresh := f.video(author, now, "music")
	f.video(author, now.Add(-10*24*time.Hour), "music") // right category, too old
	f.video(author, now.Add(-time.Minute), "tech")      // fresh, wrong category

	page, _, err := f.svc.GetLongVideoFilteredPage(context.Background(), f.viewer, 10, "", TubeFilter{Category: "music", Chip: TubeChipFresh}, false, false)
	if err != nil {
		t.Fatalf("GetLongVideoFilteredPage: %v", err)
	}
	if !sameIDs(page, musicFresh) {
		t.Fatalf("category+chip page = %v, want exactly the fresh music video %s", idsOf(page), musicFresh.ID)
	}

	// The category-only surface still goes through the same path and
	// keeps its meaning: both music videos, whatever their age.
	page, _, err = f.svc.GetLongVideoCategoryPage(context.Background(), f.viewer, 10, "", "music", false, false)
	if err != nil {
		t.Fatalf("GetLongVideoCategoryPage: %v", err)
	}
	if len(page) != 2 {
		t.Fatalf("category-only page has %d rows, want both music videos", len(page))
	}
}

func TestTubeChip_DiscoveryFillPassesTheSameChip(t *testing.T) {
	f := newTubeFixture(t)
	followed, stranger := uuid.New(), uuid.New()
	f.following = []uuid.UUID{followed}
	// The viewer's timeline is empty, so the first page is all fill. The
	// fill offers a followed author's video and a stranger's; new_to_you
	// must drop the former even though it arrived through the fill.
	fromFollowed := f.post(followed, time.Now(), "")
	fromStranger := f.post(stranger, time.Now(), "")
	f.recent = []HydratedPost{fromFollowed, fromStranger}

	page, _, err := f.svc.GetLongVideoFilteredPage(context.Background(), f.viewer, 10, "", TubeFilter{Chip: TubeChipNewToYou}, false, false)
	if err != nil {
		t.Fatalf("GetLongVideoFilteredPage: %v", err)
	}
	if !sameIDs(page, fromStranger) {
		t.Fatalf("filled new_to_you page = %v, want only the stranger's %s", idsOf(page), fromStranger.ID)
	}
}

// ─── /v1/feed/videos/:postId/related ────────────────────────────────────

func TestRelatedChip_TopicKeepsOnlyThatCategory(t *testing.T) {
	f := newTubeFixture(t)
	creator := uuid.New()
	seed := f.post(creator, time.Now(), "music")
	music := f.post(creator, time.Now(), "music")
	tech := f.post(creator, time.Now(), "tech")
	f.byAuthor = []HydratedPost{seed, music, tech}

	page, _, err := f.svc.GetRelatedVideosWithChip(context.Background(), f.viewer, seed.ID, 10, 0, RelatedChip{Kind: RelatedChipTopic, Topic: "music"})
	if err != nil {
		t.Fatalf("GetRelatedVideosWithChip: %v", err)
	}
	if !sameIDs(page, music) {
		t.Fatalf("topic:music related = %v, want exactly %s (not the tech video %s)", idsOf(page), music.ID, tech.ID)
	}
}

func TestRelatedChip_FreshDropsOldRows(t *testing.T) {
	f := newTubeFixture(t)
	creator := uuid.New()
	seed := f.post(creator, time.Now(), "")
	fresh := f.post(creator, time.Now().Add(-time.Hour), "")
	old := f.post(creator, time.Now().Add(-30*24*time.Hour), "")
	f.byAuthor = []HydratedPost{seed, fresh, old}

	page, _, err := f.svc.GetRelatedVideosWithChip(context.Background(), f.viewer, seed.ID, 10, 0, RelatedChip{Kind: TubeChipFresh})
	if err != nil {
		t.Fatalf("GetRelatedVideosWithChip: %v", err)
	}
	if !sameIDs(page, fresh) {
		t.Fatalf("fresh related = %v, want exactly %s (not the month-old %s)", idsOf(page), fresh.ID, old.ID)
	}
}

func TestRelatedChip_SeenReturnsOnlyHistory(t *testing.T) {
	f := newTubeFixture(t)
	creator := uuid.New()
	seed := f.post(creator, time.Now(), "")
	watched := f.post(creator, time.Now(), "")
	unwatched := f.post(creator, time.Now(), "")
	f.byAuthor = []HydratedPost{seed, watched, unwatched}
	f.history = []HydratedPost{watched, seed} // the seed itself is in the history and must still not be offered

	page, _, err := f.svc.GetRelatedVideosWithChip(context.Background(), f.viewer, seed.ID, 10, 0, RelatedChip{Kind: TubeChipSeen})
	if err != nil {
		t.Fatalf("GetRelatedVideosWithChip: %v", err)
	}
	if !sameIDs(page, watched) {
		t.Fatalf("seen related = %v, want exactly %s (not %s, never the seed %s)", idsOf(page), watched.ID, unwatched.ID, seed.ID)
	}
}

func TestRelatedChip_NoChipIsTheOriginalSurface(t *testing.T) {
	f := newTubeFixture(t)
	creator := uuid.New()
	seed := f.post(creator, time.Now(), "")
	other := f.post(creator, time.Now().Add(-30*24*time.Hour), "")
	f.byAuthor = []HydratedPost{seed, other}
	page, _, err := f.svc.GetRelatedVideos(context.Background(), f.viewer, seed.ID, 10, 0)
	if err != nil {
		t.Fatalf("GetRelatedVideos: %v", err)
	}
	if !sameIDs(page, other) {
		t.Fatalf("unchipped related = %v, want %s", idsOf(page), other.ID)
	}
}

// ─── The parsers ────────────────────────────────────────────────────────

func TestNormalizeRelatedChip(t *testing.T) {
	good := map[string]RelatedChip{
		"":               {},
		"fresh":          {Kind: "fresh"},
		"SEEN":           {Kind: "seen"},
		"topic:music":    {Kind: "topic", Topic: "music"},
		"topic:Hip-Hop":  {Kind: "topic", Topic: "hip-hop"},
		"topic: k-pop ":  {Kind: "topic", Topic: "k-pop"},
		" topic:gaming ": {Kind: "topic", Topic: "gaming"},
	}
	for in, want := range good {
		got, ok := NormalizeRelatedChip(in)
		if !ok || got != want {
			t.Errorf("NormalizeRelatedChip(%q) = %+v ok=%v, want %+v", in, got, ok, want)
		}
	}
	for _, bad := range []string{"new_to_you", "topic:", "topic:a", "topic:hip_hop", "topic:" + strings.Repeat("x", 41), "topic:mu sic", "viral"} {
		if got, ok := NormalizeRelatedChip(bad); ok {
			t.Errorf("NormalizeRelatedChip(%q) accepted as %+v", bad, got)
		}
	}
}

func TestChipScopeKeep_IsPureOverTheRow(t *testing.T) {
	now := time.Now()
	a, b := uuid.New(), uuid.New()
	sc := &chipScope{
		now:             now,
		seenPosts:       map[uuid.UUID]struct{}{a: {}},
		excludedAuthors: map[uuid.UUID]struct{}{b: {}},
	}
	item := func(post, author uuid.UUID, age time.Duration) FeedItem {
		return FeedItem{PostID: post, AuthorID: author, CreatedAt: now.Add(-age)}
	}
	if !sc.keep(TubeChipFresh, item(a, a, 6*24*time.Hour)) || sc.keep(TubeChipFresh, item(a, a, 8*24*time.Hour)) {
		t.Fatal("fresh is the last seven days, inclusive")
	}
	if !sc.keep(TubeChipSeen, item(a, b, 0)) || sc.keep(TubeChipSeen, item(b, a, 0)) {
		t.Fatal("seen keeps history posts only")
	}
	if sc.keep(TubeChipNewToYou, item(a, b, 0)) || !sc.keep(TubeChipNewToYou, item(a, a, 0)) {
		t.Fatal("new_to_you drops excluded authors only")
	}
	if !sc.keep("", item(b, b, 0)) {
		t.Fatal("no chip keeps everything")
	}
	if got := filterItemsByChip(nil, TubeChipFresh, sc); got == nil || len(got) != 0 {
		t.Fatalf("filtering nothing = %v, want an empty slice", got)
	}
}

// The history walk stops at the page cap so a viewer with an endless
// history cannot make one request walk it all; a decode failure is an
// error, never an empty (and therefore silently unfiltered) set.
func TestFetchWatchHistory_PagesAndStops(t *testing.T) {
	pages := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		pages++
		if r.URL.Path != "/v1/videos/history" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		items := []map[string]any{}
		for i := 0; i < watchHistoryPageSize; i++ {
			items = append(items, map[string]any{"post_id": uuid.New().String(), "post": map[string]any{"author_id": uuid.New().String()}})
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"data": items, "meta": map[string]any{"next_cursor": "more"}})
	}))
	t.Cleanup(srv.Close)
	s := New(nil, nil, nil)
	s.postServiceURL = srv.URL
	rows, err := s.fetchWatchHistory(context.Background(), uuid.New())
	if err != nil {
		t.Fatalf("fetchWatchHistory: %v", err)
	}
	if pages != watchHistoryMaxPages || len(rows) != watchHistoryMaxPages*watchHistoryPageSize {
		t.Fatalf("walked %d pages for %d rows, want %d pages", pages, len(rows), watchHistoryMaxPages)
	}
	for _, r := range rows {
		if r.AuthorID == uuid.Nil {
			t.Fatal("author id was not read off the hydrated post")
		}
	}

	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("not json"))
	}))
	t.Cleanup(bad.Close)
	s.postServiceURL = bad.URL
	if _, err := s.fetchWatchHistory(context.Background(), uuid.New()); err == nil {
		t.Fatal("an undecodable history must be an error, not an empty set")
	}
}
