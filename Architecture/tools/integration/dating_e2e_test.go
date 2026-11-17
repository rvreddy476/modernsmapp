//go:build integration

package integration

import (
	"testing"

	"github.com/google/uuid"
)

// Dating (Pulse) is not one of the launch products: the gateway's dormant
// product gate answers 404 NOT_FOUND on /v1/dating for everyone who is not on
// the pilot list (api-gateway main.go, dormantProductsFromEnv:
// DATING_PUBLIC_ENABLED, DATING_PILOT_USER_IDS).
//
// The flow here used to assert only "not a server error", which the gate's
// 404 satisfies, so the job was green without reaching dating-service at
// all. It now asserts the closed answer exactly, and keeps the read smoke
// for a stack that opens the product.

const datingRead = "/v1/dating/profile"

// TestE2E_Dating_ClosedAtLaunch: every call under /v1/dating is 404
// NOT_FOUND, signed in or not, and /v1/features reports dating as off.
func TestE2E_Dating_ClosedAtLaunch(t *testing.T) {
	parkedProductClosed(t, "dating", "/v1/dating", datingRead)
}

// TestE2E_Dating_ProfileAndMatches is the read smoke for a stack that runs
// with dating open (ATPOST_E2E_OPEN_PRODUCTS lists "dating"): a fresh user
// reaches dating-service through the gateway on the profile and matches
// reads. No profile exists yet, so either read may answer 200 empty or a
// 4xx of dating-service's own; what it must not be is the gate's refusal or
// a server error.
//
// The full profile, discovery, mutual-like, match and chat-handoff journey
// needs two coordinated actors and the like/swipe write shapes; it is a
// deeper dating follow-up (it also exercises the anti-abuse velocity checks).
func TestE2E_Dating_ProfileAndMatches(t *testing.T) {
	gw, ctx := gatewayForFlow(t)
	if !productOpen("dating") {
		t.Skipf("%s does not list %q: the product is closed on this stack", openProductsEnv, "dating")
	}
	u := NewHTTPClient(gw, uuid.New())
	for _, path := range []string{datingRead, "/v1/dating/matches"} {
		env := u.MustDo(t, ctx, "GET", path, nil)
		if env.Status >= 500 {
			t.Errorf("GET %s: server error %d (want 2xx/4xx)", path, env.Status)
		}
		if env.Status == 404 && envelopeCode(env) == "NOT_FOUND" {
			t.Errorf("GET %s: 404 NOT_FOUND, the dormant-product gate's answer; the stack was said to run with dating open", path)
		}
	}
	wantFeature(t, ctx, gw, "dating", true)
}
