package pii

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
)

func kycTestCipher(t *testing.T) *Cipher {
	t.Helper()
	provider, _, err := LocalKeyProvider(
		[]byte("0123456789abcdef0123456789abcdef"),
		[]byte("fedcba9876543210fedcba9876543210"),
		nil)
	if err != nil {
		t.Fatal(err)
	}
	c, err := New(provider, []byte("identifier-test-salt"))
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestAnIdentifierRoundTripsUnderTheKYCScope(t *testing.T) {
	c := kycTestCipher(t)
	ctx := context.Background()
	s, err := c.SealIdentifier(ctx, ScopeKYC, "bank_account", "123456789012", "HDFC0001234")
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(s.Enc, []byte("123456789012")) {
		t.Fatal("the ciphertext contains the account number")
	}
	if s.KeyVersion <= 0 || s.Hash == "" {
		t.Fatalf("sealed identifier is missing its key version or hash: %+v", s)
	}
	got, err := c.OpenIdentifier(ctx, ScopeKYC, s.Enc)
	if err != nil || got != "123456789012" {
		t.Fatalf("OpenIdentifier = %q, %v", got, err)
	}
}

// A bank account sealed as KYC must not open as a profile address, even though
// the local profile key is what the KYC key was derived from.
func TestAKYCCiphertextDoesNotOpenUnderAnotherScope(t *testing.T) {
	c := kycTestCipher(t)
	ctx := context.Background()
	s, err := c.SealIdentifier(ctx, ScopeKYC, "pan", "ABCDE1234F")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.OpenIdentifier(ctx, ScopeProfile, s.Enc); !errors.Is(err, ErrBadCiphertext) {
		t.Fatalf("opening KYC ciphertext under the profile scope: err = %v, want ErrBadCiphertext", err)
	}
}

func TestIdentifierHashesAreDomainSeparatedAndContextBound(t *testing.T) {
	c := kycTestCipher(t)
	ctx := context.Background()
	seal := func(domain, v string, parts ...string) string {
		s, err := c.SealIdentifier(ctx, ScopeKYC, domain, v, parts...)
		if err != nil {
			t.Fatal(err)
		}
		return s.Hash
	}
	a := seal("bank_account", "123456789012", "HDFC0001234")
	if b := seal("bank_account", "123456789012", "HDFC0001234"); a != b {
		t.Fatal("the same account at the same IFSC must hash the same, or duplicate detection cannot work")
	}
	if b := seal("bank_account", "123456789012", "ICIC0000001"); a == b {
		t.Fatal("the same digits at a different bank are a different account and must hash differently")
	}
	if b := seal("pan", "123456789012"); a == b {
		t.Fatal("identical digits in a different identifier domain must not collide")
	}
}

func TestSealingNothingIsRefused(t *testing.T) {
	if _, err := kycTestCipher(t).SealIdentifier(context.Background(), ScopeKYC, "pan", ""); !errors.Is(err, ErrEmptyIdentifier) {
		t.Fatalf("err = %v, want ErrEmptyIdentifier", err)
	}
	if _, err := kycTestCipher(t).OpenIdentifier(context.Background(), ScopeKYC, nil); !errors.Is(err, ErrBadCiphertext) {
		t.Fatalf("opening an absent ciphertext: err = %v, want ErrBadCiphertext", err)
	}
}

func TestMaskPAN(t *testing.T) {
	for in, want := range map[string]string{
		"ABCDE1234F":   "XXXXXX234F",
		" abcde1234f ": "XXXXXX234F",
		"ABC":          "XXX",
		"":             "",
	} {
		if got := MaskPAN(in); got != want {
			t.Errorf("MaskPAN(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestTheLocalKYCKeyIsDerivedWhenUnsetAndDistinct(t *testing.T) {
	profile := []byte("0123456789abcdef0123456789abcdef")
	snapshot := []byte("fedcba9876543210fedcba9876543210")
	p, derived, err := LocalKeyProvider(profile, snapshot, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !derived {
		t.Fatal("an unset KYC key must be reported as derived")
	}
	k := p.Keys[ScopeKYC]
	if len(k) != DataKeySize {
		t.Fatalf("derived KYC key is %d bytes", len(k))
	}
	if bytes.Equal(k, profile) || bytes.Equal(k, snapshot) {
		t.Fatal("the derived KYC key must not equal another scope's key")
	}
	again, _, _ := LocalKeyProvider(profile, snapshot, nil)
	if !bytes.Equal(again.Keys[ScopeKYC], k) {
		t.Fatal("derivation must be deterministic, or the backfill and the service seal under different keys")
	}

	explicit := []byte("kkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkk")
	p2, derived2, err := LocalKeyProvider(profile, snapshot, explicit)
	if err != nil || derived2 || !bytes.Equal(p2.Keys[ScopeKYC], explicit) {
		t.Fatalf("an explicit KYC key must be used as given (derived=%t err=%v)", derived2, err)
	}
	if _, _, err := LocalKeyProvider(profile, snapshot, []byte("short")); err == nil {
		t.Fatal("a malformed explicit KYC key must be refused")
	}
}

func TestParseModeForNamesTheVariable(t *testing.T) {
	if m, err := ParseModeFor("COMMERCE_KYC_PII_CUTOVER", "ciphertext"); err != nil || m != ModeCiphertext {
		t.Fatalf("ParseModeFor(ciphertext) = %v, %v", m, err)
	}
	_, err := ParseModeFor("COMMERCE_KYC_PII_CUTOVER", "cipher-text-ish")
	if err == nil || !strings.Contains(err.Error(), "COMMERCE_KYC_PII_CUTOVER") {
		t.Fatalf("an unrecognised mode must fail closed and name the variable: %v", err)
	}
	if _, err := ParseMode("nope"); err == nil || !strings.Contains(err.Error(), "COMMERCE_PII_CUTOVER") {
		t.Fatalf("ParseMode must keep naming COMMERCE_PII_CUTOVER: %v", err)
	}
}
