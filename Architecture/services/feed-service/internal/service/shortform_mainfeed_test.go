package service

import (
	"go/ast"
	"go/parser"
	"go/token"
	"testing"
	"time"

	"github.com/google/uuid"
)

// Founder instruction, 2026-09-25: reels are shown on the Reels page only,
// never on the main feed. dropShortForm (longvideo.go) is the read-side
// filter that enforces it; these tests pin (a) what it drops, (b) that both
// main-feed surfaces — the page and the "N new posts" pill — call it, in
// the same position, and (c) that the reels and watch surfaces do NOT.
//
// Behavioural coverage of GetHomeFeed needs a live Scylla timeline (the
// store is a concrete *scylla.TimelineStore), so the wiring is asserted at
// the source level, the same way coldstart_narrowing_test.go and
// feedsurface_safety_test.go do. The files are parsed with mode 0 — no
// ParseComments — so a function name mentioned in a comment can never
// satisfy a guard.

func shortFormItem(ct string) FeedItem {
	return FeedItem{
		PostID:      uuid.New(),
		AuthorID:    uuid.New(),
		CreatedAt:   time.Now(),
		ContentType: ct,
	}
}

func TestDropShortForm_TableOfContentTypes(t *testing.T) {
	cases := []struct {
		contentType string
		kept        bool
	}{
		{"flick", false},
		{"reel", false},
		{"short", false},
		{"post", true},
		{"poll", true},
		{"long_video", true},
		{"video", true},
		{"voice", true},
		{"repost", true},
		{"", true},
	}
	for _, tc := range cases {
		name := tc.contentType
		if name == "" {
			name = "<empty>"
		}
		t.Run(name, func(t *testing.T) {
			got := dropShortForm([]FeedItem{shortFormItem(tc.contentType)})
			if tc.kept && len(got) != 1 {
				t.Fatalf("dropShortForm dropped content_type %q; it is not short-form and belongs on the main feed", tc.contentType)
			}
			if !tc.kept && len(got) != 0 {
				t.Fatalf("dropShortForm kept content_type %q; reels belong on the Reels page only", tc.contentType)
			}
		})
	}
}

func TestDropShortForm_PreservesOrderOfTheSurvivors(t *testing.T) {
	in := []FeedItem{
		shortFormItem("post"),
		shortFormItem("flick"),
		shortFormItem("poll"),
		shortFormItem("reel"),
		shortFormItem("long_video"),
		shortFormItem("short"),
		shortFormItem("repost"),
	}
	want := []uuid.UUID{in[0].PostID, in[2].PostID, in[4].PostID, in[6].PostID}

	got := dropShortForm(in)
	if len(got) != len(want) {
		t.Fatalf("got %d items, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i].PostID != want[i] {
			t.Fatalf("order changed at index %d: got %s, want %s", i, got[i].PostID, want[i])
		}
	}
}

func TestDropShortForm_AllShortFormReturnsEmpty(t *testing.T) {
	got := dropShortForm([]FeedItem{shortFormItem("flick"), shortFormItem("reel"), shortFormItem("short")})
	if len(got) != 0 {
		t.Fatalf("an all-reels page must come back empty, got %d items", len(got))
	}
}

func TestDropShortForm_EmptyAndNilInputs(t *testing.T) {
	if got := dropShortForm(nil); len(got) != 0 {
		t.Fatalf("nil input: got %d items", len(got))
	}
	if got := dropShortForm([]FeedItem{}); len(got) != 0 {
		t.Fatalf("empty input: got %d items", len(got))
	}
}

func TestDropShortForm_NothingToDropReturnsTheInputUnchanged(t *testing.T) {
	in := []FeedItem{shortFormItem("post"), shortFormItem("poll"), shortFormItem("long_video")}
	got := dropShortForm(in)
	if len(got) != len(in) {
		t.Fatalf("got %d items, want %d", len(got), len(in))
	}
	for i := range in {
		if got[i].PostID != in[i].PostID {
			t.Fatalf("item %d changed", i)
		}
	}
}

// The classification must come from shared/postclassify, not a local list.
// Rows labelled "reel" and "short" are legacy spellings of "flick"; a
// hand-written list that only knows "flick" would leak them back into the
// main feed. Checked structurally: dropShortForm's body must call
// postclassify.IsShortForm and must contain no string literals at all.
func TestDropShortForm_UsesTheSharedClassifier(t *testing.T) {
	fn := funcDeclNamed(t, "longvideo.go", "dropShortForm")

	callsShared := false
	var literals []string
	ast.Inspect(fn.Body, func(n ast.Node) bool {
		switch x := n.(type) {
		case *ast.SelectorExpr:
			if pkg, ok := x.X.(*ast.Ident); ok && pkg.Name == "postclassify" && x.Sel.Name == "IsShortForm" {
				callsShared = true
			}
		case *ast.BasicLit:
			if x.Kind == token.STRING {
				literals = append(literals, x.Value)
			}
		}
		return true
	})
	if !callsShared {
		t.Fatal("dropShortForm does not call postclassify.IsShortForm; the short-form " +
			"definition must be the shared one, not a local list of type strings")
	}
	if len(literals) != 0 {
		t.Fatalf("dropShortForm contains string literals %v; a hand-written list of "+
			"content types here would diverge from postclassify.IsShortForm", literals)
	}
}

// Both main-feed surfaces must drop reels, and must do it BEFORE
// filterMainFeedExcluded — the seam the cold-start backfill has already
// merged into by then, so timeline and backfilled candidates are covered
// by the one call.
//
// The delta surface is deltaHome, reached from ComputeFeedDelta for
// feed_type=home and feed_type=following; the guard follows that
// delegation so the pill and the page cannot drift apart.
func TestMainFeedSurfacesDropShortFormBeforeMainFeedExclusion(t *testing.T) {
	cases := []struct {
		file, fn string
	}{
		{"feed.go", "GetHomeFeed"},
		{"delta.go", "deltaHome"},
	}
	for _, tc := range cases {
		t.Run(tc.fn, func(t *testing.T) {
			fn := funcDeclNamed(t, tc.file, tc.fn)
			drop := firstCallPos(fn, "dropShortForm")
			excl := firstCallPos(fn, "filterMainFeedExcluded")
			if !excl.IsValid() {
				t.Fatalf("%s no longer calls filterMainFeedExcluded — the seam this "+
					"guard is anchored to has moved; re-anchor the guard, do not delete it", tc.fn)
			}
			if !drop.IsValid() {
				t.Fatalf("%s never calls dropShortForm; reels would be served on the "+
					"main feed (or counted by the new-posts pill) despite the founder's "+
					"instruction that they belong on the Reels page only", tc.fn)
			}
			if drop > excl {
				t.Fatalf("%s calls dropShortForm AFTER filterMainFeedExcluded; it must "+
					"run before, at the point where timeline and cold-start candidates "+
					"have already been merged", tc.fn)
			}
		})
	}
}

// ComputeFeedDelta must still route the home and following pills through
// deltaHome; a rewrite that counted the raw timeline inline would slip
// past the guard above.
func TestComputeFeedDeltaRoutesHomeThroughDeltaHome(t *testing.T) {
	fn := funcDeclNamed(t, "delta.go", "ComputeFeedDelta")
	if !firstCallPos(fn, "deltaHome").IsValid() {
		t.Fatal("ComputeFeedDelta does not call deltaHome; the guard on deltaHome " +
			"no longer covers the \"N new posts\" pill")
	}
}

// The reels and watch surfaces read the same home timeline and must keep
// serving short-form. If one of them ever calls dropShortForm the Reels
// page goes blank.
func TestReelsAndWatchSurfacesDoNotDropShortForm(t *testing.T) {
	surfaces := []string{
		"GetFlickFeedPage",    // reels
		"videoTimelineWindow", // watch (PostTube)
	}
	for _, name := range surfaces {
		t.Run(name, func(t *testing.T) {
			fn := funcDeclNamed(t, "feed.go", name)
			if firstCallPos(fn, "dropShortForm").IsValid() {
				t.Fatalf("%s calls dropShortForm; that filter is for the main feed only — "+
					"this surface must keep serving short-form content", name)
			}
		})
	}
}

// funcDeclNamed parses file (comments excluded, so a name in a comment
// can never satisfy a guard) and returns the named function or method.
func funcDeclNamed(t *testing.T, file, name string) *ast.FuncDecl {
	t.Helper()
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, file, nil, 0)
	if err != nil {
		t.Fatalf("parse %s: %v", file, err)
	}
	for _, decl := range f.Decls {
		if fn, ok := decl.(*ast.FuncDecl); ok && fn.Name.Name == name && fn.Body != nil {
			return fn
		}
	}
	t.Fatalf("%s not found in %s — has it been renamed? This guard would otherwise pass while checking nothing", name, file)
	return nil
}

// firstCallPos returns the position of the first call to name inside fn's
// body, whether spelled as a plain call (dropShortForm(...)) or a method
// call (s.filterMainFeedExcluded(...)). Invalid when there is none.
func firstCallPos(fn *ast.FuncDecl, name string) token.Pos {
	var pos token.Pos
	ast.Inspect(fn.Body, func(n ast.Node) bool {
		if pos.IsValid() {
			return false
		}
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		switch f := call.Fun.(type) {
		case *ast.Ident:
			if f.Name == name {
				pos = call.Pos()
			}
		case *ast.SelectorExpr:
			if f.Sel.Name == name {
				pos = call.Pos()
			}
		}
		return true
	})
	return pos
}
