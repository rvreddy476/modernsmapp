package middleware

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

func init() {
	gin.SetMode(gin.TestMode)
}

// newTestEngine creates a gin engine with the given middleware and a dummy 200 handler.
func newTestEngine(mw gin.HandlerFunc, path string) *gin.Engine {
	r := gin.New()
	r.POST(path, mw, func(c *gin.Context) {
		c.Status(http.StatusOK)
	})
	return r
}

// TestOTPRateLimitNilRedis verifies that OTPRateLimit(nil) calls c.Next() and does not block.
func TestOTPRateLimitNilRedis(t *testing.T) {
	r := newTestEngine(OTPRateLimit(nil), "/request-otp")

	body := []byte(`{"phone":"+911234567890"}`)
	req := httptest.NewRequest(http.MethodPost, "/request-otp", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", w.Code)
	}
}

// TestLoginRateLimitNilRedis verifies that LoginRateLimit(nil) calls c.Next() and does not block.
func TestLoginRateLimitNilRedis(t *testing.T) {
	r := newTestEngine(LoginRateLimit(nil), "/login")

	body := []byte(`{"identifier":"user@example.com","phone":""}`)
	req := httptest.NewRequest(http.MethodPost, "/login", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", w.Code)
	}
}

// TestAllowHelperLogic confirms that when rdb is nil the middleware returns 200
// (indirectly verifying that the nil guard runs before any Redis call, so no panic occurs).
func TestAllowHelperLogic(t *testing.T) {
	// OTPRateLimit skips allow() entirely when rdb == nil.
	// We exercise the nil path via a full round-trip rather than calling allow() directly
	// because allow() requires a non-nil *redis.Client.
	r := newTestEngine(OTPRateLimit(nil), "/otp")

	req := httptest.NewRequest(http.MethodPost, "/otp", bytes.NewReader([]byte(`{"phone":"+1555000000"}`)))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	// Must not panic and must return 200 (not 429).
	r.ServeHTTP(w, req)

	if w.Code == http.StatusTooManyRequests {
		t.Error("expected request to pass through (not 429) when rdb is nil")
	}
}

// TestOTPRateLimitPassesValidRequest verifies that a POST with valid phone JSON
// returns 200 (not 429) when rdb is nil.
func TestOTPRateLimitPassesValidRequest(t *testing.T) {
	r := newTestEngine(OTPRateLimit(nil), "/request-otp")

	body := []byte(`{"phone":"+911234567890"}`)
	req := httptest.NewRequest(http.MethodPost, "/request-otp", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200 for valid request with nil rdb, got %d", w.Code)
	}
	if w.Header().Get("Retry-After") != "" {
		t.Error("Retry-After header should not be set when rdb is nil")
	}
}
