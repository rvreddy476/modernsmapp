package digilocker

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
)

// PKCE (RFC 7636) and the OAuth state, copied from food-service
// internal/digilocker/pkce.go. Both the state and the code_verifier are 32
// random bytes, base64url without padding (43 characters). Only a hash of the
// state is stored; the verifier is stored sealed.

const tokenLength = 43

func randomToken() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("digilocker: random token: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

// NewState returns a fresh single-use OAuth state.
func NewState() (string, error) { return randomToken() }

// NewCodeVerifier returns a fresh PKCE code_verifier.
func NewCodeVerifier() (string, error) { return randomToken() }

// CodeChallengeS256 is BASE64URL(SHA256(ASCII(code_verifier))), RFC 7636 §4.2.
func CodeChallengeS256(verifier string) string {
	sum := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

// HashState is the primary key the state is stored under. The domain prefix
// keeps a doorstep state hash distinct from food's for the same string.
func HashState(state string) string {
	sum := sha256.Sum256([]byte("doorstep.digilocker.state\x00" + state))
	return hex.EncodeToString(sum[:])
}

// ValidState reports whether s has the shape NewState produces.
func ValidState(s string) bool {
	if len(s) != tokenLength {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if !(c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-' || c == '_') {
			return false
		}
	}
	return true
}
