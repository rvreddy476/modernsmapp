package otp

import (
	"errors"
	"strings"
	"testing"
)

func TestGenerateAndCompare_RoundTrip(t *testing.T) {
	hash, err := GenerateFromPassword([]byte("4827"), 0)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	if !strings.HasPrefix(string(hash), "r1$") {
		t.Fatalf("hash should be versioned: %q", hash)
	}
	if err := CompareHashAndPassword(hash, []byte("4827")); err != nil {
		t.Fatalf("compare: %v", err)
	}
}

func TestCompare_Mismatch(t *testing.T) {
	hash, _ := GenerateFromPassword([]byte("4827"), 0)
	err := CompareHashAndPassword(hash, []byte("9999"))
	if !errors.Is(err, ErrMismatchedHashAndPassword) {
		t.Fatalf("expected mismatch err; got %v", err)
	}
}

func TestCompare_InvalidHash(t *testing.T) {
	cases := []string{
		"",
		"plain text",
		"r2$abcd$efgh",            // wrong version
		"r1$not-base64$AA",        // bad salt
		"r1$AAAA$not-base64",      // bad derived
		"r1$AAAA",                 // missing third part
	}
	for _, c := range cases {
		err := CompareHashAndPassword([]byte(c), []byte("4827"))
		if !errors.Is(err, ErrInvalidHash) {
			t.Errorf("case %q: expected ErrInvalidHash; got %v", c, err)
		}
	}
}

func TestGenerate_DistinctSaltsPerCall(t *testing.T) {
	a, _ := GenerateFromPassword([]byte("4827"), 0)
	b, _ := GenerateFromPassword([]byte("4827"), 0)
	if string(a) == string(b) {
		t.Fatalf("two calls must yield distinct hashes (random salt); got identical")
	}
	// Both must verify against the same plaintext.
	if err := CompareHashAndPassword(a, []byte("4827")); err != nil {
		t.Fatalf("a: %v", err)
	}
	if err := CompareHashAndPassword(b, []byte("4827")); err != nil {
		t.Fatalf("b: %v", err)
	}
}

func TestGenerate_EmptyPasswordRejected(t *testing.T) {
	if _, err := GenerateFromPassword([]byte{}, 0); err == nil {
		t.Fatalf("expected error for empty password")
	}
}

func TestPBKDF2_DeterministicGivenSalt(t *testing.T) {
	// Sanity: same inputs -> same output (no platform drift).
	salt := []byte("0123456789abcdef")
	a := pbkdf2HMAC([]byte("4827"), salt, 100, 32)
	b := pbkdf2HMAC([]byte("4827"), salt, 100, 32)
	if string(a) != string(b) {
		t.Fatalf("pbkdf2 must be deterministic")
	}
	if len(a) != 32 {
		t.Fatalf("pbkdf2 output len: %d, want 32", len(a))
	}
}
