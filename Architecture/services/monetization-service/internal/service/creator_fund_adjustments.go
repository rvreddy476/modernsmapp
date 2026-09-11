package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/atpost/monetization-service/internal/store/postgres"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// pgxTx is the transaction handle the store's *Tx methods take.
type pgxTx = pgx.Tx

// ---------------------------------------------------------------------------
// Correction primitives (plan Phase 2A)
// ---------------------------------------------------------------------------
//
// Two operations, and only two, change what a creator has been paid after
// the fact — and neither of them edits a settled row's money.
//
//   PostAdjustment      a signed wallet movement identified by its cause.
//   ReverseFundEarning  marks one accrual row reversed and, if the row had
//                       been credited, posts the negative adjustment that
//                       gives the net back — in one transaction.
//
// A corrected re-accrual, if ever one is needed, is a NEW positive
// adjustment with cause creator_fund_earning_correction. The reversed row
// keeps its UNIQUE (creator, day, type, region) slot on purpose, so the
// nightly accrual can never silently write a second row for that day.

// AdjustmentCause identifies an adjustment. Kind says what class of
// event it is; Ref is the id of the thing that caused it. Together they
// are the idempotency key, so a retried correction lands once.
type AdjustmentCause struct {
	Kind string `json:"kind"`
	Ref  string `json:"ref"`
}

// Key is the idempotency key the cause is stored under.
func (c AdjustmentCause) Key() string { return "adj:" + c.Kind + ":" + c.Ref }

// FeeKey is the idempotency key of the platform-fee leg mirrored back out
// when the cause row carried a platform_fee_paise.
func (c AdjustmentCause) FeeKey() string { return "adj_fee:" + c.Kind + ":" + c.Ref }

// Adjustment cause kinds written by this service.
const (
	CauseFundEarningReversal   = "creator_fund_earning_reversal"
	CauseFundEarningCorrection = "creator_fund_earning_correction"
	adjustmentReferenceType    = "adjustment"
)

// ErrEarningNotFound is returned when no accrual row has the given id.
var ErrEarningNotFound = errors.New("EARNING_NOT_FOUND")

// ReversalResult is what ReverseFundEarning did.
type ReversalResult struct {
	Earning         *postgres.CreatorFundEarning `json:"earning"`
	AlreadyReversed bool                         `json:"already_reversed"`
	// MoneyMoved is true only when this call posted the adjustment. A
	// never-credited row reverses without moving money: the period claim
	// filters on status = 'settled', so it can no longer be paid.
	MoneyMoved   bool                  `json:"money_moved"`
	Adjustment   *postgres.Transaction `json:"adjustment,omitempty"`
	BalanceAfter int64                 `json:"balance_after_paise"`
	// NetReversedPaise is what came back out of the wallet;
	// FeeReversedPaise is the platform's share of the same income, moved
	// back out of platform_revenue_fees in the same transaction (keyed
	// adj_fee:<cause>). Both zero when the row had never been credited.
	NetReversedPaise int64 `json:"net_reversed_paise"`
	FeeReversedPaise int64 `json:"fee_reversed_paise"`
	// LedgerFrozen is set when the reversal drove the balance below zero:
	// the creator has already withdrawn money that is now owed back, and
	// a human has to decide what happens next.
	LedgerFrozen bool `json:"ledger_frozen"`
}

// PostAdjustment posts one signed adjustment on a creator's wallet in
// its own transaction. Posting the same cause again returns the original
// transaction and moves nothing.
func (s *Service) PostAdjustment(ctx context.Context, creatorID uuid.UUID, amountPaise int64, reason string, cause AdjustmentCause) (*postgres.Transaction, error) {
	var out *postgres.AdjustmentResult
	err := s.store.WithTx(ctx, func(tx pgx.Tx) error {
		r, err := s.PostAdjustmentTx(ctx, tx, creatorID, amountPaise, reason, cause)
		out = r
		return err
	})
	if err != nil {
		return nil, err
	}
	t := out.Transaction
	return &t, nil
}

// PostAdjustmentTx is PostAdjustment inside the caller's transaction. The
// ledger accounts are resolved up front (idempotent inserts, outside tx)
// so the double-entry leg commits with the wallet movement.
//
// Call it BEFORE any other statement in tx has locked an accounts row:
// EnsureAccount runs on a pool connection, and an INSERT ... ON CONFLICT
// against a row the caller's transaction already holds waits on that
// transaction forever.
func (s *Service) PostAdjustmentTx(ctx context.Context, tx pgx.Tx, creatorID uuid.UUID, amountPaise int64, reason string, cause AdjustmentCause) (*postgres.AdjustmentResult, error) {
	if amountPaise == 0 {
		return nil, fmt.Errorf("ADJUSTMENT_ZERO: an adjustment must move money")
	}
	if strings.TrimSpace(reason) == "" {
		return nil, fmt.Errorf("ADJUSTMENT_REASON_REQUIRED")
	}
	if strings.TrimSpace(cause.Kind) == "" || strings.TrimSpace(cause.Ref) == "" {
		return nil, fmt.Errorf("ADJUSTMENT_CAUSE_REQUIRED: kind and ref identify the adjustment")
	}
	platformAcct, err := s.store.EnsureAccount(ctx, platformOwnerID, platformRevenueAccountType)
	if err != nil {
		return nil, fmt.Errorf("ensure platform revenue account: %w", err)
	}
	creatorAcct, err := s.store.EnsureAccount(ctx, creatorID, creatorWalletAccountType)
	if err != nil {
		return nil, fmt.Errorf("ensure creator wallet account: %w", err)
	}
	var ledgerRef *uuid.UUID
	if id, err := uuid.Parse(cause.Ref); err == nil {
		ledgerRef = &id
	}
	return s.store.PostAdjustmentTx(ctx, tx, postgres.AdjustmentInput{
		CreatorID:                creatorID,
		AmountPaise:              amountPaise,
		Currency:                 defaultEarningsCurrency,
		IdempotencyKey:           cause.Key(),
		ReferenceType:            cause.Kind,
		ReferenceID:              cause.Ref,
		Description:              reason,
		CreatorWalletAccountID:   creatorAcct.ID,
		PlatformRevenueAccountID: platformAcct.ID,
		LedgerReferenceType:      adjustmentReferenceType,
		LedgerReferenceID:        ledgerRef,
	})
}

// ReverseFundEarning marks one accrual row reversed. If the row had been
// credited to the wallet, the net is taken back through an adjustment
// keyed on the row, in the same transaction. Reversing a reversed row
// returns it and moves nothing.
func (s *Service) ReverseFundEarning(ctx context.Context, earningID uuid.UUID, reason string) (*ReversalResult, error) {
	reason = strings.TrimSpace(reason)
	if reason == "" {
		return nil, fmt.Errorf("REVERSAL_REASON_REQUIRED")
	}
	// Resolve the platform accounts BEFORE the transaction opens. Inside
	// it, the adjustment's ledger leg takes a row lock on platform_revenue;
	// an EnsureAccount (INSERT ... ON CONFLICT DO NOTHING on a pool
	// connection) against that same row would then wait on our own
	// uncommitted lock, and the transaction on it: a deadlock Postgres
	// cannot see. Idempotent inserts, so doing them early costs nothing.
	feeAcct, err := s.store.EnsureAccount(ctx, platformOwnerID, platformFeeAccountType)
	if err != nil {
		return nil, fmt.Errorf("ensure platform fee account: %w", err)
	}
	revAcct, err := s.store.EnsureAccount(ctx, platformOwnerID, platformRevenueAccountType)
	if err != nil {
		return nil, fmt.Errorf("ensure platform revenue account: %w", err)
	}
	var out ReversalResult
	err = s.store.WithTx(ctx, func(tx pgx.Tx) error {
		e, err := s.store.GetCreatorFundEarningForUpdateTx(ctx, tx, earningID)
		if err != nil {
			return fmt.Errorf("lock earning: %w", err)
		}
		if e == nil {
			return ErrEarningNotFound
		}
		out.Earning = e
		if e.Status == "reversed" {
			out.AlreadyReversed = true
			out.BalanceAfter, err = walletBalanceTx(ctx, tx, e.CreatorID)
			return err
		}

		now := time.Now()
		var adjustmentID *uuid.UUID
		if e.Credited && e.NetPaise > 0 {
			cause := AdjustmentCause{Kind: CauseFundEarningReversal, Ref: e.ID.String()}
			description := fmt.Sprintf("Reversal of creator fund earning %s (%s %s, %d views): %s",
				e.ID, e.DayBucket.Format("2006-01-02"), e.ContentType, e.ViewCount, reason)
			r, err := s.PostAdjustmentTx(ctx, tx, e.CreatorID, -e.NetPaise, description, cause)
			if err != nil {
				return fmt.Errorf("post reversal adjustment: %w", err)
			}
			id := r.Transaction.ID
			adjustmentID = &id
			t := r.Transaction
			out.Adjustment = &t
			out.MoneyMoved = r.Applied
			out.BalanceAfter = r.BalanceAfter
			out.NetReversedPaise = e.NetPaise
			// The platform's share of the same income goes back out of the
			// fee account too, mirrored and keyed on the same cause. Gross
			// was net + fee; both halves of it are now unwound.
			if e.PlatformFeePaise > 0 {
				earningID := e.ID
				if _, err := s.store.PostFeeReversalLegTx(ctx, tx, postgres.FeeLegInput{
					AmountPaise:         e.PlatformFeePaise,
					Currency:            defaultEarningsCurrency,
					IdempotencyKey:      cause.FeeKey(),
					PlatformFeeAccount:  feeAcct.ID,
					PlatformRevenueAcct: revAcct.ID,
					ReferenceType:       adjustmentReferenceType,
					ReferenceID:         &earningID,
					Description:         "Platform fee mirrored back: " + description,
				}); err != nil {
					return fmt.Errorf("post fee reversal leg: %w", err)
				}
				out.FeeReversedPaise = e.PlatformFeePaise
			}
			if r.BalanceAfter < 0 {
				// The money has already left. Nothing more can be
				// withdrawn until a human looks.
				if err := s.store.FreezeLedgerTx(ctx, tx, e.CreatorID); err != nil {
					return fmt.Errorf("freeze ledger: %w", err)
				}
				out.LedgerFrozen = true
			}
		} else {
			out.BalanceAfter, err = walletBalanceTx(ctx, tx, e.CreatorID)
			if err != nil {
				return err
			}
		}

		ok, err := s.store.MarkCreatorFundEarningReversedTx(ctx, tx, e.ID, reason, adjustmentID, now)
		if err != nil {
			return fmt.Errorf("mark reversed: %w", err)
		}
		if !ok {
			return fmt.Errorf("earning %s is not settled (status %q); nothing reversed", e.ID, e.Status)
		}
		e.Status = "reversed"
		e.ReversedAt = &now
		e.ReversalReason = reason
		e.ReversalTransactionID = adjustmentID
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &out, nil
}

func walletBalanceTx(ctx context.Context, tx pgx.Tx, creatorID uuid.UUID) (int64, error) {
	var balance int64
	err := tx.QueryRow(ctx,
		`SELECT COALESCE((SELECT balance FROM creator_ledger WHERE user_id = $1), 0)::BIGINT`, creatorID).Scan(&balance)
	return balance, err
}

// GetCreatorFundEarning returns one accrual row, or ErrEarningNotFound.
func (s *Service) GetCreatorFundEarning(ctx context.Context, earningID uuid.UUID) (*postgres.CreatorFundEarning, error) {
	e, err := s.store.GetCreatorFundEarning(ctx, earningID)
	if err != nil {
		return nil, err
	}
	if e == nil {
		return nil, ErrEarningNotFound
	}
	return e, nil
}
