// Package kyc holds the format rules for Indian bank identifiers that more
// than one service captures: the IFSC and bank-account-number checks were
// lifted verbatim from commerce seller onboarding
// (commerce-service/internal/kyc/validator.go) so monetization's payout
// method capture (plan Phase 4D) applies exactly the same rule.
//
// These are FORMAT checks only. Nothing here looks the IFSC up in the RBI
// directory or confirms the account exists; RazorpayX refuses a bad IFSC at
// fund-account creation and a penny-drop validation confirms the account,
// and that is where the truth comes from.
package kyc

import (
	"errors"
	"regexp"
	"strings"
)

var (
	// IFSCPattern is the RBI format: four letters (bank), a zero, six
	// alphanumerics (branch).
	IFSCPattern = regexp.MustCompile(`^[A-Z]{4}0[A-Z0-9]{6}$`)

	// ErrInvalidIFSC and ErrInvalidBankAccount are the sentinels callers
	// map to a 4xx.
	ErrInvalidIFSC        = errors.New("INVALID_IFSC")
	ErrInvalidBankAccount = errors.New("INVALID_BANK_ACCOUNT")
)

const (
	bankAccountMinDigits = 9
	bankAccountMaxDigits = 18
)

// NormalizeIFSC trims and upper-cases s and returns it if it is a
// well-formed IFSC.
func NormalizeIFSC(s string) (string, error) {
	n := strings.ToUpper(strings.TrimSpace(s))
	if !IFSCPattern.MatchString(n) {
		return "", ErrInvalidIFSC
	}
	return n, nil
}

// ValidateIFSC reports whether s is a well-formed IFSC.
func ValidateIFSC(s string) error {
	_, err := NormalizeIFSC(s)
	return err
}

// NormalizeBankAccountNumber trims s and returns it if it is 9 to 18
// digits and nothing else.
func NormalizeBankAccountNumber(s string) (string, error) {
	n := strings.TrimSpace(s)
	if len(n) < bankAccountMinDigits || len(n) > bankAccountMaxDigits {
		return "", ErrInvalidBankAccount
	}
	for _, r := range n {
		if r < '0' || r > '9' {
			return "", ErrInvalidBankAccount
		}
	}
	return n, nil
}

// ValidateBankAccountNumber reports whether s is a well-formed account
// number.
func ValidateBankAccountNumber(s string) error {
	_, err := NormalizeBankAccountNumber(s)
	return err
}

// BankAccountLast4 is the part of an account number that may be shown
// and stored in the clear.
func BankAccountLast4(s string) string {
	n := strings.TrimSpace(s)
	if len(n) <= 4 {
		return n
	}
	return n[len(n)-4:]
}
