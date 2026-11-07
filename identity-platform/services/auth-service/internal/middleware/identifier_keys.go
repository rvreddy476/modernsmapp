package middleware

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
)

// Account identifiers in limiter keys.
//
// The OTP and password-reset limiters used to key their per-account counters
// on the raw value, "otp_rl:+9198…" and "pwreset_rl:id:victim@example.com",
// so anyone who could list the Redis keys had a list of the phone numbers
// and email addresses being sent codes. The credential limiters hashed it
// with a bare SHA-256, which does not protect a phone number: there are only
// about ten billion of them, and hashing every one is an afternoon's work.
//
// Every limiter now keys an identifier by HMAC-SHA256 under a server secret
// that never leaves auth-service's configuration (Config.RateLimitKeySecret).
// Without the secret a key cannot be matched to an identifier, even by
// enumerating every phone number.

// identifierKeyLabel separates this use of the secret from every other one,
// and is the fixed domain prefix of the unkeyed fallback.
const identifierKeyLabel = "auth-service/rate-limit-identifier/v1"

// IdentifierKeys turns an account identifier into its limiter-key form. The
// zero value is the unkeyed fallback.
type IdentifierKeys struct {
	mac []byte // nil: no server secret was configured
}

// NewIdentifierKeys derives the limiter key from secret. The secret itself
// is never used as the MAC key: HMAC(secret, label) is, so the limiter can
// never produce anything that is also a signature or a token under it.
//
// A blank secret gives the unkeyed fallback, SHA-256 over the domain prefix
// and the identifier: the raw value is still never in a key, but the digest
// of a phone number can be reversed by enumeration. Production always has a
// secret (Config.ValidateForProduction requires JWT_PRIVATE_KEY_PEM).
func NewIdentifierKeys(secret string) IdentifierKeys {
	if secret == "" {
		return IdentifierKeys{}
	}
	m := hmac.New(sha256.New, []byte(secret))
	m.Write([]byte(identifierKeyLabel))
	return IdentifierKeys{mac: m.Sum(nil)}
}

// Keyed reports whether a server secret is in use.
func (k IdentifierKeys) Keyed() bool { return k.mac != nil }

// Digest is the identifier as it appears in a Redis key: 128 bits, hex. The
// caller normalises the identifier first, so variants of one account share
// one bucket.
func (k IdentifierKeys) Digest(identifier string) string {
	var sum []byte
	if k.mac != nil {
		m := hmac.New(sha256.New, k.mac)
		m.Write([]byte(identifier))
		sum = m.Sum(nil)
	} else {
		s := sha256.Sum256([]byte(identifierKeyLabel + "\x00" + identifier))
		sum = s[:]
	}
	return hex.EncodeToString(sum[:16])
}
