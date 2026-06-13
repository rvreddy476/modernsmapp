package search

import (
	"testing"
)

// TestBuildFunctionScoreQuery_AnonymousVsLoggedIn verifies the two
// halves of the ranking design contract:
//
//  1. Every entity ships with engagement_score (log1p) + recency (gauss).
//  2. The author-affinity function is only added when the caller passes
//     a non-empty FollowedAuthorIDs (i.e. logged-in viewers).
func TestBuildFunctionScoreQuery_AnonymousVsLoggedIn(t *testing.T) {
	anonymous := buildFunctionScoreQuery(EntityPosts, "hello", RankedSearchOptions{Limit: 20})
	fs := mustFunctionScore(t, anonymous)
	if got := len(fs["functions"].([]map[string]any)); got != 2 {
		t.Fatalf("anonymous viewer: expected 2 functions (engagement + recency), got %d", got)
	}
	if fs["score_mode"] != "multiply" || fs["boost_mode"] != "multiply" {
		t.Fatalf("score_mode/boost_mode must be multiply; got %v / %v", fs["score_mode"], fs["boost_mode"])
	}

	loggedIn := buildFunctionScoreQuery(EntityPosts, "hello", RankedSearchOptions{
		Limit:             20,
		FollowedAuthorIDs: []string{"a", "b", "c"},
	})
	fs2 := mustFunctionScore(t, loggedIn)
	if got := len(fs2["functions"].([]map[string]any)); got != 3 {
		t.Fatalf("logged-in viewer: expected 3 functions (engagement + recency + affinity), got %d", got)
	}
	// Third function must be a filter+weight=1.5 on author_id terms.
	aff := fs2["functions"].([]map[string]any)[2]
	if aff["weight"] != 1.5 {
		t.Fatalf("affinity weight should be 1.5, got %v", aff["weight"])
	}
	filt, ok := aff["filter"].(map[string]any)
	if !ok {
		t.Fatalf("affinity filter missing")
	}
	terms, _ := filt["terms"].(map[string]any)
	if _, ok := terms["author_id"]; !ok {
		t.Fatalf("posts entity should filter on author_id; got %v", terms)
	}
}

// TestBuildFunctionScoreQuery_UsesEntitySpecificFields makes sure each
// entity feeds its own field list into multi_match — otherwise we'd be
// running ranked posts queries against user fields.
func TestBuildFunctionScoreQuery_UsesEntitySpecificFields(t *testing.T) {
	cases := []struct {
		entity     string
		wantField  string
		affinityOn string // "" = no affinity (hashtags)
	}{
		{EntityPosts, "text^3", "author_id"},
		{EntityUsers, "username^4", "user_id"},
		{EntityHashtags, "hashtag^4", ""},
		{EntityProducts, "title^3", "seller_id"},
		{EntityCommunities, "name^3", "owner_id"},
		{EntityChannels, "name^3", "owner_id"},
	}
	for _, tc := range cases {
		t.Run(tc.entity, func(t *testing.T) {
			q := buildFunctionScoreQuery(tc.entity, "x", RankedSearchOptions{
				Limit:             10,
				FollowedAuthorIDs: []string{"u1"},
			})
			fs := mustFunctionScore(t, q)
			boolQ := fs["query"].(map[string]any)["bool"].(map[string]any)
			must := boolQ["must"].([]any)
			mm := must[0].(map[string]any)["multi_match"].(map[string]any)
			fields := mm["fields"].([]string)
			if !contains(fields, tc.wantField) {
				t.Fatalf("entity %q: expected fields to contain %q; got %v", tc.entity, tc.wantField, fields)
			}

			// Affinity check — hashtags should never carry a 3rd function.
			fns := fs["functions"].([]map[string]any)
			if tc.affinityOn == "" {
				if len(fns) != 2 {
					t.Fatalf("entity %q: expected no affinity function, got %d functions", tc.entity, len(fns))
				}
				return
			}
			if len(fns) != 3 {
				t.Fatalf("entity %q: expected affinity function, got %d functions", tc.entity, len(fns))
			}
			affFilter := fns[2]["filter"].(map[string]any)["terms"].(map[string]any)
			if _, ok := affFilter[tc.affinityOn]; !ok {
				t.Fatalf("entity %q: affinity should match on %q; got %v", tc.entity, tc.affinityOn, affFilter)
			}
		})
	}
}

// TestParseTypes ensures the multi-entity dispatcher only honors known
// entity keys and de-dupes.
func TestParseTypes_KnownEntitiesOnly(t *testing.T) {
	// (parseTypes lives in the http package; this is a thin smoke test
	// on the EntityToIndex lookup which gates the same set.)
	for _, e := range AllEntities {
		if EntityToIndex(e) == "" {
			t.Fatalf("AllEntities member %q is not in EntityToIndex", e)
		}
	}
	if EntityToIndex("nope") != "" {
		t.Fatalf("EntityToIndex should reject unknown keys")
	}
}

// TestComputeEngagementScore covers the 6 canonical formulas. These
// must match the Kafka consumer's runtime arithmetic exactly — drift
// here is a ranking bug.
func TestComputeEngagementScore(t *testing.T) {
	cases := []struct {
		entity string
		c      engagementCounters
		want   float64
	}{
		{EntityPosts, engagementCounters{Likes: 10, Comments: 2, Shares: 1, Bookmarks: 3}, 10 + 2*2 + 3*1 + 3},
		{EntityUsers, engagementCounters{Followers: 100, Posts: 20}, 100 + 0.5*20},
		{EntityHashtags, engagementCounters{UseCount: 42}, 42},
		{EntityProducts, engagementCounters{Views: 50, Purchases: 4}, 50 + 5*4},
		{EntityCommunities, engagementCounters{Members: 75}, 75},
		{EntityChannels, engagementCounters{Subscribers: 88}, 88},
	}
	for _, tc := range cases {
		got := computeEngagementScore(tc.entity, tc.c)
		if got != tc.want {
			t.Fatalf("entity %s: want %v, got %v", tc.entity, tc.want, got)
		}
	}
}

// mustFunctionScore unwraps the function_score block; fatals on the
// wrong shape so the assertions stay readable.
func mustFunctionScore(t *testing.T, q map[string]any) map[string]any {
	t.Helper()
	outer, ok := q["query"].(map[string]any)
	if !ok {
		t.Fatalf("query: expected object, got %T", q["query"])
	}
	fs, ok := outer["function_score"].(map[string]any)
	if !ok {
		t.Fatalf("expected function_score wrapper, got %v", outer)
	}
	return fs
}

func contains(xs []string, target string) bool {
	for _, x := range xs {
		if x == target {
			return true
		}
	}
	return false
}
