package service

import (
	"go/ast"
	"go/parser"
	"go/token"
	"testing"
	"time"

	"github.com/google/uuid"
)

// A reader who follows nobody got the cold-start backfill and a full feed —
// until they posted once. Their own post made the timeline non-empty,
// coldStartAllowed saw a populated feed, the backfill stopped for good, and
// their For You collapsed to nothing but their own posts. Observed on dev: an
// account went from a 39-post backfilled feed to 5 posts, all its own, from
// the moment it first posted.
//
// Your own posts are not content you came to read.
func TestCountFromOthersIgnoresTheViewersOwnPosts(t *testing.T) {
	viewer := uuid.New()
	other := uuid.New()
	now := time.Now()

	item := func(author uuid.UUID) FeedItem {
		return FeedItem{PostID: uuid.New(), AuthorID: author, CreatedAt: now}
	}

	for _, tc := range []struct {
		name  string
		items []FeedItem
		want  int
	}{
		{"empty timeline", nil, 0},
		{"only my own posts still counts as nothing to read", []FeedItem{item(viewer), item(viewer), item(viewer)}, 0},
		{"somebody else's post counts", []FeedItem{item(other)}, 1},
		{"mine are skipped, theirs are counted", []FeedItem{item(viewer), item(other), item(viewer), item(other)}, 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := countFromOthers(tc.items, viewer); got != tc.want {
				t.Fatalf("countFromOthers = %d, want %d", got, tc.want)
			}
		})
	}
}

// The decision the bug actually turned on: a timeline holding only the
// viewer's own posts must still be backfilled.
func TestColdStartRunsForAFeedOfOnlyYourOwnPosts(t *testing.T) {
	viewer := uuid.New()
	now := time.Now()
	ownOnly := []FeedItem{
		{PostID: uuid.New(), AuthorID: viewer, CreatedAt: now},
		{PostID: uuid.New(), AuthorID: viewer, CreatedAt: now},
	}

	if !coldStartAllowed(nil, "ranked", countFromOthers(ownOnly, viewer), false, false) {
		t.Fatal("a timeline of only the viewer's own posts must still be backfilled; " +
			"passing len(candidates) here is what silently ended discovery for anyone who posted early")
	}

	// And the narrowing rules still win over it — asking explicitly for the
	// people you follow must never be answered with recommended strangers.
	if coldStartAllowed(nil, "ranked", countFromOthers(ownOnly, viewer), false, true) {
		t.Fatal("following_only must still forbid the backfill")
	}
	if coldStartAllowed(nil, "ranked", countFromOthers(ownOnly, viewer), true, false) {
		t.Fatal("circle_only must still forbid the backfill")
	}
}

// The predicate tests above cannot see WHICH count the call site passes, and
// that is the whole bug: coldStartAllowed was correct all along and was simply
// being handed len(candidates), which counts the viewer's own posts. Reverting
// that one argument silently restores the dead feed and every behavioural test
// still passes — so the argument itself has to be pinned.
//
// Structural, following TestColdStartBackfillIsGuardedByThePredicate in
// coldstart_narrowing_test.go.
func TestColdStartCountExcludesTheViewersOwnPosts(t *testing.T) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "feed.go", nil, 0)
	if err != nil {
		t.Fatalf("parse feed.go: %v", err)
	}

	var found bool
	ast.Inspect(file, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		ident, ok := call.Fun.(*ast.Ident)
		if !ok || ident.Name != "coldStartAllowed" || len(call.Args) < 3 {
			return true
		}
		found = true
		inner, ok := call.Args[2].(*ast.CallExpr)
		if !ok {
			t.Fatalf("coldStartAllowed's candidate count is %T, not a call to countFromOthers", call.Args[2])
		}
		fnIdent, ok := inner.Fun.(*ast.Ident)
		if !ok || fnIdent.Name != "countFromOthers" {
			name := "?"
			if ok {
				name = fnIdent.Name
			}
			t.Fatalf("coldStartAllowed is counting with %s(), not countFromOthers(): "+
				"a viewer's own posts would again be read as a populated feed, and "+
				"discovery would stop for anyone who posts before following anybody", name)
		}
		return true
	})

	if !found {
		t.Fatal("no coldStartAllowed call found in feed.go — has the backfill moved?")
	}
}
