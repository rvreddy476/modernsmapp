package service

// openAddressRow is the one place a stored customer address becomes readable:
// ciphertext first, plaintext only while the cutover mode permits it.

import (
	"context"
	"strings"
	"testing"

	"github.com/atpost/commerce-service/internal/pii"
	"github.com/atpost/commerce-service/internal/store/postgres"
	"github.com/google/uuid"
)

func addressTestCipher(t *testing.T) *pii.Cipher {
	t.Helper()
	p, _, err := pii.LocalKeyProvider(
		[]byte("0123456789abcdef0123456789abcdef"),
		[]byte("fedcba9876543210fedcba9876543210"), nil)
	if err != nil {
		t.Fatal(err)
	}
	c, err := pii.New(p, []byte("address-open-test-salt"))
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// sealedRow is a row as the ciphertext-mode writer leaves it: sealed, with
// every identifying plaintext column empty.
func sealedRow(t *testing.T, c *pii.Cipher) *postgres.AddressRow {
	t.Helper()
	s, err := c.SealAddress(context.Background(), pii.ScopeProfile, pii.Address{
		ContactName: "Asha Rao", Phone: "9876543210",
		AddressLine1: "12 Lavelle Road", AddressLine2: "Flat 4B", Landmark: "Opp. park",
	})
	if err != nil {
		t.Fatal(err)
	}
	return &postgres.AddressRow{
		ID: uuid.New(), UserID: uuid.New(),
		ContactNameEnc: s.ContactName, PhoneEnc: s.Phone, AddressLine1Enc: s.AddressLine1,
		AddressLine2Enc: s.AddressLine2, LandmarkEnc: s.Landmark, KeyVersion: s.KeyVersion,
		City: "Bengaluru", State: "KA", PostalCode: "560002", Country: "IN",
	}
}

func legacyRow() *postgres.AddressRow {
	return &postgres.AddressRow{
		ID: uuid.New(), ContactName: "Legacy Buyer", Phone: "9111111111", AddressLine1: "5 Main St",
		City: "Bengaluru", State: "KA", PostalCode: "560002", Country: "IN",
	}
}

func TestASealedRowWithEmptyPlaintextOpensInCiphertextMode(t *testing.T) {
	c := addressTestCipher(t)
	svc := (&Service{}).WithPII(c).WithPIICutover(pii.ModeCiphertext)

	a, err := svc.openAddressRow(context.Background(), sealedRow(t, c))
	if err != nil {
		t.Fatal(err)
	}
	if a.ContactName != "Asha Rao" || a.Phone != "9876543210" || a.AddressLine1 != "12 Lavelle Road" ||
		a.AddressLine2 != "Flat 4B" || a.Landmark != "Opp. park" {
		t.Fatalf("opened %+v; the identifying fields must come from the ciphertext", a)
	}
	if a.City != "Bengaluru" || a.PostalCode != "560002" || a.Country != "IN" {
		t.Fatalf("routing fields lost: %+v", a)
	}
}

// Ciphertext wins even when stale plaintext is still beside it.
func TestCiphertextWinsOverPlaintextInEitherMode(t *testing.T) {
	c := addressTestCipher(t)
	row := sealedRow(t, c)
	row.ContactName, row.AddressLine1 = "Old Name", "Old Street"
	for _, mode := range []pii.Mode{pii.ModeDual, pii.ModeCiphertext} {
		a, err := (&Service{}).WithPII(c).WithPIICutover(mode).openAddressRow(context.Background(), row)
		if err != nil || a.ContactName != "Asha Rao" || a.AddressLine1 != "12 Lavelle Road" {
			t.Fatalf("mode %s: opened %+v (%v); want the sealed values", mode, a, err)
		}
	}
}

func TestAnUnsealedRowIsServedOnlyDuringDualWrite(t *testing.T) {
	c := addressTestCipher(t)

	a, err := (&Service{}).WithPII(c).WithPIICutover(pii.ModeDual).openAddressRow(context.Background(), legacyRow())
	if err != nil || a.ContactName != "Legacy Buyer" || a.AddressLine1 != "5 Main St" {
		t.Fatalf("dual mode legacy row = %+v, %v", a, err)
	}

	_, err = (&Service{}).WithPII(c).WithPIICutover(pii.ModeCiphertext).openAddressRow(context.Background(), legacyRow())
	if err == nil || !strings.Contains(err.Error(), "cutover is complete") {
		t.Fatalf("ciphertext mode served an unsealed row (err = %v); after cutover it is a backfill gap to surface", err)
	}
	if strings.Contains(err.Error(), "Legacy Buyer") || strings.Contains(err.Error(), "5 Main St") {
		t.Fatalf("the refusal leaks the address: %v", err)
	}
}

func TestARowWithNeitherCiphertextNorPlaintextIsRefused(t *testing.T) {
	row := &postgres.AddressRow{ID: uuid.New(), City: "Bengaluru", PostalCode: "560002"}
	if _, err := (&Service{}).WithPIICutover(pii.ModeDual).openAddressRow(context.Background(), row); err == nil {
		t.Fatal("an address with no identifying data at all was served as a blank address")
	}
}

func TestASealedRowNeedsTheCipher(t *testing.T) {
	row := sealedRow(t, addressTestCipher(t))
	if _, err := (&Service{}).WithPIICutover(pii.ModeCiphertext).openAddressRow(context.Background(), row); err == nil {
		t.Fatal("a sealed row was 'opened' with no cipher configured")
	}
}

func TestTheWireShapeOfAnOpenedAddress(t *testing.T) {
	row := &postgres.AddressRow{ID: uuid.New(), Label: "Home", AddressType: "home", IsDefault: true,
		City: "Bengaluru", State: "KA", PostalCode: "560002", Country: "IN"}
	c := customerAddressFrom(row, &pii.Address{ContactName: "Asha", Phone: "9", AddressLine1: "12 Road"})
	if c.ContactName != "Asha" || c.AddressLine1 != "12 Road" || c.Label != "Home" || !c.IsDefault {
		t.Fatalf("wire shape = %+v", c)
	}
	if c.AddressLine2 != nil || c.Landmark != nil {
		t.Fatal("an empty line 2 / landmark must be absent, not an empty string")
	}
}
