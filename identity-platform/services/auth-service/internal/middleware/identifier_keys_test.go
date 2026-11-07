package middleware

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"strings"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
)

// testKeys is the keyer every limiter test runs with, as production does.
var testKeys = NewIdentifierKeys("test-only limiter secret")

func passwordResetLimiter(rdb *redis.Client) gin.HandlerFunc {
	return PasswordResetRateLimit(rdb, testKeys)
}

func otpLimiter(rdb *redis.Client) gin.HandlerFunc {
	return OTPRateLimit(rdb, testKeys)
}

// requireNoRawIdentifier fails if any Redis key holds one of the identifiers,
// in any case.
func requireNoRawIdentifier(t *testing.T, mr *miniredis.Miniredis, identifiers ...string) {
	t.Helper()
	keys := mr.Keys()
	if len(keys) == 0 {
		t.Fatal("no limiter keys were written; the limiter did not run")
	}
	for _, k := range keys {
		for _, id := range identifiers {
			if strings.Contains(strings.ToLower(k), strings.ToLower(id)) {
				t.Errorf("Redis key %q contains the raw identifier %q", k, id)
			}
		}
	}
}

// The OTP, password-reset and credential limiters never put an email
// address or a phone number in a Redis key.
func TestLimiterKeysNeverHoldTheIdentifier(t *testing.T) {
	const phone, digits, email = "+919876543210", "9876543210", "victim@example.com"

	r, mr := limiterEngine(t, otpLimiter)
	hitFrom(r, "10.4.0.1", `{"phone":"`+phone+`"}`)
	requireNoRawIdentifier(t, mr, phone, digits)

	r, mr = limiterEngine(t, passwordResetLimiter)
	hitFrom(r, "10.4.0.2", `{"identifier":"`+email+`"}`)
	hitFrom(r, "10.4.0.3", `{"phone":"`+phone+`"}`)
	requireNoRawIdentifier(t, mr, email, "victim", phone, digits)

	r, mr = limiterEngine(t, loginLimiter)
	hitFrom(r, "10.4.0.4", `{"email":"`+email+`"}`)
	requireNoRawIdentifier(t, mr, email, "victim")
}

// The per-phone OTP caps still hold across the key change: 5 per phone in 10
// minutes, from any number of addresses.
func TestOTPRateLimitCountsOnePhoneAcrossIPs(t *testing.T) {
	r, _ := limiterEngine(t, otpLimiter)
	if n := rotatingIPs(r, 9, `{"phone":"+919876543210"}`); n != 6 {
		t.Fatalf("first denial at request %d, want 6 (5 per phone, then 429)", n)
	}
	// Another phone has its own bucket.
	if code := hitFrom(r, "10.5.0.1", `{"phone":"+919876543211"}`); code != http.StatusOK {
		t.Fatalf("a different phone was refused (%d): the buckets are not per phone", code)
	}
}

// The digest is keyed: without the secret it cannot be recomputed, so a
// phone number cannot be recovered by hashing every number.
func TestIdentifierDigestIsKeyed(t *testing.T) {
	const id = "+919876543210"
	a, b := NewIdentifierKeys("secret-a"), NewIdentifierKeys("secret-b")
	if !a.Keyed() || !b.Keyed() {
		t.Fatal("a configured secret must give a keyed digest")
	}
	if a.Digest(id) == b.Digest(id) {
		t.Fatal("two secrets give the same digest: the secret is not in the MAC")
	}
	if a.Digest(id) == (IdentifierKeys{}).Digest(id) {
		t.Fatal("the keyed digest equals the unkeyed fallback")
	}
	if a.Digest(id) != NewIdentifierKeys("secret-a").Digest(id) {
		t.Fatal("one secret must give one digest, or replicas would count in different buckets")
	}
	if a.Digest(id) == a.Digest(id+"1") {
		t.Fatal("two identifiers share a digest")
	}
	if got := len(a.Digest(id)); got != 32 {
		t.Fatalf("digest length %d, want 32 hex characters (128 bits)", got)
	}
}

// The derivation is pinned: the MAC key is HMAC(secret, label), never the
// secret itself, so a digest is never also a MAC under the signing secret.
// Changing it restarts every limiter counter, so it must not drift silently.
func TestIdentifierDigestDerivation(t *testing.T) {
	const secret, id = "secret-a", "+919876543210"
	derive := hmac.New(sha256.New, []byte(secret))
	derive.Write([]byte(identifierKeyLabel))
	m := hmac.New(sha256.New, derive.Sum(nil))
	m.Write([]byte(id))
	want := hex.EncodeToString(m.Sum(nil)[:16])
	if got := NewIdentifierKeys(secret).Digest(id); got != want {
		t.Fatalf("Digest = %s, want HMAC(HMAC(secret, %q), id)[:16] = %s", got, identifierKeyLabel, want)
	}
	raw := hmac.New(sha256.New, []byte(secret))
	raw.Write([]byte(id))
	if NewIdentifierKeys(secret).Digest(id) == hex.EncodeToString(raw.Sum(nil)[:16]) {
		t.Fatal("the digest is a MAC under the signing secret itself")
	}
}

// With no secret configured the fallback is still not the raw value and not
// a bare SHA-256 of it (which public tables would answer).
func TestIdentifierDigestFallback(t *testing.T) {
	var k IdentifierKeys
	if k.Keyed() || NewIdentifierKeys("").Keyed() {
		t.Fatal("no secret must report unkeyed")
	}
	const id = "victim@example.com"
	bare := sha256.Sum256([]byte(id))
	if d := k.Digest(id); strings.Contains(d, "victim") ||
		d == hex.EncodeToString(bare[:16]) || d == NewIdentifierKeys("x").Digest(id) {
		t.Fatalf("fallback digest %q", d)
	}
}
