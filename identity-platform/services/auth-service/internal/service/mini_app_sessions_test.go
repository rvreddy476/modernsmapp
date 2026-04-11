package service

import (
	"testing"
	"time"

	"github.com/atpost/identity-auth-service/internal/config"
	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
)

func TestMiniAppSessionSignerIssuesRS256Tokens(t *testing.T) {
	cfg := &config.Config{
		MiniAppSessionTTL:    5 * time.Minute,
		MiniAppSessionIssuer: "atpost-mini-app-runtime",
		MiniAppSessionKeyID:  "mini-app-test-key",
	}

	signer, err := NewMiniAppSessionSigner(cfg, nil)
	if err != nil {
		t.Fatalf("NewMiniAppSessionSigner returned error: %v", err)
	}

	appID := uuid.New()
	userID := uuid.New()
	resp, err := signer.Issue(appID, userID, []string{"user.profile.read"})
	if err != nil {
		t.Fatalf("Issue returned error: %v", err)
	}

	token, err := jwt.ParseWithClaims(resp.AccessToken, &MiniAppSessionClaims{}, func(token *jwt.Token) (interface{}, error) {
		return signer.publicKey, nil
	})
	if err != nil {
		t.Fatalf("failed to parse signed token: %v", err)
	}
	if !token.Valid {
		t.Fatal("expected token to be valid")
	}

	claims, ok := token.Claims.(*MiniAppSessionClaims)
	if !ok {
		t.Fatalf("unexpected claims type: %T", token.Claims)
	}
	if claims.AppID != appID.String() {
		t.Fatalf("unexpected app_id: %s", claims.AppID)
	}
	if claims.UserID != userID.String() {
		t.Fatalf("unexpected user_id: %s", claims.UserID)
	}
	if token.Header["kid"] != "mini-app-test-key" {
		t.Fatalf("unexpected key id: %#v", token.Header["kid"])
	}
}

func TestMiniAppSessionSignerExposesJWKS(t *testing.T) {
	cfg := &config.Config{
		MiniAppSessionTTL:    5 * time.Minute,
		MiniAppSessionIssuer: "atpost-mini-app-runtime",
		MiniAppSessionKeyID:  "mini-app-test-key",
	}

	signer, err := NewMiniAppSessionSigner(cfg, nil)
	if err != nil {
		t.Fatalf("NewMiniAppSessionSigner returned error: %v", err)
	}

	jwks := signer.JWKS()
	if len(jwks.Keys) != 1 {
		t.Fatalf("expected 1 jwk, got %d", len(jwks.Keys))
	}
	if jwks.Keys[0].Kid != "mini-app-test-key" {
		t.Fatalf("unexpected jwk kid: %s", jwks.Keys[0].Kid)
	}
	if jwks.Keys[0].N == "" || jwks.Keys[0].E == "" {
		t.Fatalf("expected jwk modulus and exponent to be populated: %#v", jwks.Keys[0])
	}
}
