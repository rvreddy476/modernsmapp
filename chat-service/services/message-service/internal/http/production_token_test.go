package http

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/atpost/chat-shared/accessauth"
	"github.com/gin-gonic/gin"
)

// Security sweep 2026-10-21 #3: the web tier used to mint HS256 chat tokens
// for any user id. The middleware now sees the user's own RS256 access token
// and, under the production policy, refuses every symmetric token — even one
// signed with a secret this process happens to hold — and every RS256 token
// signed by a key other than the platform's.
func TestAuthMiddlewareProductionAcceptsOnlyPlatformRS256(t *testing.T) {
	gin.SetMode(gin.TestMode)
	platformKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	foreignKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	const userID = "11111111-1111-4111-8111-111111111111"
	claims := map[string]any{
		"sub": userID, "exp": time.Now().Add(time.Hour).Unix(),
		"iss": "auth-service", "aud": "atpost-api", "sid": "session-1", "typ": "access",
	}
	keys := JWTKeySet{
		AccessKeys: accessauth.KeySet{
			ActiveKID: "v1", ActiveSecret: "leaked-shared-secret",
			RSAKeys: map[string]*rsa.PublicKey{"rsa-1": &platformKey.PublicKey},
		},
		Policy: accessauth.Policy{
			Production: true, AllowedIssuers: []string{"auth-service"},
			RequiredAudience: "atpost-api", ClockSkew: time.Minute,
		},
	}

	router := gin.New()
	router.Use(AuthMiddlewareWithKeys(keys, slog.New(slog.NewTextHandler(io.Discard, nil))))
	router.GET("/v1/chat/conversations", func(c *gin.Context) {
		c.String(http.StatusOK, c.GetString(userIDKey))
	})
	serve := func(token string) *httptest.ResponseRecorder {
		request := httptest.NewRequest(http.MethodGet, "/v1/chat/conversations", nil)
		request.Header.Set("Authorization", "Bearer "+token)
		response := httptest.NewRecorder()
		router.ServeHTTP(response, request)
		return response
	}

	if response := serve(signRS256Token(t, platformKey, "rsa-1", claims)); response.Code != http.StatusOK || response.Body.String() != userID {
		t.Fatalf("platform RS256 token: status=%d body=%q", response.Code, response.Body.String())
	}
	for name, token := range map[string]string{
		"HS256 with the held secret": signJWT(t, map[string]any{"alg": "HS256", "kid": "v1"}, claims, keys.AccessKeys.ActiveSecret),
		"RS256 with a foreign key":   signRS256Token(t, foreignKey, "rsa-1", claims),
		"RS256 with an unknown kid":  signRS256Token(t, platformKey, "rsa-9", claims),
	} {
		t.Run(name, func(t *testing.T) {
			if response := serve(token); response.Code != http.StatusUnauthorized {
				t.Fatalf("unsafe token accepted: status=%d", response.Code)
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
