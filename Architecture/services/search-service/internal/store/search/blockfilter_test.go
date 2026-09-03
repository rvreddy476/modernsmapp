package search

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// Module 2 M2-P0-4 tests.
//
// The property under test is that a blocked user's documents are excluded
// INSIDE the OpenSearch query, on every surface, whatever shape that
// surface's query happens to be — not trimmed from the result page
// afterwards, which would break pagination and leak result counts.

func decodeQuery(t *testing.T, ctx context.Context, q map[string]interface{}, idField string) map[string]interface{} {
	t.Helper()
	buf, err := encodeQuery(ctx, q, idField)
	if err != nil {
		t.Fatalf("encodeQuery: %v", err)
	}
	var out map[string]interface{}
	if err := json.Unmarshal(buf.Bytes(), &out); err != nil {
		t.Fatalf("decode encoded query: %v", err)
	}
	return out
}

// mustNotTerms digs out the values excluded by must_not on the given field.
func mustNotTerms(t *testing.T, encoded map[string]interface{}, idField string) []string {
	t.Helper()
	query, ok := encoded["query"].(map[string]interface{})
	if !ok {
		return nil
	}
	boolClause, ok := query["bool"].(map[string]interface{})
	if !ok {
		return nil
	}
	raw, ok := boolClause["must_not"].([]interface{})
	if !ok {
		return nil
	}
	var out []string
	for _, clause := range raw {
		m, ok := clause.(map[string]interface{})
		if !ok {
			continue
		}
		terms, ok := m["terms"].(map[string]interface{})
		if !ok {
			continue
		}
		vals, ok := terms[idField].([]interface{})
		if !ok {
			continue
		}
		for _, v := range vals {
			if s, ok := v.(string); ok {
				out = append(out, s)
			}
		}
	}
	return out
}

func TestEncodeQuery_ExcludesBlockedAuthorsFromBoolQuery(t *testing.T) {
	ctx := WithBlockedIDs(context.Background(), []string{"blocker-1", "blocked-2"})
	q := map[string]interface{}{
		"size": 20,
		"query": map[string]interface{}{
			"bool": map[string]interface{}{
				"must":   []interface{}{map[string]interface{}{"match": map[string]interface{}{"text": "diwali"}}},
				"filter": publicApprovedFilterAny(),
			},
		},
	}

	got := mustNotTerms(t, decodeQuery(t, ctx, q, "author_id"), "author_id")
	if len(got) != 2 {
		t.Fatalf("expected both blocked ids excluded, got %v", got)
	}
}

// A function_score query (the ranked multi-entity surface) is not a bool
// query. The exclusion must still apply, and the original scoring must
// survive intact.
func TestEncodeQuery_WrapsNonBoolQueryPreservingScoring(t *testing.T) {
	ctx := WithBlockedIDs(context.Background(), []string{"blocked-1"})
	q := map[string]interface{}{
		"size": 20,
		"query": map[string]interface{}{
			"function_score": map[string]interface{}{
				"query":      map[string]interface{}{"multi_match": map[string]interface{}{"query": "diwali"}},
				"score_mode": "multiply",
			},
		},
	}

	encoded := decodeQuery(t, ctx, q, "author_id")
	if got := mustNotTerms(t, encoded, "author_id"); len(got) != 1 || got[0] != "blocked-1" {
		t.Fatalf("blocked author not excluded from ranked query: %v", got)
	}

	// The function_score must still be in there, under must.
	raw, _ := json.Marshal(encoded)
	if !strings.Contains(string(raw), "function_score") {
		t.Fatal("wrapping the query dropped the function_score — ranking would silently change")
	}
	if !strings.Contains(string(raw), "score_mode") {
		t.Fatal("wrapping the query dropped the scoring configuration")
	}
}

// A query with no `query` clause at all (pure sort/aggregation surfaces
// like popular posts) must still gain the exclusion.
func TestEncodeQuery_AddsExclusionWhenQueryClauseAbsent(t *testing.T) {
	ctx := WithBlockedIDs(context.Background(), []string{"blocked-1"})
	q := map[string]interface{}{"size": 20}

	if got := mustNotTerms(t, decodeQuery(t, ctx, q, "author_id"), "author_id"); len(got) != 1 {
		t.Fatalf("a query without an explicit query clause must still exclude blocked users, got %v", got)
	}
}

// An existing must_not must be preserved, not overwritten.
func TestEncodeQuery_PreservesExistingMustNot(t *testing.T) {
	ctx := WithBlockedIDs(context.Background(), []string{"blocked-1"})
	q := map[string]interface{}{
		"query": map[string]interface{}{
			"bool": map[string]interface{}{
				"must_not": []interface{}{
					map[string]interface{}{"terms": map[string]interface{}{"post_type": []interface{}{"draft"}}},
				},
			},
		},
	}

	encoded := decodeQuery(t, ctx, q, "author_id")
	if got := mustNotTerms(t, encoded, "author_id"); len(got) != 1 {
		t.Fatalf("block exclusion missing: %v", got)
	}
	raw, _ := json.Marshal(encoded)
	if !strings.Contains(string(raw), "draft") {
		t.Fatal("the pre-existing must_not clause was overwritten")
	}
}

// No blocks and no scope must leave the query identical but for the
// unconditional account-control exclusion, so this change cannot perturb
// ranking beyond that for the overwhelmingly common case.
//
// For a NON-posts index that holds for every scope. For posts, a resolved
// scope — anonymous included — now always carries the private-author
// exclusion (see TestEncodeQuery_PrivateAuthors*), so only the unscoped
// context matches this baseline there.
func TestEncodeQuery_NoBlocksLeavesQueryUnchanged(t *testing.T) {
	freshQuery := func() map[string]interface{} {
		return map[string]interface{}{
			"size":  20,
			"query": map[string]interface{}{"bool": map[string]interface{}{"must": []interface{}{}}},
		}
	}
	// Account control (auth-service deactivate / scheduled deletion): the
	// is_hidden exclusion (blockfilter.go) applies unconditionally, so it
	// is present even when there is nothing else to exclude.
	hiddenBaseline := func() map[string]interface{} {
		q := freshQuery()
		q["query"].(map[string]interface{})["bool"].(map[string]interface{})["must_not"] =
			[]interface{}{map[string]interface{}{"term": map[string]interface{}{"is_hidden": true}}}
		return q
	}
	baseline, _ := json.Marshal(hiddenBaseline())

	cases := []struct {
		name    string
		ctx     context.Context
		idField string
	}{
		{"posts, no scope resolved", context.Background(), "author_id"},
		{"users, no scope resolved", context.Background(), "user_id"},
		{"users, anonymous viewer", WithBlockedIDs(context.Background(), nil), "user_id"},
		{"users, viewer blocks nobody", WithBlockedIDs(context.Background(), []string{}), "user_id"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			buf, err := encodeQuery(tc.ctx, freshQuery(), tc.idField)
			if err != nil {
				t.Fatalf("encodeQuery: %v", err)
			}
			var round map[string]interface{}
			if err := json.Unmarshal(buf.Bytes(), &round); err != nil {
				t.Fatal(err)
			}
			got, _ := json.Marshal(round)
			if string(got) != string(baseline) {
				t.Fatalf("query changed when there is nothing to exclude:\n got %s\nwant %s", got, baseline)
			}
		})
	}
}

// --- private accounts --------------------------------------------------------

// mustNotClauses returns the raw must_not list of the top-level bool.
func mustNotClauses(t *testing.T, encoded map[string]interface{}) []interface{} {
	t.Helper()
	query, _ := encoded["query"].(map[string]interface{})
	boolClause, _ := query["bool"].(map[string]interface{})
	raw, _ := boolClause["must_not"].([]interface{})
	return raw
}

// findPrivateAuthorClause locates the private-author exclusion and returns
// the author ids it lets through (nil when it is the bare term form).
func findPrivateAuthorClause(t *testing.T, encoded map[string]interface{}) (found bool, allowed []string) {
	t.Helper()
	for _, c := range mustNotClauses(t, encoded) {
		m, _ := c.(map[string]interface{})
		if term, ok := m["term"].(map[string]interface{}); ok {
			if _, ok := term["author_is_private"]; ok {
				return true, nil
			}
		}
		if b, ok := m["bool"].(map[string]interface{}); ok {
			filters, _ := b["filter"].([]interface{})
			if len(filters) != 1 {
				continue
			}
			f, _ := filters[0].(map[string]interface{})
			term, _ := f["term"].(map[string]interface{})
			if _, ok := term["author_is_private"]; !ok {
				continue
			}
			inner, _ := b["must_not"].([]interface{})
			for _, i := range inner {
				im, _ := i.(map[string]interface{})
				terms, _ := im["terms"].(map[string]interface{})
				for _, v := range terms["author_id"].([]interface{}) {
					allowed = append(allowed, v.(string))
				}
			}
			return true, allowed
		}
	}
	return false, nil
}

// An anonymous viewer follows nobody: every private author's posts are
// excluded outright.
func TestEncodeQuery_PrivateAuthorsHiddenFromAnonymous(t *testing.T) {
	ctx := WithViewerScope(context.Background(), "", nil, nil)
	q := map[string]interface{}{"query": map[string]interface{}{"bool": map[string]interface{}{}}}
	found, allowed := findPrivateAuthorClause(t, decodeQuery(t, ctx, q, "author_id"))
	if !found {
		t.Fatal("anonymous posts query carries no private-author exclusion")
	}
	if len(allowed) != 0 {
		t.Fatalf("anonymous viewer was allowed private authors: %v", allowed)
	}
}

// A signed-in viewer sees private authors they follow, and themselves.
func TestEncodeQuery_PrivateAuthorsAllowFollowingAndSelf(t *testing.T) {
	ctx := WithViewerScope(context.Background(), "me", []string{"blocked-1"}, []string{"friend-1", "friend-2", "me", ""})
	q := map[string]interface{}{"query": map[string]interface{}{"bool": map[string]interface{}{}}}
	encoded := decodeQuery(t, ctx, q, "author_id")

	found, allowed := findPrivateAuthorClause(t, encoded)
	if !found {
		t.Fatal("posts query carries no private-author exclusion")
	}
	want := map[string]bool{"me": true, "friend-1": true, "friend-2": true}
	if len(allowed) != len(want) {
		t.Fatalf("allowed = %v, want exactly %v", allowed, want)
	}
	for _, id := range allowed {
		if !want[id] {
			t.Fatalf("unexpected allowed author %q", id)
		}
	}
	// The block exclusion must still be there alongside it.
	if got := mustNotTerms(t, encoded, "author_id"); len(got) != 1 || got[0] != "blocked-1" {
		t.Fatalf("block exclusion lost: %v", got)
	}
}

// The exclusion is a posts rule. Users remain searchable (a private
// profile is findable, its posts are not), and other owner fields are
// untouched.
func TestEncodeQuery_PrivateAuthorsClauseOnlyOnPosts(t *testing.T) {
	ctx := WithViewerScope(context.Background(), "me", nil, nil)
	for _, field := range []string{"user_id", "owner_id", "seller_id"} {
		q := map[string]interface{}{"query": map[string]interface{}{"bool": map[string]interface{}{}}}
		if found, _ := findPrivateAuthorClause(t, decodeQuery(t, ctx, q, field)); found {
			t.Fatalf("private-author clause applied to %s", field)
		}
	}
}

// A non-bool posts query (function_score) is wrapped so its scoring
// survives and the exclusion still applies.
func TestEncodeQuery_PrivateAuthorsWrapNonBoolQuery(t *testing.T) {
	ctx := WithViewerScope(context.Background(), "", nil, nil)
	q := map[string]interface{}{"query": map[string]interface{}{"function_score": map[string]interface{}{"query": map[string]interface{}{"match_all": map[string]interface{}{}}}}}
	encoded := decodeQuery(t, ctx, q, "author_id")
	if found, _ := findPrivateAuthorClause(t, encoded); !found {
		t.Fatal("exclusion missing after wrapping")
	}
	query := encoded["query"].(map[string]interface{})["bool"].(map[string]interface{})
	must, _ := query["must"].([]interface{})
	if len(must) != 1 {
		t.Fatalf("original function_score not preserved: %v", query)
	}
}

func TestViewerScopeAccessors(t *testing.T) {
	ctx := WithViewerScope(context.Background(), "me", []string{"b"}, []string{"f"})
	if ViewerIDForTest(ctx) != "me" || len(FollowingIDsForTest(ctx)) != 1 || len(BlockedIDsForTest(ctx)) != 1 {
		t.Fatal("scope accessors did not round-trip")
	}
}

// Each index must be filtered on the field that names its owning user.
// Filtering posts on user_id, say, would silently exclude nothing.
func TestOwnerFieldForIndex(t *testing.T) {
	cases := map[string]string{
		IndexPosts:       "author_id",
		IndexUsers:       "user_id",
		IndexCommunities: "owner_id",
		IndexChannels:    "owner_id",
		IndexProducts:    "seller_id",
		IndexHashtags:    "", // a hashtag belongs to no one
	}
	for index, want := range cases {
		if got := ownerFieldForIndex(index); got != want {
			t.Errorf("ownerFieldForIndex(%q) = %q, want %q", index, got, want)
		}
	}
}

// Hashtag documents have no owner, so passing an empty field must be a
// no-op for the BLOCK exclusion rather than producing `terms: {"": [...]}`,
// which would match nothing and quietly disable the filter it looks like it
// applies. The unconditional is_hidden exclusion (account control) still
// applies regardless — it targets a field name, not the viewer's block
// scope, and is a no-op on indices that never set it.
func TestEncodeQuery_EmptyIDFieldAppliesNoFilter(t *testing.T) {
	ctx := WithBlockedIDs(context.Background(), []string{"blocked-1"})
	q := map[string]interface{}{"size": 20, "query": map[string]interface{}{"bool": map[string]interface{}{}}}

	encoded := decodeQuery(t, ctx, q, "")
	raw, _ := json.Marshal(encoded)
	if strings.Contains(string(raw), "blocked-1") {
		t.Fatalf("no owner field means no block exclusion clause; got %s", raw)
	}
	if !strings.Contains(string(raw), "is_hidden") {
		t.Fatalf("is_hidden exclusion must still apply with no owner field; got %s", raw)
	}
}

// hasHiddenExclusion reports whether the encoded query's top-level bool
// must_not carries the unconditional {"term":{"is_hidden":true}} clause.
func hasHiddenExclusion(t *testing.T, encoded map[string]interface{}) bool {
	t.Helper()
	for _, c := range mustNotClauses(t, encoded) {
		m, ok := c.(map[string]interface{})
		if !ok {
			continue
		}
		term, ok := m["term"].(map[string]interface{})
		if !ok {
			continue
		}
		if v, ok := term["is_hidden"]; ok && v == true {
			return true
		}
	}
	return false
}

// Account control (auth-service deactivate / 30-day scheduled deletion):
// every query — posts and users alike — must exclude is_hidden documents,
// unconditionally, regardless of viewer scope. This is the query-side half
// of hide/unhide: PurgeStore.SetUserHidden (purge_adapter.go) flips the
// flag, this exclusion keeps a hidden user/their posts out of results while
// it is true, and unhide (flag back to false) makes them match again since
// the exclusion only ever targets is_hidden == true.
func TestEncodeQuery_HiddenAccountsExcludedFromPostsAndUsers(t *testing.T) {
	for _, tc := range []struct {
		name    string
		idField string
	}{
		{"posts", "author_id"},
		{"users", "user_id"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			q := map[string]interface{}{"query": map[string]interface{}{"bool": map[string]interface{}{}}}
			encoded := decodeQuery(t, context.Background(), q, tc.idField)
			if !hasHiddenExclusion(t, encoded) {
				t.Fatalf("%s query missing the unconditional is_hidden exclusion: %v", tc.name, encoded)
			}
		})
	}
}

// The exclusion applies even to an anonymous, unscoped viewer — hide/unhide
// is not gated on block-list or private-account resolution.
func TestEncodeQuery_HiddenExclusionAppliesRegardlessOfScope(t *testing.T) {
	q := map[string]interface{}{"query": map[string]interface{}{"bool": map[string]interface{}{}}}
	encoded := decodeQuery(t, WithBlockedIDs(context.Background(), nil), q, "author_id")
	if !hasHiddenExclusion(t, encoded) {
		t.Fatal("is_hidden exclusion must apply even for an anonymous viewer with an empty block list")
	}
}

func TestBlockScopeResolved(t *testing.T) {
	if BlockScopeResolved(context.Background()) {
		t.Fatal("a bare context must not report a resolved block scope")
	}
	if !BlockScopeResolved(WithBlockedIDs(context.Background(), nil)) {
		t.Fatal("an explicitly empty scope is still a resolved scope")
	}
}
