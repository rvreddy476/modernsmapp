package service

// Migration 035 — seller KYC identifiers: sealed on write, opened on read, and
// a raw Aadhaar number refused before it reaches Postgres.

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/atpost/commerce-service/internal/kyc"
	"github.com/atpost/commerce-service/internal/pii"
	"github.com/atpost/commerce-service/internal/store/postgres"
	"github.com/google/uuid"
)

func kycCipher(t *testing.T) *pii.Cipher {
	t.Helper()
	p, _, err := pii.LocalKeyProvider(
		[]byte("0123456789abcdef0123456789abcdef"),
		[]byte("fedcba9876543210fedcba9876543210"), nil)
	if err != nil {
		t.Fatal(err)
	}
	c, err := pii.New(p, []byte("service-kyc-test-salt"))
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// anAadhaarShapedNumber finds a 12-digit value that passes the check, so the
// tests do not hard-code a real-looking Aadhaar number.
func anAadhaarShapedNumber(t *testing.T) string {
	t.Helper()
	for d := '0'; d <= '9'; d++ {
		if v := "23456789012" + string(d); kyc.LooksLikeAadhaar(v) {
			return v
		}
	}
	t.Fatal("no check digit made the payload valid; the Verhoeff implementation is broken")
	return ""
}

func strptr(s string) *string { return &s }

// ─── Aadhaar ─────────────────────────────────────────────────────────

// Refused before Postgres: the service has a nil store, so reaching it would
// panic rather than return.
func TestAnAadhaarDocumentNumberIsRefusedBeforeReachingPostgres(t *testing.T) {
	svc := &Service{}
	number := anAadhaarShapedNumber(t)

	err := svc.SaveDocuments(context.Background(), uuid.New(), []postgres.SellerDocument{
		{DocumentType: "aadhaar", DocumentNumber: strptr(number), MediaID: uuid.New()},
	})
	if !errors.Is(err, ErrAadhaarNumberNotAccepted) {
		t.Fatalf("err = %v, want ErrAadhaarNumberNotAccepted", err)
	}
	if strings.Contains(err.Error(), number) || strings.Contains(err.Error(), number[8:]) {
		t.Fatalf("the refusal echoes the number it refused: %v", err)
	}
}

// Any value at all under the aadhaar type — even one that is not a valid
// Aadhaar — because that type stores a reference only.
func TestTheAadhaarTypeStoresAReferenceOnly(t *testing.T) {
	for _, number := range []string{"XXXX-XXXX-1234", "1234", "not a number"} {
		err := validateDocumentNumbers([]postgres.SellerDocument{
			{DocumentType: "aadhaar", DocumentNumber: strptr(number), MediaID: uuid.New()},
		})
		if !errors.Is(err, ErrAadhaarNumberNotAccepted) {
			t.Errorf("aadhaar with document_number %q: err = %v, want ErrAadhaarNumberNotAccepted", number, err)
		}
	}
	if err := validateDocumentNumbers([]postgres.SellerDocument{
		{DocumentType: "aadhaar", MediaID: uuid.New()},
		{DocumentType: "aadhaar", DocumentNumber: strptr("   "), MediaID: uuid.New()},
	}); err != nil {
		t.Fatalf("an aadhaar document with only its upload reference must be accepted: %v", err)
	}
}

// A seller typing their Aadhaar into a free-text document is the realistic
// leak, so every type is checked — except a cancelled cheque, whose number is
// a bank account and legitimately a long digit string.
func TestAnAadhaarShapedNumberIsRefusedUnderEveryOtherType(t *testing.T) {
	number := anAadhaarShapedNumber(t)
	for _, typ := range postgres.SellerDocumentTypes {
		err := validateDocumentNumbers([]postgres.SellerDocument{
			{DocumentType: typ, DocumentNumber: strptr(number[:4] + " " + number[4:8] + " " + number[8:]), MediaID: uuid.New()},
		})
		if typ == "cancelled_cheque" {
			if err != nil {
				t.Errorf("a cancelled cheque's 12-digit account number was refused as Aadhaar: %v", err)
			}
			continue
		}
		if !errors.Is(err, ErrAadhaarNumberNotAccepted) {
			t.Errorf("type %s with an Aadhaar-shaped number: err = %v, want ErrAadhaarNumberNotAccepted", typ, err)
		}
	}
	if err := validateDocumentNumbers([]postgres.SellerDocument{
		{DocumentType: "pan_card", DocumentNumber: strptr("ABCDE1234F"), MediaID: uuid.New()},
	}); err != nil {
		t.Fatalf("an ordinary PAN document was refused: %v", err)
	}
}

// ─── Payout account ──────────────────────────────────────────────────

// Refused BEFORE the store: a nil store would panic if the seal did not come
// first, and a service with no cipher must never store an account in plaintext.
func TestAPayoutAccountIsRefusedWithoutACipher(t *testing.T) {
	svc := &Service{}
	err := svc.SavePayout(context.Background(), uuid.New(), postgres.OnboardingPayoutInput{
		AccountHolderName: "Asha", AccountNumber: "123456789012", IFSCCode: strptr("HDFC0001234"),
	})
	if err == nil || !strings.Contains(err.Error(), "cipher") {
		t.Fatalf("err = %v, want a refusal naming the missing cipher", err)
	}
}

func TestAPayoutAccountIsSealedWithItsDisplayAndLookupFields(t *testing.T) {
	c := kycCipher(t)
	ctx := context.Background()
	in := postgres.OnboardingPayoutInput{
		AccountHolderName: "Asha", AccountNumber: " 123456789012 ", IFSCCode: strptr("HDFC0001234"),
	}
	for _, mode := range []pii.Mode{pii.ModeDual, pii.ModeCiphertext} {
		svc := (&Service{}).WithPII(c).WithKYCCutover(mode)
		w, err := svc.sealPayoutForWrite(ctx, in)
		if err != nil {
			t.Fatal(err)
		}
		got, err := c.OpenIdentifier(ctx, pii.ScopeKYC, w.AccountNumberEnc)
		if err != nil || got != "123456789012" {
			t.Fatalf("sealed account opens to %q (%v), want the trimmed number", got, err)
		}
		if w.KeyVersion <= 0 || w.Hash == "" || w.Last4 != "9012" {
			t.Fatalf("sealed write is missing version/hash/last4: %+v", w)
		}
		if w.WritePlaintext != (mode == pii.ModeDual) {
			t.Fatalf("mode %s: WritePlaintext = %t", mode, w.WritePlaintext)
		}
	}
}

// After cutover a row with no ciphertext is a defect, not a legacy row.
func TestAPayoutAccountReadFallsBackToPlaintextOnlyInDualMode(t *testing.T) {
	c := kycCipher(t)
	ctx := context.Background()
	legacy := &postgres.PayoutAccountRow{AccountNumber: "123456789012"}

	dual := (&Service{}).WithPII(c).WithKYCCutover(pii.ModeDual)
	if got, err := dual.openPayoutAccountNumber(ctx, legacy); err != nil || got != "123456789012" {
		t.Fatalf("dual mode legacy read = %q, %v", got, err)
	}

	cut := (&Service{}).WithPII(c).WithKYCCutover(pii.ModeCiphertext)
	if _, err := cut.openPayoutAccountNumber(ctx, legacy); err == nil {
		t.Fatal("ciphertext mode served a payout account from plaintext; after cutover that " +
			"hides exactly the missing-ciphertext defect the cutover exists to surface")
	}

	s, err := c.SealIdentifier(ctx, pii.ScopeKYC, "bank_account", "999988887777")
	if err != nil {
		t.Fatal(err)
	}
	// Ciphertext wins over stale plaintext in either mode.
	sealed := &postgres.PayoutAccountRow{AccountNumber: "123456789012", AccountNumberEnc: s.Enc}
	for _, svc := range []*Service{dual, cut} {
		if got, err := svc.openPayoutAccountNumber(ctx, sealed); err != nil || got != "999988887777" {
			t.Fatalf("sealed read = %q, %v; want the ciphertext's value", got, err)
		}
	}
}

// ─── PAN ─────────────────────────────────────────────────────────────

func TestASellerPANIsOpenedFromCiphertextAndFallsBackOnlyInDualMode(t *testing.T) {
	c := kycCipher(t)
	ctx := context.Background()

	dual := (&Service{}).WithPII(c).WithKYCCutover(pii.ModeDual)
	cut := (&Service{}).WithPII(c).WithKYCCutover(pii.ModeCiphertext)

	legacy := &postgres.Seller{PANNumber: strptr("ABCDE1234F")}
	if got, err := dual.sellerPAN(ctx, legacy); err != nil || got != "ABCDE1234F" {
		t.Fatalf("dual legacy PAN = %q, %v", got, err)
	}
	if _, err := cut.sellerPAN(ctx, legacy); err == nil {
		t.Fatal("ciphertext mode served a PAN from plaintext")
	}

	s, err := c.SealIdentifier(ctx, pii.ScopeKYC, "pan", "PQRSX6789Z")
	if err != nil {
		t.Fatal(err)
	}
	if got, err := cut.sellerPAN(ctx, &postgres.Seller{PANEnc: s.Enc}); err != nil || got != "PQRSX6789Z" {
		t.Fatalf("sealed PAN = %q, %v", got, err)
	}
	if got, err := cut.sellerPAN(ctx, &postgres.Seller{}); err != nil || got != "" {
		t.Fatalf("a seller with no PAN at all = %q, %v; want empty, not an error", got, err)
	}
}
