package pii

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"testing"
)

var (
	saltA = bytes.Repeat([]byte("salt-a-"), 3)
	saltB = bytes.Repeat([]byte("salt-b-"), 3)
)

func mustHasher(t *testing.T, salt []byte, domain string, n Normaliser) *LookupHasher {
	t.Helper()
	h, err := NewLookupHasher(salt, domain, n)
	if err != nil {
		t.Fatalf("NewLookupHasher: %v", err)
	}
	return h
}

func mustHash(t *testing.T, h *LookupHasher, v string) string {
	t.Helper()
	out, err := h.Hash(v)
	if err != nil {
		t.Fatalf("Hash: %v", err)
	}
	return out
}

func TestLookupHashFormat(t *testing.T) {
	h := mustHasher(t, saltA, "pan", CompactUpper)
	got := mustHash(t, h, "abcde 1234 f")
	mac := hmac.New(sha256.New, saltA)
	mac.Write([]byte("pan\x00ABCDE1234F"))
	want := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	if got != want {
		t.Fatalf("Hash = %s, want %s", got, want)
	}
	if len(got) != 43 {
		t.Fatalf("len = %d, want 43", len(got))
	}
}

func TestLookupHashStable(t *testing.T) {
	h1 := mustHasher(t, saltA, "pan", CompactUpper)
	h2 := mustHasher(t, saltA, "pan", CompactUpper)
	a, b, c := mustHash(t, h1, "ABCDE1234F"), mustHash(t, h1, "ABCDE1234F"), mustHash(t, h2, "ABCDE1234F")
	if a != b || a != c {
		t.Fatalf("hash not stable: %s %s %s", a, b, c)
	}
}

func TestLookupHashDiffersAcrossSalts(t *testing.T) {
	a := mustHash(t, mustHasher(t, saltA, "pan", CompactUpper), "ABCDE1234F")
	b := mustHash(t, mustHasher(t, saltB, "pan", CompactUpper), "ABCDE1234F")
	if a == b {
		t.Fatal("different salts produced the same hash")
	}
}

func TestLookupHashDiffersAcrossDomains(t *testing.T) {
	a := mustHash(t, mustHasher(t, saltA, "vehicle_registration", CompactUpper), "KA01AB1234")
	b := mustHash(t, mustHasher(t, saltA, "driving_licence", CompactUpper), "KA01AB1234")
	if a == b {
		t.Fatal("different domains produced the same hash")
	}
}

func TestLookupHashNormalises(t *testing.T) {
	h := mustHasher(t, saltA, "vehicle_registration", CompactUpper)
	want := mustHash(t, h, "KA01AB1234")
	for _, v := range []string{"KA 01 AB 1234", "ka01ab1234", "ka-01-ab-1234", " KA.01/AB 1234\t"} {
		if got := mustHash(t, h, v); got != want {
			t.Fatalf("Hash(%q) differs from canonical form", v)
		}
	}
}

func TestLookupHashRejectsEmpty(t *testing.T) {
	h := mustHasher(t, saltA, "pan", CompactUpper)
	for _, v := range []string{"", " ", " - . / ", "\t\n"} {
		got, err := h.Hash(v)
		expectErr(t, err, ErrEmptyLookupInput)
		if got != "" {
			t.Fatalf("Hash(%q) returned %q", v, got)
		}
	}
}

func TestLookupHasherConstruction(t *testing.T) {
	cases := []struct {
		name   string
		salt   []byte
		domain string
		norm   Normaliser
		want   error
	}{
		{"short_salt", saltA[:15], "pan", CompactUpper, ErrSaltTooShort},
		{"nil_salt", nil, "pan", CompactUpper, ErrSaltTooShort},
		{"nil_normaliser", saltA, "pan", nil, ErrNoNormaliser},
		{"empty_domain", saltA, "", CompactUpper, ErrInvalidDomain},
		{"nul_domain", saltA, "pan\x00x", CompactUpper, ErrInvalidDomain},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			h, err := NewLookupHasher(c.salt, c.domain, c.norm)
			expectErr(t, err, c.want)
			if h != nil {
				t.Fatal("hasher returned on failure")
			}
		})
	}
	if _, err := NewLookupHasher(saltA[:16], "pan", CompactUpper); err != nil {
		t.Fatalf("16-byte salt refused: %v", err)
	}
}

func TestLookupHasherCopiesSalt(t *testing.T) {
	salt := append([]byte(nil), saltA...)
	h := mustHasher(t, salt, "pan", CompactUpper)
	before := mustHash(t, h, "ABCDE1234F")
	salt[0] ^= 0xFF
	if after := mustHash(t, h, "ABCDE1234F"); after != before {
		t.Fatal("mutating the caller's salt changed the hash")
	}
}

func TestLookupHashNormaliserErrorPropagates(t *testing.T) {
	errNorm := errors.New("not an IFSC")
	h := mustHasher(t, saltA, "ifsc", func(string) (string, error) { return "", errNorm })
	got, err := h.Hash("anything")
	expectErr(t, err, errNorm)
	if got != "" {
		t.Fatalf("Hash returned %q", got)
	}
}

func TestCompactUpper(t *testing.T) {
	for in, want := range map[string]string{
		"KA 01 AB 1234":        "KA01AB1234",
		"mh-12/de.1433":        "MH12DE1433",
		"abcde1234f":           "ABCDE1234F",
		"\tDL 0420110012345\n": "DL0420110012345",
		"":                     "",
	} {
		got, err := CompactUpper(in)
		if err != nil || got != want {
			t.Fatalf("CompactUpper(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	if _, err := CompactUpper("KA\xff01"); !errors.Is(err, ErrInvalidLookupInput) {
		t.Fatalf("invalid UTF-8 error = %v", err)
	}
}
