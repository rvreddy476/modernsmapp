// Audit H1 follow-up: the rule every action keyed by a candidate id uses
// (requireActionableCandidate: pass, stash, spark, Super Spark) and the
// dealt-card rule explain uses (inCurrentDeck), pinned branch by branch with
// an in-memory ActionLookups, so these run without a database. The database
// flows (the store queries, the spark and explain wiring) are in
// h1_spark_explain_it_test.go.
package service

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
)

type pair struct{ a, b uuid.UUID }

// fakeLookups answers each question from a set of (viewer, candidate) pairs.
type fakeLookups struct {
	blocked, matched, incoming, picks, dealt, passed, visible map[pair]bool
	premium                                                  map[uuid.UUID]bool
	premiumErr, dealtErr                                     error
}

func newFakeLookups() *fakeLookups {
	return &fakeLookups{
		blocked: map[pair]bool{}, matched: map[pair]bool{}, incoming: map[pair]bool{}, picks: map[pair]bool{},
		dealt: map[pair]bool{}, passed: map[pair]bool{}, visible: map[pair]bool{}, premium: map[uuid.UUID]bool{},
	}
}

func (f *fakeLookups) IsBlockedEitherWay(_ context.Context, a, b uuid.UUID) (bool, error) {
	return f.blocked[pair{a, b}] || f.blocked[pair{b, a}], nil
}
func (f *fakeLookups) HasOpenMatch(_ context.Context, x, y uuid.UUID) (bool, error) {
	return f.matched[pair{x, y}] || f.matched[pair{y, x}], nil
}

// HasIncomingSparkFrom: senderID sparked viewerID.
func (f *fakeLookups) HasIncomingSparkFrom(_ context.Context, viewerID, senderID uuid.UUID) (bool, error) {
	return f.incoming[pair{viewerID, senderID}], nil
}
func (f *fakeLookups) IsPremium(_ context.Context, userID uuid.UUID) (bool, error) {
	return f.premium[userID], f.premiumErr
}
func (f *fakeLookups) InRecentPicks(_ context.Context, userID, candidateID uuid.UUID) (bool, error) {
	return f.picks[pair{userID, candidateID}], nil
}
func (f *fakeLookups) WasDealt(_ context.Context, userID, candidateID uuid.UUID) (bool, error) {
	return f.dealt[pair{userID, candidateID}], f.dealtErr
}
func (f *fakeLookups) HasStandingPass(_ context.Context, viewerID, candidateID uuid.UUID) (bool, error) {
	return f.passed[pair{viewerID, candidateID}], nil
}
func (f *fakeLookups) CandidateVisibleTo(_ context.Context, viewerID, candidateID uuid.UUID) (bool, error) {
	return f.visible[pair{viewerID, candidateID}], nil
}

func ruleSvc(f *fakeLookups, m MechanicsConfig) *Service {
	s := New(nil, nil)
	s.actionLookups = f
	s.mechanics = m
	return s
}

// TestActionableCandidateRule is the rule a spark, a Super Spark and a pass
// share: a dealt card still visible, an open match, a daily pick (with M7
// on), or an incoming spark the viewer may see (sparking back). Anything
// else — a deck-eligible stranger never dealt above all — is the one
// ErrCandidateUnavailable.
func TestActionableCandidateRule(t *testing.T) {
	v, c := uuid.New(), uuid.New()
	vc := pair{v, c}
	cases := []struct {
		name  string
		mech  MechanicsConfig
		setup func(f *fakeLookups)
		ok    bool
	}{
		{"no relationship", MechanicsConfig{}, func(f *fakeLookups) {}, false},
		{"visible but never dealt", MechanicsConfig{}, func(f *fakeLookups) { f.visible[vc] = true }, false},
		{"dealt and visible", MechanicsConfig{}, func(f *fakeLookups) { f.dealt[vc], f.visible[vc] = true, true }, true},
		{"dealt, hidden since", MechanicsConfig{}, func(f *fakeLookups) { f.dealt[vc] = true }, false},
		{"dealt, visible, passed", MechanicsConfig{}, func(f *fakeLookups) { f.dealt[vc], f.visible[vc], f.passed[vc] = true, true, true }, false},
		{"open match", MechanicsConfig{}, func(f *fakeLookups) { f.matched[vc] = true }, true},
		{"sparking back, gate off", MechanicsConfig{}, func(f *fakeLookups) { f.incoming[vc] = true }, true},
		{"sparking back, gate on, locked", MechanicsConfig{LikedYouGate: true}, func(f *fakeLookups) { f.incoming[vc] = true }, false},
		{"sparking back, gate on, pass holder", MechanicsConfig{LikedYouGate: true}, func(f *fakeLookups) { f.incoming[vc], f.premium[v] = true, true }, true},
		{"sparking back, gate on, premium lookup fails", MechanicsConfig{LikedYouGate: true}, func(f *fakeLookups) {
			f.incoming[vc], f.premium[v], f.premiumErr = true, true, errors.New("down")
		}, false},
		{"they were sparked by the viewer (not a spark back)", MechanicsConfig{}, func(f *fakeLookups) { f.incoming[pair{c, v}] = true }, false},
		{"daily pick, picks on", MechanicsConfig{Picks: true}, func(f *fakeLookups) { f.picks[vc] = true }, true},
		{"daily pick, picks off", MechanicsConfig{}, func(f *fakeLookups) { f.picks[vc] = true }, false},
		{"blocked beats everything", MechanicsConfig{Picks: true}, func(f *fakeLookups) {
			f.dealt[vc], f.visible[vc], f.matched[vc], f.incoming[vc], f.picks[vc] = true, true, true, true, true
			f.blocked[pair{c, v}] = true
		}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newFakeLookups()
			tc.setup(f)
			err := ruleSvc(f, tc.mech).requireActionableCandidate(context.Background(), v, c)
			if tc.ok && err != nil {
				t.Fatalf("refused: %v", err)
			}
			if !tc.ok && !errors.Is(err, ErrCandidateUnavailable) {
				t.Fatalf("err = %v, want ErrCandidateUnavailable", err)
			}
		})
	}
	// Yourself and nil ids are never actionable.
	f := newFakeLookups()
	f.dealt[pair{v, v}], f.visible[pair{v, v}] = true, true
	for _, target := range []uuid.UUID{v, uuid.Nil} {
		if err := ruleSvc(f, MechanicsConfig{}).requireActionableCandidate(context.Background(), v, target); !errors.Is(err, ErrCandidateUnavailable) {
			t.Fatalf("target %s: err = %v, want ErrCandidateUnavailable", target, err)
		}
	}
}

// A lookup failure is returned as itself, never mistaken for an answer.
func TestActionableCandidateRuleReturnsLookupErrors(t *testing.T) {
	v, c := uuid.New(), uuid.New()
	f := newFakeLookups()
	f.dealtErr = errors.New("db down")
	err := ruleSvc(f, MechanicsConfig{}).requireActionableCandidate(context.Background(), v, c)
	if err == nil || errors.Is(err, ErrCandidateUnavailable) || err.Error() != "db down" {
		t.Fatalf("err = %v, want the lookup error", err)
	}
}

// TestDealtCardRule is the explain rule (inCurrentDeck): only a card dealt
// to the viewer, not passed, and still visible. A match, a pick or an
// incoming spark opens the person card and allows a spark, but is not a card
// in the deck, so it does not open explain.
func TestDealtCardRule(t *testing.T) {
	v, c := uuid.New(), uuid.New()
	vc := pair{v, c}
	cases := []struct {
		name  string
		setup func(f *fakeLookups)
		want  bool
	}{
		{"dealt and visible", func(f *fakeLookups) { f.dealt[vc], f.visible[vc] = true, true }, true},
		{"deck-eligible, never dealt", func(f *fakeLookups) { f.visible[vc] = true }, false},
		{"dealt, hidden since", func(f *fakeLookups) { f.dealt[vc] = true }, false},
		{"dealt, passed", func(f *fakeLookups) { f.dealt[vc], f.visible[vc], f.passed[vc] = true, true, true }, false},
		{"match, pick and incoming spark only", func(f *fakeLookups) {
			f.visible[vc], f.matched[vc], f.picks[vc], f.incoming[vc] = true, true, true, true
		}, false},
		{"another viewer's dealt card", func(f *fakeLookups) {
			f.dealt[pair{uuid.New(), c}], f.visible[vc] = true, true
		}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newFakeLookups()
			tc.setup(f)
			got, err := ruleSvc(f, MechanicsConfig{Picks: true}).inCurrentDeck(context.Background(), v, c)
			if err != nil || got != tc.want {
				t.Fatalf("inCurrentDeck = %v, %v; want %v", got, err, tc.want)
			}
		})
	}
}
