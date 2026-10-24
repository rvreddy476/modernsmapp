package service

import (
	"go/ast"
	"go/parser"
	"go/token"
	"go/types"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// The members-only inventory (2026-10-23 audit).
//
// The leak this closes was not a wrong check but a MISSING one: the direct
// read redacted a gated body and every list beside it — the batch every feed
// hydrates from, by-author, recent, hashtags, trending, bookmarks, threads,
// sounds, watch progress, playlists — did not. A convention ("remember to
// redact") is what failed, so this is a test over the real source instead:
//
//   - every *Service method whose results carry a post body (postRowResult)
//     must reach redactForViewer through this package's own calls, or be
//     listed in membersReadExempt with the reason it may skip it;
//   - every comment entry point must reach the comment gate;
//   - an exemption that no longer names a row-returning method, or that now
//     reaches the helper anyway, is stale and fails too.
//
// A new list that returns post rows therefore fails here until it either
// goes through the helper or someone writes down why it need not.

// postRowResult matches a result type that carries a post's body.
var postRowResult = regexp.MustCompile(`\bPostDetail\b|\bReelFeedItem\b|\bContinueWatchingItem\b|\bPlaylistItemDetail\b|\bSoundPage\b|\bUploadDetail\b|\bpostgres\.Post\b`)

// membersReadExempt is every row-returning method that may skip the helper,
// and why. "owner": answers only the post's author (who always reads in
// full). "admin": the token-gated admin console. "loader": a step inside a
// read that redacts on its way out (named in the reason).
var membersReadExempt = map[string]string{
	// Author-only answers.
	"CreatePost":              "owner: the author's own new post",
	"CreateThread":            "owner: the author's own new thread",
	"PublishDraft":            "owner: the author publishing their draft",
	"PublishPostDraft":        "owner: the author publishing their draft",
	"UpdatePost":              "owner: edit after publish, author only",
	"UpdateDistribution":      "owner: author only",
	"ReschedulePost":          "owner: author only",
	"ListMyScheduledPosts":    "owner: the caller's own scheduled posts",
	"ListMyDeletedPosts":      "owner: the caller's own Recently deleted",
	"GetMyPosts":              "owner: the caller's own uploads",
	"GetMyVideos":             "owner: the caller's own uploads",
	"GetMyVideosOpts":         "owner: the caller's own uploads",
	"GetMyFlicks":             "owner: the caller's own uploads",
	"GetMyFlicksOpts":         "owner: the caller's own uploads",
	"enrichUploads":           "owner: the caller's own uploads (Creator Hub)",
	"applyPostEdit":           "owner: edit after publish, author only",
	"publishClaimedReelDraft": "owner: the author publishing their draft",
	"publishDraftRow":         "owner: the author publishing their draft",
	// Admin console.
	"AdminGetPost": "admin: the token-gated admin console",
	// Loaders and page steps inside reads that redact on the way out, or
	// decisions that never serialise the row.
	"loadPostsByIDs":              "loader: GetPostsByIDs redacts its result",
	"attachMediaStateToDetails":   "loader: page filter; every list calling it redacts after it",
	"attachContinueWatchingPosts": "loader: assembles rows hydrateWatchProgress read through GetPostsByIDs, which redacts",
	"hashtagPostsVisibleTo":       "loader: author gate inside GetPostsByHashtag, which redacts",
	"decorateHashtagDetails":      "loader: counts + media state inside FollowedHashtagPosts, which redacts",
	"GetPost":                     "loader: service-internal callers need the full row; the GET /v1/posts/:postId and restore handlers redact (members_read_routes_test.go in internal/http)",
	"loadPostForEngagement":       "loader: engagement gate, never serialised",
	"loadVisiblePost":             "loader: read gate, never serialised",
	"getCachedPostBody":           "loader: the body cache, never serialised by itself",
	"approvedPostsForMedia":       "loader: media byte gate (media_access.go), never serialised",
	"evaluateOfflineCopies":       "loader: offline copy decision; the cards it feeds carry no body and the decision asks viewerEntitled itself",
}

// commentEntryPoints must reach the comment thread's members gate.
var commentEntryPoints = []string{
	"ListCommentsSortedPG",
	"GetCommentsAroundPG",
	"CreateCommentPG",
	"CreateReply",
	"ListReplies",
	"SetCommentReaction",
	"RemoveCommentReaction",
	"ToggleCommentLike",
	"ToggleCommentDislike",
	"AddReelComment",
	"ListReelComments",
}

type pkgFunc struct {
	name    string
	results string
	calls   map[string]bool
}

// scanServicePackage parses this package's non-test sources: every *Service
// method and package function with its result types and the names it calls
// or takes as a value (s.X, X(...)).
func scanServicePackage(t *testing.T) map[string]*pkgFunc {
	t.Helper()
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	out := map[string]*pkgFunc{}
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		src, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		file, err := parser.ParseFile(fset, f, src, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", f, err)
		}
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}
			recv := ""
			if fn.Recv != nil {
				if len(fn.Recv.List) != 1 || types.ExprString(fn.Recv.List[0].Type) != "*Service" {
					continue
				}
				if len(fn.Recv.List[0].Names) == 1 {
					recv = fn.Recv.List[0].Names[0].Name
				}
			}
			pf := &pkgFunc{name: fn.Name.Name, calls: map[string]bool{}}
			if fn.Type.Results != nil {
				var parts []string
				for _, r := range fn.Type.Results.List {
					// A returned closure (a per-row predicate) is not a row.
					if _, isFunc := r.Type.(*ast.FuncType); isFunc {
						continue
					}
					parts = append(parts, types.ExprString(r.Type))
				}
				pf.results = strings.Join(parts, ", ")
			}
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				switch x := n.(type) {
				case *ast.SelectorExpr:
					if id, ok := x.X.(*ast.Ident); ok && recv != "" && id.Name == recv {
						pf.calls[x.Sel.Name] = true
					}
				case *ast.CallExpr:
					if id, ok := x.Fun.(*ast.Ident); ok {
						pf.calls[id.Name] = true
					}
				}
				return true
			})
			// Methods and functions share one namespace here; a clash
			// would only ever widen "reaches", so it is refused outright.
			if prev, dup := out[pf.name]; dup && prev.results != pf.results {
				t.Fatalf("two package functions named %s; the inventory cannot tell them apart", pf.name)
			}
			out[pf.name] = pf
		}
	}
	return out
}

// reaches reports whether fn reaches target through package calls.
func reaches(funcs map[string]*pkgFunc, fn, target string) bool {
	seen := map[string]bool{}
	var walk func(string) bool
	walk = func(name string) bool {
		if name == target {
			return true
		}
		if seen[name] {
			return false
		}
		seen[name] = true
		f := funcs[name]
		if f == nil {
			return false
		}
		for callee := range f.calls {
			if walk(callee) {
				return true
			}
		}
		return false
	}
	return walk(fn)
}

func TestEveryPostRowReadGoesThroughTheMembersRedaction(t *testing.T) {
	funcs := scanServicePackage(t)
	if funcs["redactForViewer"] == nil {
		t.Fatal("redactForViewer is gone; the inventory has nothing to hold the reads to")
	}
	var missing []string
	for name, f := range funcs {
		if !postRowResult.MatchString(f.results) {
			continue
		}
		if _, exempt := membersReadExempt[name]; exempt {
			continue
		}
		if name == "redactForViewer" || !reaches(funcs, name, "redactForViewer") {
			if name != "redactForViewer" {
				missing = append(missing, name+" ("+f.results+")")
			}
		}
	}
	sort.Strings(missing)
	for _, m := range missing {
		t.Errorf("%s returns post rows but never reaches redactForViewer.\n"+
			"A members-only post's body would reach every viewer through it. Call "+
			"s.redactDetailsForViewer / redactDetailPtrsForViewer / redactPostRowsForViewer / "+
			"redactForViewer on the page before returning it (members_read.go), or add it to "+
			"membersReadExempt with the reason it answers only the author.", m)
	}
}

func TestMembersReadExemptionsAreNotStale(t *testing.T) {
	funcs := scanServicePackage(t)
	for name, why := range membersReadExempt {
		f := funcs[name]
		if f == nil {
			t.Errorf("membersReadExempt names %s (%s), which no longer exists. A stale exemption hides "+
				"the fact that nothing checks it.", name, why)
			continue
		}
		if !postRowResult.MatchString(f.results) {
			t.Errorf("membersReadExempt names %s, which returns no post rows (%s); drop the entry.", name, f.results)
		}
		if reaches(funcs, name, "redactForViewer") {
			t.Errorf("membersReadExempt names %s, which reaches redactForViewer anyway; drop the entry "+
				"so the inventory holds it to the helper.", name)
		}
	}
}

func TestEveryCommentEntryRunsTheMembersGate(t *testing.T) {
	funcs := scanServicePackage(t)
	for _, name := range commentEntryPoints {
		if funcs[name] == nil {
			t.Errorf("comment entry point %s no longer exists; update commentEntryPoints", name)
			continue
		}
		if !reaches(funcs, name, "requireMemberOf") {
			t.Errorf("%s does not reach requireMemberOf: a non-member could read or write the "+
				"thread of a members-only post through it (members_read.go)", name)
		}
	}
}

// The redaction helpers themselves must keep calling the one rule.
func TestMembersRedactionHelpersShareTheRule(t *testing.T) {
	funcs := scanServicePackage(t)
	for _, name := range []string{"RedactDetailForViewer", "redactDetailsForViewer", "redactDetailPtrsForViewer", "redactPostRowsForViewer", "GetPostsByIDs"} {
		if funcs[name] == nil || !funcs[name].calls["redactForViewer"] && !reaches(funcs, name, "redactForViewer") {
			t.Errorf("%s must reach redactForViewer", name)
		}
	}
}
