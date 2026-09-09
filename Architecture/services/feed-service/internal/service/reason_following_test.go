package service

import (
	"context"
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/google/uuid"
)

// A viewer who follows nobody must never be told a post is "from someone
// you follow".
//
// Measured on the running stack (2026-09-09): momentum.sso.test@example.com
// has zero rows in `follows`, /v1/graph/following returned [], and
// /v1/feed/reels, /flicks, /videos, /watch and /home ALL returned
// "reason": "following", "reason_text": "From someone you follow" for
// author 7cd6ea3a — whose home-timeline rows survive from before that
// account's follows were cleared.
//
// The cause was structural, in two layers, and these tests pin both:
//
//  1. deriveReason ENDED in `return ReasonFollowing, "From someone you
//     follow"`, so "following" was the default for any candidate whose
//     Source was not one of the recognised special cases. GetFlickFeedPage
//     sets no Source at all, so every reel got it.
//
//  2. Even sourceTimeline does not mean "follows". FanoutPost writes home
//     timeline rows to followers UNION the author's connections, and
//     nothing retracts them on unfollow. A timeline row is evidence that a
//     fanout once targeted this viewer, nothing more.

// relationshipGraph is a graph-service stub for
// POST /v1/graph/relationships/batch. `follows` and `connections` name the
// targets the viewer follows / is connected to; every other target comes
// back with all-false, which is what the real service returns. calls counts
// round trips, targets records every id asked about.
func relationshipGraph(t *testing.T, follows, connections []uuid.UUID, calls *int32, targets *[]string) *httptest.Server {
	t.Helper()
	followSet := map[string]bool{}
	for _, id := range follows {
		followSet[id.String()] = true
	}
	connSet := map[string]bool{}
	for _, id := range connections {
		connSet[id.String()] = true
	}
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/graph/relationships/batch" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		atomic.AddInt32(calls, 1)
		raw, _ := io.ReadAll(r.Body)
		var req struct {
			ViewerID  string   `json:"viewer_id"`
			TargetIDs []string `json:"target_ids"`
		}
		if err := json.Unmarshal(raw, &req); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		out := map[string]map[string]bool{}
		for _, id := range req.TargetIDs {
			if targets != nil {
				*targets = append(*targets, id)
			}
			out[id] = map[string]bool{
				"follows":       followSet[id],
				"is_connection": connSet[id],
			}
		}
		_ = json.NewEncoder(w).Encode(out)
	}))
}

func hydratedBy(author uuid.UUID) HydratedPost {
	return HydratedPost{ID: uuid.New(), AuthorID: author}
}

// The defect itself. A stale fanout row from an author the viewer does not
// follow must not claim a follow.
func TestReason_NonFollowerGetsNoFollowingReason(t *testing.T) {
	viewer, stranger := uuid.New(), uuid.New()
	var calls int32
	graph := relationshipGraph(t, nil, nil, &calls, nil)
	defer graph.Close()

	posts := []HydratedPost{hydratedBy(stranger)}
	s := newFeedServiceWithGraph(graph.URL)
	s.resolveFollowReasons(context.Background(), posts, viewer)

	if posts[0].Reason == ReasonFollowing {
		t.Fatalf("a viewer who follows nobody was told %q / %q",
			posts[0].Reason, posts[0].ReasonText)
	}
	if posts[0].Reason != "" || posts[0].ReasonText != "" {
		t.Fatalf("an unestablished reason must be absent, got (%q, %q)",
			posts[0].Reason, posts[0].ReasonText)
	}
	if calls != 1 {
		t.Fatalf("expected exactly one graph round trip, got %d", calls)
	}
}

// The other half of the same rule: a viewer who DOES follow still gets the
// correct reason. Silence must not be the answer to everything.
func TestReason_RealFollowStillSaysFollowing(t *testing.T) {
	viewer, followed := uuid.New(), uuid.New()
	var calls int32
	graph := relationshipGraph(t, []uuid.UUID{followed}, nil, &calls, nil)
	defer graph.Close()

	posts := []HydratedPost{hydratedBy(followed)}
	s := newFeedServiceWithGraph(graph.URL)
	s.resolveFollowReasons(context.Background(), posts, viewer)

	if posts[0].Reason != ReasonFollowing || posts[0].ReasonText != "From someone you follow" {
		t.Fatalf("a followed author must still say so, got (%q, %q)",
			posts[0].Reason, posts[0].ReasonText)
	}
}

// A connection who is not followed is how a non-follower's row reached the
// timeline in the first place — FanoutPost writes to the author's
// connections. Saying "From your circle" is true; saying "From someone you
// follow" was not.
func TestReason_ConnectionIsNotAFollow(t *testing.T) {
	viewer, friend := uuid.New(), uuid.New()
	var calls int32
	graph := relationshipGraph(t, nil, []uuid.UUID{friend}, &calls, nil)
	defer graph.Close()

	posts := []HydratedPost{hydratedBy(friend)}
	s := newFeedServiceWithGraph(graph.URL)
	s.resolveFollowReasons(context.Background(), posts, viewer)

	if posts[0].Reason != ReasonConnection || posts[0].ReasonText != "From your circle" {
		t.Fatalf("a connection who is not followed got (%q, %q)",
			posts[0].Reason, posts[0].ReasonText)
	}
}

// A repost's reason is about the REPOSTER, not the original author: the
// feed event is "X reposted this". Asking about the author would answer a
// question nobody posed — and would claim a follow the viewer never made.
func TestReason_RepostAsksAboutTheReposter(t *testing.T) {
	viewer, author, reposter := uuid.New(), uuid.New(), uuid.New()
	var calls int32
	var asked []string
	graph := relationshipGraph(t, []uuid.UUID{reposter}, nil, &calls, &asked)
	defer graph.Close()

	post := hydratedBy(author)
	post.IsRepost = true
	post.RepostedBy = &reposter
	posts := []HydratedPost{post}

	s := newFeedServiceWithGraph(graph.URL)
	s.resolveFollowReasons(context.Background(), posts, viewer)

	if len(asked) != 1 || asked[0] != reposter.String() {
		t.Fatalf("the graph was asked about %v, want only the reposter %s", asked, reposter)
	}
	if posts[0].Reason != ReasonFollowing || posts[0].ReasonText != "Reposted by someone you follow" {
		t.Fatalf("repost by a followed user got (%q, %q)", posts[0].Reason, posts[0].ReasonText)
	}
}

// The viewer's own post is nobody's business to explain, and must not cost
// a graph call.
func TestReason_OwnPostIsNeverAskedAbout(t *testing.T) {
	viewer := uuid.New()
	var calls int32
	graph := relationshipGraph(t, nil, nil, &calls, nil)
	defer graph.Close()

	posts := []HydratedPost{hydratedBy(viewer)}
	s := newFeedServiceWithGraph(graph.URL)
	s.resolveFollowReasons(context.Background(), posts, viewer)

	if posts[0].Reason != "" || calls != 0 {
		t.Fatalf("own post got reason %q after %d graph calls", posts[0].Reason, calls)
	}
}

// An unresolved graph must leave the reason ABSENT. This is the one
// direction that matters: a graph-service outage may cost the sentence, but
// it must never restore the old unchecked "From someone you follow" — which
// is exactly what a fallback-to-the-merge-time-value would have done.
func TestReason_GraphFailureLeavesTheReasonAbsent(t *testing.T) {
	viewer, stranger := uuid.New(), uuid.New()

	for _, status := range []int{
		http.StatusNotFound,
		http.StatusUnauthorized,
		http.StatusInternalServerError,
	} {
		graph := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(status)
			_, _ = w.Write([]byte(`{}`))
		}))
		posts := []HydratedPost{hydratedBy(stranger)}
		s := newFeedServiceWithGraph(graph.URL)
		s.resolveFollowReasons(context.Background(), posts, viewer)
		graph.Close()
		if posts[0].Reason != "" {
			t.Errorf("status %d produced reason %q — an unchecked claim", status, posts[0].Reason)
		}
	}

	posts := []HydratedPost{hydratedBy(stranger)}
	s := newFeedServiceWithGraph("http://127.0.0.1:1")
	s.resolveFollowReasons(context.Background(), posts, viewer)
	if posts[0].Reason != "" {
		t.Fatalf("an unreachable graph-service produced reason %q", posts[0].Reason)
	}
}

// A target missing from the answer is unknown, not "no". graph-service
// returns an entry per target, but a partial answer must degrade to silence
// rather than to a claim.
func TestReason_TargetAbsentFromTheAnswerIsUnknown(t *testing.T) {
	viewer, stranger := uuid.New(), uuid.New()
	graph := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{}`))
	}))
	defer graph.Close()

	posts := []HydratedPost{hydratedBy(stranger)}
	s := newFeedServiceWithGraph(graph.URL)
	s.resolveFollowReasons(context.Background(), posts, viewer)
	if posts[0].Reason != "" {
		t.Fatalf("an unanswered target produced reason %q", posts[0].Reason)
	}
}

// Rows that already explained themselves — the circle view, a close-friends
// post, a cold-start recommendation, the related-videos surface — need no
// graph traffic and must not be rewritten by it. A page entirely of those
// makes no call at all.
func TestReason_SelfEvidentSourcesCostNoGraphTraffic(t *testing.T) {
	viewer, author := uuid.New(), uuid.New()
	var calls int32
	// The stub would report a follow if asked; if any of these rows were
	// sent to it, the assertions below would still pass but `calls` would
	// not be zero.
	graph := relationshipGraph(t, []uuid.UUID{author}, nil, &calls, nil)
	defer graph.Close()

	sources := []string{sourceCircle, sourceColdStart, sourceRelatedAuthor, sourceRelatedTopic}
	posts := make([]HydratedPost, 0, len(sources))
	for _, src := range sources {
		p := hydratedBy(author)
		p.source = src
		p.Reason, p.ReasonText = deriveReason(src, p, viewer, 0)
		if p.Reason == "" {
			t.Fatalf("source %q derived no reason — the fixture is wrong", src)
		}
		posts = append(posts, p)
	}
	before := make([]string, len(posts))
	for i, p := range posts {
		before[i] = p.Reason
	}

	s := newFeedServiceWithGraph(graph.URL)
	s.resolveFollowReasons(context.Background(), posts, viewer)

	if calls != 0 {
		t.Fatalf("already-explained rows cost %d graph round trip(s)", calls)
	}
	for i, p := range posts {
		if p.Reason != before[i] {
			t.Fatalf("source %q was rewritten from %q to %q", sources[i], before[i], p.Reason)
		}
	}
}

// One round trip for a page, however many rows share an author.
func TestReason_OneGraphCallPerPage(t *testing.T) {
	viewer := uuid.New()
	a, b := uuid.New(), uuid.New()
	var calls int32
	var asked []string
	graph := relationshipGraph(t, []uuid.UUID{a}, nil, &calls, &asked)
	defer graph.Close()

	posts := []HydratedPost{hydratedBy(a), hydratedBy(b), hydratedBy(a), hydratedBy(b)}
	s := newFeedServiceWithGraph(graph.URL)
	s.resolveFollowReasons(context.Background(), posts, viewer)

	if calls != 1 {
		t.Fatalf("expected 1 graph round trip for the page, got %d", calls)
	}
	if len(asked) != 2 {
		t.Fatalf("expected 2 distinct subjects, got %v", asked)
	}
	if posts[0].Reason != ReasonFollowing || posts[2].Reason != ReasonFollowing {
		t.Fatal("the followed author's rows lost their reason")
	}
	if posts[1].Reason != "" || posts[3].Reason != "" {
		t.Fatalf("the unfollowed author's rows claimed %q", posts[1].Reason)
	}
}

// The source-level guard, in the spirit of coldstart_narrowing_test.go's:
// it fails if the deleted inline condition is put back.
//
// deriveReason is pure and has no graph client, so it CANNOT know about a
// follow. The specific way this defect existed was a bare
// `return ReasonFollowing, ...` as its fallthrough — a default dressed as a
// derivation. Asserting on behaviour alone would not catch its return: a
// table test would simply be updated alongside it. This asserts that the
// function does not mention the token at all.
func TestDeriveReasonCannotClaimAFollow(t *testing.T) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "reason.go", nil, 0)
	if err != nil {
		t.Fatalf("parse reason.go: %v", err)
	}

	var fn *ast.FuncDecl
	for _, decl := range file.Decls {
		d, ok := decl.(*ast.FuncDecl)
		if ok && d.Name.Name == "deriveReason" && d.Body != nil {
			fn = d
			break
		}
	}
	if fn == nil {
		t.Fatal("deriveReason not found in reason.go — has it been renamed? " +
			"This test would otherwise pass while checking nothing")
	}

	var claims int
	ast.Inspect(fn.Body, func(n ast.Node) bool {
		if id, ok := n.(*ast.Ident); ok && id.Name == "ReasonFollowing" {
			claims++
		}
		if lit, ok := n.(*ast.BasicLit); ok && lit.Kind == token.STRING &&
			(lit.Value == `"following"` || lit.Value == `"From someone you follow"` ||
				lit.Value == `"Reposted by someone you follow"`) {
			claims++
		}
		return true
	})

	if claims != 0 {
		t.Fatalf("deriveReason asserts a follow in %d place(s). It is pure and has no "+
			"graph client, so it cannot know one: a timeline row is written by fanout to "+
			"followers UNION connections and is never retracted on unfollow. The follow "+
			"belongs to resolveFollowReasons, which asks graph-service.", claims)
	}
}

// And the pass that CAN claim it must actually be reached from hydration —
// on both paths, the cache-only one included. Same reasoning as
// coldstart_narrowing_test.go: HydratePosts needs live upstreams to
// exercise end to end, so the wiring is asserted at the source level.
func TestHydratePostsResolvesFollowReasons(t *testing.T) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "hydration.go", nil, 0)
	if err != nil {
		t.Fatalf("parse hydration.go: %v", err)
	}

	var fn *ast.FuncDecl
	for _, decl := range file.Decls {
		d, ok := decl.(*ast.FuncDecl)
		if ok && d.Name.Name == "HydratePosts" && d.Body != nil {
			fn = d
			break
		}
	}
	if fn == nil {
		t.Fatal("HydratePosts not found in hydration.go — has it been renamed?")
	}

	var merges, resolves int
	ast.Inspect(fn.Body, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		switch sel.Sel.Name {
		case "mergeHydratedItems":
			merges++
		case "resolveFollowReasons":
			resolves++
		}
		return true
	})

	if merges == 0 {
		t.Fatal("no mergeHydratedItems call found in HydratePosts — has it been renamed? " +
			"This test would otherwise pass while checking nothing")
	}
	if resolves != merges {
		t.Fatalf("%d of %d hydration paths in HydratePosts do not call resolveFollowReasons; "+
			"rows on an unresolved path reach the client with no reason at all (the "+
			"cache-only path is the one that gets forgotten)", merges-resolves, merges)
	}
}
