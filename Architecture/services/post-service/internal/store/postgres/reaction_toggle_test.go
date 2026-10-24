package postgres

import "testing"

// nextReactionToggle (heart only, 24 Oct 2026): on a post, any existing
// reaction toggles off, so a viewer whose old emoji reads as the heart
// un-hearts on the next tap instead of being switched to "like".
func TestNextReactionToggle(t *testing.T) {
	cases := []struct {
		name, target, existing, requested string
		want                              ReactionToggle
	}{
		{"post: none adds the heart", "post", "", "like", ReactionToggle{NewType: "like", IsSet: true}},
		{"post: heart toggles off", "post", "like", "like", ReactionToggle{PrevType: "like"}},
		{"post: old emoji toggles off", "post", "love", "like", ReactionToggle{PrevType: "love"}},
		{"post: old angry toggles off", "post", "angry", "like", ReactionToggle{PrevType: "angry"}},
		// Any other target keeps the multi-reaction switch.
		{"other: different type switches", "story", "love", "like", ReactionToggle{NewType: "like", PrevType: "love", IsSet: true}},
		{"other: same type toggles off", "story", "like", "like", ReactionToggle{PrevType: "like"}},
		{"other: none adds", "story", "", "haha", ReactionToggle{NewType: "haha", IsSet: true}},
	}
	for _, tc := range cases {
		if got := nextReactionToggle(tc.target, tc.existing, tc.requested); got != tc.want {
			t.Errorf("%s: got %+v, want %+v", tc.name, got, tc.want)
		}
	}
}
