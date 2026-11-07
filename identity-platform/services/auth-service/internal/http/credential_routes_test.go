package http

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/atpost/identity-auth-service/internal/config"
	"github.com/atpost/identity-auth-service/internal/middleware"
	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
)

func credentialRoutes(t *testing.T, cfg *config.Config) *gin.Engine {
	t.Helper()
	mr := miniredis.RunT(t)
	gin.SetMode(gin.TestMode)
	h := New(&stubAuthService{}, cfg, nil, redis.NewClient(&redis.Options{Addr: mr.Addr()}))
	r := gin.New()
	h.RegisterRoutes(r, noopMiddleware(), noopMiddleware())
	return r
}

func postFrom(r *gin.Engine, path, ip, body string) int {
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.RemoteAddr = ip + ":5555"
	return serve(r, req).Code
}

// The mobile Google sign-in route had no limit in this service at all. It now
// answers 429 once one IP has spent its credential budget. The body has no
// id_token, so every request stops at the handler's 400 without a service
// call; only the limiter can turn one into a 429.
func TestOAuthTokenRouteIsRateLimited(t *testing.T) {
	r := credentialRoutes(t, &config.Config{AuthRateLimitPerIP: 3})
	for i := 1; i <= 3; i++ {
		if code := postFrom(r, "/v1/auth/oauth/google/token", "100.64.1.1", `{}`); code == http.StatusTooManyRequests {
			t.Fatalf("attempt %d refused before the budget was spent", i)
		}
	}
	if code := postFrom(r, "/v1/auth/oauth/google/token", "100.64.1.1", `{}`); code != http.StatusTooManyRequests {
		t.Fatalf("4th token sign-in from one IP answered %d, want 429", code)
	}
}

// Login, register, 2FA and OAuth verify-signup no longer drain one shared
// bucket, and the per-IP budget is the configured one.
func TestCredentialRoutesDoNotShareABucket(t *testing.T) {
	r := credentialRoutes(t, &config.Config{AuthRateLimitPerIP: 2})
	for i := 0; i < 2; i++ {
		postFrom(r, "/v1/auth/login", "100.64.1.2", `{}`)
	}
	if code := postFrom(r, "/v1/auth/login", "100.64.1.2", `{}`); code != http.StatusTooManyRequests {
		t.Fatalf("login past the configured budget answered %d, want 429", code)
	}
	for _, p := range []string{"/v1/auth/register", "/v1/auth/2fa/verify", "/v1/auth/oauth/verify-signup", "/v1/auth/oauth/google/token"} {
		if code := postFrom(r, p, "100.64.1.2", `{}`); code == http.StatusTooManyRequests {
			t.Fatalf("%s refused after only login's budget was spent", p)
		}
	}
}

// Unset config is the launch default: 100 per IP.
func TestCredentialRoutesDefaultToTheLaunchLimit(t *testing.T) {
	r := credentialRoutes(t, &config.Config{})
	for i := 1; i <= middleware.DefaultCredentialPerIP; i++ {
		if code := postFrom(r, "/v1/auth/login", "100.64.1.3", `{}`); code == http.StatusTooManyRequests {
			t.Fatalf("login %d of %d from one address refused", i, middleware.DefaultCredentialPerIP)
		}
	}
	if code := postFrom(r, "/v1/auth/login", "100.64.1.3", `{}`); code != http.StatusTooManyRequests {
		t.Fatalf("login %d answered %d, want 429", middleware.DefaultCredentialPerIP+1, code)
	}
}
