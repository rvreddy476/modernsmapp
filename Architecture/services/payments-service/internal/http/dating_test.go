package http

// Momentum Dating Premium (migration 011): the mappings used for callers that
// present no verified identity. A dating_premium intent from the user-facing
// family or a legacy internal-key caller must be owned by dating-service and
// attributed to the application dating, or dating-service's own token could
// neither read nor refund it.

import (
	"testing"

	"github.com/atpost/shared/servicetoken"
)

func TestDatingPremiumOwnerDomainAndApplication(t *testing.T) {
	if got := ownerDomainForReference(servicetoken.RefDatingPremium); got != "dating-service" {
		t.Fatalf("owner domain for dating_premium = %q, want dating-service", got)
	}
	if got := legacyApplications[servicetoken.RefDatingPremium]; got != "dating" {
		t.Fatalf("legacy application for dating_premium = %q, want dating", got)
	}
	// The existing mappings are unchanged.
	for ref, want := range map[string][2]string{
		servicetoken.RefOrder:     {"commerce-service", "mstore"},
		servicetoken.RefFoodOrder: {"food-service", "feast"},
	} {
		if got := ownerDomainForReference(ref); got != want[0] {
			t.Errorf("owner domain for %s = %q, want %q", ref, got, want[0])
		}
		if got := legacyApplications[ref]; got != want[1] {
			t.Errorf("legacy application for %s = %q, want %q", ref, got, want[1])
		}
	}
}
