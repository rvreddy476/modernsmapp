// Package payout holds the bank-account verification seam for food payout
// accounts. Payouts are OFF: nothing in food-service calls a transfer, and
// the only verifier implemented is DisabledVerifier.
package payout

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

type Status string

const (
	StatusNotVerified Status = "NOT_VERIFIED"
	StatusPending     Status = "PENDING"
	StatusVerified    Status = "VERIFIED"
	StatusFailed      Status = "FAILED"
)

// ReasonVerificationPendingOps is recorded while penny-drop is disabled:
// operations verifies the account out of band before any payout is enabled.
const ReasonVerificationPendingOps = "verification_pending_ops"

// BankAccountDetails is what a verifier receives. Its String and GoString
// redact, so a stray log line cannot print the account number.
type BankAccountDetails struct {
	HolderName    string
	AccountNumber string
	IFSC          string
}

func (d BankAccountDetails) String() string   { return "BankAccountDetails{redacted}" }
func (d BankAccountDetails) GoString() string { return d.String() }

type Verification struct {
	Status       Status
	Reason       string
	VerifiedName string
}

// BankVerifier confirms an account exists and names its holder (penny-drop).
type BankVerifier interface {
	Verify(ctx context.Context, account BankAccountDetails) (Verification, error)
}

// DisabledVerifier is selected while FOOD_PENNY_DROP_ENABLED is false. It
// never contacts a bank and never reports VERIFIED.
type DisabledVerifier struct{}

func (DisabledVerifier) Verify(context.Context, BankAccountDetails) (Verification, error) {
	return Verification{Status: StatusNotVerified, Reason: ReasonVerificationPendingOps}, nil
}

// ErrPennyDropNotImplemented stops startup when the flag is on: there is no
// adapter, and pretending otherwise would leave accounts unverified silently.
var ErrPennyDropNotImplemented = errors.New("FOOD_PENNY_DROP_ENABLED is true but no bank verifier adapter exists; leave it false")

// VerifierFromEnv reads FOOD_PENNY_DROP_ENABLED (default false).
func VerifierFromEnv(getenv func(string) string) (BankVerifier, error) {
	raw := strings.TrimSpace(getenv("FOOD_PENNY_DROP_ENABLED"))
	if raw == "" {
		return DisabledVerifier{}, nil
	}
	enabled, err := strconv.ParseBool(raw)
	if err != nil {
		return nil, fmt.Errorf("FOOD_PENNY_DROP_ENABLED must be true or false")
	}
	if !enabled {
		return DisabledVerifier{}, nil
	}
	return nil, ErrPennyDropNotImplemented
}
