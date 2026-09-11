package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/atpost/monetization-service/internal/store/postgres"
	"github.com/google/uuid"
)

// ---------------------------------------------------------------------------
// The withdrawal path (plan Phase 3A, M-04)
// ---------------------------------------------------------------------------
//
// Until this phase every control on a payout — the minimum, KYC, the
// new-account hold, fraud review, TDS — was implemented and unreachable:
// RequestPayout locked the ledger, moved balance to pending_payout, wrote
// one transactions row and returned. Nothing ever inserted into
// payout_requests.
//
// RequestPayout is now a pipeline, in this order and no other, with
// everything from the payout-method check onward inside ONE transaction:
//
//   1. payouts enabled (Phase 3C)         else PAYOUTS_NOT_ENABLED
//   2. minimum payout (Rs 100)            else MINIMUM_PAYOUT_NOT_MET
//   3. KYC: creator_tax_profiles.verified_at
//                                         else KYC_NOT_VERIFIED
//   4. payout method exists, is verified, belongs to the caller
//   5. ledger row locked; not frozen; balance >= amount
//   6. new-account hold: ledger under 7 days old
//                                         -> payout_requests(held) +
//                                            fraud_reviews(new_creator_hold)
//   7. velocity: more than 3 requests in 24 h
//                                         -> held + fraud_reviews(velocity)
//   8. TDS on cumulative gross for the financial year -> tds_ledger
//   9. balance -= gross, pending_payout += gross; transactions(payout,
//      pending) keyed payout:<id>; ledger leg user_wallet -> payout_hold
//      keyed payout_hold:<id>; payout_requests(requested, amount=gross,
//      tds_paise, net_paise)
//
// A hold (6, 7) is recorded before any money moves: the request row and
// the review exist, the balance does not change, and the caller gets a
// 202 rather than an error. What happens to a held request next is
// Phase 4's state machine.

const (
	// minimumPayoutPaise is the minimum payout amount (Rs 100 = 10000 paise).
	minimumPayoutPaise int64 = 10_000
	// newCreatorHoldAge is how old a creator's ledger must be before a
	// withdrawal is not held for review. Re-based on creator_ledger.created_at
	// rather than the Redis account-age keys, which nothing wrote.
	newCreatorHoldAge = 7 * 24 * time.Hour
	// velocityWindow and velocityMaxRequests: more than this many requests
	// in the window holds the next one.
	velocityWindow      = 24 * time.Hour
	velocityMaxRequests = 3
	// holdRiskScore is recorded on the fraud review a hold creates. The
	// score is a queue-ordering hint for the reviewer, not a decision.
	holdRiskScore = 50

	payoutRequestReferenceType = "payout_request"
	payoutHoldAccountType      = "payout_hold"

	// HoldReasonNewCreator and HoldReasonVelocity are the review_type
	// values a hold writes (both are in fraud_reviews' CHECK).
	HoldReasonNewCreator = "new_creator_hold"
	HoldReasonVelocity   = "velocity"

	// PayoutStatusRequested (the state a withdrawal starts in once it has
	// passed every gate) and PayoutStatusHeld (recorded for review before
	// any money moved) are defined with the rest of the state machine in
	// payout_state.go.
)

// The refusals, one sentinel each, so the HTTP layer maps them by
// identity rather than by string.
var (
	ErrPayoutsNotEnabled       = errors.New("PAYOUTS_NOT_ENABLED")
	ErrMinimumPayoutNotMet     = errors.New("MINIMUM_PAYOUT_NOT_MET")
	ErrKYCNotVerified          = errors.New("KYC_NOT_VERIFIED")
	ErrPayoutMethodNotFound    = errors.New("PAYOUT_METHOD_NOT_FOUND")
	ErrPayoutMethodNotVerified = errors.New("PAYOUT_METHOD_NOT_VERIFIED")
	ErrWalletNotFound          = errors.New("WALLET_NOT_FOUND")
	ErrWalletFrozen            = errors.New("WALLET_FROZEN")
	ErrInsufficientBalance     = errors.New("INSUFFICIENT_BALANCE")
)

// CheckKYCGate verifies that the user has a verified creator_tax_profiles entry.
func (s *Service) CheckKYCGate(ctx context.Context, userID uuid.UUID) error {
	verified, err := s.store.CheckKYCVerified(ctx, userID)
	if err != nil {
		return fmt.Errorf("check KYC: %w", err)
	}
	if !verified {
		return ErrKYCNotVerified
	}
	return nil
}

// EnforceMinimumPayout returns ErrMinimumPayoutNotMet if the amount is
// below Rs 100.
func (s *Service) EnforceMinimumPayout(amountPaise int64) error {
	if amountPaise < minimumPayoutPaise {
		return ErrMinimumPayoutNotMet
	}
	return nil
}

// PayoutOutcome is what a withdrawal request produced. Exactly one of
// two shapes: a REQUESTED payout (Transaction set, money moved into
// pending_payout, TDS priced) or a HELD one (Held true, HoldReason and
// FraudReview set, no money moved, NetPaise zero).
type PayoutOutcome struct {
	Request     *postgres.PayoutRequestRow `json:"request"`
	Transaction *postgres.Transaction      `json:"transaction,omitempty"`
	Held        bool                       `json:"held"`
	HoldReason  string                     `json:"hold_reason,omitempty"`
	FraudReview *postgres.FraudReview      `json:"fraud_review,omitempty"`
	GrossPaise  int64                      `json:"gross_paise"`
	TDSPaise    int64                      `json:"tds_paise"`
	NetPaise    int64                      `json:"net_paise"`
	// Replayed is true when the client's idempotency key had already
	// been used: Request is the original row and nothing happened.
	Replayed bool `json:"replayed"`
}

// RequestPayout runs the withdrawal pipeline with no client idempotency
// key. See the file comment for the order of gates.
func (s *Service) RequestPayout(ctx context.Context, userID uuid.UUID, amountPaise int64, payoutMethodID uuid.UUID) (*PayoutOutcome, error) {
	return s.RequestPayoutWithKey(ctx, userID, amountPaise, payoutMethodID, "")
}

// RequestPayoutWithKey is RequestPayout with an optional client
// idempotency key (the X-Idempotency-Key header). A repeated key returns
// the request it first produced and moves nothing.
func (s *Service) RequestPayoutWithKey(ctx context.Context, userID uuid.UUID, amountPaise int64, payoutMethodID uuid.UUID, clientKey string) (*PayoutOutcome, error) {
	// 1. The beta boundary, at the service layer so it holds for every
	//    caller and not just the HTTP route.
	if !s.payoutsEnabled {
		return nil, ErrPayoutsNotEnabled
	}
	if userID == uuid.Nil || payoutMethodID == uuid.Nil {
		return nil, fmt.Errorf("INVALID_ID: user and payout method are required")
	}
	if amountPaise <= 0 || amountPaise > maxAmountPaise {
		return nil, fmt.Errorf("amount out of valid range: %w", ErrInvalidAmount)
	}
	// 2.
	if err := s.EnforceMinimumPayout(amountPaise); err != nil {
		return nil, err
	}
	// 3.
	if err := s.CheckKYCGate(ctx, userID); err != nil {
		return nil, err
	}
	// 4-9, together or not at all.
	var out *PayoutOutcome
	err := s.store.WithTx(ctx, func(tx pgxTx) error {
		o, err := s.requestPayoutTx(ctx, tx, userID, amountPaise, payoutMethodID, clientKey)
		out = o
		return err
	})
	if err != nil {
		return nil, err
	}
	if out.Held {
		slog.Warn("payout held for review",
			"user_id", userID, "request_id", out.Request.ID, "amount_paise", amountPaise, "reason", out.HoldReason)
	} else if !out.Replayed {
		slog.Info("payout requested",
			"user_id", userID, "request_id", out.Request.ID,
			"gross_paise", out.GrossPaise, "tds_paise", out.TDSPaise, "net_paise", out.NetPaise)
	}
	return out, nil
}

// requestPayoutTx is gates 4-9 inside the caller's transaction.
func (s *Service) requestPayoutTx(ctx context.Context, tx pgxTx, userID uuid.UUID, grossPaise int64, payoutMethodID uuid.UUID, clientKey string) (*PayoutOutcome, error) {
	var keyPtr *string
	if clientKey != "" {
		key := "payout:" + userID.String() + ":" + clientKey
		existing, err := s.store.GetPayoutRequestByIdempotencyKeyTx(ctx, tx, key)
		if err != nil {
			return nil, fmt.Errorf("idempotency lookup: %w", err)
		}
		if existing != nil {
			return &PayoutOutcome{
				Request:    existing,
				Held:       existing.Status == PayoutStatusHeld,
				HoldReason: heldReasonFromNotes(existing),
				GrossPaise: existing.AmountPaise,
				TDSPaise:   existing.TDSPaise,
				NetPaise:   derefInt64(existing.NetPaise),
				Replayed:   true,
			}, nil
		}
		keyPtr = &key
	}

	// 4. The method exists, is verified, and is the caller's own.
	method, err := s.store.GetPayoutMethodTx(ctx, tx, userID, payoutMethodID)
	if err != nil {
		return nil, fmt.Errorf("payout method: %w", err)
	}
	if method == nil {
		return nil, ErrPayoutMethodNotFound
	}
	if !method.IsVerified {
		return nil, ErrPayoutMethodNotVerified
	}

	// 5. Lock the ledger row for the rest of the transaction.
	ledger, err := s.store.LockLedgerTx(ctx, tx, userID)
	if err != nil {
		return nil, err
	}
	if ledger == nil {
		return nil, ErrWalletNotFound
	}
	if ledger.IsFrozen {
		return nil, ErrWalletFrozen
	}
	if ledger.BalancePaise < grossPaise {
		return nil, ErrInsufficientBalance
	}

	now := time.Now()
	requestID := uuid.New()

	// 6. New-account hold, on the age of the ledger row itself.
	if now.Sub(ledger.CreatedAt) < newCreatorHoldAge {
		return s.holdPayoutTx(ctx, tx, requestID, userID, grossPaise, payoutMethodID, keyPtr, now,
			HoldReasonNewCreator,
			fmt.Sprintf("ledger is %s old; withdrawals are held for the first %s",
				now.Sub(ledger.CreatedAt).Round(time.Hour), newCreatorHoldAge))
	}

	// 7. Velocity: this request would be the (n+1)th in the window.
	recent, err := s.store.CountPayoutRequestsSinceTx(ctx, tx, userID, now.Add(-velocityWindow))
	if err != nil {
		return nil, fmt.Errorf("count recent payout requests: %w", err)
	}
	if recent >= velocityMaxRequests {
		return s.holdPayoutTx(ctx, tx, requestID, userID, grossPaise, payoutMethodID, keyPtr, now,
			HoldReasonVelocity,
			fmt.Sprintf("%d requests in the last %s; the limit is %d", recent, velocityWindow, velocityMaxRequests))
	}

	// 8. TDS, on cumulative gross for the year, keyed to this request.
	netPaise, tdsPaise, err := s.deductTDSTx(ctx, tx, userID, grossPaise, &requestID)
	if err != nil {
		return nil, err
	}

	// 9. The money move and the records that explain it.
	txn := &postgres.Transaction{
		ID:            uuid.New(),
		WalletID:      userID,
		Type:          "payout",
		AmountPaise:   grossPaise,
		Currency:      "INR",
		Status:        "pending",
		ReferenceType: payoutRequestReferenceType,
		ReferenceID:   requestID.String(),
		Description:   fmt.Sprintf("Payout requested: gross %d paise, TDS %d paise, net %d paise", grossPaise, tdsPaise, netPaise),
		CreatedAt:     now,
	}
	if err := s.store.InsertPayoutTransactionTx(ctx, tx, txn, "payout:"+requestID.String()); err != nil {
		return nil, err
	}
	if err := s.store.MoveBalanceToPendingPayoutTx(ctx, tx, userID, grossPaise); err != nil {
		if errors.Is(err, postgres.ErrInsufficientFunds) {
			return nil, ErrInsufficientBalance
		}
		return nil, err
	}
	walletAcct, err := s.store.EnsureAccountTx(ctx, tx, userID, creatorWalletAccountType)
	if err != nil {
		return nil, fmt.Errorf("ensure wallet account: %w", err)
	}
	holdAcct, err := s.store.EnsureAccountTx(ctx, tx, userID, payoutHoldAccountType)
	if err != nil {
		return nil, fmt.Errorf("ensure payout_hold account: %w", err)
	}
	refID := requestID
	if err := s.store.InsertLedgerEntryTx(ctx, tx, &postgres.LedgerEntry{
		DebitAccountID:  walletAcct.ID,
		CreditAccountID: holdAcct.ID,
		AmountPaise:     grossPaise,
		Currency:        "INR",
		ReferenceType:   payoutRequestReferenceType,
		ReferenceID:     &refID,
		IdempotencyKey:  "payout_hold:" + requestID.String(),
		Description:     "Payout requested: balance reserved pending transfer",
	}); err != nil {
		return nil, fmt.Errorf("ledger leg: %w", err)
	}

	row := &postgres.PayoutRequestRow{
		ID:             requestID,
		UserID:         userID,
		TransactionID:  &txn.ID,
		AmountPaise:    grossPaise,
		Currency:       "INR",
		Status:         PayoutStatusRequested,
		PayoutMethodID: &payoutMethodID,
		RequestedAt:    now,
		TDSPaise:       tdsPaise,
		NetPaise:       &netPaise,
		IdempotencyKey: keyPtr,
	}
	if err := s.store.InsertPayoutRequestTx(ctx, tx, row); err != nil {
		return nil, err
	}
	return &PayoutOutcome{
		Request:     row,
		Transaction: txn,
		GrossPaise:  grossPaise,
		TDSPaise:    tdsPaise,
		NetPaise:    netPaise,
	}, nil
}

// holdPayoutTx records a withdrawal for review: the request row in
// status 'held' and the fraud review that says why. No money moves; the
// balance is exactly as the lock found it.
func (s *Service) holdPayoutTx(ctx context.Context, tx pgxTx, requestID, userID uuid.UUID, grossPaise int64, payoutMethodID uuid.UUID, keyPtr *string, now time.Time, reason, why string) (*PayoutOutcome, error) {
	row := &postgres.PayoutRequestRow{
		ID:             requestID,
		UserID:         userID,
		AmountPaise:    grossPaise,
		Currency:       "INR",
		Status:         PayoutStatusHeld,
		PayoutMethodID: &payoutMethodID,
		RequestedAt:    now,
		Notes:          reason + ": " + why,
		IdempotencyKey: keyPtr,
	}
	if err := s.store.InsertPayoutRequestTx(ctx, tx, row); err != nil {
		return nil, err
	}
	notes := fmt.Sprintf("payout request %s for %d paise held: %s", requestID, grossPaise, why)
	review, err := s.store.CreateFraudReviewTx(ctx, tx, &postgres.FraudReview{
		CreatorID:  userID,
		ReviewType: reason,
		RiskScore:  holdRiskScore,
		Status:     "pending",
		Notes:      &notes,
	})
	if err != nil {
		return nil, fmt.Errorf("create fraud review: %w", err)
	}
	return &PayoutOutcome{
		Request:     row,
		Held:        true,
		HoldReason:  reason,
		FraudReview: review,
		GrossPaise:  grossPaise,
	}, nil
}

// heldReasonFromNotes recovers the hold reason a held row was written
// with (the notes start with it) for a replayed outcome.
func heldReasonFromNotes(r *postgres.PayoutRequestRow) string {
	if r.Status != PayoutStatusHeld {
		return ""
	}
	for _, reason := range []string{HoldReasonNewCreator, HoldReasonVelocity} {
		if len(r.Notes) >= len(reason) && r.Notes[:len(reason)] == reason {
			return reason
		}
	}
	return ""
}

func derefInt64(p *int64) int64 {
	if p == nil {
		return 0
	}
	return *p
}

// ---------------------------------------------------------------------------
// Provider webhook
// ---------------------------------------------------------------------------
//
// The verified, deduplicated, converging webhook is HandleProviderWebhook
// in payout_rail.go (plan Phase 4C). The unsigned status-string handler
// that used to live here was removed with it.

// StorePayoutWebhookEvent records a provider callback that arrived while
// payouts were disabled (Phase 3C) or the rail was not configured:
// nothing is acted on, the event is kept in the audit log.
func (s *Service) StorePayoutWebhookEvent(ctx context.Context, rawBody []byte, remoteAddr, reason string) error {
	return s.store.WriteAuditLog(ctx, &postgres.AuditLogEntry{
		TableName: "payout_requests",
		Operation: "webhook_stored_" + reason,
		NewData:   rawBody,
		IPAddress: remoteAddr,
	})
}
