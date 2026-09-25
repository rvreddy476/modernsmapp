package service

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"regexp"
	"strings"
	"testing"
)

/*
Guards for in-group post search, above the store.

The store decides which posts match. This layer decides whether the caller may
see any of them, so what is pinned here is the authorization:

  - the read rule is the FEED's rule, applied by calling the feed's own
    predicate rather than restating it;

  - a private group the caller is not in produces the same answer as a group
    that does not exist, so the endpoint cannot be used to discover private
    groups;

  - the response type is store.GroupPostV2, because that type's MarshalJSON is
    the anonymity mask and a bespoke struct would bypass it.

Every assertion runs against codeOnly() output. Three guards in this tree have
already passed for the wrong reason by matching a name that also appeared in
their own comment; stripping comments makes that impossible rather than
something to remember.
*/

var (
	goLineComment  = regexp.MustCompile(`(?m)//.*$`)
	goBlockComment = regexp.MustCompile(`(?s)/\*.*?\*/`)
)

func codeOnly(src string) string {
	return goLineComment.ReplaceAllString(goBlockComment.ReplaceAllString(src, " "), "")
}

// funcResults returns the source text of a function's result list, so two
// signatures can be compared as written.
func funcResults(t *testing.T, path, name string) string {
	t.Helper()
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
	if err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	for _, decl := range f.Decls {
		fd, ok := decl.(*ast.FuncDecl)
		if !ok || fd.Name.Name != name || fd.Type.Results == nil {
			continue
		}
		return string(raw)[fset.Position(fd.Type.Results.Pos()).Offset:fset.Position(fd.Type.Results.End()).Offset]
	}
	t.Fatalf("%s: no function named %s with results", path, name)
	return ""
}

/*
Search applies the feed's read rule, by calling it.

A second predicate that "does the same thing" is how a search endpoint ends up
looser than the feed it searches. There is one predicate — checkGroupAccess —
and both paths call it. The assertion is on the CALL FORM, and on the feed's
side of it too, so renaming or deleting the shared predicate fails here rather
than silently leaving search with a copy of an old rule.
*/
func TestGroupPostSearchAppliesTheFeedsOwnReadRule(t *testing.T) {
	const predicate = "s.checkGroupAccess("

	feed := codeOnly(funcSource(t, "group.go", "GetGroupFeedV2"))
	if !strings.Contains(feed, predicate) {
		t.Fatalf("GetGroupFeedV2 no longer calls %s — this guard asserts search applies the FEED's rule and has lost its reference point", predicate)
	}

	gate := codeOnly(funcSource(t, "group_post_search.go", "readableGroupForSearch"))
	if !strings.Contains(gate, predicate) {
		t.Fatalf("readableGroupForSearch does not call %s — search is applying some other rule than the feed's, and a looser one reveals posts the viewer cannot read", predicate)
	}
	// The feed refuses a nil group before asking about access; so must search,
	// or checkGroupAccess dereferences nil.
	if !strings.Contains(gate, "g == nil") {
		t.Fatal("readableGroupForSearch does not handle a nil group")
	}

	// And the endpoint must go through the gate, not around it.
	entry := codeOnly(funcSource(t, "group_post_search.go", "SearchGroupPostsV2"))
	auth := strings.Index(entry, "s.readableGroupForSearch(")
	if auth < 0 {
		t.Fatal("SearchGroupPostsV2 does not call readableGroupForSearch — the store query is reachable with no authorization at all")
	}
	read := strings.Index(entry, "s.store.SearchGroupPostsV2(")
	if read < 0 {
		t.Fatal("SearchGroupPostsV2 does not call the store")
	}
	if read < auth {
		t.Fatal("SearchGroupPostsV2 queries posts BEFORE authorizing — even when the result is discarded, the work done is observable, and any later refactor that returns early leaks the rows")
	}
}

/*
A private group answers exactly as a missing one.

If "private and you are not a member" and "no such group" differ at all, the
endpoint is an oracle: fire it at any uuid and the difference tells you whether
a private group exists there and whether you are in it. cross_post.go already
decided this, deliberately, for the same reason — its OutcomeUnavailable
collapses missing, deleted, archived and private-not-a-member into one answer.

So every refusal in readableGroupForSearch must be the ONE sentinel value.
A genuine failure of the membership read underneath is not a refusal and is
allowed through as itself: reporting a database outage as "group not found"
would tell people their group had been deleted.
*/
func TestPrivateGroupSearchIsIndistinguishableFromAMissingGroup(t *testing.T) {
	gate := codeOnly(funcSource(t, "group_post_search.go", "readableGroupForSearch"))

	returns := regexp.MustCompile(`return nil, ([^\n]+)`).FindAllStringSubmatch(gate, -1)
	if len(returns) < 3 {
		t.Fatalf("readableGroupForSearch has %d `return nil, …` sites; expected at least three (missing group, access refusal, propagated failure) — the guard below cannot be trusted if the shape has changed", len(returns))
	}
	for _, m := range returns {
		v := strings.TrimSpace(m[1])
		if v != "err" && v != "ErrGroupSearchUnavailable" {
			t.Errorf("readableGroupForSearch returns %q — every refusal must be the single ErrGroupSearchUnavailable. A distinct error gets a distinct status sooner or later, and then the endpoint tells anyone who asks whether a given private group exists.", v)
		}
	}
	if n := strings.Count(gate, "ErrGroupSearchUnavailable"); n < 2 {
		t.Errorf("ErrGroupSearchUnavailable is returned from %d branches, want at least 2 — the missing-group branch and the access-refusal branch must answer identically", n)
	}
	// No bespoke message minted in the gate: that is how the two answers drift.
	for _, mint := range []string{"fmt.Errorf(", "errors.New("} {
		if strings.Contains(gate, mint) {
			t.Errorf("readableGroupForSearch calls %s — a message minted here is a second, distinguishable answer", mint)
		}
	}

	// The sentinel itself must not name the reason. The message reaches the
	// client verbatim (see api.ErrorWithContext), so a word like "private" or
	// "member" in it is the leak the single value was supposed to prevent.
	msg := ErrGroupSearchUnavailable.Error()
	if !strings.Contains(msg, "not found") {
		t.Errorf("ErrGroupSearchUnavailable = %q — without \"not found\" handleServiceError maps it to 500 rather than the 404 a missing group gets", msg)
	}
	for _, leak := range []string{"forbidden", "private", "member", "banned"} {
		if strings.Contains(strings.ToLower(msg), leak) {
			t.Errorf("ErrGroupSearchUnavailable = %q contains %q — the message is returned to the client verbatim and names why", msg, leak)
		}
	}
}

/*
The endpoint returns the type that masks anonymous authors.

store.GroupPostV2 has a MarshalJSON (internal/store/anonymous.go) that
substitutes the per-post alias for the author_id of an anonymous post. The mask
lives on the TYPE so that a reader added later is masked for free. Return
anything else — a leaner struct, a view model, a map — and every anonymous post
in the results ships with its real author.
*/
func TestGroupPostSearchReturnsTheMaskingType(t *testing.T) {
	const masking = "[]store.GroupPostV2"

	feed := funcResults(t, "group.go", "GetGroupFeedV2")
	if !strings.Contains(feed, masking) {
		t.Fatalf("GetGroupFeedV2 returns %s, not %s — this guard compares search against the feed and has lost its reference point", feed, masking)
	}

	got := funcResults(t, "group_post_search.go", "SearchGroupPostsV2")
	if !strings.Contains(got, masking) {
		t.Fatalf("SearchGroupPostsV2 returns %s — it must return %s, whose MarshalJSON is the anonymity mask. Any other shape serialises the real author_id of every anonymous post that matched.", got, masking)
	}
	if got != feed {
		t.Errorf("search returns %s and the feed returns %s — the task is the same post shape as the feed", got, feed)
	}
}

// Whatever the caller sends, the store is asked for the group in the URL.
// A query parameter that could redirect the search at another group would walk
// straight past the authorization done above it.
func TestGroupPostSearchQueriesTheAuthorizedGroup(t *testing.T) {
	entry := codeOnly(funcSource(t, "group_post_search.go", "SearchGroupPostsV2"))
	call := entry[strings.Index(entry, "s.store.SearchGroupPostsV2("):]
	if !strings.Contains(call, "ctx, groupID,") {
		t.Fatalf("the store call does not pass groupID as the group to search: %q", strings.SplitN(call, "\n", 2)[0])
	}
	if !strings.Contains(call, "viewerKey(actorID)") {
		t.Error("the store call does not pass viewerKey(actorID) — the viewer_* flags would be wrong, or worse, another user's")
	}
}
