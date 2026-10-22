//go:build webauthn

package http

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/atpost/identity-auth-service/internal/config"
	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
)

// Security sweep #25 (21 Oct 2026): the passkey sign-in pair carried no
// LoginRateLimit, while password, 2FA, OTP, reset and register all did. Both
// routes now answer 429 once one IP has spent the login quota (10 per 15
// min), exactly as POST /v1/auth/login does.
func TestPasskeyLoginRoutesAreLoginRateLimited(t *testing.T) {
	for _, path := range []string{"/v1/auth/passkeys/login/begin", "/v1/auth/passkeys/login/finish"} {
		t.Run(path, func(t *testing.T) {
			mr, err := miniredis.Run()
			if err != nil {
				t.Fatal(err)
			}
			defer mr.Close()
			gin.SetMode(gin.TestMode)
			cfg := &config.Config{
				WebAuthnRPID:          "localhost",
				WebAuthnRPDisplayName: "Momentum",
				WebAuthnRPOrigins:     []string{"http://localhost:3000"},
			}
			h := New(&stubAuthService{}, cfg, nil, redis.NewClient(&redis.Options{Addr: mr.Addr()}))
			r := gin.New()
			h.RegisterWebAuthnRoutes(r, noopMiddleware(), noopMiddleware())

			post := func() int {
				req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(`{}`))
				req.Header.Set("Content-Type", "application/json")
				req.RemoteAddr = "203.0.113.7:4242"
				w := httptest.NewRecorder()
				r.ServeHTTP(w, req)
				return w.Code
			}
			for i := 1; i <= 10; i++ {
				if code := post(); code == http.StatusTooManyRequests || code == http.StatusNotFound {
					t.Fatalf("attempt %d answered %d before the quota was spent", i, code)
				}
			}
			if code := post(); code != http.StatusTooManyRequests {
				t.Fatalf("11th passkey sign-in attempt from one IP answered %d, want 429", code)
			}
		})
	}
}
