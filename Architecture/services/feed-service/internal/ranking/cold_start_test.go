package ranking

import (
	"math"
	"testing"
	"time"

	"github.com/google/uuid"
)

// A viewer with no history is the common case, not the edge case.
//
// Personalisation that improves the feed for people the system knows and
// breaks it for everyone else is not an improvement — and every one of the
// new signals is absent for a brand-new account, for anyone who has not
// watched anything yet, and for everyone at all if the warmer stops or
// Redis is flushed. So "absent signals degrade to exactly the previous
// behaviour" is a property worth pinning down, not a hope.
//
// These tests hold the whole personalization change to that: an empty
// ViewerSignals must score identically to the pre-change formula, and the
// ordering and page size a cold viewer gets must be unchanged.

// coldSignals is what LoadSignals returns for a viewer with nothing in
// Redis: every map allocated, every map empty, no seed. Constructed the
// way the loader constructs it so the test cannot drift from reality by
// omitting a field the loader always initialises.
func coldSignals() *ViewerSignals {
	return &ViewerSignals{
		AuthorAffinities: map[string]float64{},
		Velocities:       map[string]float64{},
		Interactions:     map[string]bool{},
		MutualFollows:    map[string]bool{},
		ContentQuality:   map[string]float64{},
		AuthorFeedback:   map[string]float64{},
		TopicAffinity:    map[string]float64{},
		PostTopics:       map[string][]string{},
		Completions:      map[string]bool{},
	}
}

// legacyScore is the scoring formula EXACTLY as it stood before the
// personalization work, transcribed rather than called. It is the control:
// if ScoreCandidates and this disagree for a viewer with no signals, the
// change has moved the ground under everyone who has no history.
func legacyScore(c Candidate, now time.Time) float64 {
	interest := 0.3
	ageHours := now.Sub(c.CreatedAt).Hours()
	recency := math.Exp(-0.05 * ageHours)
	if ageHours < 0.5 {
		recency = math.Max(recency, 0.9)
	}
	mediaBoost := 1.0
	switch c.ContentType {
	case "image":
		mediaBoost = 1.2
	case "reel":
		mediaBoost = 1.3
	case "video":
		mediaBoost = 1.1 // no dwell data
	}
	socialProximity := 0.1
	return (interest * recency * mediaBoost) + socialProximity
}

func TestColdViewer_ScoresMatchThePreviousFormula(t *testing.T) {
	now := time.Now()
	// Content types chosen to cover every branch of the media switch that
	// the old formula knew about. "flick" and "long_video" are excluded
	// here deliberately — they are the two the old switch got WRONG, and
	// they have their own test below.
	cands := []Candidate{
		{PostID: uuid.New(), AuthorID: uuid.New(), CreatedAt: now.Add(-10 * time.Minute), ContentType: "post"},
		{PostID: uuid.New(), AuthorID: uuid.New(), CreatedAt: now.Add(-3 * time.Hour), ContentType: "image"},
		{PostID: uuid.New(), AuthorID: uuid.New(), CreatedAt: now.Add(-20 * time.Hour), ContentType: "reel"},
		{PostID: uuid.New(), AuthorID: uuid.New(), CreatedAt: now.Add(-2 * time.Hour), ContentType: "video"},
	}

	scored := ScoreCandidates(cands, coldSignals())
	if len(scored) != len(cands) {
		t.Fatalf("scored %d candidates, want %d", len(scored), len(cands))
	}
	for i, got := range scored {
		want := legacyScore(cands[i], now)
		// The tolerance covers only the microseconds between the test's
		// `now` and the scorer's own; it is far tighter than any of the
		// new terms could contribute if one of them were live.
		if math.Abs(got.Score-want) > 1e-6 {
			t.Errorf("cold viewer, %s: score %.9f, want the pre-change %.9f (a new term is firing when it should not)",
				cands[i].ContentType, got.Score, want)
		}
	}
}

func TestColdViewer_NewTermsContributeExactlyZero(t *testing.T) {
	sigs := coldSignals()
	pid := uuid.New()

	// The topical term with no viewer history and no post topics.
	if got := TopicInterest(nil, sigs.TopicAffinity); got != 0 {
		t.Errorf("TopicInterest with no topics = %v, want 0", got)
	}
	if got := TopicInterest([]string{"cat:music"}, sigs.TopicAffinity); got != 0 {
		t.Errorf("TopicInterest with no viewer history = %v, want 0", got)
	}
	// A viewer WITH history looking at a post with no topics.
	sigs.TopicAffinity["cat:music"] = 0.95
	if got := TopicInterest(nil, sigs.TopicAffinity); got != 0 {
		t.Errorf("TopicInterest on an untagged post = %v, want 0", got)
	}
	// And a post whose topics the viewer has no opinion about.
	if got := TopicInterest([]string{"cat:gaming", "tag:speedrun"}, sigs.TopicAffinity); got != 0 {
		t.Errorf("TopicInterest on unknown topics = %v, want 0", got)
	}

	// The relatedness term off the related surface.
	if got := SeedRelatedness(nil, uuid.New().String(), []string{"cat:music"}); got != 0 {
		t.Errorf("SeedRelatedness with no seed = %v, want 0", got)
	}
	_ = pid
}

func TestColdViewer_StillGetsAFullPage(t *testing.T) {
	// The failure this guards against is a personalisation change that
	// leaves a viewer with no history holding an empty or truncated feed
	// — the diversity pass dropping everything because no candidate
	// scored, or a filter keyed on a signal that is not there.
	now := time.Now()
	author := uuid.New()
	cands := make([]Candidate, 0, 25)
	for i := 0; i < 25; i++ {
		// All from ONE author, the hardest case for the diversity pass:
		// its consecutive-author rule caps a run at three, and only the
		// relaxed second pass saves the page.
		cands = append(cands, Candidate{
			PostID:      uuid.New(),
			AuthorID:    author,
			CreatedAt:   now.Add(-time.Duration(i) * time.Hour),
			ContentType: "long_video",
		})
	}

	scored := ScoreCandidates(cands, coldSignals())
	page := ApplyDiversity(scored, 20)
	if len(page) != 20 {
		t.Fatalf("cold viewer got %d items, want a full page of 20", len(page))
	}
	// And the ordering must still be the sane one: newest first, since
	// recency is the only term that varies here.
	for i := 1; i < len(page); i++ {
		if page[i].CreatedAt.After(page[i-1].CreatedAt) {
			t.Fatalf("cold viewer page is not in recency order at position %d", i)
		}
	}
}

func TestColdViewer_EmptySignalsNeverPanic(t *testing.T) {
	// A zero-valued ViewerSignals — every map nil, not merely empty — is
	// what a caller constructing signals by hand would produce, and what
	// a future refactor might hand the scorer. Reading a nil map is legal
	// Go; ranging one is too. This asserts the scorer relies on nothing
	// stronger than that.
	now := time.Now()
	cands := []Candidate{
		{PostID: uuid.New(), AuthorID: uuid.New(), CreatedAt: now, ContentType: "long_video"},
	}
	scored := ScoreCandidates(cands, &ViewerSignals{})
	if len(scored) != 1 {
		t.Fatalf("scored %d, want 1", len(scored))
	}
	if math.IsNaN(scored[0].Score) || math.IsInf(scored[0].Score, 0) {
		t.Fatalf("score is %v on nil signal maps", scored[0].Score)
	}
}

// The media-boost switch used to name only the legacy content-type
// spellings, so the reel boost never applied to a reel and the
// dwell-sensitive branch never saw a long video. This pins the fix.
func TestMediaBoost_CoversTheSpellingsPostServiceActuallyStores(t *testing.T) {
	now := time.Now()
	base := func(ct string) Candidate {
		return Candidate{PostID: uuid.New(), AuthorID: uuid.New(), CreatedAt: now, ContentType: ct}
	}

	cold := coldSignals()
	flick := ScoreCandidates([]Candidate{base("flick")}, cold)[0].Score
	reel := ScoreCandidates([]Candidate{base("reel")}, cold)[0].Score
	if math.Abs(flick-reel) > 1e-9 {
		t.Errorf("flick scored %.6f but reel scored %.6f — the two spellings of the same thing must score alike", flick, reel)
	}
	plain := ScoreCandidates([]Candidate{base("post")}, cold)[0].Score
	if flick <= plain {
		t.Errorf("flick (%.6f) did not beat a plain post (%.6f): the reel media boost is not being applied", flick, plain)
	}

	// The long-video branch must respond to the dwell preference, which
	// is the entire reason user:media_prefs exists.
	heavy := coldSignals()
	heavy.MediaPrefs.VideoP95Dwell = 120
	light := coldSignals()
	light.MediaPrefs.VideoP95Dwell = 0

	for _, ct := range []string{"long_video", "video"} {
		hi := ScoreCandidates([]Candidate{base(ct)}, heavy)[0].Score
		lo := ScoreCandidates([]Candidate{base(ct)}, light)[0].Score
		if hi <= lo {
			t.Errorf("%s: a viewer who watches long videos (%.6f) must score them above one who does not (%.6f)", ct, hi, lo)
		}
	}
}
