// Package kyc verifies seller onboarding documents (GSTIN, PAN, bank,
// UPI) before the admin can approve a seller. Phase 3.2.
//
// The interface is the integration point for a real vendor (Karza,
// Signzy, Hyperverge, IDfy) — those services additionally verify the
// document exists at the issuing authority and that the holder name
// matches. StubValidator below performs format-only validation and is
// safe for dev/QA only; the per-check `Source` field on the report
// makes a stub-only verdict obvious to operators reviewing a seller.
package kyc

import (
	"context"
	"regexp"
	"strings"
)

// Check is one field-level KYC verification verdict.
type Check struct {
	Field   string `json:"field"`
	Status  string `json:"status"` // "valid" | "invalid" | "skipped"
	Message string `json:"message,omitempty"`
	// Source is the adapter that produced the verdict. "stub" means
	// format-only — production builds must show this prominently so
	// admins know they're approving on incomplete verification.
	Source string `json:"source"`
}

// Report aggregates per-field checks. AllValid is true only when every
// supplied field validated; skipped fields don't count against it.
type Report struct {
	Checks   []Check `json:"checks"`
	AllValid bool    `json:"all_valid"`
}

// SellerSnapshot is the input to a KYC verification — the values the
// seller stored during onboarding. Empty fields are reported as "skipped"
// rather than rejected, since the seller may not be GST-registered or
// may use a UPI handle instead of a bank account.
type SellerSnapshot struct {
	GSTIN         string
	PAN           string
	IFSC          string
	BankAccountNo string
	UPI           string
}

// Validator is the interface a real KYC vendor drops into. Implementations
// must be safe for concurrent use — the service holds one instance.
type Validator interface {
	Verify(ctx context.Context, in SellerSnapshot) (*Report, error)
	Name() string
}

// StubValidator runs format-only checks. Use only when no production
// vendor is configured; an admin reviewing a seller with a stub-sourced
// report should treat the seller as unverified for risk purposes.
type StubValidator struct{}

func (StubValidator) Name() string { return "stub" }

var (
	gstinRe = regexp.MustCompile(`^[0-9]{2}[A-Z]{5}[0-9]{4}[A-Z][1-9A-Z]Z[0-9A-Z]$`)
	panRe   = regexp.MustCompile(`^[A-Z]{5}[0-9]{4}[A-Z]$`)
	ifscRe  = regexp.MustCompile(`^[A-Z]{4}0[A-Z0-9]{6}$`)
	upiRe   = regexp.MustCompile(`^[\w.\-]+@[\w.\-]+$`)
)

func (v StubValidator) Verify(_ context.Context, in SellerSnapshot) (*Report, error) {
	checks := []Check{
		formatCheck("gstin", in.GSTIN, gstinRe, "format only — vendor verification required"),
		formatCheck("pan", in.PAN, panRe, "format only — NSDL lookup required"),
		formatCheck("ifsc", in.IFSC, ifscRe, "format only — RBI directory lookup required"),
		bankAccountCheck(in.BankAccountNo),
		formatCheck("upi", in.UPI, upiRe, "format only — UPI handle resolution required"),
	}
	allValid := true
	anyChecked := false
	for _, c := range checks {
		if c.Status == "invalid" {
			allValid = false
		}
		if c.Status != "skipped" {
			anyChecked = true
		}
	}
	if !anyChecked {
		allValid = false
	}
	return &Report{Checks: checks, AllValid: allValid}, nil
}

func formatCheck(field, value string, re *regexp.Regexp, msg string) Check {
	if strings.TrimSpace(value) == "" {
		return Check{Field: field, Status: "skipped", Source: "stub"}
	}
	if !re.MatchString(strings.ToUpper(strings.TrimSpace(value))) {
		return Check{Field: field, Status: "invalid", Source: "stub", Message: "format does not match"}
	}
	return Check{Field: field, Status: "valid", Source: "stub", Message: msg}
}

func bankAccountCheck(value string) Check {
	s := strings.TrimSpace(value)
	if s == "" {
		return Check{Field: "bank_account", Status: "skipped", Source: "stub"}
	}
	if len(s) < 9 || len(s) > 18 {
		return Check{Field: "bank_account", Status: "invalid", Source: "stub", Message: "length must be 9-18 digits"}
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return Check{Field: "bank_account", Status: "invalid", Source: "stub", Message: "digits only"}
		}
	}
	return Check{Field: "bank_account", Status: "valid", Source: "stub", Message: "format only — penny-drop verification required"}
}
