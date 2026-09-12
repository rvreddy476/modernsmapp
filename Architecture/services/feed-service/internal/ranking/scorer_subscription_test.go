package ranking

import (
	"context"
	"math"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

// The subscriptionBoost term: a candidate whose author's channel the
// viewer subscribes to scores exactly SubscriptionWeight higher than an
// otherwise identical one, and the signal comes from the same Redis set
// the Subscriptions tab is served from.

func TestScoreCandidates_SubscribedAuthorGetsExactlyTheBoost(t *testing.T) {
	now := time.Now()
	subscribed, other := uuid.New(), uuid.New()
	cands := []Candidate{
		{PostID: uuid.New(), AuthorID: subscribed, CreatedAt: now, ContentType: "long_video"},
		{PostID: uuid.New(), AuthorID: other, CreatedAt: now, ContentType: "long_video"},
	}
	sigs := &ViewerSignals{
		AuthorAffinities: map[string]float64{},
		Velocities:       map[string]float64{},
		Interactions:     map[string]bool{},
		MutualFollows:    map[string]bool{},
		ContentQuality:   map[string]float64{},
		AuthorFeedback:   map[string]float64{},
		Subscribed:       map[string]bool{subscribed.String(): true},
	}
	scored := ScoreCandidates(cands, sigs)
	diff := scored[0].Score - scored[1].Score
	if math.Abs(diff-SubscriptionWeight) > 1e-9 {
		t.Fatalf("subscribed author scored %v higher, want exactly %v", diff, SubscriptionWeight)
	}
}

// The weight sits between the mutual-follow bump and the quality ceiling;
// the reasoning is on the constant, this pins it.
func TestSubscriptionWeight_SitsBetweenMutualAndQuality(t *testing.T) {
	const mutualBump = 0.2 - 0.1 // socialProximity mutual minus baseline
	const qualityMax = 0.25
	if !(SubscriptionWeight > mutualBump) {
		t.Fatalf("SubscriptionWeight %v must outweigh the mutual-follow bump %v", SubscriptionWeight, mutualBump)
	}
	if !(SubscriptionWeight < qualityMax) {
		t.Fatalf("SubscriptionWeight %v must stay under the quality ceiling %v", SubscriptionWeight, qualityMax)
	}
}

// A nil Subscribed map (older signal builders, the empty-signals path) is
// simply "not subscribed to anyone".
func TestScoreCandidates_NilSubscribedIsNoBoost(t *testing.T) {
	now := time.Now()
	a, b := uuid.New(), uuid.New()
	cands := []Candidate{
		{PostID: uuid.New(), AuthorID: a, CreatedAt: now, ContentType: "long_video"},
		{PostID: uuid.New(), AuthorID: b, CreatedAt: now, ContentType: "long_video"},
	}
	scored := ScoreCandidates(cands, &ViewerSignals{})
	if scored[0].Score != scored[1].Score {
		t.Fatalf("without a Subscribed map the two must tie, got %v vs %v", scored[0].Score, scored[1].Score)
	}
}

// LoadSignals reads user:subscribed_owners:{viewer} and intersects it with
// the candidate authors.
func TestLoadSignals_ReadsSubscribedOwnersSet(t *testing.T) {
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	viewer, subscribed, other, notACandidate := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	if _, err := mr.SAdd(SubscribedOwnersKey(viewer), subscribed.String(), notACandidate.String()); err != nil {
		t.Fatal(err)
	}

	sl := NewSignalLoader(rdb, nil)
	sigs, err := sl.LoadSignals(context.Background(), viewer, []Candidate{
		{PostID: uuid.New(), AuthorID: subscribed, CreatedAt: time.Now(), ContentType: "long_video"},
		{PostID: uuid.New(), AuthorID: other, CreatedAt: time.Now(), ContentType: "long_video"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !sigs.Subscribed[subscribed.String()] {
		t.Fatalf("subscribed author missing from signals: %v", sigs.Subscribed)
	}
	if sigs.Subscribed[other.String()] {
		t.Fatal("an unsubscribed author was marked subscribed")
	}
	if sigs.Subscribed[notACandidate.String()] {
		t.Fatal("the set was not intersected with the candidate authors")
	}
}

// Nothing in Redis means nobody is subscribed, not an error.
func TestLoadSignals_NoSubscribedOwnersSetIsEmpty(t *testing.T) {
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	sl := NewSignalLoader(rdb, nil)
	sigs, err := sl.LoadSignals(context.Background(), uuid.New(), []Candidate{
		{PostID: uuid.New(), AuthorID: uuid.New(), CreatedAt: time.Now(), ContentType: "long_video"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(sigs.Subscribed) != 0 {
		t.Fatalf("expected no subscribed authors, got %v", sigs.Subscribed)
	}
}
