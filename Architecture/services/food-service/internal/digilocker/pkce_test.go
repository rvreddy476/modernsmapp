package digilocker

import (
	"regexp"
	"strings"
	"testing"
)

// RFC 7636 appendix B: the worked S256 example.
func TestCodeChallengeS256_RFC7636AppendixB(t *testing.T) {
	const verifier = "dBjftJeZ4CVP-mB92K27uhbUJU1p1r_wW1gFWFOEjXk"
	const want = "E9Melhoa2OwvFrEMTJguCHaoeK1t8URWbuGJSstw-cM"
	if got := CodeChallengeS256(verifier); got != want {
		t.Fatalf("challenge = %s, want %s", got, want)
	}
}

var base64URL43 = regexp.MustCompile(`^[A-Za-z0-9_-]{43}$`)

func TestNewCodeVerifierIs32RandomBytes(t *testing.T) {
	a, err := NewCodeVerifier()
	if err != nil {
		t.Fatal(err)
	}
	b, err := NewCodeVerifier()
	if err != nil {
		t.Fatal(err)
	}
	// 32 bytes base64url without padding is 43 characters, inside RFC 7636's
	// 43..128 range and its unreserved alphabet.
	if !base64URL43.MatchString(a) || !base64URL43.MatchString(b) {
		t.Fatal("verifier is not 43 base64url characters")
	}
	if a == b {
		t.Fatal("two verifiers are equal")
	}
	if CodeChallengeS256(a) == a {
		t.Fatal("challenge equals verifier")
	}
}

func TestStateShapeAndHash(t *testing.T) {
	s, err := NewState()
	if err != nil {
		t.Fatal(err)
	}
	s2, _ := NewState()
	if !ValidState(s) || s == s2 {
		t.Fatal("state is not a fresh 32-byte base64url token")
	}
	h := HashState(s)
	if len(h) != 64 || strings.Contains(h, s) || h != HashState(s) || h == HashState(s2) {
		t.Fatal("state hash is not a deterministic hex SHA-256 distinct per state")
	}
	for _, bad := range []string{"", "short", strings.Repeat("a", 42), strings.Repeat("a", 42) + "!", strings.Repeat("a", 44)} {
		if ValidState(bad) {
			t.Fatalf("ValidState accepted a malformed state of length %d", len(bad))
		}
	}
}
