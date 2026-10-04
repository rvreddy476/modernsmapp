package matcher

import (
	"testing"

	"github.com/google/uuid"
)

func id(n byte) uuid.UUID { var u uuid.UUID; u[15] = n; return u }

func TestSmoothing(t *testing.T) {
	if r := SmoothedRating(Candidate{}); r != RatingPrior {
		t.Fatalf("new professional rating %v, want the prior", r)
	}
	// One five-star job does not beat a long 4.8 record.
	thin := SmoothedRating(Candidate{RatingSum: 5, RatingCount: 1})
	long := SmoothedRating(Candidate{RatingSum: 480, RatingCount: 100})
	if thin >= long {
		t.Fatalf("thin %v >= long %v", thin, long)
	}
	if a := AcceptanceRate(Candidate{}); a != 0.5 {
		t.Fatalf("new acceptance %v", a)
	}
	if c := CancellationRate(Candidate{JobsCompleted: 8, Cancellations: 2}); c != 0.2 {
		t.Fatalf("cancellation %v", c)
	}
}

func TestRankOrdersByScoreThenID(t *testing.T) {
	near := Candidate{ProID: id(3), DistanceM: 1000}
	far := Candidate{ProID: id(1), DistanceM: 9000}
	canceller := Candidate{ProID: id(2), DistanceM: 1000, JobsCompleted: 5, Cancellations: 5}
	busy := Candidate{ProID: id(4), DistanceM: 1000, WeekLoad: 10}
	got := Rank([]Candidate{far, canceller, busy, near})
	if got[0].Candidate.ProID != near.ProID {
		t.Fatalf("best is %v", got[0].Candidate.ProID)
	}
	for _, s := range got[1:] {
		if s.Score > got[0].Score {
			t.Fatal("not sorted")
		}
	}
	// Ties go to the lower id, whatever the input order.
	a, b := Candidate{ProID: id(9)}, Candidate{ProID: id(5)}
	if r := Rank([]Candidate{a, b}); r[0].Candidate.ProID != b.ProID {
		t.Fatal("tie not broken by id")
	}
	if len(got[0].Reasons) != 5 {
		t.Fatalf("reasons %v", got[0].Reasons)
	}
}
