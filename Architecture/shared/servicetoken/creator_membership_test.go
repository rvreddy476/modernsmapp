package servicetoken

import (
	"testing"
	"time"
)

// ─── creator_membership ──────────────────────────────────────────────

// The wire string is a contract with payments-service's owner and
// application maps, migration 016 and every stored intent; a rename would
// orphan them.
func TestRefCreatorMembershipWireValue(t *testing.T) {
	if RefCreatorMembership != "creator_membership" {
		t.Fatalf("RefCreatorMembership = %q, want creator_membership", RefCreatorMembership)
	}
	all := []string{RefOrder, RefFoodOrder, RefDatingPremium, RefMopeduRide, RefMopeduSubscription,
		RefDoorstepBooking, RefDoorstepExtras, RefCreatorMembership}
	seen := map[string]bool{}
	for _, r := range all {
		if seen[r] {
			t.Fatalf("reference type %q is declared twice", r)
		}
		seen[r] = true
	}
}

// membershipsHarness adds monetization-service, allowed exactly
// creator_membership and intent.create / intent.read / refund.create (the
// contract's caller config), to the two-caller harness.
func membershipsHarness(t *testing.T) (*harness, *Signer) {
	t.Helper()
	h := newHarness(t)
	pub, priv, err := GenerateKeypair()
	if err != nil {
		t.Fatal(err)
	}
	ms, err := NewSignerFromBase64("monetization-service", "m1", priv)
	if err != nil {
		t.Fatal(err)
	}
	if err := h.v.RegisterBase64("monetization-service", "m1", pub,
		[]string{OpIntentCreate, OpIntentRead, OpRefundCreate},
		[]string{RefCreatorMembership}); err != nil {
		t.Fatal(err)
	}
	return h, ms
}

func TestMembershipsTokenIsRecognised(t *testing.T) {
	h, monetization := membershipsHarness(t)
	for _, op := range []string{OpIntentCreate, OpIntentRead, OpRefundCreate} {
		tok, err := monetization.Mint(AudiencePayments, "memberships", []string{op}, []string{RefCreatorMembership}, time.Minute)
		if err != nil {
			t.Fatal(err)
		}
		got, err := h.v.Verify(tok, op, RefCreatorMembership)
		if err != nil {
			t.Fatalf("%s on creator_membership: %v", op, err)
		}
		if got.Issuer != "monetization-service" {
			t.Fatalf("issuer = %q", got.Issuer)
		}
	}
}

func TestMembershipsTokenCannotActOnOtherReferences(t *testing.T) {
	h, monetization := membershipsHarness(t)
	for _, ref := range []string{RefOrder, RefFoodOrder, RefDatingPremium, RefMopeduRide, RefMopeduSubscription, RefDoorstepBooking, RefDoorstepExtras} {
		tok, err := monetization.Mint(AudiencePayments, "memberships", []string{OpIntentCreate}, []string{ref}, time.Minute)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := h.v.Verify(tok, OpIntentCreate, ref); err != ErrRefTypeDenied {
			t.Fatalf("monetization token on %s should be denied by policy, got %v", ref, err)
		}
	}
}

// No caller other than monetization-service may act on a creator_membership,
// even with a token that claims it.
func TestOtherTokensCannotActOnCreatorMembership(t *testing.T) {
	h, _ := membershipsHarness(t)
	for name, s := range map[string]*Signer{"commerce": h.commerce, "food": h.food} {
		for _, op := range []string{OpIntentCreate, OpIntentRead, OpRefundCreate} {
			tok, err := s.Mint(AudiencePayments, name, []string{op}, []string{RefCreatorMembership}, time.Minute)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := h.v.Verify(tok, op, RefCreatorMembership); err != ErrRefTypeDenied {
				t.Fatalf("%s token %s on creator_membership should be denied by policy, got %v", name, op, err)
			}
		}
	}
}

// monetization-service does not get payments:payment.fetch.
func TestMembershipsTokenHasNoPaymentFetch(t *testing.T) {
	h, monetization := membershipsHarness(t)
	tok, err := monetization.Mint(AudiencePayments, "memberships", []string{OpPaymentFetch}, []string{RefCreatorMembership}, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.v.Verify(tok, OpPaymentFetch, RefCreatorMembership); err != ErrScopeDenied {
		t.Fatalf("payment.fetch should be denied by policy, got %v", err)
	}
}
