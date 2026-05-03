package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"

	"github.com/atpost/bill-pay-service/internal/setu"
	"github.com/atpost/bill-pay-service/internal/store"
	"github.com/google/uuid"
)

// PayRequest is the inbound shape for POST /v1/billpay/pay.
type PayRequest struct {
	AccountID      *uuid.UUID
	ProviderID     uuid.UUID
	Identifier     string
	AmountPaise    int64
	PaymentMethod  string // 'wallet'|'upi'|'card'
	IdempotencyKey string
	BillID         *uuid.UUID
	ExtraParams    map[string]string
}

// PayResult is what handlers return after a Pay call.
type PayResult struct {
	PaymentID      uuid.UUID `json:"payment_id"`
	Status         string    `json:"status"`
	AmountPaise    int64     `json:"amount_paise"`
	SetuPaymentRef string    `json:"setu_payment_ref,omitempty"`
	ReceiptNumber  string    `json:"receipt_number,omitempty"`
}

// Pay executes a bill payment. Saga steps (per spec §4):
//
//  1. Idempotency check — replay returns the original PayResult.
//  2. Validate provider + account.
//  3. If payment_method='wallet': call wallet-service /internal/debit. On
//     success, attach the wallet_txn_id to the (yet-to-be-created) payment.
//     On failure, NO payment row is inserted (saga step §3.1 of the spec).
//  4. Insert billpay.payments row with status='initiated'.
//  5. Call setu.SubmitPayment. On submit success → status='submitted' +
//     setu_payment_ref. On submit failure → status='failed' AND if the wallet
//     was debited, refund the wallet via /internal/refund.
//  6. Setu confirms via webhook later → succeeded | failed (with wallet
//     refund if needed). Webhook handler is in service/payments.go.
//  7. Emit billpay.payment.* events.
func (s *Service) Pay(ctx context.Context, userID uuid.UUID, req PayRequest) (*PayResult, error) {
	if userID == uuid.Nil {
		return nil, fmt.Errorf("invalid: user id required")
	}
	if req.IdempotencyKey == "" {
		return nil, fmt.Errorf("invalid: idempotency_key required")
	}
	if req.AmountPaise <= 0 {
		return nil, fmt.Errorf("invalid: amount must be positive")
	}
	if req.ProviderID == uuid.Nil {
		return nil, fmt.Errorf("invalid: provider_id required")
	}
	if req.Identifier == "" {
		return nil, fmt.Errorf("invalid: identifier required")
	}
	switch req.PaymentMethod {
	case "wallet", "upi", "card":
	default:
		return nil, fmt.Errorf("invalid: payment_method must be wallet|upi|card")
	}

	// Step 1: idempotency replay.
	if existing, err := s.store.FindIdempotency(ctx, req.IdempotencyKey, userID); err == nil {
		if existing.ResponseBody != nil {
			var cached PayResult
			if err := json.Unmarshal(existing.ResponseBody, &cached); err != nil {
				return nil, fmt.Errorf("decode cached idempotency body: %w", err)
			}
			return &cached, nil
		}
		if existing.PaymentID != nil {
			pmt, err := s.store.GetPaymentByID(ctx, *existing.PaymentID)
			if err != nil {
				return nil, err
			}
			return resultFromPayment(pmt), nil
		}
	} else if !errors.Is(err, store.ErrIdempotencyKeyNotFound) {
		return nil, err
	}

	// Step 2: validate provider + account (if supplied).
	prov, err := s.store.GetProvider(ctx, req.ProviderID)
	if err != nil {
		return nil, err
	}
	if !prov.IsActive {
		return nil, fmt.Errorf("invalid: provider is not active")
	}
	if req.AccountID != nil {
		acc, err := s.store.GetAccount(ctx, userID, *req.AccountID)
		if err != nil {
			return nil, err
		}
		if acc.ProviderID != prov.ID {
			return nil, fmt.Errorf("invalid: account does not belong to provider")
		}
	}

	// Step 3: wallet pre-debit (if applicable).
	var walletTxnID *uuid.UUID
	if req.PaymentMethod == "wallet" {
		// We use idempotencyKey + ":wallet" as the wallet-side dedup key so
		// retries through this service are idempotent on both sides.
		walletKey := req.IdempotencyKey + ":wallet"
		// Stage a placeholder payment id so wallet has a merchant_ref to log.
		stagedPaymentID := uuid.New()
		dr, derr := s.wallet.DebitForBillPay(ctx, userID, req.AmountPaise, stagedPaymentID, walletKey)
		if derr != nil {
			// Per spec: wallet debit fails → NO payment row inserted.
			slog.Warn("billpay: wallet debit failed",
				"user", userID, "amount", req.AmountPaise, "error", derr,
			)
			return nil, fmt.Errorf("wallet debit failed: %w", derr)
		}
		walletTxnID = &dr.TransactionID
	}

	// Step 4: insert payment row.
	pmt, err := s.store.InsertPayment(ctx, store.CreatePaymentInput{
		UserID:         userID,
		AccountID:      req.AccountID,
		ProviderID:     prov.ID,
		AmountPaise:    req.AmountPaise,
		FeePaise:       0,
		PaymentMethod:  req.PaymentMethod,
		BillID:         req.BillID,
		IdempotencyKey: req.IdempotencyKey,
	})
	if err != nil {
		// Compensation: if we already debited the wallet but couldn't even
		// insert the local row, refund the wallet immediately.
		if walletTxnID != nil {
			if rerr := s.wallet.RefundForBillPay(ctx, *walletTxnID, req.AmountPaise, "billpay_insert_failed"); rerr != nil {
				slog.Error("billpay: wallet refund after insert failure ALSO failed",
					"wallet_txn", walletTxnID, "error", rerr)
			}
		}
		return nil, fmt.Errorf("insert payment: %w", err)
	}
	if walletTxnID != nil {
		if err := s.store.AttachWalletTxn(ctx, pmt.ID, *walletTxnID); err != nil {
			slog.Warn("billpay: attach wallet txn failed", "payment", pmt.ID, "error", err)
		}
	}
	if err := s.producer.PublishPaymentInitiated(ctx, userID, pmt.ID, prov.ID, req.AmountPaise, req.PaymentMethod); err != nil {
		slog.Warn("billpay: publish initiated failed", "payment", pmt.ID, "error", err)
	}

	// Step 5: hand off to Setu.
	setuReq := setu.PaymentRequest{
		SetuBillerID:    prov.SetuBillerID,
		Identifier:      req.Identifier,
		ExtraParams:     req.ExtraParams,
		AmountPaise:     req.AmountPaise,
		IdempotencyKey:  req.IdempotencyKey,
		PaymentChannel:  req.PaymentMethod,
		AtPostPaymentID: pmt.ID.String(),
	}
	if req.BillID != nil {
		bill, err := s.store.GetBill(ctx, *req.BillID)
		if err == nil && bill.SetuBillRef != nil {
			setuReq.SetuBillRef = *bill.SetuBillRef
		}
	}
	resp, serr := s.setu.SubmitPayment(ctx, setuReq)
	if serr != nil {
		// Setu transport / submit failure. Mark failed + refund wallet.
		s.failPaymentAndRefund(ctx, pmt, walletTxnID, "setu_submit_error: "+serr.Error())
		return nil, fmt.Errorf("setu submit: %w", serr)
	}
	if resp.Status == "failed" {
		s.failPaymentAndRefund(ctx, pmt, walletTxnID, resp.FailureReason)
		return resultFromPayment(&store.Payment{
			ID: pmt.ID, AmountPaise: pmt.AmountPaise, Status: "failed",
		}), nil
	}

	// Step 6: mark submitted. Final state arrives via webhook.
	if err := s.store.MarkPaymentSubmitted(ctx, pmt.ID, resp.SetuPaymentRef); err != nil {
		slog.Warn("billpay: mark submitted failed", "payment", pmt.ID, "error", err)
	}

	// Persist idempotency body for client replays.
	res := &PayResult{
		PaymentID:      pmt.ID,
		Status:         "submitted",
		AmountPaise:    pmt.AmountPaise,
		SetuPaymentRef: resp.SetuPaymentRef,
		ReceiptNumber:  resp.ReceiptNumber,
	}
	if body, err := json.Marshal(res); err == nil {
		_ = s.store.RecordIdempotency(ctx, req.IdempotencyKey, userID, &pmt.ID, body)
	}
	return res, nil
}

// failPaymentAndRefund is the saga compensation: mark the payment failed and
// refund the wallet (if it was debited). Best-effort logging on each step —
// we never silently drop a refund.
func (s *Service) failPaymentAndRefund(ctx context.Context, pmt *store.Payment, walletTxnID *uuid.UUID, reason string) {
	if err := s.store.MarkPaymentFailed(ctx, pmt.ID, reason); err != nil {
		slog.Error("billpay: mark payment failed failed", "payment", pmt.ID, "error", err)
	}
	if walletTxnID != nil {
		if err := s.wallet.RefundForBillPay(ctx, *walletTxnID, pmt.AmountPaise, "billpay_setu_failed"); err != nil {
			slog.Error("billpay: wallet refund after setu failure ALSO failed",
				"payment", pmt.ID, "wallet_txn", walletTxnID, "error", err)
		} else {
			if err := s.producer.PublishPaymentRefunded(ctx, pmt.UserID, pmt.ID, pmt.AmountPaise, "setu_failed"); err != nil {
				slog.Warn("billpay: publish refunded failed", "payment", pmt.ID, "error", err)
			}
		}
	}
	if err := s.producer.PublishPaymentFailed(ctx, pmt.UserID, pmt.ID, pmt.ProviderID, pmt.AmountPaise, reason); err != nil {
		slog.Warn("billpay: publish failed failed", "payment", pmt.ID, "error", err)
	}
}

// resultFromPayment maps a payment row to the wire shape used by Pay's
// idempotency-replay path.
func resultFromPayment(p *store.Payment) *PayResult {
	out := &PayResult{
		PaymentID:   p.ID,
		Status:      p.Status,
		AmountPaise: p.AmountPaise,
	}
	if p.SetuPaymentRef != nil {
		out.SetuPaymentRef = *p.SetuPaymentRef
	}
	if p.ReceiptNumber != nil {
		out.ReceiptNumber = *p.ReceiptNumber
	}
	return out
}
