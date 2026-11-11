package servicetoken

import (
	"testing"
	"time"
)

// The wire string is a contract with payments-service's owner and application
// maps, payments migration 019 and monetization's premium_periods.
func TestRefPlatformPremiumWireValue(t *testing.T) {
	if RefPlatformPremium != "platform_premium" {
		t.Fatalf("RefPlatformPremium = %q", RefPlatformPremium)
	}
	for _, r := range []string{RefOrder, RefFoodOrder, RefDatingPremium, RefMopeduRide, RefMopeduSubscription,
		RefDoorstepBooking, RefDoorstepExtras, RefCreatorMembership} {
		if r == RefPlatformPremium {
			t.Fatalf("platform_premium collides with %q", r)
		}
	}
}

// A monetization token registered only for creator_membership cannot act on
// platform_premium: the REFTYPES env must list it before Premium can sell.
func TestPlatformPremiumNeedsItsOwnRegistration(t *testing.T) {
	h, monetization := membershipsHarness(t)
	tok, err := monetization.Mint(AudiencePayments, "premium", []string{OpIntentCreate}, []string{RefPlatformPremium}, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.v.Verify(tok, OpIntentCreate, RefPlatformPremium); err != ErrRefTypeDenied {
		t.Fatalf("got %v, want ErrRefTypeDenied", err)
	}
}
