package servicetoken

import (
	"testing"
	"time"
)

// ─── doorstep_booking / doorstep_extras ──────────────────────────────

// The wire strings are a contract with payments-service's owner and
// application maps, migration 015 and every stored intent; a rename would
// orphan them.
func TestRefDoorstepWireValues(t *testing.T) {
	if RefDoorstepBooking != "doorstep_booking" {
		t.Fatalf("RefDoorstepBooking = %q, want doorstep_booking", RefDoorstepBooking)
	}
	if RefDoorstepExtras != "doorstep_extras" {
		t.Fatalf("RefDoorstepExtras = %q, want doorstep_extras", RefDoorstepExtras)
	}
	all := []string{RefOrder, RefFoodOrder, RefDatingPremium, RefMopeduRide, RefMopeduSubscription, RefDoorstepBooking, RefDoorstepExtras}
	seen := map[string]bool{}
	for _, r := range all {
		if seen[r] {
			t.Fatalf("reference type %q is declared twice", r)
		}
		seen[r] = true
	}
}

// doorstepHarness adds doorstep-service, allowed exactly its two reference
// types and intent.create / intent.read / refund.create (the contract's
// caller config), to the two-caller harness.
func doorstepHarness(t *testing.T) (*harness, *Signer) {
	t.Helper()
	h := newHarness(t)
	pub, priv, err := GenerateKeypair()
	if err != nil {
		t.Fatal(err)
	}
	ds, err := NewSignerFromBase64("doorstep-service", "s1", priv)
	if err != nil {
		t.Fatal(err)
	}
	if err := h.v.RegisterBase64("doorstep-service", "s1", pub,
		[]string{OpIntentCreate, OpIntentRead, OpRefundCreate},
		[]string{RefDoorstepBooking, RefDoorstepExtras}); err != nil {
		t.Fatal(err)
	}
	return h, ds
}

func TestDoorstepTokenIsRecognisedForBothRefs(t *testing.T) {
	h, doorstep := doorstepHarness(t)
	for _, ref := range []string{RefDoorstepBooking, RefDoorstepExtras} {
		for _, op := range []string{OpIntentCreate, OpIntentRead, OpRefundCreate} {
			tok, err := doorstep.Mint(AudiencePayments, "doorstep", []string{op}, []string{ref}, time.Minute)
			if err != nil {
				t.Fatal(err)
			}
			got, err := h.v.Verify(tok, op, ref)
			if err != nil {
				t.Fatalf("%s on %s: %v", op, ref, err)
			}
			if got.Issuer != "doorstep-service" {
				t.Fatalf("issuer = %q", got.Issuer)
			}
		}
	}
}

func TestDoorstepTokenCannotActOnOtherReferences(t *testing.T) {
	h, doorstep := doorstepHarness(t)
	for _, ref := range []string{RefOrder, RefFoodOrder, RefDatingPremium, RefMopeduRide, RefMopeduSubscription} {
		tok, err := doorstep.Mint(AudiencePayments, "doorstep", []string{OpRefundCreate}, []string{ref}, time.Minute)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := h.v.Verify(tok, OpRefundCreate, ref); err != ErrRefTypeDenied {
			t.Fatalf("doorstep token on %s should be denied by policy, got %v", ref, err)
		}
	}
}

// A booking-only token must not be replayed against an extras bill (and the
// reverse): the reference type is per token, not per caller.
func TestDoorstepTokenIsPerReference(t *testing.T) {
	h, doorstep := doorstepHarness(t)
	tok, err := doorstep.Mint(AudiencePayments, "doorstep", []string{OpRefundCreate}, []string{RefDoorstepBooking}, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.v.Verify(tok, OpRefundCreate, RefDoorstepExtras); err != ErrRefTypeDenied {
		t.Fatalf("a booking token must not act on an extras bill, got %v", err)
	}
}

func TestOtherTokensCannotActOnDoorstepRefs(t *testing.T) {
	h, _ := doorstepHarness(t)
	for name, s := range map[string]*Signer{"commerce": h.commerce, "food": h.food} {
		for _, ref := range []string{RefDoorstepBooking, RefDoorstepExtras} {
			tok, err := s.Mint(AudiencePayments, name, []string{OpIntentCreate}, []string{ref}, time.Minute)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := h.v.Verify(tok, OpIntentCreate, ref); err != ErrRefTypeDenied {
				t.Fatalf("%s token on %s should be denied by policy, got %v", name, ref, err)
			}
		}
	}
}

// doorstep-service does not get payments:payment.fetch.
func TestDoorstepTokenHasNoPaymentFetch(t *testing.T) {
	h, doorstep := doorstepHarness(t)
	tok, err := doorstep.Mint(AudiencePayments, "doorstep", []string{OpPaymentFetch}, []string{RefDoorstepBooking}, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.v.Verify(tok, OpPaymentFetch, RefDoorstepBooking); err != ErrScopeDenied {
		t.Fatalf("payment.fetch should be denied by policy, got %v", err)
	}
}
