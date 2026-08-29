// Package otp implements the ride-OTP hashing and verification primitive used by the
// partner-arrived -> in_progress transition.
package otp

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"strings"

	"golang.org/x/crypto/bcrypt"
)

// ErrMismatchedHashAndPassword mirrors bcrypt.ErrMismatchedHashAndPassword.
var ErrMismatchedHashAndPassword = bcrypt.ErrMismatchedHashAndPassword

// ErrInvalidHash is returned when the stored hash string can't be parsed.
var ErrInvalidHash = errors.New("rider/otp: invalid hash format")

// DefaultCost is the bcrypt cost used for OTP hashing.
const DefaultCost = 10

const Iterations = 25000

// GenerateFromPassword returns the bcrypt hash for the given OTP plaintext.
func GenerateFromPassword(password []byte, cost int) ([]byte, error) {
	if len(password) == 0 {
		return nil, errors.New("rider/otp: empty password")
	}
	if cost <= 0 {
		cost = DefaultCost
	}
	return bcrypt.GenerateFromPassword(password, cost)
}

// CompareHashAndPassword returns nil on match and ErrMismatchedHashAndPassword
// on mismatch. Supports bcrypt standard hash ($2a$, $2b$, $2y$) and legacy r1$ format.
func CompareHashAndPassword(hashed, password []byte) error {
	if len(hashed) == 0 || len(password) == 0 {
		return ErrInvalidHash
	}

	// Bcrypt hashes start with '$'
	if hashed[0] == '$' {
		return bcrypt.CompareHashAndPassword(hashed, password)
	}

	// Legacy r1$ PBKDF2 hash support for existing rows
	parts := strings.Split(string(hashed), "$")
	if len(parts) == 3 && parts[0] == "r1" {
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

	return ErrInvalidHash
}

func pbkdf2HMAC(password, salt []byte, iter, keyLen int) []byte {
	hashFn := sha256.New
	prf := hmac.New(hashFn, password)
	hashLen := prf.Size()
	numBlocks := (keyLen + hashLen - 1) / hashLen
	var out []byte
	for blockIdx := 1; blockIdx <= numBlocks; blockIdx++ {
		prf.Reset()
		prf.Write(salt)
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
