package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"time"

	"github.com/atpost/wallet-service/internal/store"
	"github.com/google/uuid"
)

// TopUpResult is what StartTopUp returns to the handler. The mobile client
// reads UPIIntentURL and launches the user's UPI app; the server has already
// recorded a pending wallet.transactions row.
type TopUpResult struct {
	TransactionID uuid.UUID `json:"transaction_id"`
	UPIIntentURL  string    `json:"upi_intent_url"`
	ExpiresAt     time.Time `json:"expires_at"`
	Status        string    `json:"status"`
	AmountPaise   int64     `json:"amount_paise"`
}

const topUpOperation = "top_up"

// StartTopUp begins a top-up by creating a pending transaction row and
// returning a UPI Intent URL the client can open. Idempotent on
// idempotencyKey — a replay returns the original TopUpResult.
func (s *Service) StartTopUp(ctx context.Context, userID uuid.UUID, amountPaise int64, idempotencyKey string) (*TopUpResult, error) {
	if amountPaise <= 0 {
		return nil, fmt.Errorf("invalid: amount must be positive")
	}
	if idempotencyKey == "" {
		return nil, fmt.Errorf("invalid: idempotency_key required")
	}

	// Idempotency check FIRST — replay returns the cached response.
	if existing, err := s.store.FindIdempotency(ctx, idempotencyKey, userID, topUpOperation); err == nil {
		var cached TopUpResult
		if existing.ResponseBody != nil {
			if err := json.Unmarshal(existing.ResponseBody, &cached); err != nil {
				return nil, fmt.Errorf("decode cached idempotency body: %w", err)
			}
			return &cached, nil
		}
		if existing.TransactionID != nil {
			return s.topUpResultFromTransaction(ctx, *existing.TransactionID)
		}
	} else if !errors.Is(err, store.ErrIdempotencyKeyNotFound) {
		return nil, err
	}

	// KYC limit check. (Top-up is *credit* but we still cap by monthly limit
	// per RBI minimum-KYC rules — minimal KYC users cap at 10k INR balance.)
	if err := s.AssertWithinMonthlyLimit(ctx, userID, amountPaise); err != nil {
		return nil, err
	}

	// Ensure balance row + bank account ref exist.
	if _, err := s.GetBalance(ctx, userID); err != nil {
		return nil, err
	}

	// Create the pending wallet.transactions row and bump pending_in_paise.
	idem := idempotencyKey
	tx, err := s.store.InsertTransaction(ctx, nil, store.CreateTransactionInput{
		UserID:         userID,
		Type:           "top_up",
		Direction:      "credit",
		AmountPaise:    amountPaise,
		Status:         "pending",
		IdempotencyKey: &idem,
		Metadata:       map[string]any{"source": "upi_intent"},
	})
	if err != nil {
		return nil, fmt.Errorf("insert top-up tx: %w", err)
	}
	if err := s.store.AdjustPendingIn(ctx, userID, amountPaise); err != nil {
		return nil, fmt.Errorf("bump pending_in: %w", err)
	}

	expires := tx.CreatedAt.Add(time.Duration(s.cfg.TopUpExpirySeconds) * time.Second)
	res := &TopUpResult{
		TransactionID: tx.ID,
		UPIIntentURL:  buildUPIIntentURL(s.cfg.PartnerBankVPA, s.cfg.AppDisplayName, amountPaise, tx.ID),
		ExpiresAt:     expires,
		Status:        "pending",
		AmountPaise:   amountPaise,
	}

	body, err := json.Marshal(res)
	if err != nil {
		return nil, fmt.Errorf("marshal idempotency body: %w", err)
	}
	if err := s.store.RecordIdempotency(ctx, idempotencyKey, userID, topUpOperation, &tx.ID, body); err != nil {
		slog.Warn("wallet: idempotency record failed", "key", idempotencyKey, "error", err)
		// Non-fatal: the transaction row is the canonical record. A retry
		// without the cached row will hit the duplicate-key path and return
		// the same transaction by lookup.
	}

	if err := s.producer.PublishTopUpStarted(ctx, userID, tx.ID, amountPaise); err != nil {
		slog.Warn("wallet: publish top-up started failed", "tx", tx.ID, "error", err)
	}
	return res, nil
}

// ConfirmTopUp is called by the client after the UPI app reports success. We
// verify with the partner bank that the inbound credit actually landed; if
// it did, mirror the credit + emit an event. Idempotent on transactionID:
// a second call with the same id returns success without double-crediting.
func (s *Service) ConfirmTopUp(ctx context.Context, userID, txID uuid.UUID, upiTxnRef string) (*store.Transaction, error) {
	if upiTxnRef == "" {
		return nil, fmt.Errorf("invalid: upi_txn_ref required")
	}
	tx, err := s.store.GetTransaction(ctx, userID, txID)
	if err != nil {
		return nil, err
	}
	if tx.Type != "top_up" {
		return nil, fmt.Errorf("invalid: transaction is not a top-up")
	}
	// Idempotency: already-settled rows return as-is.
	if tx.Status == "succeeded" {
		return tx, nil
	}
	if tx.Status == "failed" {
		return nil, fmt.Errorf("invalid: top-up already failed")
	}

	if err := s.store.SetUPIRef(ctx, txID, upiTxnRef); err != nil {
		return nil, fmt.Errorf("record upi ref: %w", err)
	}

	verified, err := s.bank.VerifyUPIInbound(ctx, upiTxnRef, tx.AmountPaise)
	if err != nil {
		return nil, fmt.Errorf("bank verify upi: %w", err)
	}
	if !verified {
		// The UPI may still arrive shortly. Stay pending; the expirer will
		// flip to failed if too much time passes.
		slog.Info("wallet: top-up not yet verified, keeping pending", "tx", txID)
		return tx, nil
	}

	bankRef := upiTxnRef
	if err := s.store.MarkSettled(ctx, nil, txID, "succeeded", &bankRef, nil); err != nil {
		return nil, fmt.Errorf("mark settled: %w", err)
	}
	if err := s.store.CreditAvailable(ctx, userID, tx.AmountPaise); err != nil {
		return nil, fmt.Errorf("credit available: %w", err)
	}
	if err := s.producer.PublishTopUpSucceeded(ctx, userID, txID, tx.AmountPaise, bankRef, upiTxnRef); err != nil {
		slog.Warn("wallet: publish top-up succeeded failed", "tx", txID, "error", err)
	}
	out, err := s.store.GetTransaction(ctx, userID, txID)
	if err != nil {
		return nil, err
	}
	return out, nil
}

// ExpireStaleTopUps marks pending top-ups older than the configured cutoff
// as failed and refunds pending_in_paise. Run from cmd/expirer on a 5-minute
// cadence. Returns the number of rows expired.
func (s *Service) ExpireStaleTopUps(ctx context.Context) (int, error) {
	cutoff := time.Now().Add(-time.Duration(s.cfg.TopUpExpirySeconds) * time.Second)
	stale, err := s.store.ListPendingTopUpsOlderThan(ctx, cutoff, 200)
	if err != nil {
		return 0, fmt.Errorf("list stale top-ups: %w", err)
	}
	expired := 0
	for _, t := range stale {
		reason := "expired_no_upi_credit"
		if err := s.store.MarkSettled(ctx, nil, t.ID, "failed", nil, &reason); err != nil {
			slog.Warn("wallet: mark expired top-up failed", "tx", t.ID, "error", err)
			continue
		}
		if err := s.store.AdjustPendingIn(ctx, t.UserID, -t.AmountPaise); err != nil {
			slog.Warn("wallet: refund pending_in failed", "tx", t.ID, "error", err)
			continue
		}
		if err := s.producer.PublishTopUpFailed(ctx, t.UserID, t.ID, t.AmountPaise, reason); err != nil {
			slog.Warn("wallet: publish top-up failed", "tx", t.ID, "error", err)
		}
		expired++
	}
	return expired, nil
}

// topUpResultFromTransaction reconstructs a TopUpResult from a stored
// transaction id. Used as a fallback if the idempotency body cache is empty.
func (s *Service) topUpResultFromTransaction(ctx context.Context, txID uuid.UUID) (*TopUpResult, error) {
	tx, err := s.store.GetTransactionByID(ctx, txID)
	if err != nil {
		return nil, err
	}
	expires := tx.CreatedAt.Add(time.Duration(s.cfg.TopUpExpirySeconds) * time.Second)
	return &TopUpResult{
		TransactionID: tx.ID,
		UPIIntentURL:  buildUPIIntentURL(s.cfg.PartnerBankVPA, s.cfg.AppDisplayName, tx.AmountPaise, tx.ID),
		ExpiresAt:     expires,
		Status:        tx.Status,
		AmountPaise:   tx.AmountPaise,
	}, nil
}

// buildUPIIntentURL produces an upi:// deep-link the mobile client opens to
// hand off to the user's UPI app. Format per NPCI UPI deep-link spec.
//
//	upi://pay?pa=<vpa>&pn=<name>&am=<rupees>&tn=<txnNote>&cu=INR
func buildUPIIntentURL(vpa, displayName string, amountPaise int64, txID uuid.UUID) string {
	rupees := fmt.Sprintf("%.2f", float64(amountPaise)/100.0)
	q := url.Values{}
	q.Set("pa", vpa)
	q.Set("pn", displayName)
	q.Set("am", rupees)
	q.Set("tn", txID.String())
	q.Set("cu", "INR")
	return "upi://pay?" + q.Encode()
}
