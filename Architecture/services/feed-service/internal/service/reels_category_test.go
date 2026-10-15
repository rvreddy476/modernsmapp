package service

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/atpost/feed-service/internal/store/scylla"
	"github.com/google/uuid"
)

// Reels category chips (TikTok parity T-18, reels_category.go). The tests
// drive the real GetReelCategoryPage over the fake timeline from
// muted_author_window_test.go, the fake feedback store, and one stub
// standing in for post-service, graph-service, trust-safety,
// identity-profile and analytics-service.

type reelCategoryFixture struct {
	svc    *Service
	viewer uuid.UUID
	tl     *fakeReelTimeline
	fb     *fakeFeedbackStore

	mu sync.Mutex
	// posts is what post-service's viewer-scoped batch answers with.
	posts map[uuid.UUID]HydratedPost
	// recent is post-service's recent-public list (the discovery fill
	// source). It honours ?category= unless ignoreRecentCategory is set,
	// which stands in for a stale row the re-check must catch.
	recent               []HydratedPost
	ignoreRecentCategory bool
	recentQueries        []url.Values
	// blocked is graph-service's blocked-and-muted list for the viewer.
	blocked []uuid.UUID
	// following is graph-service's one-way follow list for the viewer.
	following []uuid.UUID
}

func newReelCategoryFixture(t *testing.T) *reelCategoryFixture {
	t.Helper()
	t.Setenv("INTERNAL_SERVICE_KEY", "test-internal")
	f := &reelCategoryFixture{
		viewer: uuid.New(),
		tl:     &fakeReelTimeline{},
		fb:     newFakeFeedbackStore(),
		posts:  map[uuid.UUID]HydratedPost{},
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/v1/internal/graph/blocked-and-muted":
			ids := make([]string, 0, len(f.blocked))
			for _, id := range f.blocked {
				ids = append(ids, id.String())
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"user_ids": ids})
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
		case r.URL.Path == "/v1/analytics/internal/content-views":
			_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{}})
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
		case r.URL.Path == "/v1/posts/recent":
			q := r.URL.Query()
			f.recentQueries = append(f.recentQueries, q)
			out := make([]HydratedPost, 0, len(f.recent))
			for _, p := range f.recent {
				if cat := q.Get("category"); cat != "" && !f.ignoreRecentCategory && p.Category != cat {
					continue
				}
				out = append(out, p)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"data": out})
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
	f.svc.feedback = f.fb
	return f
}

// reel registers a reel by `author`, published at `at`, in `category`, on
// both the timeline (in call order, so callers add newest first) and
// post-service's batch.
func (f *reelCategoryFixture) reel(author uuid.UUID, at time.Time, category string) HydratedPost {
	f.mu.Lock()
	defer f.mu.Unlock()
	p := f.postLocked(author, at, "reel", category)
	f.tl.rows = append(f.tl.rows, scylla.FeedItem{
		PostID: p.ID, AuthorID: author, ContentType: "reel",
		CreatedAt: at, CursorToken: "tok-" + p.ID.String(),
	})
	return p
}

// public registers a post with post-service only (no timeline row) and
// offers it on the recent-public list: a discovery-fill candidate.
func (f *reelCategoryFixture) public(author uuid.UUID, at time.Time, contentType, category string) HydratedPost {
	f.mu.Lock()
	defer f.mu.Unlock()
	p := f.postLocked(author, at, contentType, category)
	f.recent = append(f.recent, p)
	return p
}

func (f *reelCategoryFixture) postLocked(author uuid.UUID, at time.Time, contentType, category string) HydratedPost {
	p := HydratedPost{
		ID: uuid.New(), AuthorID: author, ContentType: contentType,
		Category: category, CreatedAt: at.UTC().Format(time.RFC3339Nano),
	}
	f.posts[p.ID] = p
	return p
}

func (f *reelCategoryFixture) queries() []url.Values {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]url.Values(nil), f.recentQueries...)
}

// ─── ranked (timeline) path ─────────────────────────────────────────────

func TestReelCategory_TimelineKeepsOnlyThatCategory(t *testing.T) {
	f := newReelCategoryFixture(t)
	a := uuid.New()
	now := time.Now()
	m1 := f.reel(a, now, "music")
	f.reel(a, now.Add(-1*time.Minute), "comedy")
	m2 := f.reel(a, now.Add(-2*time.Minute), "music")
	f.reel(a, now.Add(-3*time.Minute), "")
	m3 := f.reel(a, now.Add(-4*time.Minute), "music")

	page, next, err := f.svc.GetReelCategoryPage(context.Background(), f.viewer, 10, "", "music", false)
	if err != nil {
		t.Fatalf("GetReelCategoryPage: %v", err)
	}
	if !sameIDs(page, m1, m2, m3) {
		t.Fatalf("page = %v, want the three music reels newest first", idsOf(page))
	}
	if next != "" {
		t.Fatalf("next = %q, want none: the timeline is exhausted", next)
	}
	for _, p := range page {
		if p.Score <= 0 {
			t.Fatalf("row %s carries no recency score; the plain reels page scores every row", p.ID)
		}
	}
}

func TestReelCategory_BlockedAndDontRecommendAuthorsExcluded(t *testing.T) {
	f := newReelCategoryFixture(t)
	ok, blocked, muted := uuid.New(), uuid.New(), uuid.New()
	f.blocked = []uuid.UUID{blocked}
	f.fb.mute(f.viewer, muted) // "Don't recommend this account"
	now := time.Now()
	f.reel(blocked, now, "music")
	want := f.reel(ok, now.Add(-time.Minute), "music")
	f.reel(muted, now.Add(-2*time.Minute), "music")
	// The same authors offer music reels through the discovery fill too.
	f.public(blocked, now, "reel", "music")
	f.public(muted, now, "flick", "music")

	page, _, err := f.svc.GetReelCategoryPage(context.Background(), f.viewer, 10, "", "music", false)
	if err != nil {
		t.Fatalf("GetReelCategoryPage: %v", err)
	}
	if !sameIDs(page, want) {
		t.Fatalf("page = %v, want only %s: blocked and don't-recommend authors never appear, timeline or fill", idsOf(page), want.ID)
	}
}

func TestReelCategory_BlockLookupFailureFailsClosed(t *testing.T) {
	f := newReelCategoryFixture(t)
	f.reel(uuid.New(), time.Now(), "music")
	f.svc.graphURL = "http://127.0.0.1:1" // nothing listens: block state unresolved
	if _, _, err := f.svc.GetReelCategoryPage(context.Background(), f.viewer, 10, "", "music", false); err == nil {
		t.Fatal("an unresolved block set must be an error, never an unfiltered category page")
	}
}

// ─── cursor continuity ──────────────────────────────────────────────────

func TestReelCategory_CursorWalksEveryMatchOnceInOrder(t *testing.T) {
	f := newReelCategoryFixture(t)
	a := uuid.New()
	now := time.Now()
	cats := []string{"music", "comedy", "comedy", "music", "music", "comedy", "comedy", "comedy", "music", "dance", "music", "comedy", "music"}
	var want []uuid.UUID
	for i, c := range cats {
		p := f.reel(a, now.Add(-time.Duration(i)*time.Minute), c)
		if c == "music" {
			want = append(want, p.ID)
		}
	}

	var got []uuid.UUID
	cursor := ""
	for pages := 0; ; pages++ {
		if pages > len(cats) {
			t.Fatal("the cursor never terminated")
		}
		page, next, err := f.svc.GetReelCategoryPage(context.Background(), f.viewer, 2, cursor, "music", false)
		if err != nil {
			t.Fatalf("page %d: %v", pages, err)
		}
		if len(page) == 0 && next != "" {
			t.Fatalf("page %d is empty although later pages hold music reels", pages)
		}
		if len(page) > 2 {
			t.Fatalf("page %d has %d rows, limit is 2", pages, len(page))
		}
		for _, p := range page {
			if p.Category != "music" {
				t.Fatalf("page %d carries a %q reel", pages, p.Category)
			}
			got = append(got, p.ID)
		}
		if next == "" {
			break
		}
		cursor = next
	}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("walked %v, want every music reel exactly once in timeline order %v", got, want)
	}
}

func TestReelCategory_LongForeignRunStillYieldsAMatch(t *testing.T) {
	// More other-category reels than the usual window budget (5 windows of
	// `limit`) before the first match: the extended while-empty budget
	// must reach it rather than hand back an empty page with a cursor.
	f := newReelCategoryFixture(t)
	a := uuid.New()
	now := time.Now()
	for i := 0; i < 5*maxCategoryWindows; i++ {
		f.reel(a, now.Add(-time.Duration(i)*time.Minute), "comedy")
	}
	want := f.reel(a, now.Add(-time.Hour), "music")

	page, _, err := f.svc.GetReelCategoryPage(context.Background(), f.viewer, 2, "", "music", false)
	if err != nil {
		t.Fatalf("GetReelCategoryPage: %v", err)
	}
	if !sameIDs(page, want) {
		t.Fatalf("page = %v, want the music reel behind the comedy run", idsOf(page))
	}
}

func TestCollectCategoryPageWithin_ExtendedBudgetOnlyWhileEmpty(t *testing.T) {
	ctx := context.Background()
	// 12 foreign rows, then two matches, then more foreign rows.
	cats := make([]string, 0, 30)
	for i := 0; i < 12; i++ {
		cats = append(cats, "x")
	}
	cats = append(cats, "m", "x", "x", "x", "x", "x", "x", "x", "x", "x", "x", "m")
	ft := newFakeTimeline(cats...)

	// Usual budget (5 windows of 2 = 10 rows): empty page with a cursor.
	page, err := collectCategoryPage(ctx, 2, "", "m", ft.fetch, ft.hydrate)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Posts) != 0 || page.Next == "" {
		t.Fatalf("usual budget: %d rows, next %q; want an empty page that still carries a cursor", len(page.Posts), page.Next)
	}

	// Extended while-empty budget: scans on to the first match, then stops
	// at the usual budget instead of hunting for the second.
	page, err = collectCategoryPageWithin(ctx, 2, "", "m", ft.fetch, ft.hydrate, 5, 15)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Posts) != 1 || page.Exhausted {
		t.Fatalf("extended budget: %d rows (exhausted %v), want the first match and a live cursor", len(page.Posts), page.Exhausted)
	}
	rest, err := collectCategoryPageWithin(ctx, 2, page.Next, "m", ft.fetch, ft.hydrate, 5, 15)
	if err != nil {
		t.Fatal(err)
	}
	if len(rest.Posts) != 1 || rest.Posts[0].ID == page.Posts[0].ID {
		t.Fatalf("continuation: %d rows, want the second match exactly once", len(rest.Posts))
	}
}

// ─── discovery fill ─────────────────────────────────────────────────────

func TestReelCategory_FillIsNarrowedToCategoryAndShortForm(t *testing.T) {
	f := newReelCategoryFixture(t)
	f.ignoreRecentCategory = true // a stale row reaches the fill; the re-check must drop it
	now := time.Now()
	stranger := uuid.New()
	want := f.public(stranger, now, "reel", "music")
	f.public(stranger, now, "reel", "comedy")      // other category
	f.public(stranger, now, "long_video", "music") // right category, not short-form
	f.public(stranger, now, "post", "music")       // not a video at all

	page, next, err := f.svc.GetReelCategoryPage(context.Background(), f.viewer, 10, "", "music", false)
	if err != nil {
		t.Fatalf("GetReelCategoryPage: %v", err)
	}
	if !sameIDs(page, want) {
		t.Fatalf("filled page = %v, want only the music reel %s", idsOf(page), want.ID)
	}
	if next != "" {
		t.Fatalf("next = %q; a filled first page has no timeline left to continue", next)
	}
	qs := f.queries()
	if len(qs) != 1 {
		t.Fatalf("recent-public was asked %d times, want once", len(qs))
	}
	if qs[0].Get("category") != "music" || qs[0].Get("content_type") != "flick,reel" {
		t.Fatalf("fill query = %v, want category=music and content_type=flick,reel", qs[0])
	}
}

func TestReelCategory_FillOnlyWhenTimelineExhausted(t *testing.T) {
	f := newReelCategoryFixture(t)
	a := uuid.New()
	now := time.Now()
	// A long comedy run behind one music reel: the first page matches one
	// reel within the usual budget but the timeline is NOT exhausted, so
	// the fill must not run (it could repeat a row a later page serves).
	f.reel(a, now, "music")
	for i := 1; i <= 30; i++ {
		f.reel(a, now.Add(-time.Duration(i)*time.Minute), "comedy")
	}
	f.public(uuid.New(), now, "reel", "music")

	page, next, err := f.svc.GetReelCategoryPage(context.Background(), f.viewer, 2, "", "music", false)
	if err != nil {
		t.Fatalf("GetReelCategoryPage: %v", err)
	}
	if len(page) != 1 || next == "" {
		t.Fatalf("page = %d rows, next %q; want the one timeline match and a live cursor", len(page), next)
	}
	if n := len(f.queries()); n != 0 {
		t.Fatalf("the fill ran %d times on a page whose timeline is not exhausted", n)
	}
}

func TestReelCategory_FollowingOnlyIsNeverFilled(t *testing.T) {
	f := newReelCategoryFixture(t)
	followed, stranger := uuid.New(), uuid.New()
	f.following = []uuid.UUID{followed}
	now := time.Now()
	want := f.reel(followed, now, "music")
	f.reel(stranger, now.Add(-time.Minute), "music")
	f.public(stranger, now, "reel", "music")

	page, _, err := f.svc.GetReelCategoryPage(context.Background(), f.viewer, 10, "", "music", true)
	if err != nil {
		t.Fatalf("GetReelCategoryPage: %v", err)
	}
	if !sameIDs(page, want) {
		t.Fatalf("following page = %v, want only the followed author's music reel", idsOf(page))
	}
	if n := len(f.queries()); n != 0 {
		t.Fatalf("following_only was topped up from the fill (%d calls)", n)
	}
}

// ─── unknown category ───────────────────────────────────────────────────

func TestReelCategory_UnknownCategoryIsAnEmptyPage(t *testing.T) {
	f := newReelCategoryFixture(t)
	now := time.Now()
	f.reel(uuid.New(), now, "music")
	f.public(uuid.New(), now, "reel", "music")

	page, next, err := f.svc.GetReelCategoryPage(context.Background(), f.viewer, 10, "", "astrophysics", false)
	if err != nil {
		t.Fatalf("an id outside the taxonomy is an empty page, not an error: %v", err)
	}
	if page == nil || len(page) != 0 || next != "" {
		t.Fatalf("page = %v (nil %v), next %q; want JSON [] and no cursor", idsOf(page), page == nil, next)
	}
}

// ─── ordering ───────────────────────────────────────────────────────────

func TestRecencyOrderedHydratedPage(t *testing.T) {
	now := time.Now()
	older := FeedItem{PostID: uuid.New(), CreatedAt: now.Add(-3 * time.Hour)}
	newer := FeedItem{PostID: uuid.New(), CreatedAt: now.Add(-time.Minute)}
	orphan := HydratedPost{ID: uuid.New()}
	page := categoryPage{
		Posts: []HydratedPost{{ID: older.PostID}, orphan, {ID: newer.PostID}},
		Items: map[uuid.UUID]FeedItem{older.PostID: older, newer.PostID: newer},
	}
	got := recencyOrderedHydratedPage(page)
	if len(got) != 3 || got[0].ID != newer.PostID || got[1].ID != older.PostID || got[2].ID != orphan.ID {
		t.Fatalf("order = %v, want newest, older, then the row with no candidate", idsOf(got))
	}
	if !(got[0].Score > got[1].Score && got[1].Score > 0) {
		t.Fatalf("scores = %v, %v; want the recency score carried, newest highest", got[0].Score, got[1].Score)
	}
	if empty := recencyOrderedHydratedPage(categoryPage{}); empty == nil || len(empty) != 0 {
		t.Fatal("an empty page must be JSON [], never null")
	}
}
