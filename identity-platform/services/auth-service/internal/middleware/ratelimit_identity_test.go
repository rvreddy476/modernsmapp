package middleware

// Regression tests for audit-auth-login H1, H4 and M1, from the audit's probe
// tests: the per-account caps must read the fields clients actually send, and
// the window must not slide.

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
)

// hitFrom posts body from a given client IP and returns the status.
func hitFrom(r *gin.Engine, ip, body string) int {
	req := httptest.NewRequest(http.MethodPost, "/x", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	req.RemoteAddr = ip + ":1111"
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w.Code
}

func limiterEngine(t *testing.T, mw func(*redis.Client) gin.HandlerFunc) (*gin.Engine, *miniredis.Miniredis) {
	t.Helper()
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	return newTestEngine(mw(rdb), "/x"), mr
}

// Each request comes from a different IP, so only a per-ACCOUNT cap can stop
// the run: the per-IP cap never trips.
func rotatingIPs(r *gin.Engine, n int, body string) (firstDenied int) {
	for i := 0; i < n; i++ {
		ip := "10.0." + string(rune('0'+i/10)) + "." + string(rune('0'+i%10))
		if hitFrom(r, ip, body) == http.StatusTooManyRequests {
			return i + 1
		}
	}
	return 0
}

// H4: /login and the admin console accept `email`; the per-account cap used to
// read only `identifier`/`phone`.
func TestLoginRateLimitCountsEmailField(t *testing.T) {
	r, _ := limiterEngine(t, LoginRateLimit)
	if n := rotatingIPs(r, 9, `{"email":"victim@example.com","password":"x"}`); n != 6 {
		t.Fatalf("first denial at request %d, want 6 (5 per account, then 429)", n)
	}
}

// H1: every client sends `identifier` to forgot/reset; the cap used to read
// only `email`/`phone`.
func TestPasswordResetRateLimitCountsIdentifierField(t *testing.T) {
	r, _ := limiterEngine(t, PasswordResetRateLimit)
	if n := rotatingIPs(r, 9, `{"identifier":"victim@example.com","code":"000000","new_password":"Aa1!aaaa"}`); n != 4 {
		t.Fatalf("first denial at request %d, want 4 (3 per account, then 429)", n)
	}
}

// Case and whitespace variants of one account share one bucket, and the
// fields are interchangeable.
func TestRateLimitIdentifierIsNormalised(t *testing.T) {
	r, _ := limiterEngine(t, LoginRateLimit)
	bodies := []string{
		`{"identifier":"victim@example.com"}`,
		`{"identifier":"Victim@Example.com"}`,
		`{"email":" VICTIM@example.com "}`,
		`{"identifier":"victim@EXAMPLE.com"}`,
		`{"email":"victim@example.com"}`,
		`{"identifier":"victim@example.com "}`,
	}
	for i, b := range bodies {
		code := hitFrom(r, "10.1.0."+string(rune('1'+i)), b)
		if i < 5 && code != http.StatusOK {
			t.Fatalf("request %d denied early (%d)", i+1, code)
		}
		if i == 5 && code != http.StatusTooManyRequests {
			t.Fatalf("6th request for the same account via variants got %d, want 429", code)
		}
	}
}

// M1: one request every 14 minutes kept an identifier locked forever.
func TestLimiterWindowDoesNotSlide(t *testing.T) {
	r, mr := limiterEngine(t, LoginRateLimit)
	body := `{"identifier":"victim@example.com"}`
	for i := 0; i < 6; i++ {
		hitFrom(r, "10.2.0.1", body)
	}
	mr.FastForward(14 * time.Minute)
	if code := hitFrom(r, "10.2.0.2", body); code != http.StatusTooManyRequests {
		t.Fatalf("over-limit identifier allowed inside its window: %d", code)
	}
	mr.FastForward(2 * time.Minute) // 16 minutes after the window opened
	if code := hitFrom(r, "10.2.0.3", body); code != http.StatusOK {
		t.Fatalf("identifier still locked after its 15-minute window ended: %d (ttl %v)",
			code, mr.TTL("login_rl_id:victim@example.com"))
	}
}

// A counter must never be left without an expiry, or it is a permanent lock.
func TestLimiterCounterAlwaysExpires(t *testing.T) {
	r, mr := limiterEngine(t, PasswordResetRateLimit)
	mr.Set("pwreset_rl:id:victim@example.com", "1") // a counter that lost its TTL
	hitFrom(r, "10.3.0.1", `{"identifier":"victim@example.com"}`)
	if ttl := mr.TTL("pwreset_rl:id:victim@example.com"); ttl <= 0 {
		t.Fatalf("counter has no expiry (ttl %v): it would lock the account forever", ttl)
	}
}
