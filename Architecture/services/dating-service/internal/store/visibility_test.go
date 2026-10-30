package store

import (
	"strings"
	"testing"

	"github.com/google/uuid"
)

// Audit H1 guard (no database): the shared visibility rule carries every
// condition discovery applies. A mutation that drops one (paused, hidden,
// under review / restricted via status, 18+, risk, blocks, incognito, either
// gender direction, hide-known, identity birth date) fails here; the
// database half is visibility_it_test.go.
func TestCandidateVisibleClausesCarryEveryRule(t *testing.T) {
	s := &Store{hideKnownEnabled: true, requireIdentityDOB: true}
	sql := andClauses(s.candidateVisibleClauses("p", "$1::uuid", viewerGenderFromDB("$1::uuid")))
	for _, want := range []string{
		"p.user_id <> $1::uuid",
		"p.profile_status = 'active'",
		"p.deleted_at IS NULL",
		"p.paused = false",
		"p.visible_to_public = true",
		"p.birth_date IS NOT NULL",
		"EXTRACT(YEAR FROM AGE(p.birth_date)) >= 18",
		"ph.moderation_status = 'approved'",
		"NOT EXISTS (SELECT 1 FROM dating_blocks blk",
		"('hide_from_discovery','chat_hold','admin_review','suspend')",
		"p.incognito = false OR EXISTS",
		// Forward gender: the viewer's preference against the candidate.
		"OR p.gender = (SELECT vpr.interested_in_gender FROM dating_preferences vpr WHERE vpr.user_id = $1::uuid)",
		// Reverse gender: the candidate's preference against the viewer.
		"IN ('everyone', (SELECT vpp.gender FROM dating_profiles vpp WHERE vpp.user_id = $1::uuid))",
		"p.dob_source IN ('identity','identity_registration','identity_profile')",
		"NOT EXISTS (SELECT 1 FROM dating_known_people k",
	} {
		if !strings.Contains(sql, want) {
			t.Errorf("visibility rule lacks %q:\n%s", want, sql)
		}
	}
	// Both switches off: neither clause appears.
	plain := andClauses((&Store{}).candidateVisibleClauses("p", "$1", viewerGender{wants: "$2::text", gender: "$3::text"}))
	if strings.Contains(plain, "dob_source") || strings.Contains(plain, "dating_known_people") {
		t.Fatalf("switched-off clauses present:\n%s", plain)
	}
}

// The point check and the list predicate are the same rule (one predicate,
// no drift): the EXISTS wraps exactly candidateVisibleClauses.
func TestCandidateVisiblePredicateWrapsTheRule(t *testing.T) {
	s := &Store{}
	pred := s.candidateVisiblePredicate("dp.candidate_id", "$1::uuid")
	inner := andClauses(s.candidateVisibleClauses("cv", "$1::uuid", viewerGenderFromDB("$1::uuid")))
	if !strings.Contains(pred, "cv.user_id = dp.candidate_id") || !strings.Contains(pred, inner) {
		t.Fatalf("predicate does not wrap the rule:\n%s", pred)
	}
}

// Audit L4 guard: a photo's owner must be out in the open (active, not
// paused, visible_to_public) unless the viewer is a match; a restricted
// (under-18 hold) owner is visible to nobody.
func TestPhotoOwnerVisiblePredicate(t *testing.T) {
	sql := photoOwnerVisiblePredicate("$1::uuid", "$2::uuid")
	for _, want := range []string{
		"p.profile_status NOT IN ('suspended','deleted','restricted')",
		"p.profile_status = 'active'",
		"p.paused = false",
		"p.visible_to_public = true",
		") OR EXISTS (SELECT 1 FROM dating_matches m",
		"m.status IN ('matched','conversing','quiet')",
		"NOT EXISTS (SELECT 1 FROM dating_blocks blk",
		"p.incognito = false",
	} {
		if !strings.Contains(sql, want) {
			t.Errorf("photo owner predicate lacks %q:\n%s", want, sql)
		}
	}
}

// Audit M1 guard: the selfie badge follows the photo it was checked against.
func TestSelfieBindingStep(t *testing.T) {
	a, b, c := uuid.New(), uuid.New(), uuid.New()
	p := func(u uuid.UUID) *uuid.UUID { return &u }
	cases := []struct {
		name                string
		ref, cur, rebind    *uuid.UUID
		status              string
		wantRef, wantRebind *uuid.UUID
		wantStatus          string
		wantOut             SelfieBinding
	}{
		{"unchanged", p(a), p(a), nil, "", p(a), nil, "", SelfieBindingNone},
		{"legacy pass adopts today's primary", nil, p(a), nil, "", p(a), nil, "", SelfieBindingAdopted},
		{"legacy pass, no primary", nil, nil, nil, "", nil, nil, "", SelfieBindingNone},
		{"primary changed: withdrawn, queued", p(a), p(b), nil, "", p(a), p(b), SelfieRebindPending, SelfieBindingWithdrawn},
		{"already queued for that photo", p(a), p(b), p(b), SelfieRebindPending, p(a), p(b), SelfieRebindPending, SelfieBindingNone},
		{"declined for that photo stays declined", p(a), p(b), p(b), SelfieRebindRejected, p(a), p(b), SelfieRebindRejected, SelfieBindingNone},
		{"another new photo re-queues", p(a), p(c), p(b), SelfieRebindRejected, p(a), p(c), SelfieRebindPending, SelfieBindingWithdrawn},
		{"bound photo back: restored", p(a), p(a), p(b), SelfieRebindPending, p(a), nil, "", SelfieBindingRestored},
		{"no approved primary: badge off", p(a), nil, nil, "", p(a), nil, SelfieRebindAwaitingPhoto, SelfieBindingWithdrawn},
		{"no primary, already off", p(a), nil, nil, SelfieRebindAwaitingPhoto, p(a), nil, SelfieRebindAwaitingPhoto, SelfieBindingNone},
	}
	eq := func(x, y *uuid.UUID) bool { return (x == nil && y == nil) || (x != nil && y != nil && *x == *y) }
	for _, tc := range cases {
		ref, rebind, st, out := selfieBindingStep(tc.ref, tc.cur, tc.rebind, tc.status)
		if !eq(ref, tc.wantRef) || !eq(rebind, tc.wantRebind) || st != tc.wantStatus || out != tc.wantOut {
			t.Errorf("%s: got ref=%v rebind=%v status=%q out=%q, want ref=%v rebind=%v status=%q out=%q",
				tc.name, ref, rebind, st, out, tc.wantRef, tc.wantRebind, tc.wantStatus, tc.wantOut)
		}
	}
}
