package service

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/atpost/post-service/internal/store/postgres"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

// 2026-11-07: a listed page whose every row the viewer gate dropped came
// back empty WITH a next cursor, and Android stops paging on an empty page.

func flaggedPosts(n int) []postgres.Post {
	out := make([]postgres.Post, n)
	for i := range out {
		out[i] = listPost(uuid.New(), "public", "flagged")
	}
	return out
}

func TestGetTrendingPosts_ReadsOnPastPagesTheGatesEmpty(t *testing.T) {
	f := newListFixture(t)
	f.store.trendingPages = map[string]listPage{
		"":   {posts: flaggedPosts(3), next: "c1"},
		"c1": {posts: []postgres.Post{f.private, f.flagged}, next: "c2"},
		"c2": {posts: []postgres.Post{f.flagged, f.open}, next: "c3"},
	}
	details, next, err := f.svc.GetTrendingPosts(context.Background(), nil, 20, "", &f.viewer)
	if err != nil {
		t.Fatal(err)
	}
	if len(details) != 1 || details[0].Post.ID != f.open.ID {
		t.Fatalf("page = %d rows, want the one visible row after two hidden pages", len(details))
	}
	if next != "c3" {
		t.Fatalf("next = %q, want the cursor after the page that had rows (c3)", next)
	}
	if got := strings.Join(f.store.trendingReads, ","); got != ",c1,c2" {
		t.Fatalf("store reads = %q, want \",c1,c2\"", got)
	}
}

func TestGetTrendingPosts_ExhaustedSourceReturnsNoCursor(t *testing.T) {
	f := newListFixture(t)
	f.store.trendingPages = map[string]listPage{
		"":   {posts: flaggedPosts(2), next: "c1"},
		"c1": {posts: flaggedPosts(2), next: ""},
	}
	details, next, err := f.svc.GetTrendingPosts(context.Background(), nil, 20, "", &f.viewer)
	if err != nil || len(details) != 0 || next != "" {
		t.Fatalf("rows=%d next=%q err=%v, want an empty last page with no cursor", len(details), next, err)
	}
}

func TestGetTrendingPosts_RefillIsBounded(t *testing.T) {
	f := newListFixture(t)
	f.store.trendingPages = map[string]listPage{}
	cur := ""
	for i := 0; i < 2*listRefillMaxRounds; i++ {
		f.store.trendingPages[cur] = listPage{posts: flaggedPosts(1), next: cur + "x"}
		cur += "x"
	}
	details, next, err := f.svc.GetTrendingPosts(context.Background(), nil, 20, "", &f.viewer)
	if err != nil || len(details) != 0 {
		t.Fatalf("rows=%d err=%v", len(details), err)
	}
	if len(f.store.trendingReads) != listRefillMaxRounds {
		t.Fatalf("store reads = %d, want the bound %d", len(f.store.trendingReads), listRefillMaxRounds)
	}
	if next != strings.Repeat("x", listRefillMaxRounds) {
		t.Fatalf("next = %q: a source that is not exhausted keeps its cursor", next)
	}
}

func TestGetBookmarks_ReadsOnPastPagesTheGatesEmpty(t *testing.T) {
	f := newListFixture(t)
	f.store.bookmarkPages = map[string]listPage{
		"":   {posts: []postgres.Post{f.private, f.blocking, f.flagged}, next: "c1"},
		"c1": {posts: []postgres.Post{f.held, f.open}, next: ""},
	}
	details, next, err := f.svc.GetBookmarks(context.Background(), f.viewer, nil, 20, "", postgres.SavedScope{})
	if err != nil {
		t.Fatal(err)
	}
	if len(details) != 1 || details[0].Post.ID != f.open.ID || next != "" {
		t.Fatalf("rows=%d next=%q, want the one visible saved post and no cursor", len(details), next)
	}
}

// The first trending page is cached as its ranking only: a hit re-reads the
// rows, so a post moderated since is dropped, and the viewer gate still runs.
func TestGetTrendingPosts_FirstPageCacheKeepsEveryGate(t *testing.T) {
	f := newListFixture(t)
	srv := newRESPServer(t)
	rdb := redis.NewClient(&redis.Options{Addr: srv.ln.Addr().String()})
	t.Cleanup(func() { _ = rdb.Close() })
	f.svc.rdb = rdb

	soonFlagged := listPost(uuid.New(), "public", "approved")
	// The viewer's own rows pass the viewer gate whatever their state, so
	// only the re-read keeps the ranking query's own predicate for them.
	ownFlagged := listPost(f.viewer, "public", "approved")
	ownNarrowed := listPost(f.viewer, "public", "approved")
	ownScheduled := listPost(f.viewer, "public", "approved")
	page := []postgres.Post{f.open, soonFlagged, f.private, ownFlagged, ownNarrowed, ownScheduled}
	for i := range page {
		p := page[i]
		f.store.posts[p.ID] = &p
	}
	f.store.trendingPages = map[string]listPage{"": {posts: page, next: "c1"}}

	first, next, err := f.svc.GetTrendingPosts(context.Background(), []string{"flick"}, 20, "", &f.viewer)
	if err != nil || next != "c1" {
		t.Fatalf("miss: next=%q err=%v", next, err)
	}
	if ids := idsOf(first); !ids[f.open.ID] || !ids[soonFlagged.ID] || ids[f.private.ID] || !ids[ownScheduled.ID] {
		t.Fatalf("miss page = %v", ids)
	}
	raw, ok := srv.data[trendingCacheKey([]string{"flick"}, 20)]
	if !ok {
		t.Fatal("the first page was not cached")
	}
	var cached trendingFirstPage
	if err := json.Unmarshal([]byte(raw), &cached); err != nil || len(cached.IDs) != len(page) || cached.Next != "c1" {
		t.Fatalf("cached = %s (%v)", raw, err)
	}

	f.store.posts[soonFlagged.ID].ReviewStatus = "flagged"
	f.store.posts[ownFlagged.ID].ReviewStatus = "flagged"
	f.store.posts[ownNarrowed.ID].Visibility = "followers"
	later := time.Now().Add(time.Hour)
	f.store.posts[ownScheduled.ID].PublishAt = &later
	again, next, err := f.svc.GetTrendingPosts(context.Background(), []string{"flick"}, 20, "", &f.viewer)
	if err != nil || next != "c1" {
		t.Fatalf("hit: next=%q err=%v", next, err)
	}
	if len(f.store.trendingReads) != 1 || f.store.byIDReads != 1 {
		t.Fatalf("hit ranked again: trending reads %d, by-id reads %d", len(f.store.trendingReads), f.store.byIDReads)
	}
	ids := idsOf(again)
	if !ids[f.open.ID] {
		t.Error("the cache hit lost a visible post")
	}
	if ids[soonFlagged.ID] {
		t.Error("the cache hit served a post flagged after it was ranked")
	}
	if ids[f.private.ID] {
		t.Error("the cache hit skipped the viewer gate")
	}
	for name, p := range map[string]postgres.Post{"flagged": ownFlagged, "narrowed": ownNarrowed, "rescheduled": ownScheduled} {
		if ids[p.ID] {
			t.Errorf("the cache hit served the viewer's own post %s after it was ranked", name)
		}
	}

	// Later pages are never cached: the cursor pins their ranking.
	f.store.trendingPages["c1"] = listPage{posts: []postgres.Post{f.followersYes}}
	if _, _, err := f.svc.GetTrendingPosts(context.Background(), []string{"flick"}, 20, "c1", &f.viewer); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(f.store.trendingReads, ","); got != ",c1" {
		t.Fatalf("store reads = %q", got)
	}
}

func TestTrendingCacheKey_NamesTheTypesAndSize(t *testing.T) {
	if trendingCacheKey([]string{"long_video", "flick"}, 20) != trendingCacheKey([]string{"flick", "long_video"}, 20) {
		t.Error("the same type set got two cache entries")
	}
	if trendingCacheKey([]string{"flick"}, 20) == trendingCacheKey([]string{"long_video"}, 20) {
		t.Error("two type sets share one cache entry")
	}
	if trendingCacheKey(nil, 10) == trendingCacheKey(nil, 20) {
		t.Error("two page sizes share one cache entry")
	}
	if trendingFirstPageTTL.Seconds() != 60 {
		t.Errorf("trendingFirstPageTTL = %v, want 60 s", trendingFirstPageTTL)
	}
}
