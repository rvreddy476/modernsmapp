package postgres

import (
	"go/ast"
	"go/parser"
	"go/printer"
	"go/token"
	"path/filepath"
	"strings"
	"testing"
)

// Source-scan guard for the 2026-10-30 feed read fixes, in the style of
// viewer_eligibility_guard_test.go. The behaviour is proven against real
// Postgres in CI (feed_reads_integration_test.go); this pins, in every local
// run, that the listings keep their viewer predicate and the narrowing
// writers keep their post.visibility_changed.

// funcSource is the printed source of the named function or method in this
// package's production files, whitespace collapsed.
func funcSource(t *testing.T, name string) string {
	t.Helper()
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	for _, file := range files {
		if strings.HasSuffix(file, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, file, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, d := range f.Decls {
			fn, ok := d.(*ast.FuncDecl)
			if !ok || fn.Name.Name != name {
				continue
			}
			var b strings.Builder
			if err := printer.Fprint(&b, fset, fn); err != nil {
				t.Fatal(err)
			}
			// One space for every run of whitespace, so alignment padding
			// in a composite literal does not decide a match.
			return strings.Join(strings.Fields(b.String()), " ")
		}
	}
	t.Fatalf("no function %s in the package", name)
	return ""
}

func TestFeedListingsKeepTheViewerPredicate(t *testing.T) {
	for _, c := range []struct {
		fn, want, why string
	}{
		{"GetTrendingPosts", `ViewerEligibleSQL("p")`, "trending ranked flagged, rejected, pending and held posts (audit B2)"},
		{"GetBookmarks", `viewerApproved("p")`, "saved listed someone else's flagged or held post (audit B3)"},
		{"GetBookmarks", `p.publish_at IS NULL`, "saved listed someone else's scheduled post (audit B3)"},
		{"GetBookmarks", `p.author_id = $1 OR`, "saved lost the saver's own posts"},
		{"GetPostAccessState", `tier_required_id`, "a cache hit served a members-only post ungated (audit B4)"},
		{"GetPostAccessState", `&state.TierRequiredID`, "a cache hit served a members-only post ungated (audit B4)"},
	} {
		if !strings.Contains(funcSource(t, c.fn), c.want) {
			t.Errorf("%s no longer has %s: %s", c.fn, c.want, c.why)
		}
	}
}

func TestNarrowingWritersEmitVisibilityChanged(t *testing.T) {
	for _, c := range []struct {
		fn   string
		want []string
	}{
		{"ModeratePost", []string{"moderationNarrows(current, resulting)", "emitVisibilityNarrowedTx(ctx, tx, in.PostID, VisibilityReasonModeration)"}},
		{"ApplyPostRestriction", []string{"restrictionNarrows(changed, next)", "emitVisibilityNarrowedTx(ctx, tx, in.PostID, restrictionReason(in.Source))"}},
		{"UpdatePostFields", []string{
			"Reason: VisibilityReasonAudience",
			"gateNarrows(before.TierRequiredID, after.TierRequiredID)",
			"emitVisibilityNarrowedTx(ctx, tx, postID, VisibilityReasonMembershipGate)",
		}},
	} {
		src := funcSource(t, c.fn)
		for _, w := range c.want {
			if !strings.Contains(src, w) {
				t.Errorf("%s no longer has %s: a narrowing would leave the old post in every feed cache", c.fn, w)
			}
		}
	}
}
