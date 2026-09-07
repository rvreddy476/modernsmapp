package ranking

import (
	"math"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestNormalizeTopicAffinity_AnchoredOnTheColdStartFloor(t *testing.T) {
	// The neutral point is the ranker's own cold-start interest floor,
	// not zero. A token scored there must contribute nothing, or writing
	// the topic hash would re-rank subjects the viewer has no opinion
	// about.
	if got := normalizeTopicAffinity(0.3); math.Abs(got) > 1e-9 {
		t.Errorf("neutral affinity normalised to %v, want 0", got)
	}
	if got := normalizeTopicAffinity(1.0); math.Abs(got-1) > 1e-9 {
		t.Errorf("maximum affinity normalised to %v, want +1", got)
	}
	if got := normalizeTopicAffinity(0.0); math.Abs(got+1) > 1e-9 {
		t.Errorf("zero affinity normalised to %v, want -1", got)
	}
	// Monotone across the join.
	prev := math.Inf(-1)
	for a := 0.0; a <= 1.0001; a += 0.01 {
		got := normalizeTopicAffinity(a)
		if got < prev-1e-12 {
			t.Fatalf("normalizeTopicAffinity is not monotone at %v (%v after %v)", a, got, prev)
		}
		prev = got
	}
}

func TestTopicInterest_StrongestTokenWins(t *testing.T) {
	affinity := map[string]float64{
		"cat:music":    0.9,  // loves
		"tag:2026":     0.32, // barely anything
		"cat:politics": 0.05, // actively dislikes
	}

	// A liked topic is not diluted by incidental tags the viewer has no
	// strong feeling about.
	loved := TopicInterest([]string{"cat:music", "tag:2026", "tag:unknown"}, affinity)
	alone := TopicInterest([]string{"cat:music"}, affinity)
	if math.Abs(loved-alone) > 1e-9 {
		t.Errorf("incidental tags diluted the signal: %.4f with them, %.4f alone", loved, alone)
	}

	// A strongly rejected topic is not rescued by an INCIDENTAL like: the
	// rule is strongest-by-magnitude, so a mild positive cannot bury a
	// strong negative. This is what makes "not interested" mean something.
	if got := TopicInterest([]string{"tag:2026", "cat:politics"}, affinity); got >= 0 {
		t.Errorf("a rejected topic beside a weak like scored %.4f, want negative", got)
	}

	// Two comparably strong opposing opinions: the stronger one wins,
	// which here is the like (0.9 is 0.857 above neutral, 0.05 is 0.833
	// below it). This is the deliberate behaviour and not a bug — a
	// veto rule would let one incidental tag from a disliked subject sink
	// a video the viewer would otherwise have loved.
	if got := TopicInterest([]string{"cat:music", "cat:politics"}, affinity); got <= 0 {
		t.Errorf("the marginally stronger opinion did not win: %.4f", got)
	}
	// ...and flipping which is stronger flips the answer.
	affinity["cat:politics"] = 0.0
	if got := TopicInterest([]string{"cat:music", "cat:politics"}, affinity); got >= 0 {
		t.Errorf("a total rejection did not outweigh a strong like: %.4f", got)
	}
}

func TestTopicBoost_LiftsAStrangerAboveAFollowedAccount(t *testing.T) {
	// The point of the topical term: a video about something the viewer
	// demonstrably watches, by a creator they have never seen, should be
	// able to beat an equally fresh video by someone they follow but have
	// no particular affinity for. Author affinity alone can never do this.
	now := time.Now()
	stranger, followed := uuid.New(), uuid.New()
	strangerPost := Candidate{PostID: uuid.New(), AuthorID: stranger, CreatedAt: now, ContentType: "long_video"}
	followedPost := Candidate{PostID: uuid.New(), AuthorID: followed, CreatedAt: now, ContentType: "long_video"}

	sigs := coldSignals()
	sigs.TopicAffinity["cat:music"] = 0.95
	sigs.PostTopics[strangerPost.PostID.String()] = []string{"cat:music"}

	scored := ScoreCandidates([]Candidate{followedPost, strangerPost}, sigs)
	if scored[1].Score <= scored[0].Score {
		t.Errorf("the on-topic stranger scored %.4f, the off-topic followed account %.4f — the topical term is not biting",
			scored[1].Score, scored[0].Score)
	}

	// But it must not be able to outrank a strong author affinity on its
	// own: a topic match is a nudge, not a takeover.
	sigs.AuthorAffinities[followed.String()] = 0.95
	scored = ScoreCandidates([]Candidate{followedPost, strangerPost}, sigs)
	if scored[1].Score >= scored[0].Score {
		t.Errorf("a topic match (%.4f) outranked a strongly-liked author (%.4f); TopicWeight is too large",
			scored[1].Score, scored[0].Score)
	}
}

func TestJaccard(t *testing.T) {
	cases := []struct {
		name string
		a, b []string
		want float64
	}{
		{"identical", []string{"cat:music", "tag:guitar"}, []string{"cat:music", "tag:guitar"}, 1},
		{"disjoint", []string{"cat:music"}, []string{"cat:food"}, 0},
		{"half", []string{"cat:music", "tag:guitar"}, []string{"cat:music"}, 0.5},
		// Two untagged posts are NOT maximally related, whatever set
		// theory says about the empty set.
		{"both empty", nil, nil, 0},
		{"one empty", []string{"cat:music"}, nil, 0},
		// A shared tag out of two beats a shared tag out of many.
		{"diluted", []string{"a", "b", "c", "d", "e"}, []string{"a"}, 0.2},
	}
	for _, tc := range cases {
		if got := jaccard(tc.a, tc.b); math.Abs(got-tc.want) > 1e-9 {
			t.Errorf("%s: jaccard = %v, want %v", tc.name, got, tc.want)
		}
	}
	// Duplicates on one side must not inflate the union.
	if got := jaccard([]string{"a"}, []string{"a", "a", "a"}); math.Abs(got-1) > 1e-9 {
		t.Errorf("duplicate tokens changed the overlap: %v, want 1", got)
	}
}

func TestSeedRelatedness(t *testing.T) {
	author := uuid.New()
	other := uuid.New()
	seed := &Seed{PostID: uuid.New(), AuthorID: author, Topics: []string{"cat:music", "tag:guitar"}}

	sameEverything := SeedRelatedness(seed, author.String(), []string{"cat:music", "tag:guitar"})
	if math.Abs(sameEverything-1) > 1e-9 {
		t.Errorf("same author and same topics = %v, want 1", sameEverything)
	}
	sameAuthorOnly := SeedRelatedness(seed, author.String(), nil)
	if math.Abs(sameAuthorOnly-seedAuthorShare) > 1e-9 {
		t.Errorf("same author, no topics = %v, want %v", sameAuthorOnly, seedAuthorShare)
	}
	sameTopicOnly := SeedRelatedness(seed, other.String(), []string{"cat:music", "tag:guitar"})
	if math.Abs(sameTopicOnly-seedTopicShare) > 1e-9 {
		t.Errorf("different author, same topics = %v, want %v", sameTopicOnly, seedTopicShare)
	}
	if sameAuthorOnly <= sameTopicOnly {
		t.Errorf("the creator half (%v) should outweigh the topic half (%v): the author is always known, the topics usually are not",
			sameAuthorOnly, sameTopicOnly)
	}
	if got := SeedRelatedness(seed, other.String(), nil); got != 0 {
		t.Errorf("unrelated candidate = %v, want 0", got)
	}
}

func TestExcludeSeen_DropsTheSeedAndWhatTheViewerFinished(t *testing.T) {
	seedID, finishedID, freshID := uuid.New(), uuid.New(), uuid.New()
	seed := &Seed{PostID: seedID, AuthorID: uuid.New()}
	sigs := coldSignals()
	sigs.Completions[finishedID.String()] = true

	got := ExcludeSeen([]Candidate{
		{PostID: seedID},
		{PostID: finishedID},
		{PostID: freshID},
	}, seed, sigs)

	if len(got) != 1 || got[0].PostID != freshID {
		t.Fatalf("ExcludeSeen kept %v, want only the unwatched %v", got, freshID)
	}
}

func TestDiversity_StillBitesWhenOneCreatorIsHeavilyFavoured(t *testing.T) {
	// Personalisation must not collapse the feed into one creator. The
	// author penalty and the consecutive-author cap already exist; this
	// asserts a maximum affinity plus a maximum topic match cannot
	// overpower them.
	now := time.Now()
	favourite := uuid.New()
	sigs := coldSignals()
	sigs.AuthorAffinities[favourite.String()] = 1.0
	sigs.TopicAffinity["cat:music"] = 1.0

	cands := make([]Candidate, 0, 20)
	for i := 0; i < 10; i++ {
		p := uuid.New()
		sigs.PostTopics[p.String()] = []string{"cat:music"}
		cands = append(cands, Candidate{
			PostID: p, AuthorID: favourite,
			CreatedAt: now.Add(-time.Duration(i) * time.Minute), ContentType: "long_video",
		})
	}
	for i := 0; i < 10; i++ {
		cands = append(cands, Candidate{
			PostID: uuid.New(), AuthorID: uuid.New(),
			CreatedAt: now.Add(-time.Duration(i) * time.Minute), ContentType: "long_video",
		})
	}

	page := ApplyDiversity(ScoreCandidates(cands, sigs), 12)
	if len(page) != 12 {
		t.Fatalf("page had %d items, want 12", len(page))
	}
	run := 0
	for i, c := range page {
		if c.AuthorID == favourite {
			run++
			if run > 3 {
				t.Fatalf("the favourite creator took %d consecutive slots ending at position %d; the diversity cap is 3", run, i)
			}
		} else {
			run = 0
		}
	}
}
