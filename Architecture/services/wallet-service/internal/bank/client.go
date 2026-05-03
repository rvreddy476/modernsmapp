// Package bank abstracts the partner-bank PPI integration.
//
// BC-of-PPI MODEL: AtPost is a Business Correspondent of a partner bank that
// owns the PPI license. Funds live in PPI sub-accounts at the partner bank,
// indexed by an opaque "ref" id we receive at OpenSubAccount time. Every
// movement of money MUST go through this Client; the wallet.balances mirror
// in our DB is just a snapshot.
//
// Two implementations live in this package:
//   - MockClient — deterministic, used in tests and local-dev (BANK_PARTNER=mock).
//   - HTTPClient (icici.go) — production stub matching ICICI's developer-portal
//     API shape. To be wired in once the BC agreement is signed.
//
// Selection is by env BANK_PARTNER=mock|icici. Default is mock to prevent
// accidental real-bank calls during development.
package bank

import (
	"context"

	"github.com/google/uuid"
)

// BankClient is the contract the wallet service depends on. Both MockClient
// and HTTPClient implement it.
type BankClient interface {
	// OpenSubAccount creates a PPI sub-account at the partner bank for the
	// given user. Returns the partner-side reference (bank_account_ref)
	// stored on wallet.balances. Idempotent on the bank side; safe to retry.
	OpenSubAccount(ctx context.Context, userID uuid.UUID) (string, error)

	// GetBalance returns the up-to-the-second balance held by the partner
	// bank for ref. Used by service.GetBalance's lazy-refresh path.
	GetBalance(ctx context.Context, ref string) (paise int64, err error)

	// Transfer moves amountPaise from fromRef to toRef. Both must be PPI
	// sub-accounts at the partner bank. txnRef is OUR transaction id, used
	// by the bank for idempotency. Returns nil on success.
	Transfer(ctx context.Context, fromRef, toRef string, amountPaise int64, txnRef string) error

	// VerifyUPIInbound checks whether an inbound UPI top-up identified by
	// upiTxnRef has actually landed in the bank's PPI account. Returns
	// verified=true only when the bank confirms credit AND the amount
	// matches expectedAmountPaise.
	VerifyUPIInbound(ctx context.Context, upiTxnRef string, expectedAmountPaise int64) (verified bool, err error)

	// Refund issues a refund of amountPaise against an originalTxnRef. Used
	// by the internal refund endpoint when a merchant (Pulse Premium /
	// Commerce / Food / Bill-pay) requests a reversal.
	Refund(ctx context.Context, originalTxnRef string, amountPaise int64) error
}
