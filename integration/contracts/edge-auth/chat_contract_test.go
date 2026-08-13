package edgeauth_test

import (
	"crypto/rand"
	"crypto/rsa"
	"testing"
	"time"

	"github.com/atpost/chat-shared/accessauth"
	"github.com/atpost/identity-auth-service/pkg/accesstoken"
	"github.com/google/uuid"
)

// Module 5 P0-1: exercise the real auth-service minter against the exact
// verifier imported by message-service, call-service, and ws-gateway.
func TestRealMintedTokenIsAcceptedByEveryChatVerifier(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	userID, sessionID := uuid.New(), uuid.New()
	now := time.Now()
	token, err := accesstoken.Mint(accesstoken.Config{
		Issuer: "auth-service", Audience: "atpost-api", TTL: 15 * time.Minute,
		RS256KID: "rsa-1",
	}, key, userID, sessionID, "chat", now)
	if err != nil {
		t.Fatal(err)
	}
	identity, err := accessauth.Verify(token, accessauth.KeySet{
		RSAKeys: map[string]*rsa.PublicKey{"rsa-1": &key.PublicKey},
	}, accessauth.Policy{
		Production: true, AllowedIssuers: []string{"auth-service"},
		RequiredAudience: "atpost-api", ClockSkew: time.Minute,
	}, now)
	if err != nil {
		t.Fatalf("chat services reject the real production access token: %v", err)
	}
	if identity.UserID != userID.String() || identity.SessionID != sessionID.String() {
		t.Fatalf("identity mismatch: %#v", identity)
	}
}
