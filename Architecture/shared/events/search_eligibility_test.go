package events

import "testing"

// Module 2 M2-P0-1 — the eligibility rule, which is the single point
// where "may this content appear on a public surface?" is decided.
//
// Codex acceptance 2: "Comparisons are normalized and allowlisted;
// empty, missing, malformed, pending, flagged, rejected, and
// needs_changes all fail closed."
//
// Codex explicitly REJECTED a legacy-permissive default (empty ⇒ index),
// because replayed events, old producers, partially-deployed producers
// and malformed payloads would each reopen the exposure.

func TestSearchEligible_OnlyPublicApproved(t *testing.T) {
	if !SearchEligible("public", "approved", false) {
		t.Fatal("public + approved must be the ONE eligible combination")
	}
}

func TestSearchEligible_EveryReviewStateFailsClosed(t *testing.T) {
	// Every non-approved review state the schema permits, plus the
	// malformed cases that motivated the fail-closed rule.
	for _, review := range []string{
		"pending",       // Module 1 video/voice safety hold
		"flagged",       // spam detector
		"rejected",      // moderator decision
		"needs_changes", // super-admin creator loop
		"",              // MISSING — old producer / replayed event
		"   ",           // whitespace
		"approve",       // near-miss typo
		"APPROVED_",     // malformed
		"unknown",
		"null",
	} {
		if SearchEligible("public", review, false) {
			t.Errorf("review_status %q must be INELIGIBLE (fail closed)", review)
		}
	}
}

func TestSearchEligible_EveryNonPublicVisibilityFailsClosed(t *testing.T) {
	for _, vis := range []string{
		"followers", "private", "unlisted", "trusted", "close_friends",
		"staged", "", "   ", "publik", "PUBLIC_",
	} {
		if SearchEligible(vis, "approved", false) {
			t.Errorf("visibility %q must be INELIGIBLE (fail closed)", vis)
		}
	}
}

// Deletion short-circuits regardless of the other fields.
func TestSearchEligible_DeletedAlwaysIneligible(t *testing.T) {
	if SearchEligible("public", "approved", true) {
		t.Fatal("a deleted post must never be eligible")
	}
}

// Case and surrounding whitespace are normalized — a producer that sends
// "Public"/"APPROVED" is still correctly admitted, and one that sends
// " public " is not accidentally rejected.
func TestSearchEligible_NormalizesCaseAndWhitespace(t *testing.T) {
	for _, c := range []struct{ vis, review string }{
		{"PUBLIC", "APPROVED"},
		{"Public", "Approved"},
		{" public ", " approved "},
		{"public\t", "\napproved"},
	} {
		if !SearchEligible(c.vis, c.review, false) {
			t.Errorf("(%q,%q) should normalize to eligible", c.vis, c.review)
		}
	}
}

// Guard against a future edit turning the allowlist into a denylist.
// A newly-invented value must be ineligible until deliberately admitted.
func TestSearchEligible_UnknownValuesAreNotAdmitted(t *testing.T) {
	if SearchEligible("public_v2", "approved", false) {
		t.Fatal("a new visibility must be ineligible until explicitly allowlisted")
	}
	if SearchEligible("public", "auto_approved", false) {
		t.Fatal("a new review status must be ineligible until explicitly allowlisted")
	}
}

// The specific regression this whole module exists to prevent: a voice or
// video post held pending by the Module 1 safety gate.
func TestSearchEligible_HeldVoiceAndVideoPostsAreNotSearchable(t *testing.T) {
	if SearchEligible("public", "pending", false) {
		t.Fatal("a post held pending by the Module 1 media safety gate " +
			"must NOT be publicly searchable — this is the M2-P0-1 regression")
	}
}
