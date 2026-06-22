package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

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
	mw := jwtExtractMiddleware(jwtKeySet{activeKID: "v1", activeSecret: "secret"}, captureHandler(&got))

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
	mw := jwtExtractMiddleware(keys, captureHandler(&got))

	// A genuine ordinary-user token that carries NO scopes claim.
	token := signJWT(t, map[string]any{"alg": "HS256", "kid": "v1"}, map[string]any{
		"user_id": "real-user",
		"exp":     time.Now().Add(time.Hour).Unix(),
	}, keys.activeSecret)

	req := httptest.NewRequest(http.MethodGet, "/v1/admin/thing", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("X-Scopes", "admin superadmin") // forged privilege escalation

	mw.ServeHTTP(httptest.NewRecorder(), req)

	if got["X-User-Id"] != "real-user" {
		t.Fatalf("X-User-Id=%q want real-user (from token)", got["X-User-Id"])
	}
	if got["X-Scopes"] != "" {
		t.Fatalf("forged X-Scopes survived: %q (privilege escalation)", got["X-Scopes"])
	}
}

func TestGatewayHonoursScopesFromVerifiedToken(t *testing.T) {
	keys := jwtKeySet{activeKID: "v1", activeSecret: "secret"}
	var got map[string]string
	mw := jwtExtractMiddleware(keys, captureHandler(&got))

	// A real admin token: the scopes claim was stamped server-side at mint.
	token := signJWT(t, map[string]any{"alg": "HS256", "kid": "v1"}, map[string]any{
		"user_id": "admin-user",
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
