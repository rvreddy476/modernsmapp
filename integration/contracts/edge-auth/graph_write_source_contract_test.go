package edgeauth_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/atpost/api-gateway/pkg/edgeheaders"
	"github.com/atpost/graph-service/pkg/writesource"
)

// Module 3 LB-3 — the gateway's stamp must satisfy graph-service's guard.
//
// These are two independently-written pieces of code in two services that have
// to agree on a header name and a value. They did not: graph-service defaulted
// to STRICT mode and required X-Graph-Write-Source, and no caller stamped it —
// so every normal follow and block from the app would have received 403. The
// guard was correct and unreachable at the same time.
//
// Each service's own tests passed. Only a test that drives BOTH real
// implementations catches the disagreement, which is why this lives in the
// neutral CI-only module rather than in either service: neither may depend on
// the other in production.

// throughTheEdge reproduces what the gateway actually does to a request:
// strip any client-supplied label, then stamp its own.
func throughTheEdge(method, path, forgedClientLabel string) *http.Request {
	req := httptest.NewRequest(method, path, nil)
	if forgedClientLabel != "" {
		req.Header.Set(edgeheaders.GraphWriteSourceHeader, forgedClientLabel)
	}
	req.Header.Del(edgeheaders.GraphWriteSourceHeader) // the trusted-header strip
	edgeheaders.StampGraphWriteSource(req)
	return req
}

// decide runs graph-service's REAL evaluation on the request the edge produced.
func decide(req *http.Request) writesource.Decision {
	return writesource.Evaluate(
		req.Method,
		req.Header.Get(writesource.Header),
		true, // strict, as production runs
	)
}

// THE ONE THAT MATTERS: a normal end-user follow/block must succeed.
func TestGatewayStampedMutationIsPermittedByTheStrictGuard(t *testing.T) {
	for _, tc := range []struct{ method, path string }{
		{http.MethodPost, "/v1/graph/follow"},
		{http.MethodPost, "/v1/graph/block"},
		{http.MethodDelete, "/v1/graph/block"},
		{http.MethodPost, "/v1/graph/connection-request"},
		{http.MethodPost, "/v1/graph/mute"},
	} {
		req := throughTheEdge(tc.method, tc.path, "")
		if got := decide(req); got != writesource.Permit {
			t.Fatalf("%s %s: decision=%v, want Permit. The gateway's stamp does not "+
				"satisfy graph-service's guard, so every follow and block from the "+
				"app returns 403.", tc.method, tc.path, got)
		}
	}
}

// The header NAME must match. They are separate constants in separate modules
// by design, so the agreement needs an explicit assertion.
func TestHeaderNameAgreesAcrossServices(t *testing.T) {
	if writesource.Header != edgeheaders.GraphWriteSourceHeader {
		t.Fatalf("header mismatch: graph-service reads %q, the gateway stamps %q. "+
			"Every proxied graph mutation would be refused.",
			writesource.Header, edgeheaders.GraphWriteSourceHeader)
	}
}

// The VALUE the gateway stamps must be on graph-service's allowlist.
func TestGatewaySourceValueIsAllowed(t *testing.T) {
	if !writesource.Allowed[edgeheaders.GatewayWriteSource] {
		t.Fatalf("the gateway stamps %q, which graph-service does not allow. Every "+
			"end-user follow and block would be refused.", edgeheaders.GatewayWriteSource)
	}
}

// A forged client label must be replaced, and the request must still succeed
// under the gateway's own attribution.
func TestForgedClientLabelIsReplacedAndStillPermitted(t *testing.T) {
	for _, forged := range []string{
		"trust-safety-service", // an approved source the client is not
		"graph-service",        // claiming to be an internal reconciler
		"identity-profile-service",
		"not-a-service",
	} {
		req := throughTheEdge(http.MethodPost, "/v1/graph/block", forged)

		if got := req.Header.Get(writesource.Header); got != edgeheaders.GatewayWriteSource {
			t.Fatalf("forged %q survived the edge as %q; attribution can be faked",
				forged, got)
		}
		if got := decide(req); got != writesource.Permit {
			t.Errorf("forged %q: decision=%v, want Permit under the gateway's own label",
				forged, got)
		}
	}
}

// A direct, unstamped mutation — a service bypassing the gateway without
// having been reviewed — must be REFUSED.
func TestUnstampedDirectMutationIsRefused(t *testing.T) {
	for _, method := range []string{
		http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete,
	} {
		if got := writesource.Evaluate(method, "", true); got != writesource.Refuse {
			t.Errorf("%s with no source: decision=%v, want Refuse. An unreviewed "+
				"service could create follows and blocks.", method, got)
		}
	}
}

// Every approved internal source must be permitted when it stamps its own
// label — otherwise adding a service to the allowlist silently fails.
func TestEachApprovedInternalSourceIsPermitted(t *testing.T) {
	if len(writesource.Allowed) == 0 {
		t.Fatal("no approved graph writers: every mutation would be refused")
	}
	for source := range writesource.Allowed {
		if got := writesource.Evaluate(http.MethodPost, source, true); got != writesource.Permit {
			t.Errorf("approved source %q was refused (decision=%v)", source, got)
		}
	}
}

// The retired duplicate writer must never be re-admitted.
func TestProfileServiceIsNotAnApprovedGraphWriter(t *testing.T) {
	for _, banned := range []string{
		"identity-profile-service", "profile-service", "identity-profile",
	} {
		if writesource.Allowed[banned] {
			t.Errorf("%q is an approved graph writer again. Its graph routes were "+
				"retired in SR-3 precisely because the blocks it wrote were enforced "+
				"by nothing.", banned)
		}
	}
}

// Reads must never be gated: block enforcement in feed, search and chat
// depends on graph reads succeeding.
func TestReadsAreNotGated(t *testing.T) {
	for _, method := range []string{http.MethodGet, http.MethodHead, http.MethodOptions} {
		if got := writesource.Evaluate(method, "", true); got != writesource.Permit {
			t.Errorf("%s with no source: decision=%v, want Permit", method, got)
		}
	}
}

// Non-graph routes are not stamped, so a mutation that reaches graph-service
// on such a path is by definition not from the gateway and must be refused.
func TestNonGraphPathsAreNotStampedAndWouldBeRefused(t *testing.T) {
	req := throughTheEdge(http.MethodPost, "/v1/posts", "api-gateway")
	if got := req.Header.Get(writesource.Header); got != "" {
		t.Fatalf("a non-graph route carried source %q", got)
	}
	if got := decide(req); got != writesource.Refuse {
		t.Errorf("decision=%v, want Refuse for an unattributed mutation", got)
	}
}
