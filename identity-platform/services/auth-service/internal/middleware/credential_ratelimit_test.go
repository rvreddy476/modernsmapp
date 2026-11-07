package middleware

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
)

// loginLimiter is the login action at its default limits.
func loginLimiter(rdb *redis.Client) gin.HandlerFunc {
	return CredentialRateLimit(rdb, testKeys, ActionLogin, CredentialLimits{})
}

// credentialEngine mounts several actions' limiters on one Redis, the way
// the routes share it in production.
func credentialEngine(t *testing.T, lim CredentialLimits, actions map[string]CredentialAction) (*gin.Engine, *miniredis.Miniredis) {
	t.Helper()
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	r := gin.New()
	for path, a := range actions {
		r.POST(path, CredentialRateLimit(rdb, testKeys, a, lim), func(c *gin.Context) { c.Status(http.StatusOK) })
	}
	return r, mr
}

func hitPath(r *gin.Engine, path, ip, body string) int {
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.RemoteAddr = ip + ":1111"
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w.Code
}

// Carrier NAT: many people behind one IP, each signing in to their own
// account. The old shared bucket refused the eleventh; the per-IP cap now
// takes the default 100, and the 101st from that address is refused.
func TestCredentialLimitPerIPIsSizedForSharedAddresses(t *testing.T) {
	r, _ := credentialEngine(t, CredentialLimits{}, map[string]CredentialAction{"/login": ActionLogin})
	for i := 1; i <= DefaultCredentialPerIP; i++ {
		body := `{"identifier":"user` + strings.Repeat("x", i) + `@example.com"}`
		if code := hitPath(r, "/login", "100.64.0.1", body); code != http.StatusOK {
			t.Fatalf("attempt %d of %d from one NAT address refused (%d)", i, DefaultCredentialPerIP, code)
		}
	}
	if code := hitPath(r, "/login", "100.64.0.1", `{"identifier":"one-more@example.com"}`); code != http.StatusTooManyRequests {
		t.Fatalf("attempt %d from one address got %d, want 429", DefaultCredentialPerIP+1, code)
	}
}

// One account keeps the strict cap however many addresses the guesses use.
func TestCredentialLimitPerAccountStaysStrict(t *testing.T) {
	r, _ := credentialEngine(t, CredentialLimits{}, map[string]CredentialAction{"/login": ActionLogin})
	if n := rotatingIPsOn(r, "/login", 9, `{"identifier":"victim@example.com"}`); n != DefaultCredentialPerAccount+1 {
		t.Fatalf("first denial at request %d, want %d", n, DefaultCredentialPerAccount+1)
	}
}

// 2FA bodies name the account by user_id; the cap used to read only
// identifier/email/phone, so a TOTP guess run had only the per-IP cap.
func TestTwoFactorLimitIsPerUserID(t *testing.T) {
	r, _ := credentialEngine(t, CredentialLimits{}, map[string]CredentialAction{"/2fa": ActionTwoFactor})
	body := `{"user_id":"5B0E8D7C-1111-4222-8333-944455556666","code":"000000","pending_token":"p"}`
	if n := rotatingIPsOn(r, "/2fa", 9, body); n != DefaultCredentialPerAccount+1 {
		t.Fatalf("first 2FA denial at request %d, want %d", n, DefaultCredentialPerAccount+1)
	}
}

// Spending one action's IP budget leaves the others untouched.
func TestCredentialActionsHaveSeparateBuckets(t *testing.T) {
	lim := CredentialLimits{PerIP: 3}
	r, _ := credentialEngine(t, lim, map[string]CredentialAction{
		"/login":    ActionLogin,
		"/register": ActionRegister,
		"/2fa":      ActionTwoFactor,
		"/signup":   ActionOAuthVerifySignup,
		"/token":    ActionOAuthToken,
	})
	for i := 0; i < 4; i++ {
		hitPath(r, "/login", "100.64.0.2", `{}`)
	}
	if code := hitPath(r, "/login", "100.64.0.2", `{}`); code != http.StatusTooManyRequests {
		t.Fatalf("login past its budget got %d", code)
	}
	for _, p := range []string{"/register", "/2fa", "/signup", "/token"} {
		if code := hitPath(r, p, "100.64.0.2", `{}`); code != http.StatusOK {
			t.Fatalf("%s refused after only login's budget was spent (%d)", p, code)
		}
	}
}

// The OAuth token route had no limit of its own; it is capped per IP.
func TestOAuthTokenIsLimitedPerIP(t *testing.T) {
	r, _ := credentialEngine(t, CredentialLimits{PerIP: 2}, map[string]CredentialAction{"/token": ActionOAuthToken})
	for i := 0; i < 2; i++ {
		if code := hitPath(r, "/token", "100.64.0.3", `{"id_token":"x"}`); code != http.StatusOK {
			t.Fatalf("attempt %d refused (%d)", i+1, code)
		}
	}
	if code := hitPath(r, "/token", "100.64.0.3", `{"id_token":"x"}`); code != http.StatusTooManyRequests {
		t.Fatalf("third token sign-in from one IP got %d, want 429", code)
	}
}

// No email address, phone number or user id is ever part of a Redis key.
func TestCredentialKeysNeverHoldTheIdentifier(t *testing.T) {
	r, mr := credentialEngine(t, CredentialLimits{}, map[string]CredentialAction{"/login": ActionLogin, "/2fa": ActionTwoFactor})
	hitPath(r, "/login", "100.64.0.4", `{"identifier":"Victim@Example.com"}`)
	hitPath(r, "/2fa", "100.64.0.4", `{"user_id":"5b0e8d7c-1111-4222-8333-944455556666"}`)
	keys := mr.Keys()
	if len(keys) != 4 {
		t.Fatalf("keys %v, want an IP and an account key per action", keys)
	}
	for _, k := range keys {
		low := strings.ToLower(k)
		if strings.Contains(low, "victim") || strings.Contains(low, "example.com") || strings.Contains(low, "5b0e8d7c") {
			t.Fatalf("Redis key %q holds the raw identifier", k)
		}
	}
}

// Configured limits are honoured; zero fields take the defaults.
func TestCredentialLimitsDefaults(t *testing.T) {
	got := CredentialLimits{PerIP: 7}.withDefaults()
	want := CredentialLimits{PerIP: 7, PerAccount: DefaultCredentialPerAccount, Window: DefaultCredentialWindow}
	if got != want {
		t.Fatalf("withDefaults = %+v, want %+v", got, want)
	}
	if DefaultCredentialPerIP != 100 || DefaultCredentialPerAccount != 5 || DefaultCredentialWindow != 15*time.Minute {
		t.Fatal("default credential limits changed; update the launch decision first")
	}
}

func rotatingIPsOn(r *gin.Engine, path string, n int, body string) (firstDenied int) {
	for i := 0; i < n; i++ {
		ip := "10.9." + string(rune('0'+i/10)) + "." + string(rune('0'+i%10))
		if hitPath(r, path, ip, body) == http.StatusTooManyRequests {
			return i + 1
		}
	}
	return 0
}
