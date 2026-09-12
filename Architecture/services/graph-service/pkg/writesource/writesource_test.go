package writesource

import (
	"net/http"
	"testing"
)

// Phase B0 admitted post-service to the allowlist: a Tube channel subscribe
// is a follow edge plus a notify preference, and post-service owns the
// preference, so it has to be the one that creates the edge. The test pins
// that the entry is present, because a missing entry does not fail loudly;
// every subscribe from the app would receive 403 and nobody would see it
// until a user reported that subscribing did nothing.
func TestPostServiceIsAnApprovedGraphWriter(t *testing.T) {
	if !Allowed["post-service"] {
		t.Fatal("post-service is not an approved graph writer; Tube channel subscribes would be refused")
	}
	for _, method := range []string{http.MethodPost, http.MethodDelete} {
		if got := Evaluate(method, "post-service", true); got != Permit {
			t.Errorf("%s from post-service refused in strict mode (decision=%v)", method, got)
		}
	}
}

// Admitting one more writer must not loosen the guard for anyone else: an
// unknown or absent source is still refused in strict mode, and only
// permissive rollout mode lets it through, flagged as unattributed.
func TestUnknownSourceIsStillRefused(t *testing.T) {
	for _, source := range []string{"", "   ", "an-unknown-service", "post-service-v2", "identity-profile-service"} {
		if got := Evaluate(http.MethodPost, source, true); got != Refuse {
			t.Errorf("strict mode permitted a mutation from source %q (decision=%v)", source, got)
		}
		if got := Evaluate(http.MethodPost, source, false); got != PermitUnattributed {
			t.Errorf("permissive mode did not flag source %q as unattributed (decision=%v)", source, got)
		}
	}
}

// Reads are never gated, whatever the source: block enforcement in feed,
// search and chat depends on graph reads succeeding.
func TestReadsAreNeverGated(t *testing.T) {
	for _, method := range []string{http.MethodGet, http.MethodHead, http.MethodOptions} {
		if got := Evaluate(method, "", true); got != Permit {
			t.Errorf("unattributed %s was refused (decision=%v)", method, got)
		}
	}
}
