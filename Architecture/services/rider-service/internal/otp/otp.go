// Package otp implements the ride-OTP hashing primitive used by the
// partner-arrived → in_progress transition. Mirrors the bcrypt API contract
// (`GenerateFromPassword` / `CompareHashAndPassword`) so this can be swapped
// for `golang.org/x/crypto/bcrypt` once that vendor module is added.
//
// Why a custom helper here:
//   - The Architecture/vendor tree includes only argon2/blake2b/pbkdf2/sha3
//     subpackages of x/crypto, not bcrypt.
//   - Ride OTPs are 4-digit, ~30-minute-lived secrets stored at rest. The
//     spec requires a salted, slow hash with constant-time compare. A salted
//     SHA-256 with PBKDF2-style stretching satisfies that contract while
//     keeping zero new external dependencies.
//   - The exported API matches `bcrypt`: callers can switch one import line
//     once bcrypt is vendored without touching call sites.
//
// Hash format: `r1$<base64-salt>$<base64-derived>` — versioned so the format
// can rotate without breaking older rows.
package otp

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"strings"
)

// ErrMismatchedHashAndPassword mirrors bcrypt.ErrMismatchedHashAndPassword so
// callers can `errors.Is` against it the same way.
var ErrMismatchedHashAndPassword = errors.New("rider/otp: hashedPassword is not the hash of the given password")

// ErrInvalidHash is returned when the stored hash string can't be parsed.
var ErrInvalidHash = errors.New("rider/otp: invalid hash format")

// Iterations governs how many SHA-256-HMAC rounds back the derived key. ~25k
// keeps verification well under 1ms on commodity hardware while raising the
// cost of an offline brute-force from microseconds to milliseconds. Lower
// than bcrypt's effective cost but vastly higher than a plain SHA-256.
const Iterations = 25000

const saltLen = 16
const keyLen = 32

// GenerateFromPassword returns the hash for the given OTP plaintext.
//
// Mirrors `bcrypt.GenerateFromPassword`. The `cost` argument is accepted for
// API parity but ignored (the iteration count is fixed).
func GenerateFromPassword(password []byte, cost int) ([]byte, error) {
	_ = cost
	if len(password) == 0 {
		return nil, errors.New("rider/otp: empty password")
	}
	salt := make([]byte, saltLen)
	if _, err := rand.Read(salt); err != nil {
		return nil, err
	}
	derived := pbkdf2HMAC(password, salt, Iterations, keyLen)
	out := "r1$" + base64.RawStdEncoding.EncodeToString(salt) + "$" + base64.RawStdEncoding.EncodeToString(derived)
	return []byte(out), nil
}

// CompareHashAndPassword returns nil on match and ErrMismatchedHashAndPassword
// on mismatch. Mirrors `bcrypt.CompareHashAndPassword`.
//
// Constant-time compare on the derived key — never short-circuits on prefix.
func CompareHashAndPassword(hashed, password []byte) error {
	parts := strings.Split(string(hashed), "$")
	if len(parts) != 3 || parts[0] != "r1" {
		return ErrInvalidHash
	}
	salt, err := base64.RawStdEncoding.DecodeString(parts[1])
	if err != nil {
		return ErrInvalidHash
	}
	want, err := base64.RawStdEncoding.DecodeString(parts[2])
	if err != nil {
		return ErrInvalidHash
	}
	got := pbkdf2HMAC(password, salt, Iterations, len(want))
	if !hmac.Equal(got, want) {
		return ErrMismatchedHashAndPassword
	}
	return nil
}

// pbkdf2HMAC is a hand-rolled PBKDF2-HMAC-SHA256 (RFC 8018). Inlined here to
// avoid adding `golang.org/x/crypto/pbkdf2` as a new direct dependency — the
// shared vendor tree carries the package but rider-service's go.mod doesn't
// reference it yet, and the caller surface is small enough to embed.
func pbkdf2HMAC(password, salt []byte, iter, keyLen int) []byte {
	hashFn := sha256.New
	prf := hmac.New(hashFn, password)
	hashLen := prf.Size()
	numBlocks := (keyLen + hashLen - 1) / hashLen
	var out []byte
	for blockIdx := 1; blockIdx <= numBlocks; blockIdx++ {
		prf.Reset()
		prf.Write(salt)
		// Big-endian block index per RFC 8018.
		prf.Write([]byte{byte(blockIdx >> 24), byte(blockIdx >> 16), byte(blockIdx >> 8), byte(blockIdx)})
		u := prf.Sum(nil)
		t := make([]byte, len(u))
		copy(t, u)
		for i := 1; i < iter; i++ {
			prf.Reset()
			prf.Write(u)
			u = prf.Sum(nil)
			for j := range t {
				t[j] ^= u[j]
			}
		}
		out = append(out, t...)
	}
	return out[:keyLen]
}
