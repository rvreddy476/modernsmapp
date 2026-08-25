package service

import "testing"

// Module 1 fixes-v1 / Codex P1-1.
//
// The v1 implementation resolved EVERY missing policy to main_feed=true,
// which silently discarded an old client's explicit
// `publish_to_feed:false`. These tests pin the corrected precedence.

func TestResolveDistributionWithLegacy_ExplicitOptOutHonored(t *testing.T) {
	f := false
	got := ResolveDistributionWithLegacy(nil, LegacyDistributionFields{PublishToFeed: &f})
	if got.MainFeed {
		t.Fatal("explicit publish_to_feed:false must keep the post out of social home")
	}
	if !got.NotifySubscribers {
		t.Fatal("publish_to_feed says nothing about notifications; it must stay default-on")
	}
}

func TestResolveDistributionWithLegacy_ExplicitOptInHonored(t *testing.T) {
	tr := true
	got := ResolveDistributionWithLegacy(nil, LegacyDistributionFields{PublishToFeed: &tr})
	if !got.MainFeed {
		t.Fatal("explicit publish_to_feed:true must place the post in social home")
	}
}

func TestResolveDistributionWithLegacy_AbsentKeepsHistoricalDefault(t *testing.T) {
	got := ResolveDistributionWithLegacy(nil, LegacyDistributionFields{})
	if !got.MainFeed || !got.NotifySubscribers {
		t.Fatalf("a client that expressed no opinion must keep the historical default, got %+v", got)
	}
}

// The typed policy always outranks legacy fields.
func TestResolveDistributionWithLegacy_PolicyWins(t *testing.T) {
	legacyTrue := true
	policyFalse := false
	got := ResolveDistributionWithLegacy(
		&DistributionPolicy{Version: 1, MainFeed: &policyFalse},
		LegacyDistributionFields{PublishToFeed: &legacyTrue},
	)
	if got.MainFeed {
		t.Fatal("typed policy must outrank a conflicting legacy field")
	}

	legacyFalse := false
	policyTrue := true
	got = ResolveDistributionWithLegacy(
		&DistributionPolicy{Version: 1, MainFeed: &policyTrue},
		LegacyDistributionFields{PublishToFeed: &legacyFalse},
	)
	if !got.MainFeed {
		t.Fatal("typed policy must outrank a conflicting legacy field in both directions")
	}
}

// Documented conflict rule: when legacy fields disagree, the more
// restrictive one wins.
func TestResolveDistributionWithLegacy_ConflictPrefersRestrictive(t *testing.T) {
	tr, f := true, false
	got := ResolveDistributionWithLegacy(nil, LegacyDistributionFields{
		PublishToFeed:   &tr,
		ShareToPostbook: &f,
	})
	if got.MainFeed {
		t.Fatal("when legacy fields disagree the restrictive value must win")
	}
}

// Explicit legacy intent must be materialized into a canonical policy so
// the stored row AND the emitted event carry it — otherwise downstream
// consumers apply their own default and the opt-out is lost in transit.
func TestPolicyFromLegacy(t *testing.T) {
	if PolicyFromLegacy(LegacyDistributionFields{}) != nil {
		t.Fatal("no expressed intent must not fabricate a policy")
	}

	f := false
	p := PolicyFromLegacy(LegacyDistributionFields{PublishToFeed: &f})
	if p == nil {
		t.Fatal("explicit intent must produce a canonical policy")
	}
	if p.Version != distributionPolicyVersion {
		t.Fatalf("materialized policy must carry the current version, got %d", p.Version)
	}
	if p.MainFeed == nil || *p.MainFeed {
		t.Fatal("materialized policy must record main_feed=false")
	}

	// Round-trips through storage validation unchanged.
	raw, err := MarshalPolicy(p)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	back, err := ParseDistributionPolicy(raw)
	if err != nil {
		t.Fatalf("re-parse: %v", err)
	}
	if back.MainFeed == nil || *back.MainFeed {
		t.Fatal("materialized policy must survive a storage round-trip")
	}
}

// ResolveDistribution (the no-legacy helper) must stay identical to the
// legacy-aware version when no legacy fields are supplied.
func TestResolveDistribution_MatchesLegacyAwareWithNoFields(t *testing.T) {
	f := false
	cases := []*DistributionPolicy{
		nil,
		{Version: 1},
		{Version: 1, MainFeed: &f},
	}
	for _, p := range cases {
		a := ResolveDistribution(p)
		b := ResolveDistributionWithLegacy(p, LegacyDistributionFields{})
		if a != b {
			t.Fatalf("helpers diverged for %+v: %+v vs %+v", p, a, b)
		}
	}
}
