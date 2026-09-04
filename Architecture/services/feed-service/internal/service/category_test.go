package service

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
)

// Tube category filter (2026-09-05): the query value is a slug, forgiven
// for case and whitespace, refused for anything else.
func TestNormalizeCategoryFilter(t *testing.T) {
	cases := []struct {
		raw  string
		want string
		ok   bool
	}{
		{"", "", true},
		{"tech", "tech", true},
		{"  Music ", "music", true},
		{"how-to_2", "how-to_2", true},
		{"-tech", "", false},
		{"te ch", "", false},
		{"tech;drop", "", false},
		{"a-category-id-that-is-far-too-long-to-be-real", "", false},
	}
	for _, tc := range cases {
		got, ok := NormalizeCategoryFilter(tc.raw)
		if ok != tc.ok || (ok && got != tc.want) {
			t.Fatalf("%q: got (%q,%v) want (%q,%v)", tc.raw, got, ok, tc.want, tc.ok)
		}
	}
}

// A fake timeline of N long videos, each with a category, cut into keyset
// windows exactly like keysetWindow does: `limit` rows and the token of
// the last one as the cursor.
type fakeTimeline struct {
	rows    []FeedItem
	cats    map[uuid.UUID]string
	fetches int
}

func newFakeTimeline(categories ...string) *fakeTimeline {
	ft := &fakeTimeline{cats: map[uuid.UUID]string{}}
	now := time.Now()
	for i, c := range categories {
		id := uuid.New()
		ft.rows = append(ft.rows, FeedItem{
			PostID: id, AuthorID: uuid.New(), ContentType: "long_video",
			CreatedAt: now.Add(-time.Duration(i) * time.Minute), CursorToken: fmt.Sprintf("tok-%d", i), Source: sourceTimeline,
		})
		ft.cats[id] = c
	}
	return ft
}

func (ft *fakeTimeline) fetch(_ context.Context, before string, limit int) ([]FeedItem, string, error) {
	ft.fetches++
	start := 0
	if before != "" {
		for i, r := range ft.rows {
			if r.CursorToken == before {
				start = i + 1
			}
		}
	}
	rest := ft.rows[start:]
	if len(rest) <= limit {
		return rest, "", nil
	}
	window := rest[:limit]
	return window, window[len(window)-1].CursorToken, nil
}

func (ft *fakeTimeline) hydrate(_ context.Context, items []FeedItem) ([]HydratedPost, error) {
	out := make([]HydratedPost, 0, len(items))
	for _, it := range items {
		out = append(out, HydratedPost{ID: it.PostID, AuthorID: it.AuthorID, ContentType: it.ContentType, Category: ft.cats[it.PostID]})
	}
	return out, nil
}

func TestCollectCategoryPage(t *testing.T) {
	ctx := context.Background()

	t.Run("keeps pulling windows until the page is full and resumes after the last row kept", func(t *testing.T) {
		// 9 rows, window 3: tech at 0,2,4,6,8. limit 3 → page = rows 0,2,4
		// over two windows. Window 2 (rows 3–5) was consumed whole — row 5
		// was seen and rejected — so the cursor is the window's own, tok-5,
		// and page 2 starts at row 6.
		ft := newFakeTimeline("tech", "music", "tech", "music", "tech", "music", "tech", "music", "tech")
		page, err := collectCategoryPage(ctx, 3, "", "tech", ft.fetch, ft.hydrate)
		if err != nil {
			t.Fatal(err)
		}
		if len(page.Posts) != 3 || ft.fetches != 2 {
			t.Fatalf("posts=%d fetches=%d want 3 posts over 2 windows", len(page.Posts), ft.fetches)
		}
		for _, p := range page.Posts {
			if p.Category != "tech" {
				t.Fatalf("leaked category %q", p.Category)
			}
		}
		if page.Next != "tok-5" || page.Exhausted {
			t.Fatalf("next=%q exhausted=%v want tok-5,false", page.Next, page.Exhausted)
		}

		ft.fetches = 0
		page2, err := collectCategoryPage(ctx, 3, page.Next, "tech", ft.fetch, ft.hydrate)
		if err != nil {
			t.Fatal(err)
		}
		if len(page2.Posts) != 2 || !page2.Exhausted || page2.Next != "" {
			t.Fatalf("page2: posts=%d exhausted=%v next=%q want 2,true,\"\"", len(page2.Posts), page2.Exhausted, page2.Next)
		}
		if page2.Posts[0].ID != ft.rows[6].PostID || page2.Posts[1].ID != ft.rows[8].PostID {
			t.Fatalf("page2 did not resume right after the last kept row")
		}
	})

	t.Run("a window with more matches than the page needs is cut after the last row kept", func(t *testing.T) {
		// window 3, limit 3: window 1 (rows 0–2) keeps 0,2; window 2 (rows
		// 3–5) is all tech but only one fits → cut at row 3, cursor tok-3,
		// so rows 4 and 5 are neither skipped nor repeated.
		ft := newFakeTimeline("tech", "music", "tech", "tech", "tech", "tech", "music", "tech")
		page, err := collectCategoryPage(ctx, 3, "", "tech", ft.fetch, ft.hydrate)
		if err != nil {
			t.Fatal(err)
		}
		if len(page.Posts) != 3 || page.Next != "tok-3" || page.Posts[2].ID != ft.rows[3].PostID {
			t.Fatalf("posts=%d next=%q", len(page.Posts), page.Next)
		}
		page2, err := collectCategoryPage(ctx, 3, page.Next, "tech", ft.fetch, ft.hydrate)
		if err != nil {
			t.Fatal(err)
		}
		if len(page2.Posts) != 3 || !page2.Exhausted || page2.Next != "" {
			t.Fatalf("page2: posts=%d exhausted=%v next=%q", len(page2.Posts), page2.Exhausted, page2.Next)
		}
		for i, want := range []int{4, 5, 7} {
			if page2.Posts[i].ID != ft.rows[want].PostID {
				t.Fatalf("page2[%d] is not row %d", i, want)
			}
		}
	})

	t.Run("a window consumed whole hands over its own cursor", func(t *testing.T) {
		// window 2, limit 2: rows 0,1 both tech → cursor tok-1 (window's).
		ft := newFakeTimeline("tech", "tech", "tech")
		page, err := collectCategoryPage(ctx, 2, "", "tech", ft.fetch, ft.hydrate)
		if err != nil {
			t.Fatal(err)
		}
		if len(page.Posts) != 2 || page.Next != "tok-1" || ft.fetches != 1 {
			t.Fatalf("posts=%d next=%q fetches=%d", len(page.Posts), page.Next, ft.fetches)
		}
	})

	t.Run("the window budget bounds the walk and leaves a cursor", func(t *testing.T) {
		cats := make([]string, 0, 20)
		for i := 0; i < 20; i++ {
			cats = append(cats, "music")
		}
		ft := newFakeTimeline(cats...)
		page, err := collectCategoryPage(ctx, 2, "", "tech", ft.fetch, ft.hydrate)
		if err != nil {
			t.Fatal(err)
		}
		if ft.fetches != maxCategoryWindows {
			t.Fatalf("fetches=%d want %d", ft.fetches, maxCategoryWindows)
		}
		if len(page.Posts) != 0 || page.Exhausted || page.Next != "tok-9" {
			t.Fatalf("posts=%d exhausted=%v next=%q", len(page.Posts), page.Exhausted, page.Next)
		}
	})

	t.Run("an unknown category is an empty, exhausted page", func(t *testing.T) {
		ft := newFakeTimeline("tech", "music")
		page, err := collectCategoryPage(ctx, 5, "", "nope", ft.fetch, ft.hydrate)
		if err != nil {
			t.Fatal(err)
		}
		if len(page.Posts) != 0 || !page.Exhausted || page.Next != "" {
			t.Fatalf("posts=%d exhausted=%v next=%q", len(page.Posts), page.Exhausted, page.Next)
		}
	})

	t.Run("fetch and hydration errors fail the page", func(t *testing.T) {
		boom := errors.New("boom")
		ft := newFakeTimeline("tech")
		if _, err := collectCategoryPage(ctx, 5, "", "tech",
			func(context.Context, string, int) ([]FeedItem, string, error) { return nil, "", boom }, ft.hydrate); !errors.Is(err, boom) {
			t.Fatalf("fetch error not surfaced: %v", err)
		}
		if _, err := collectCategoryPage(ctx, 5, "", "tech", ft.fetch,
			func(context.Context, []FeedItem) ([]HydratedPost, error) { return nil, boom }); !errors.Is(err, boom) {
			t.Fatalf("hydration error not surfaced: %v", err)
		}
	})
}
