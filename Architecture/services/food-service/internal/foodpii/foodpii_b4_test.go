package foodpii

import (
	"bytes"
	"context"
	"errors"
	"testing"
)

// Wave 1 B4: delivery-partner document numbers and the DigiLocker PKCE
// verifier. Synthetic values only.

func TestSealDeliveryDocumentNumbers(t *testing.T) {
	c := testCrypto(t)
	ctx := context.Background()
	const dlNumber, rcNumber = "KA0120200000001", "KA01AB1234"

	dl, err := c.SealDrivingLicence(ctx, dlNumber)
	if err != nil {
		t.Fatal(err)
	}
	// testCrypto registers v1 and v2; the highest version seals.
	if dl.KeyVersion != 2 || dl.Lookup == "" || bytes.Contains(dl.Blob, []byte(dlNumber)) {
		t.Fatal("driving licence is not sealed with a lookup hash")
	}
	if got, err := c.OpenPartnerDocumentNumber(ctx, dl.Blob); err != nil || got != dlNumber {
		t.Fatalf("open = %v", err)
	}
	spaced, _ := c.SealDrivingLicence(ctx, "ka-01-2020-0000001")
	if spaced.Lookup != dl.Lookup {
		t.Fatal("the DL lookup does not normalise separators and case")
	}

	rc, err := c.SealVehicleRegistration(ctx, rcNumber)
	if err != nil || rc.Lookup == "" {
		t.Fatalf("rc = %v", err)
	}
	asDL, _ := c.SealDrivingLicence(ctx, rcNumber)
	if asDL.Lookup == rc.Lookup {
		t.Fatal("driving_licence and vehicle_registration share a lookup domain")
	}

	other, err := c.SealPartnerDocumentNumber(ctx, "POL-778")
	if err != nil || other.Lookup != "" || len(other.Blob) == 0 {
		t.Fatalf("other document = %+v, %v; want sealed without a lookup", other.KeyVersion, err)
	}

	pan, _ := c.SealPAN(ctx, "ZZZPZ0000Z")
	if _, err := c.OpenPartnerDocumentNumber(ctx, pan.Blob); err == nil {
		t.Fatal("a PAN blob opened under the partner-document scope")
	}
}

func TestSealCodeVerifier(t *testing.T) {
	c := testCrypto(t)
	ctx := context.Background()
	const verifier = "synthetic-verifier-for-unit-tests-000000001"
	blob, version, err := c.SealCodeVerifier(ctx, verifier)
	if err != nil || version != 2 || bytes.Contains(blob, []byte(verifier)) {
		t.Fatalf("seal verifier = %d, %v", version, err)
	}
	if got, err := c.OpenCodeVerifier(ctx, blob); err != nil || got != verifier {
		t.Fatalf("open verifier = %v", err)
	}
	doc, _ := c.SealPartnerDocumentNumber(ctx, "POL-778")
	if _, err := c.OpenCodeVerifier(ctx, doc.Blob); err == nil {
		t.Fatal("a partner-document blob opened as a verifier")
	}
}

func TestNilCryptoB4FailsClosed(t *testing.T) {
	var c *Crypto
	ctx := context.Background()
	checks := []error{}
	_, err := c.SealDrivingLicence(ctx, "KA0120200000001")
	checks = append(checks, err)
	_, err = c.SealVehicleRegistration(ctx, "KA01AB1234")
	checks = append(checks, err)
	_, err = c.SealPartnerDocumentNumber(ctx, "X")
	checks = append(checks, err)
	_, _, err = c.SealCodeVerifier(ctx, "v")
	checks = append(checks, err)
	_, err = c.OpenCodeVerifier(ctx, []byte("x"))
	checks = append(checks, err)
	_, err = c.OpenPartnerDocumentNumber(ctx, []byte("x"))
	checks = append(checks, err)
	for i, err := range checks {
		if !errors.Is(err, ErrNotConfigured) {
			t.Fatalf("call %d on nil Crypto = %v, want ErrNotConfigured", i, err)
		}
	}
}
