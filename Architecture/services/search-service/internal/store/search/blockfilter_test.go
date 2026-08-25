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

// No blocks and no scope must leave the query byte-identical, so this
// change cannot perturb ranking for the overwhelmingly common case.
func TestEncodeQuery_NoBlocksLeavesQueryUnchanged(t *testing.T) {
	q := map[string]interface{}{
		"size":  20,
		"query": map[string]interface{}{"bool": map[string]interface{}{"must": []interface{}{}}},
	}
	baseline, _ := json.Marshal(q)

	for _, ctx := range []context.Context{
		context.Background(),                             // no scope resolved
		WithBlockedIDs(context.Background(), nil),        // anonymous viewer
		WithBlockedIDs(context.Background(), []string{}), // viewer blocks nobody
	} {
		buf, err := encodeQuery(ctx, q, "author_id")
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
// no-op rather than producing `terms: {"": [...]}`, which would match
// nothing and quietly disable the filter it looks like it applies.
func TestEncodeQuery_EmptyIDFieldAppliesNoFilter(t *testing.T) {
	ctx := WithBlockedIDs(context.Background(), []string{"blocked-1"})
	q := map[string]interface{}{"size": 20, "query": map[string]interface{}{"bool": map[string]interface{}{}}}

	encoded := decodeQuery(t, ctx, q, "")
	raw, _ := json.Marshal(encoded)
	if strings.Contains(string(raw), "must_not") {
		t.Fatalf("no owner field means no exclusion clause; got %s", raw)
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
