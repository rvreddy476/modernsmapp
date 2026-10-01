package postgres

// The counter arithmetic behind like_count and helpful_count: only a LIKE
// and only a HELPFUL vote ever move a public number.

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestLikeDelta(t *testing.T) {
	for _, tc := range []struct {
		prev, next string
		want       int64
	}{
		{"", ReactionLike, 1},
		{"", ReactionDislike, 0},
		{ReactionLike, ReactionLike, 0},
		{ReactionLike, ReactionDislike, -1},
		{ReactionDislike, ReactionLike, 1},
		{ReactionDislike, ReactionDislike, 0},
		{ReactionLike, "", -1},
		{ReactionDislike, "", 0},
		{"", "", 0},
	} {
		if got := likeDelta(tc.prev, tc.next); got != tc.want {
			t.Errorf("likeDelta(%q→%q) = %d, want %d", tc.prev, tc.next, got, tc.want)
		}
	}
}

func TestHelpfulDelta(t *testing.T) {
	y, n := true, false
	for _, tc := range []struct {
		name       string
		prev, next *bool
		want       int
	}{
		{"none→helpful", nil, &y, 1},
		{"none→not", nil, &n, 0},
		{"helpful→not", &y, &n, -1},
		{"not→helpful", &n, &y, 1},
		{"helpful→helpful", &y, &y, 0},
		{"not→not", &n, &n, 0},
		{"helpful→none", &y, nil, -1},
		{"not→none", &n, nil, 0},
	} {
		if got := helpfulDelta(tc.prev, tc.next); got != tc.want {
			t.Errorf("helpfulDelta %s = %d, want %d", tc.name, got, tc.want)
		}
	}
}

func TestReviewOrderBy(t *testing.T) {
	if got := reviewOrderBy(ReviewSortHelpful); !strings.HasPrefix(got, "helpful_count DESC, created_at DESC") {
		t.Errorf("helpful: %s", got)
	}
	if got := reviewOrderBy(""); !strings.HasPrefix(got, "helpful_count DESC") {
		t.Errorf("default: %s", got)
	}
	if got := reviewOrderBy(ReviewSortRecent); !strings.HasPrefix(got, "created_at DESC") {
		t.Errorf("recent: %s", got)
	}
}

// The wire shape of the detail read's viewer_reaction and the list's vote
// fields.
func TestEngagementWireShape(t *testing.T) {
	one := int64(1)
	b, _ := json.Marshal(Product{Title: "x", LikeCount: &one, ViewerReaction: &ViewerReaction{}})
	if !strings.Contains(string(b), `"viewer_reaction":null`) || !strings.Contains(string(b), `"like_count":1`) {
		t.Errorf("signed-in, no reaction: %s", b)
	}
	b, _ = json.Marshal(Product{Title: "x", ViewerReaction: &ViewerReaction{Kind: ReactionDislike}})
	if !strings.Contains(string(b), `"viewer_reaction":"dislike"`) {
		t.Errorf("own dislike: %s", b)
	}
	b, _ = json.Marshal(Product{Title: "x"})
	if strings.Contains(string(b), "viewer_reaction") || strings.Contains(string(b), "like_count") || strings.Contains(string(b), "share_count") {
		t.Errorf("a surface that read none of them carries them: %s", b)
	}
	b, _ = json.Marshal(ReviewRow{Review: &Review{Rating: 4}})
	if !strings.Contains(string(b), `"helpful_count":0`) || !strings.Contains(string(b), `"viewer_vote":null`) ||
		strings.Count(string(b), "helpful_count") != 1 {
		t.Errorf("review row: %s", b)
	}
}
