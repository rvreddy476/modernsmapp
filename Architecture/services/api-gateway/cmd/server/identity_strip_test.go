package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/atpost/api-gateway/pkg/edgeheaders"
	"github.com/atpost/api-gateway/pkg/tokenpolicy"
)

// devTestPolicy is the development-shaped policy these gateway tests run
// under: HS256 permitted, no issuer/audience pinning. The production rules
// (RS256-only, mandatory iss/aud/sid/typ) are proven in
// pkg/tokenpolicy/policy_test.go, which is where the policy itself now lives —
// the root .gitignore's bare `server` rule makes any NEW file under
// cmd/server/ untracked, so the policy source cannot live here. It is under
// pkg/ rather than internal/ so auth-service can import the REAL verifier for
// the mint↔verify contract test.
func devTestPolicy() tokenpolicy.Policy {
	return tokenpolicy.Policy{
		Production: false,
		AllowHS256: true,
		ClockSkew:  60 * time.Second,
	}
}

// These tests pin the gateway's #1 security invariant: the trusted identity
// headers (X-User-Id, X-Scopes, X-Internal-Service-Key, ...) can ONLY be set by
// the gateway from a verified token — never smuggled in by a client. They prove
// the privilege-escalation / impersonation holes are closed.

// captureHandler records the identity headers the downstream service would see.
func captureHandler(got *map[string]string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		m := map[string]string{}
		for _, h := range trustedIdentityHeaders {
			m[h] = r.Header.Get(h)
		}
		*got = m
		w.WriteHeader(http.StatusOK)
	})
}

func TestGatewayStripsSpoofedIdentityHeadersWhenUnauthenticated(t *testing.T) {
	var got map[string]string
	mw := jwtExtractMiddleware(jwtKeySet{activeKID: "v1", activeSecret: "secret"}, devTestPolicy(), captureHandler(&got))

	req := httptest.NewRequest(http.MethodGet, "/v1/anything", nil)
	// Attacker forges identity + scope + the internal-service key, no token.
	req.Header.Set("X-User-Id", "victim-user")
	req.Header.Set("X-Scopes", "admin superadmin")
	req.Header.Set("X-Internal-Service-Key", "guessed-key")
	req.Header.Set("X-Verified-User-Id", "victim-user")

	mw.ServeHTTP(httptest.NewRecorder(), req)

	for _, h := range trustedIdentityHeaders {
		if got[h] != "" {
			t.Fatalf("unauthenticated request leaked %s=%q (must be stripped)", h, got[h])
		}
	}
}

func TestGatewayIgnoresClientScopesWithLowPrivToken(t *testing.T) {
	keys := jwtKeySet{activeKID: "v1", activeSecret: "secret"}
	var got map[string]string
	mw := jwtExtractMiddleware(keys, devTestPolicy(), captureHandler(&got))

	// A genuine ordinary-user token that carries NO scopes claim.
	token := signJWT(t, map[string]any{"alg": "HS256", "kid": "v1"}, map[string]any{
		"user_id": "11111111-1111-4111-8111-111111111111",
		"exp":     time.Now().Add(time.Hour).Unix(),
	}, keys.activeSecret)

	req := httptest.NewRequest(http.MethodGet, "/v1/admin/thing", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("X-Scopes", "admin superadmin") // forged privilege escalation

	mw.ServeHTTP(httptest.NewRecorder(), req)

	if got["X-User-Id"] != "11111111-1111-4111-8111-111111111111" {
		t.Fatalf("X-User-Id=%q want 11111111-1111-4111-8111-111111111111 (from token)", got["X-User-Id"])
	}
	if got["X-Scopes"] != "" {
		t.Fatalf("forged X-Scopes survived: %q (privilege escalation)", got["X-Scopes"])
	}
}

func TestGatewayHonoursScopesFromVerifiedToken(t *testing.T) {
	keys := jwtKeySet{activeKID: "v1", activeSecret: "secret"}
	var got map[string]string
	mw := jwtExtractMiddleware(keys, devTestPolicy(), captureHandler(&got))

	// A real admin token: the scopes claim was stamped server-side at mint.
	token := signJWT(t, map[string]any{"alg": "HS256", "kid": "v1"}, map[string]any{
		"user_id": "22222222-2222-4222-8222-222222222222",
		"scopes":  "admin moderator",
		"exp":     time.Now().Add(time.Hour).Unix(),
	}, keys.activeSecret)

	req := httptest.NewRequest(http.MethodGet, "/v1/admin/thing", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("X-Scopes", "superadmin") // attacker tries to upgrade

	mw.ServeHTTP(httptest.NewRecorder(), req)

	if got["X-Scopes"] != "admin moderator" {
		t.Fatalf("X-Scopes=%q want %q (only the token's scopes, not the forged one)", got["X-Scopes"], "admin moderator")
	}
}

// Module 3 LB-3 — the graph write-source label must be stripped like any other
// trusted header, and re-stamped by the gateway on graph routes.
//
// graph-service refuses a mutating request whose source is not an approved
// caller. That is only attribution if a CLIENT cannot supply the value: if a
// forged label survives, any caller claims to be `api-gateway` and the guard
// becomes decoration.
func TestGraphWriteSourceIsStrippedAndRestamped(t *testing.T) {
	var got map[string]string
	mw := jwtExtractMiddleware(
		jwtKeySet{activeKID: "v1", activeSecret: "secret"},
		devTestPolicy(),
		captureHandler(&got),
	)

	// A graph mutation: the forged label must be replaced with the gateway's.
	req := httptest.NewRequest(http.MethodPost, "/v1/graph/block", nil)
	req.Header.Set(edgeheaders.GraphWriteSourceHeader, "trust-safety-service")
	mw.ServeHTTP(httptest.NewRecorder(), req)
	if got[edgeheaders.GraphWriteSourceHeader] != edgeheaders.GatewayWriteSource {
		t.Fatalf("graph route: source=%q, want %q — a forged attribution survived",
			got[edgeheaders.GraphWriteSourceHeader], edgeheaders.GatewayWriteSource)
	}

	// A non-graph route: the forged label must be stripped and NOT replaced.
	req = httptest.NewRequest(http.MethodPost, "/v1/posts", nil)
	req.Header.Set(edgeheaders.GraphWriteSourceHeader, "trust-safety-service")
	mw.ServeHTTP(httptest.NewRecorder(), req)
	if got[edgeheaders.GraphWriteSourceHeader] != "" {
		t.Fatalf("non-graph route: source=%q survived; a client-supplied label "+
			"reached the upstream service", got[edgeheaders.GraphWriteSourceHeader])
	}
}
