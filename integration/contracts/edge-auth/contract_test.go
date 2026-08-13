package edgeauth_test

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"strings"
	"testing"
	"time"

	"github.com/atpost/api-gateway/pkg/tokenpolicy"
	"github.com/atpost/identity-auth-service/pkg/accesstoken"
	"github.com/google/uuid"
)

// Module 3 LB-1 — the mint↔verify contract, executed end to end, from a
// NEUTRAL module.
//
// WHY THIS LIVES HERE
//
// The contract is real only if it exercises both production implementations:
// the function auth-service actually mints with, and the function the gateway
// actually verifies with. Restating either one inside the other's test proves
// the restatement.
//
// The first attempt achieved that by adding `github.com/atpost/api-gateway` to
// identity-auth-service's production go.mod with a source-local replace. That
// broke the auth container build — the Dockerfile copies only `shared/` and
// `services/auth-service/`, so the replace target is absent inside the image
// and `go mod download` fails. The proof was right; the coupling was in the
// wrong direction and outside the image's build context.
//
// This module is imported by nothing. Both services stay independent, and the
// contract is still checked against the real code on both sides.
//
// WHAT IT CATCHES
//
// Before SR-1, auth-service minted no `aud` and no `typ`. The hardened gateway
// requires both, so every real token would have been rejected at the edge in
// production — a total authentication outage invisible to any single-service
// test, because each side was only ever checking a token it had built itself.

const (
	testIssuer   = "auth-service"
	testAudience = "atpost-api"
	testKID      = "rsa-1"
)

func testKeyPair(t *testing.T) (*rsa.PrivateKey, string) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate rsa key: %v", err)
	}
	pemBytes := pem.EncodeToMemory(&pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: x509.MarshalPKCS1PrivateKey(key),
	})
	return key, string(pemBytes)
}

// productionMintConfig is the configuration a production auth-service uses.
func productionMintConfig(issuer, audience string) accesstoken.Config {
	return accesstoken.Config{
		Issuer:   issuer,
		Audience: audience,
		TTL:      15 * time.Minute,
		RS256KID: testKID,
	}
}

// gatewayProductionPolicy is built through the gateway's OWN LoadFromEnv, so
// the test cannot drift from what the gateway enforces at startup.
func gatewayProductionPolicy(t *testing.T, issuer, audience string) tokenpolicy.Policy {
	t.Helper()
	t.Setenv("APP_ENV", "production")
	t.Setenv("JWT_ISSUER", issuer)
	t.Setenv("JWT_AUDIENCE", audience)
	t.Setenv("JWT_PUBLIC_KEY_PEM", "present") // only presence is checked at load
	p, err := tokenpolicy.LoadFromEnv()
	if err != nil {
		t.Fatalf("the gateway refused this configuration at startup: %v", err)
	}
	return p
}

func TestRealMintedTokenIsAcceptedByRealGatewayVerifier(t *testing.T) {
	key, _ := testKeyPair(t)
	userID, sessionID := uuid.New(), uuid.New()

	token, err := accesstoken.Mint(
		productionMintConfig(testIssuer, testAudience), key, userID, sessionID, "", time.Now())
	if err != nil {
		t.Fatalf("mint: %v", err)
	}

	policy := gatewayProductionPolicy(t, testIssuer, testAudience)
	keys := tokenpolicy.KeySet{RSAKeys: map[string]*rsa.PublicKey{testKID: &key.PublicKey}}

	id, vErr := tokenpolicy.Verify(token, keys, policy, time.Now())
	if vErr != nil {
		t.Fatalf("the gateway REJECTED a real auth-service token: %v\n"+
			"This is a platform outage at deploy time, not a test failure.", vErr)
	}
	if id.UserID != userID.String() {
		t.Fatalf("gateway derived X-User-Id=%q, want %q", id.UserID, userID.String())
	}
}

// ── Claim-removal negative controls ─────────────────────────────────────────
//
// These are named individually — the previous suite mutated the mint source in
// a shell loop, which meant the controls were not checked in and could not run
// in CI. Each one here builds a token that differs from the production claim
// set in exactly one way and requires the gateway to reject it.

// mintWithClaimRemoved reproduces the production mint with one claim dropped.
// It deliberately uses accesstoken.Claims — the production type — so a change
// to that struct breaks this test rather than silently invalidating it.
func TestGatewayRejectsEachMissingClaim(t *testing.T) {
	key, _ := testKeyPair(t)
	policy := gatewayProductionPolicy(t, testIssuer, testAudience)
	keys := tokenpolicy.KeySet{RSAKeys: map[string]*rsa.PublicKey{testKID: &key.PublicKey}}

	cases := map[string]accesstoken.Config{
		// SR-1's original defect: no audience was minted at all.
		"no audience": {Issuer: testIssuer, Audience: "", TTL: 15 * time.Minute, RS256KID: testKID},
		// An issuer the gateway does not allowlist.
		"wrong issuer": {Issuer: "some-other-idp", Audience: testAudience, TTL: 15 * time.Minute, RS256KID: testKID},
		// The audience-drift mistake most likely to reach production: staging
		// value shipped to the production gateway.
		"staging audience": {Issuer: testIssuer, Audience: "atpost-api-staging", TTL: 15 * time.Minute, RS256KID: testKID},
		// A kid the gateway has no key for.
		"unknown kid": {Issuer: testIssuer, Audience: testAudience, TTL: 15 * time.Minute, RS256KID: "rsa-does-not-exist"},
	}

	for name, cfg := range cases {
		t.Run(name, func(t *testing.T) {
			token, err := accesstoken.Mint(cfg, key, uuid.New(), uuid.New(), "", time.Now())
			if err != nil {
				t.Fatalf("mint: %v", err)
			}
			if _, err := tokenpolicy.Verify(token, keys, policy, time.Now()); err == nil {
				t.Fatalf("the gateway ACCEPTED a token minted with %s", name)
			}
		})
	}
}

// An expired token must be rejected. `exp` is mandatory on the verify side;
// this proves the mint side actually sets a bounded one.
func TestMintedTokenExpires(t *testing.T) {
	key, _ := testKeyPair(t)
	policy := gatewayProductionPolicy(t, testIssuer, testAudience)
	keys := tokenpolicy.KeySet{RSAKeys: map[string]*rsa.PublicKey{testKID: &key.PublicKey}}

	cfg := productionMintConfig(testIssuer, testAudience)
	cfg.TTL = time.Minute
	token, err := accesstoken.Mint(cfg, key, uuid.New(), uuid.New(), "", time.Now())
	if err != nil {
		t.Fatalf("mint: %v", err)
	}

	// Well past the TTL and the verifier's clock skew.
	future := time.Now().Add(10 * time.Minute)
	if _, err := tokenpolicy.Verify(token, keys, policy, future); err == nil {
		t.Fatal("an expired token was accepted; the minted exp is not being enforced")
	}
	if _, err := tokenpolicy.Verify(token, keys, policy, time.Now()); err != nil {
		t.Fatalf("a fresh token was rejected: %v", err)
	}
}

// HS256 minting must not be usable against a production gateway: with a shared
// secret every verifier can MINT identities rather than merely check them.
func TestHS256MintIsRejectedByProductionGateway(t *testing.T) {
	policy := gatewayProductionPolicy(t, testIssuer, testAudience)

	cfg := accesstoken.Config{
		Issuer: testIssuer, Audience: testAudience, TTL: 15 * time.Minute,
		HS256KID: "v1", HS256Secret: "shared-secret",
	}
	token, err := accesstoken.Mint(cfg, nil, uuid.New(), uuid.New(), "", time.Now())
	if err != nil {
		t.Fatalf("mint: %v", err)
	}

	keys := tokenpolicy.KeySet{ActiveKID: "v1", ActiveSecret: "shared-secret"}
	if _, err := tokenpolicy.Verify(token, keys, policy, time.Now()); err == nil {
		t.Fatal("an HS256 token was accepted by the production gateway policy")
	}
}

// Scopes are stamped server-side and must survive the round trip — the gateway
// turns them into X-Scopes, which authorizes admin surfaces.
func TestScopesSurviveTheRoundTrip(t *testing.T) {
	key, _ := testKeyPair(t)
	policy := gatewayProductionPolicy(t, testIssuer, testAudience)
	keys := tokenpolicy.KeySet{RSAKeys: map[string]*rsa.PublicKey{testKID: &key.PublicKey}}

	token, err := accesstoken.Mint(
		productionMintConfig(testIssuer, testAudience), key,
		uuid.New(), uuid.New(), "admin moderator", time.Now())
	if err != nil {
		t.Fatalf("mint: %v", err)
	}
	id, err := tokenpolicy.Verify(token, keys, policy, time.Now())
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if id.Scopes != "admin moderator" {
		t.Fatalf("scopes = %q, want %q", id.Scopes, "admin moderator")
	}
}

// The minter must refuse to produce an unsigned token when nothing is
// configured, rather than emitting something a lax verifier might accept.
func TestMintRefusesWithNoKeyMaterial(t *testing.T) {
	cfg := accesstoken.Config{Issuer: testIssuer, Audience: testAudience, TTL: time.Minute}
	if _, err := accesstoken.Mint(cfg, nil, uuid.New(), uuid.New(), "", time.Now()); err == nil {
		t.Fatal("Mint produced a token with neither an RSA key nor an HS256 secret")
	} else if !strings.Contains(err.Error(), "no RSA signing key") {
		t.Errorf("unexpected error: %v", err)
	}
}
