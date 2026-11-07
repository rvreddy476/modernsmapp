package config

import "testing"

// The rate limiters key account identifiers with the RS256 signing key when
// there is one (only this service holds it), else the HS256 secret. Never
// the internal service key: every pod holds that.
func TestRateLimitKeySecret(t *testing.T) {
	both := &Config{AccessTokenPrivateKeyPEM: " pem ", JWTSecret: "hs", InternalServiceKey: "shared"}
	if got := both.RateLimitKeySecret(); got != "pem" {
		t.Fatalf("with an RS256 key: %q, want the private key", got)
	}
	hs := &Config{JWTSecret: " hs ", InternalServiceKey: "shared"}
	if got := hs.RateLimitKeySecret(); got != "hs" {
		t.Fatalf("HS256 only: %q, want the JWT secret", got)
	}
	none := &Config{InternalServiceKey: "shared"}
	if got := none.RateLimitKeySecret(); got != "" {
		t.Fatalf("no signing secret: %q, want blank (the unkeyed fallback), never the internal key", got)
	}
}
