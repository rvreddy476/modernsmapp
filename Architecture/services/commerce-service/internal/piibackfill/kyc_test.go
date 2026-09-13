package piibackfill

import (
	"context"
	"errors"
	"testing"

	"github.com/atpost/commerce-service/internal/pii"
)

func unitKYCCipher(t *testing.T) *pii.Cipher {
	t.Helper()
	p, _, err := pii.LocalKeyProvider(
		[]byte("0123456789abcdef0123456789abcdef"),
		[]byte("fedcba9876543210fedcba9876543210"), nil)
	if err != nil {
		t.Fatal(err)
	}
	c, err := pii.New(p, []byte("kyc-unit-test-salt"))
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// The verify step is what stands between "encrypted" and "encrypted to the
// wrong thing". A ciphertext that opens cleanly to a DIFFERENT value must fail.
func TestVerifySealedRefusesACiphertextOfAnotherValue(t *testing.T) {
	c := unitKYCCipher(t)
	ctx := context.Background()
	s, err := c.SealIdentifier(ctx, pii.ScopeKYC, "bank_account", "123456789012")
	if err != nil {
		t.Fatal(err)
	}
	if err := verifySealed(ctx, c, s.Enc, "123456789012"); err != nil {
		t.Fatalf("a correct seal failed verification: %v", err)
	}
	if err := verifySealed(ctx, c, s.Enc, "999988887777"); !errors.Is(err, errVerifyMismatch) {
		t.Fatalf("a seal of another account passed verification (err = %v)", err)
	}
	if err := verifySealed(ctx, c, []byte("garbage-garbage-garbage"), "123456789012"); err == nil {
		t.Fatal("an unopenable ciphertext passed verification")
	}
}

// The backfill must seal exactly what the service seals, or duplicate
// detection sees one account as two.
func TestTheKYCInventoryNormalisesLikeTheService(t *testing.T) {
	byName := map[string]kycField{}
	for _, f := range kycFields {
		byName[f.name] = f
	}
	acct := byName["seller_payout_accounts"]
	if got := acct.normalize(" 123456789012 "); got != "123456789012" {
		t.Fatalf("account normalises to %q", got)
	}
	if got := acct.hashParts(kycCandidate{ifsc: " hdfc0001234 "}); len(got) != 1 || got[0] != "HDFC0001234" {
		t.Fatalf("account hash context = %v", got)
	}
	if got := acct.display("123456789012"); got != "9012" {
		t.Fatalf("account display = %q", got)
	}
	for _, name := range []string{"sellers", "organizations"} {
		f := byName[name]
		if got := f.normalize(" abcde1234f "); got != "ABCDE1234F" {
			t.Fatalf("%s PAN normalises to %q", name, got)
		}
		if got := f.display("ABCDE1234F"); got != "XXXXXX234F" {
			t.Fatalf("%s PAN display = %q", name, got)
		}
		if f.domain != "pan" {
			t.Fatalf("%s hashes under domain %q; a seller PAN and an organization PAN must be comparable", name, f.domain)
		}
	}
	if len(KYCTables()) != 3 {
		t.Fatalf("KYC inventory = %v", KYCTables())
	}
}
