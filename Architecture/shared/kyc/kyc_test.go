package kyc

import (
	"errors"
	"testing"
)

// The IFSC rule lifted from commerce seller onboarding
// (commerce-service/internal/kyc/validator.go): four letters, a zero,
// six alphanumerics. Lower case is normalised, whitespace trimmed.
func TestIFSCValidation(t *testing.T) {
	valid := map[string]string{
		"HDFC0000053":    "HDFC0000053",
		"SBIN0001234":    "SBIN0001234",
		"ICIC0000001":    "ICIC0000001",
		"hdfc0000053":    "HDFC0000053",
		" KKBK0000958 ":  "KKBK0000958",
		"UTIB0000ABC":    "UTIB0000ABC",
		"PUNB0123456":    "PUNB0123456",
	}
	for in, want := range valid {
		got, err := NormalizeIFSC(in)
		if err != nil {
			t.Errorf("NormalizeIFSC(%q): %v", in, err)
			continue
		}
		if got != want {
			t.Errorf("NormalizeIFSC(%q) = %q, want %q", in, got, want)
		}
		if err := ValidateIFSC(in); err != nil {
			t.Errorf("ValidateIFSC(%q): %v", in, err)
		}
	}
	invalid := []string{
		"",
		"HDFC000053",   // ten characters
		"HDFC00000531", // twelve
		"HDFC1000053",  // fifth character must be 0
		"HDF00000053",  // three letters
		"1DFC0000053",  // digit in the bank code
		"HDFC0000 53",  // inner space
		"HDFC-000053",
		"BAD",
	}
	for _, in := range invalid {
		if err := ValidateIFSC(in); !errors.Is(err, ErrInvalidIFSC) {
			t.Errorf("ValidateIFSC(%q) = %v, want ErrInvalidIFSC", in, err)
		}
	}
}

// Bank account numbers: 9 to 18 digits, nothing else.
func TestBankAccountNumberValidation(t *testing.T) {
	for _, in := range []string{"123456789", "765432123456789", "123456789012345678", " 123456789 "} {
		if err := ValidateBankAccountNumber(in); err != nil {
			t.Errorf("ValidateBankAccountNumber(%q): %v", in, err)
		}
	}
	for _, in := range []string{"", "12345678", "1234567890123456789", "12345678a", "1234 56789", "+123456789"} {
		if err := ValidateBankAccountNumber(in); !errors.Is(err, ErrInvalidBankAccount) {
			t.Errorf("ValidateBankAccountNumber(%q) = %v, want ErrInvalidBankAccount", in, err)
		}
	}
	if got := BankAccountLast4("765432123456789"); got != "6789" {
		t.Errorf("BankAccountLast4 = %q", got)
	}
}
