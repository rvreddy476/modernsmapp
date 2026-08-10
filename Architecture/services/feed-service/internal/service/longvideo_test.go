package service

import (
	"testing"
	"time"

	"github.com/google/uuid"
)

func lvTestService() *Service {
	return &Service{lvTiers: loadLVTiers()}
}

func mixedCandidates(n int, everyNthLV int) []FeedItem {
	base := time.Now()
	items := make([]FeedItem, 0, n)
	for i := 0; i < n; i++ {
		ct := "post"
		if everyNthLV > 0 && i%everyNthLV == 0 {
			ct = "long_video"
		}
		items = append(items, FeedItem{
			PostID:      uuid.New(),
			AuthorID:    uuid.New(),
			CreatedAt:   base.Add(-time.Duration(i) * time.Minute),
			ContentType: ct,
			Score:       float64(n - i),
		})
	}
	return items
}

func countLV(items []FeedItem) int {
	n := 0
	for _, it := range items {
		if isLongVideoType(it.ContentType) {
			n++
		}
	}
	return n
}

// TestHiddenIsHardExclusion: hidden yields zero long videos regardless of
// input composition, and never drops non-video posts.
func TestHiddenIsHardExclusion(t *testing.T) {
	s := lvTestService()
	in := mixedCandidates(40, 2) // 50% long video
	out := s.applyLongVideoFrequency(in, "hidden", true)
	if countLV(out) != 0 {
		t.Fatalf("hidden must remove all long videos, found %d", countLV(out))
	}
	if len(out) != 20 {
		t.Fatalf("hidden must keep all %d non-video posts, kept %d", 20, len(out))
	}
	// legacy "video" spelling is covered too
	in2 := []FeedItem{{ContentType: "video"}, {ContentType: "post"}}
	if got := s.applyLongVideoFrequency(in2, "hidden", false); len(got) != 1 || got[0].ContentType != "post" {
		t.Fatalf("legacy 'video' content type must be excluded under hidden")
	}
}

// TestCompositionMonotonicity: with the same 50%-LV input, the top-of-feed
// long-video share must increase monotonically reduced → balanced →
// preferred (Codex acceptance fixture).
func TestCompositionMonotonicity(t *testing.T) {
	s := lvTestService()
	page := 20
	share := func(freq string) int {
		in := mixedCandidates(60, 2)
		out := s.applyLongVideoFrequency(in, freq, true)
		if len(out) != 60 {
			t.Fatalf("%s: demote-not-drop violated: %d items in, %d out", freq, 60, len(out))
		}
		return countLV(out[:page])
	}
	r, b, p := share("reduced"), share("balanced"), share("preferred")
	if !(r <= b && b <= p) {
		t.Fatalf("composition not monotonic: reduced=%d balanced=%d preferred=%d", r, b, p)
	}
	// Targets: 10% / 25% / 50% of a 20-item page → ≤2 / ≤5 / ≤10 while
	// non-video supply lasts.
	if r > 2 {
		t.Errorf("reduced page share %d exceeds 10%% target of 2", r)
	}
	if b > 5 {
		t.Errorf("balanced page share %d exceeds 25%% target of 5", b)
	}
	if p > 10 {
		t.Errorf("preferred page share %d exceeds 50%% target of 10", p)
	}
}

// TestCapIsTargetNotDrop: when only long videos remain, they still surface
// (the cap demotes, it never deletes).
func TestCapIsTargetNotDrop(t *testing.T) {
	s := lvTestService()
	in := mixedCandidates(10, 1) // 100% long video
	out := s.applyLongVideoFrequency(in, "reduced", false)
	if len(out) != 10 || countLV(out) != 10 {
		t.Fatalf("all-LV input must pass through entirely, got %d items (%d LV)", len(out), countLV(out))
	}
}

// TestRankedMultiplierReorders: in ranked mode the reduced multiplier
// pushes an initially top-ranked long video below stronger text posts;
// preferred boosts it upward.
func TestRankedMultiplierReorders(t *testing.T) {
	s := lvTestService()
	build := func() []FeedItem {
		return []FeedItem{
			{PostID: uuid.New(), ContentType: "long_video", Score: 100, CreatedAt: time.Now()},
			{PostID: uuid.New(), ContentType: "post", Score: 90, CreatedAt: time.Now()},
			{PostID: uuid.New(), ContentType: "post", Score: 80, CreatedAt: time.Now()},
		}
	}
	out := s.applyLongVideoFrequency(build(), "reduced", true)
	if out[0].ContentType == "long_video" {
		t.Fatalf("reduced multiplier (0.25) should demote the 100-score LV below 90/80 text posts")
	}
	out = s.applyLongVideoFrequency(build(), "preferred", true)
	if out[0].ContentType != "long_video" {
		t.Fatalf("preferred multiplier should keep the top LV on top")
	}
}

// TestChronologicalUnaffectedByMultiplier: chronological mode must not
// reorder by score — only the composition target applies.
func TestChronologicalUnaffectedByMultiplier(t *testing.T) {
	s := lvTestService()
	in := []FeedItem{
		{PostID: uuid.New(), ContentType: "post", Score: 0, CreatedAt: time.Now()},
		{PostID: uuid.New(), ContentType: "post", Score: 0, CreatedAt: time.Now().Add(-time.Minute)},
	}
	out := s.applyLongVideoFrequency(in, "balanced", false)
	if out[0].PostID != in[0].PostID || out[1].PostID != in[1].PostID {
		t.Fatalf("no-LV chronological input must pass through in order")
	}
}

func TestValidLongVideoFrequency(t *testing.T) {
	for _, ok := range []string{"hidden", "reduced", "balanced", "preferred"} {
		if !ValidLongVideoFrequency(ok) {
			t.Errorf("%s should be valid", ok)
		}
	}
	for _, bad := range []string{"", "off", "HIDDEN", "always"} {
		if ValidLongVideoFrequency(bad) {
			t.Errorf("%s should be invalid", bad)
		}
	}
}
