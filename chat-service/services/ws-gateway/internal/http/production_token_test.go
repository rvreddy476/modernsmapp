package http

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/atpost/chat-shared/accessauth"
	"github.com/google/uuid"
)

// Security sweep 2026-10-21 #3: the socket is opened with the user's own
// RS256 access token. Under the production policy the gateway refuses every
// HS256 token — even one signed with a secret this process holds — and every
// RS256 token signed by a key other than the platform's.
func TestAuthenticateUserFromJWTProductionAcceptsOnlyPlatformRS256(t *testing.T) {
	platformKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	foreignKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	userID := uuid.New()
	claims := map[string]any{
		"sub": userID.String(), "exp": time.Now().Add(time.Hour).Unix(),
		"iss": "auth-service", "aud": "atpost-api", "sid": "session-1", "typ": "access",
	}
	keys := JWTKeySet{
		ActiveKID: "v1", ActiveSecret: "leaked-shared-secret",
		RSAKeys: map[string]*rsa.PublicKey{"rsa-1": &platformKey.PublicKey},
		Policy: accessauth.Policy{
			Production: true, AllowedIssuers: []string{"auth-service"},
			RequiredAudience: "atpost-api", ClockSkew: time.Minute,
		},
	}
	connect := func(token string) (uuid.UUID, error) {
		req := httptest.NewRequest("GET", "/v1/ws/connect", nil)
		req.Header.Set("Sec-WebSocket-Protocol", "bearer, bearer."+token)
		id, _, err := authenticateUserFromJWTWithKeys(req, keys, false)
		return id, err
	}

	id, err := connect(signRS256Token(t, platformKey, "rsa-1", claims))
	if err != nil || id != userID {
		t.Fatalf("platform RS256 token: id=%v err=%v", id, err)
	}
	for name, token := range map[string]string{
		"HS256 with the held secret": signJWT(t, map[string]any{"alg": "HS256", "kid": "v1"}, claims, keys.ActiveSecret),
		"RS256 with a foreign key":   signRS256Token(t, foreignKey, "rsa-1", claims),
		"RS256 with an unknown kid":  signRS256Token(t, platformKey, "rsa-9", claims),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := connect(token); err == nil {
				t.Fatal("unsafe token accepted")
			}
		})
	}
}

func signRS256Token(t *testing.T, key *rsa.PrivateKey, kid string, claims map[string]any) string {
	t.Helper()
	header, _ := json.Marshal(map[string]any{"alg": "RS256", "kid": kid, "typ": "JWT"})
	payload, _ := json.Marshal(claims)
	input := base64.RawURLEncoding.EncodeToString(header) + "." + base64.RawURLEncoding.EncodeToString(payload)
	digest := sha256.Sum256([]byte(input))
	signature, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, digest[:])
	if err != nil {
		t.Fatal(err)
	}
	return input + "." + base64.RawURLEncoding.EncodeToString(signature)
}
