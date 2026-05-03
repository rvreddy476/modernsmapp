package service

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/atpost/bill-pay-service/internal/store"
	"github.com/google/uuid"
)

// GetPayment returns one payment scoped to user.
func (s *Service) GetPayment(ctx context.Context, userID, paymentID uuid.UUID) (*store.Payment, error) {
	if userID == uuid.Nil || paymentID == uuid.Nil {
		return nil, fmt.Errorf("invalid: ids required")
	}
	return s.store.GetPayment(ctx, userID, paymentID)
}

// ListPayments returns paginated payments for a user. cursor is the
// created_at of the last row of the previous page (RFC3339Nano).
func (s *Service) ListPayments(ctx context.Context, userID uuid.UUID, status, cursor string, limit int) ([]store.Payment, error) {
	if userID == uuid.Nil {
		return nil, fmt.Errorf("invalid: user id required")
	}
	return s.store.ListPaymentsByUser(ctx, userID, status, cursor, limit)
}

// SetuWebhookEvent is the payload Setu posts to /v1/billpay/internal/setu-webhook.
type SetuWebhookEvent struct {
	SetuPaymentRef string `json:"setu_payment_ref"`
	Status         string `json:"status"` // 'succeeded'|'failed'
	ReceiptNumber  string `json:"receipt_number,omitempty"`
	FailureReason  string `json:"failure_reason,omitempty"`
}

// HandleSetuWebhook applies a status update from Setu to the canonical
// payment row. Idempotent — a duplicate webhook on a 'succeeded' row is a
// no-op. On 'failed' AND payment was wallet-funded, refunds the wallet.
//
// CALLER MUST verify the X-Setu-Signature header BEFORE calling this method.
// The HTTP layer does the signature check.
func (s *Service) HandleSetuWebhook(ctx context.Context, evt SetuWebhookEvent) error {
	if evt.SetuPaymentRef == "" {
		return fmt.Errorf("invalid: setu_payment_ref required")
	}
	if evt.Status != "succeeded" && evt.Status != "failed" {
		return fmt.Errorf("invalid: status must be succeeded|failed")
	}
	pmt, err := s.store.GetPaymentBySetuRef(ctx, evt.SetuPaymentRef)
	if err != nil {
		return fmt.Errorf("payment lookup: %w", err)
	}
	if pmt.Status == "succeeded" || pmt.Status == "refunded" {
		// Already terminal — webhook replays are no-ops.
		return nil
	}

	if evt.Status == "succeeded" {
		if err := s.store.MarkPaymentSucceeded(ctx, pmt.ID, evt.ReceiptNumber); err != nil {
			return err
		}
		// Mark linked bill as paid (best-effort).
		if pmt.BillID != nil {
			if err := s.store.MarkBillPaid(ctx, *pmt.BillID, pmt.ID); err != nil {
				slog.Warn("billpay: mark bill paid failed", "bill", pmt.BillID, "error", err)
			}
		}
		if err := s.producer.PublishPaymentSucceeded(ctx, pmt.UserID, pmt.ID, pmt.ProviderID, pmt.AmountPaise, evt.ReceiptNumber, evt.SetuPaymentRef); err != nil {
			slog.Warn("billpay: publish succeeded failed", "payment", pmt.ID, "error", err)
		}
		return nil
	}

	// status=failed
	reason := evt.FailureReason
	if reason == "" {
		reason = "setu_webhook_failed"
	}
	if err := s.store.MarkPaymentFailed(ctx, pmt.ID, reason); err != nil {
		return err
	}
	// If wallet-funded, refund.
	if pmt.PaymentMethod == "wallet" && pmt.WalletTxnID != nil {
		if err := s.wallet.RefundForBillPay(ctx, *pmt.WalletTxnID, pmt.AmountPaise, "billpay_webhook_failed"); err != nil {
			// Don't silently drop. Re-surface — Setu's webhook will retry.
			slog.Error("billpay: wallet refund on webhook failure failed",
				"payment", pmt.ID, "wallet_txn", pmt.WalletTxnID, "error", err)
			return fmt.Errorf("wallet refund: %w", err)
		}
		if err := s.producer.PublishPaymentRefunded(ctx, pmt.UserID, pmt.ID, pmt.AmountPaise, "setu_webhook_failed"); err != nil {
			slog.Warn("billpay: publish refunded failed", "payment", pmt.ID, "error", err)
		}
	}
	if err := s.producer.PublishPaymentFailed(ctx, pmt.UserID, pmt.ID, pmt.ProviderID, pmt.AmountPaise, reason); err != nil {
		slog.Warn("billpay: publish failed failed", "payment", pmt.ID, "error", err)
	}
	return nil
}
