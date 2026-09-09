package service

import (
	"encoding/json"
	"testing"

	"github.com/google/uuid"
)

// "Why you're seeing this post" — the derivation table. Every row is a
// (candidate source, post shape, velocity) the feed can actually produce
// today; the tokens are the client contract.
//
// The fanout rows changed on 2026-09-09. They used to assert
// ReasonFollowing here, which was a default rather than a derivation:
// deriveReason is pure, has no graph client, and a home-timeline row is
// written by fanout to followers UNION connections and never retracted on
// unfollow — so it cannot support the claim. Those rows now derive nothing
// and resolveFollowReasons settles them against graph-service; see
// reason_following_test.go.
func TestDeriveReason(t *testing.T) {
	viewer, other := uuid.New(), uuid.New()

	cases := []struct {
		name     string
		source   string
		post     HydratedPost
		velocity float64
		reason   string
		text     string
	}{
		{"a timeline row proves no follow and derives nothing", sourceTimeline,
			HydratedPost{AuthorID: other}, 0, "", ""},
		{"empty source is a timeline row and derives nothing", "",
			HydratedPost{AuthorID: other}, 0, "", ""},
		{"a repost derives nothing either — the reposter must be looked up", sourceTimeline,
			HydratedPost{AuthorID: other, IsRepost: true}, 0, "", ""},
		{"viewer's own post carries nothing", sourceTimeline, HydratedPost{AuthorID: viewer}, 0, "", ""},
		{"a repost of the viewer's own post is still about the reposter", sourceTimeline,
			HydratedPost{AuthorID: viewer, IsRepost: true}, 0, "", ""},
		{"circle_only view", sourceCircle, HydratedPost{AuthorID: other}, 0,
			ReasonConnection, "From your circle"},
		{"close-friends post", sourceTimeline, HydratedPost{AuthorID: other, Visibility: "trusted"}, 0,
			ReasonConnection, "Shared with close friends"},
		{"recommendation, no signal", sourceColdStart, HydratedPost{AuthorID: other}, 0,
			ReasonRecommended, "Suggested for you"},
		{"recommendation with category but no velocity", sourceColdStart, HydratedPost{AuthorID: other, Category: "comedy"}, 0,
			ReasonRecommended, "Suggested for you"},
		{"recommendation with velocity", sourceColdStart, HydratedPost{AuthorID: other}, 3.5,
			ReasonTrending, "Trending now"},
		{"recommendation with velocity in a category", sourceColdStart, HydratedPost{AuthorID: other, Category: "comedy"}, 3.5,
			"category:comedy", "Popular in Comedy"},
		{"unknown category id is title-cased", sourceColdStart, HydratedPost{AuthorID: other, Category: "cooking"}, 1,
			"category:cooking", "Popular in Cooking"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			reason, text := deriveReason(tc.source, tc.post, viewer, tc.velocity)
			if reason != tc.reason || text != tc.text {
				t.Fatalf("got (%q, %q), want (%q, %q)", reason, text, tc.reason, tc.text)
			}
		})
	}
}

// The wire form: `reason` and `reason_text` present when derived, both
// absent (not empty strings) on the viewer's own post, and the internal
// source never leaks.
func TestReasonWireShape(t *testing.T) {
	viewer := uuid.New()
	items := []FeedItem{
		{PostID: uuid.New(), AuthorID: uuid.New(), Source: sourceColdStart},
		{PostID: uuid.New(), AuthorID: viewer},
	}
	data := map[string]HydratedPost{}
	for _, it := range items {
		data[it.PostID.String()] = HydratedPost{ID: it.PostID, AuthorID: it.AuthorID}
	}
	s := &Service{}
	merged := s.mergeHydratedItems(items, data, nil, viewer)
	if len(merged) != 2 {
		t.Fatalf("merged %d, want 2", len(merged))
	}

	raw, _ := json.Marshal(merged)
	var wire []map[string]any
	if err := json.Unmarshal(raw, &wire); err != nil {
		t.Fatal(err)
	}
	if wire[0]["reason"] != ReasonRecommended || wire[0]["reason_text"] != "Suggested for you" {
		t.Fatalf("recommended row on the wire: %v", wire[0])
	}
	if _, ok := wire[1]["reason"]; ok {
		t.Fatalf("own post must omit reason, got %v", wire[1]["reason"])
	}
	if _, ok := wire[1]["reason_text"]; ok {
		t.Fatalf("own post must omit reason_text, got %v", wire[1]["reason_text"])
	}
	for _, row := range wire {
		if _, ok := row["source"]; ok {
			t.Fatal("candidate source must not be serialized")
		}
	}
}
