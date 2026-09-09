package service

import (
	"go/ast"
	"go/parser"
	"go/token"
	"testing"
	"time"

	"github.com/google/uuid"
)

// GET /v1/feed/home?following_only=true for a viewer who follows nobody.
//
// The cold-start fallback backfills recommended public posts when the
// timeline comes back empty. Its condition was `before == nil &&
// len(candidates) == 0 && feedMode == "ranked"` — it never consulted the
// narrowing flags. So a viewer who follows nobody, asking explicitly for
// only the people they follow, was served strangers under a heading that
// promises follows, with nothing in the response to reveal it. Measured on
// the running stack: ?feed_mode=ranked and ?feed_mode=ranked&following_only=true
// returned the SAME five posts.
//
// These pin the decision, not the happy path: a test that only asserted
// "a new account gets a non-empty ranked feed" passed throughout.

func TestColdStart_NotAllowedWhenNarrowedToFollowing(t *testing.T) {
	if coldStartAllowed(nil, "ranked", 0, false, true) {
		t.Fatal("following_only with no follows must return an empty feed, " +
			"not a backfill of recommended strangers under the Following heading")
	}
}

func TestColdStart_NotAllowedWhenNarrowedToCircle(t *testing.T) {
	if coldStartAllowed(nil, "ranked", 0, true, false) {
		t.Fatal("circle_only with no connections must return an empty feed, " +
			"not a backfill of recommended strangers")
	}
}

func TestColdStart_NotAllowedWhenBothNarrowingsAreSet(t *testing.T) {
	if coldStartAllowed(nil, "ranked", 0, true, true) {
		t.Fatal("a doubly-narrowed request must not be backfilled")
	}
}

// The reason the fallback exists, and the common path the fix must not
// disturb: a brand-new account asking for the plain ranked feed. Android's
// For You sends no feed_mode at all and the saved preference resolves it,
// so this is reached with `ranked` and no narrowing.
func TestColdStart_StillFillsTheEmptyFrontDoor(t *testing.T) {
	if !coldStartAllowed(nil, "ranked", 0, false, false) {
		t.Fatal("an un-narrowed ranked first page with an empty timeline must " +
			"still cold-start, or every new account lands on an empty front door")
	}
}

func TestColdStart_UnchangedForTheOtherPreconditions(t *testing.T) {
	cursor := time.Now().UTC()
	cases := []struct {
		name           string
		before         *time.Time
		feedMode       string
		candidateCount int
		want           bool
	}{
		{"a later page is never backfilled", &cursor, "ranked", 0, false},
		{"chronological is never backfilled", nil, "chronological", 0, false},
		{"shadow is never backfilled", nil, "shadow", 0, false},
		{"a non-empty timeline needs no backfill", nil, "ranked", 3, false},
		{"empty ranked first page", nil, "ranked", 0, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := coldStartAllowed(tc.before, tc.feedMode, tc.candidateCount, false, false)
			if got != tc.want {
				t.Fatalf("coldStartAllowed = %v, want %v", got, tc.want)
			}
		})
	}
}

// A viewer who follows nobody keeps an empty page even when the timeline
// itself had rows on it (a follow removed after fanout, say) — the filter
// and the fallback must agree that empty is the answer.
func TestFollowingNarrowing_NoFollowsIsAnEmptyHomeFeed(t *testing.T) {
	candidates := []FeedItem{feedItemBy(uuid.New()), feedItemBy(uuid.New())}

	filtered := filterByAuthorSet(candidates, nil)
	if len(filtered) != 0 {
		t.Fatalf("a viewer who follows nobody must see nothing, got %d posts", len(filtered))
	}
	if coldStartAllowed(nil, "ranked", len(filtered), false, true) {
		t.Fatal("and the fallback must not then refill the page with strangers")
	}
}

// /v1/feed/videos read `following_only` off the query string, dropped it,
// and served the whole Tube surface — discovery fill and all — under a
// narrowed request. The parameter is now honoured, and the fill obeys the
// same rule as the home feed's cold start.
func TestDiscoveryFill_NotAllowedWhenNarrowedToFollowing(t *testing.T) {
	if discoveryFillAllowed(true, "", 0, 20) {
		t.Fatal("a Tube page narrowed to following_only must stay short, " +
			"not be topped up with recommended strangers")
	}
}

func TestDiscoveryFill_UnchangedForTheUnnarrowedSurface(t *testing.T) {
	cases := []struct {
		name   string
		before string
		have   int
		limit  int
		want   bool
	}{
		{"a short first page is filled", "", 3, 20, true},
		{"an empty first page is filled", "", 0, 20, true},
		{"a full first page needs no fill", "", 20, 20, false},
		{"a later page is never filled", "djE6...", 3, 20, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := discoveryFillAllowed(false, tc.before, tc.have, tc.limit); got != tc.want {
				t.Fatalf("discoveryFillAllowed = %v, want %v", got, tc.want)
			}
		})
	}
}

// The predicate is only worth anything if GetHomeFeed actually asks it.
// Same reasoning as internal/http/hydration_fallback_test.go: `scyllaStore`
// is a concrete *scylla.TimelineStore, so exercising GetHomeFeed end to end
// needs a live Scylla timeline. This asserts at the source level that the
// backfill is unreachable except through the guard, which is the specific
// way this defect was introduced — the condition was spelled inline and one
// clause was forgotten.
func TestColdStartBackfillIsGuardedByThePredicate(t *testing.T) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "feed.go", nil, 0)
	if err != nil {
		t.Fatalf("parse feed.go: %v", err)
	}

	var fn *ast.FuncDecl
	for _, decl := range file.Decls {
		d, ok := decl.(*ast.FuncDecl)
		if ok && d.Name.Name == "GetHomeFeed" && d.Body != nil {
			fn = d
			break
		}
	}
	if fn == nil {
		t.Fatal("GetHomeFeed not found in feed.go — has it been renamed?")
	}

	var backfills, guarded int
	var stack []ast.Node
	ast.Inspect(fn.Body, func(n ast.Node) bool {
		if n == nil {
			stack = stack[:len(stack)-1]
			return true
		}
		stack = append(stack, n)
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || sel.Sel.Name != "getRecentPublicPosts" {
			return true
		}
		backfills++
		for _, anc := range stack {
			ifStmt, ok := anc.(*ast.IfStmt)
			if ok && conditionCalls(ifStmt.Cond, "coldStartAllowed") {
				guarded++
				break
			}
		}
		return true
	})

	if backfills == 0 {
		t.Fatal("no cold-start backfill found in GetHomeFeed — has getRecentPublicPosts " +
			"been renamed? This test would otherwise pass while checking nothing")
	}
	if guarded != backfills {
		t.Fatalf("%d of %d cold-start backfills in GetHomeFeed are not guarded by "+
			"coldStartAllowed; an unguarded one serves recommended strangers to a "+
			"viewer who asked for following_only or circle_only", backfills-guarded, backfills)
	}
}

func conditionCalls(cond ast.Expr, name string) bool {
	found := false
	ast.Inspect(cond, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		if id, ok := call.Fun.(*ast.Ident); ok && id.Name == name {
			found = true
			return false
		}
		return true
	})
	return found
}
