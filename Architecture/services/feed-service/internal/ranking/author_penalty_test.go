package ranking

import (
	"testing"
	"time"

	"github.com/google/uuid"
)

// The authorPenalty term — named in the scoring formula since v2.0, fed
// for the first time by the post "more" sheet's "Not interested" answers.

func TestAuthorPenalty(t *testing.T) {
	cases := []struct {
		net  float64
		want float64
	}{
		{0, 0}, {1, 0}, {5, 0}, // interested never boosts
		{-1, 0.25}, {-2, 0.5}, {-3, 0.5}, {-10, 0.5}, // capped
	}
	for _, tc := range cases {
		if got := AuthorPenalty(tc.net); got != tc.want {
			t.Errorf("AuthorPenalty(%v) = %v, want %v", tc.net, got, tc.want)
		}
	}
}

func TestScoreCandidates_NotInterestedAuthorRanksLower(t *testing.T) {
	now := time.Now()
	liked, disliked := uuid.New(), uuid.New()
	cands := []Candidate{
		{PostID: uuid.New(), AuthorID: disliked, CreatedAt: now, ContentType: "post", Source: "cold_start"},
		{PostID: uuid.New(), AuthorID: liked, CreatedAt: now, ContentType: "post"},
	}
	sigs := &ViewerSignals{
		AuthorAffinities: map[string]float64{},
		Velocities:       map[string]float64{},
		Interactions:     map[string]bool{},
		MutualFollows:    map[string]bool{},
		ContentQuality:   map[string]float64{},
		AuthorFeedback:   map[string]float64{disliked.String(): -1},
	}
	scored := ScoreCandidates(cands, sigs)
	if !(scored[0].Score < scored[1].Score) {
		t.Fatalf("not_interested author must score lower: %v vs %v", scored[0].Score, scored[1].Score)
	}
	if scored[1].Score-scored[0].Score != 0.25 {
		t.Fatalf("the only difference is one 0.25 penalty, got %v", scored[1].Score-scored[0].Score)
	}
	if scored[0].Source != "cold_start" {
		t.Fatal("Source must pass through scoring untouched")
	}
}

// "Don't recommend this account": an author-level mute is the maximum
// penalty regardless of post-level history; without a mute the post-level
// net passes through untouched.
func TestNetWithMute(t *testing.T) {
	cases := []struct {
		postNet float64
		muted   bool
		want    float64
	}{
		{0, false, 0}, {3, false, 3}, {-1, false, -1},
		{0, true, -2}, {-1, true, -3}, {-4, true, -6},
		{5, true, -2}, // earlier "Interested" taps never soften a mute
	}
	for _, tc := range cases {
		if got := NetWithMute(tc.postNet, tc.muted); got != tc.want {
			t.Errorf("NetWithMute(%v, %v) = %v, want %v", tc.postNet, tc.muted, got, tc.want)
		}
		if tc.muted && AuthorPenalty(NetWithMute(tc.postNet, tc.muted)) != 0.5 {
			t.Errorf("a muted author must carry the cap: net %v", tc.postNet)
		}
	}
}

func TestScoreCandidates_MutedAuthorCarriesMaxPenalty(t *testing.T) {
	now := time.Now()
	liked, muted := uuid.New(), uuid.New()
	cands := []Candidate{
		{PostID: uuid.New(), AuthorID: muted, CreatedAt: now, ContentType: "post"},
		{PostID: uuid.New(), AuthorID: liked, CreatedAt: now, ContentType: "post"},
	}
	sigs := &ViewerSignals{
		AuthorAffinities: map[string]float64{},
		Velocities:       map[string]float64{},
		Interactions:     map[string]bool{},
		MutualFollows:    map[string]bool{},
		ContentQuality:   map[string]float64{},
		// What service.mirrorAuthorFeedback writes for a muted author with
		// three earlier "Interested" answers: NetWithMute(3, true).
		AuthorFeedback: map[string]float64{muted.String(): NetWithMute(3, true)},
	}
	scored := ScoreCandidates(cands, sigs)
	if scored[1].Score-scored[0].Score != 0.5 {
		t.Fatalf("the only difference is the 0.5 cap, got %v", scored[1].Score-scored[0].Score)
	}
}
