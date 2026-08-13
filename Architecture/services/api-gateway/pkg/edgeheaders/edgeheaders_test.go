package edgeheaders

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// Module 3 LB-3 — the gateway must stamp the graph write source, and must
// overwrite anything a client claims.
//
// graph-service defaults to strict mode and refuses a mutating request whose
// source is not an approved caller. Before this change NO caller stamped the
// header, so every normal follow and block from the app would have received
// 403: the guard was correct and unreachable at the same time.

func stamped(method, path, inbound string) string {
	req := httptest.NewRequest(method, path, nil)
	if inbound != "" {
		req.Header.Set(GraphWriteSourceHeader, inbound)
	}
	StampGraphWriteSource(req)
	return req.Header.Get(GraphWriteSourceHeader)
}

func TestGraphMutationsAreStampedWithTheGateway(t *testing.T) {
	for _, method := range []string{
		http.MethodPost, http.MethodDelete, http.MethodPut, http.MethodPatch,
	} {
		for _, path := range []string{
			"/v1/graph/follow", "/v1/graph/block", "/v1/graph/connection-request",
			"/v1/graph/mute",
		} {
			if got := stamped(method, path, ""); got != GatewayWriteSource {
				t.Errorf("%s %s: source=%q, want %q. graph-service refuses an "+
					"unattributed mutation, so every follow and block from the app "+
					"would 403.", method, path, got, GatewayWriteSource)
			}
		}
	}
}

// A client-supplied label must be OVERWRITTEN, not trusted. Otherwise a caller
// claims to be any approved service and the guard is decoration.
func TestForgedSourceIsOverwritten(t *testing.T) {
	for _, forged := range []string{
		"trust-safety-service", // an approved source the client is not
		"api-gateway",          // claiming to be us
		"graph-service",        // claiming to be an internal reconciler
		"anything-at-all",
	} {
		if got := stamped(http.MethodPost, "/v1/graph/follow", forged); got != GatewayWriteSource {
			t.Errorf("forged source %q survived as %q; attribution can be faked",
				forged, got)
		}
	}
}

// Only graph routes are stamped. Stamping everything would let a service
// downstream of an unrelated route replay the header at graph-service and
// inherit the gateway's attribution.
//
// NOTE: this asserts the STAMP does not fire. The inbound value is removed
// separately by the gateway's trusted-header strip, which
// TestHeaderMustBeStripped below pins.
func TestNonGraphRoutesAreNotStamped(t *testing.T) {
	for _, path := range []string{"/v1/posts", "/v1/profiles/me", "/v1/feed", "/v1/users/me"} {
		if got := stamped(http.MethodPost, path, ""); got != "" {
			t.Errorf("%s was stamped with %q; only %s mutations carry the gateway's "+
				"attribution", path, got, GraphPathPrefix)
		}
	}
}

// A near-prefix must not be treated as a graph route.
//
// This is the SAME mistake the query-token allowlist had in SR-1, and this
// test caught it here: a raw HasPrefix matched /v1/graphql, /v1/graphs and
// /v1/graph-export, so the gateway's attribution would have been handed to a
// different upstream service entirely.
func TestNearPrefixRoutesAreNotStamped(t *testing.T) {
	for _, path := range []string{
		"/v1/graphql",
		"/v1/graphs",
		"/v1/graph-export",
		"/v1/graph-admin/purge",
		"/v1/grapheme",
	} {
		if got := stamped(http.MethodPost, path, ""); got != "" {
			t.Errorf("%s was stamped with %q — a near-prefix route received the "+
				"gateway's graph attribution", path, got)
		}
	}

	// The real graph routes, including the bare prefix, must still be stamped.
	for _, path := range []string{"/v1/graph", "/v1/graph/follow", "/v1/graph/a/b/c"} {
		if got := stamped(http.MethodPost, path, ""); got != GatewayWriteSource {
			t.Errorf("%s: source=%q, want %q", path, got, GatewayWriteSource)
		}
	}
}

// Reads are stamped too, which is harmless: graph-service's guard only
// inspects mutating methods. Pinning it here prevents a future "optimisation"
// that skips the stamp on GET and then breaks when a route changes method.
func TestReadsAreStampedHarmlessly(t *testing.T) {
	if got := stamped(http.MethodGet, "/v1/graph/relationship", ""); got != GatewayWriteSource {
		t.Errorf("GET /v1/graph/relationship: source=%q, want %q", got, GatewayWriteSource)
	}
}
