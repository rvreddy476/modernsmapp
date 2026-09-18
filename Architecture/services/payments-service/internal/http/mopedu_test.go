package http

// Mopedu ride-hailing (migration 012): the mappings used for callers that
// present no verified identity. A mopedu_ride intent from the user-facing
// family or a legacy internal-key caller must be owned by rider-service and
// attributed to the application mopedu, or rider-service's own token could
// neither read nor refund it. Migration 013 adds mopedu_subscription (a
// captain's plan period paid to Mopedu) under the same owner and application.

import (
	"testing"

	"github.com/atpost/shared/servicetoken"
)

func TestMopeduRideOwnerDomainAndApplication(t *testing.T) {
	for _, ref := range []string{servicetoken.RefMopeduRide, servicetoken.RefMopeduSubscription} {
		if got := ownerDomainForReference(ref); got != "rider-service" {
			t.Fatalf("owner domain for %s = %q, want rider-service", ref, got)
		}
		if got := legacyApplications[ref]; got != "mopedu" {
			t.Fatalf("legacy application for %s = %q, want mopedu", ref, got)
		}
	}
	// The existing mappings are unchanged.
	for ref, want := range map[string][2]string{
		servicetoken.RefOrder:         {"commerce-service", "mstore"},
		servicetoken.RefFoodOrder:     {"food-service", "feast"},
		servicetoken.RefDatingPremium: {"dating-service", "dating"},
	} {
		if got := ownerDomainForReference(ref); got != want[0] {
			t.Errorf("owner domain for %s = %q, want %q", ref, got, want[0])
		}
		if got := legacyApplications[ref]; got != want[1] {
			t.Errorf("legacy application for %s = %q, want %q", ref, got, want[1])
		}
	}
}
