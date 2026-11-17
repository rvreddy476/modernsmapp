package search

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// reels-19 / V4-4: the trending aggregation counts only public, approved
// posts inside the window. A removal is a tombstone (visibility "removed"),
// so a deleted or downgraded post drops out with no decrement.
func TestBuildTrendingHashtagsQuery_PublicApprovedWindowOnly(t *testing.T) {
	now := time.Date(2026, 11, 17, 12, 0, 0, 0, time.UTC)
	raw, _ := json.Marshal(BuildTrendingHashtagsQuery(now, 20))
	s := string(raw)
	for _, must := range []string{
		`{"term":{"visibility":"public"}}`,
		`{"term":{"review_status":"approved"}}`,
		`"created_at":{"gte":"2026-11-16T12:00:00Z"}`,
		`"terms":{"field":"hashtags","size":20}`,
	} {
		if !strings.Contains(s, must) {
			t.Fatalf("query lacks %s: %s", must, s)
		}
	}
	if strings.Contains(s, `"must"`) {
		t.Fatalf("trending filters must be filters (no scoring clause): %s", s)
	}
}

func TestBuildTrendingHashtagsQuery_LimitIsBounded(t *testing.T) {
	for _, limit := range []int{0, -1, 51, 10000} {
		raw, _ := json.Marshal(BuildTrendingHashtagsQuery(time.Now(), limit))
		if !strings.Contains(string(raw), `"size":20}`) {
			t.Fatalf("limit %d not bounded: %s", limit, raw)
		}
	}
}
