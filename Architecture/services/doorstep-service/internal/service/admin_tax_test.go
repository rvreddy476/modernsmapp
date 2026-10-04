package service

import "testing"

func TestTaxRegistrationVerifiedGuard(t *testing.T) {
	gst, reason := "29ZZZPZ0000Z1Z6", "Reviewed registration ownership"
	if _, err := validateTaxRegistration(TaxRegistrationInput{GSTIN: &gst, Reason: &reason}); err == nil {
		t.Fatal("unverified registration accepted")
	}
}
func TestTaxRegistrationChecksumGuard(t *testing.T) {
	gst, reason := "29ZZZPZ0000Z1Z7", "Reviewed registration ownership"
	if _, err := validateTaxRegistration(TaxRegistrationInput{GSTIN: &gst, Verified: true, Reason: &reason}); err == nil {
		t.Fatal("invalid checksum accepted")
	}
}
func TestTaxRegistrationReasonGuard(t *testing.T) {
	gst, reason := "29ZZZPZ0000Z1Z6", "short"
	if _, err := validateTaxRegistration(TaxRegistrationInput{GSTIN: &gst, Verified: true, Reason: &reason}); err == nil {
		t.Fatal("short review reason accepted")
	}
}
func TestTaxRegistrationNormaliseAndRemove(t *testing.T) {
	gst, reason := " 29zzzpz0000z1z6 ", "Reviewed registration ownership"
	v, err := validateTaxRegistration(TaxRegistrationInput{GSTIN: &gst, Verified: true, Reason: &reason})
	if err != nil || v == nil || *v != "29ZZZPZ0000Z1Z6" {
		t.Fatalf("normalise: %v", err)
	}
	gst = ""
	v, err = validateTaxRegistration(TaxRegistrationInput{GSTIN: &gst, Reason: &reason})
	if err != nil || v != nil {
		t.Fatalf("remove: %v", err)
	}
}
