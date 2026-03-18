package service

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/atpost/monetization-service/internal/store/postgres"
	"github.com/google/uuid"
)

// ---------------------------------------------------------------------------
// Disputes
// ---------------------------------------------------------------------------

// CreateDispute creates a dispute for a transaction, validating the transaction
// exists and belongs to the user.
func (s *Service) CreateDispute(ctx context.Context, userID, transactionID uuid.UUID, reason, description string) (*postgres.Dispute, error) {
	// Validate transaction exists and belongs to user
	txn, err := s.store.GetTransactionByID(ctx, transactionID)
	if err != nil {
		return nil, fmt.Errorf("lookup transaction: %w", err)
	}
	if txn == nil {
		return nil, fmt.Errorf("TRANSACTION_NOT_FOUND")
	}
	if txn.WalletID != userID {
		return nil, fmt.Errorf("TRANSACTION_NOT_OWNED")
	}

	var desc *string
	if description != "" {
		desc = &description
	}

	dispute := &postgres.Dispute{
		UserID:        userID,
		TransactionID: transactionID,
		Reason:        reason,
		Description:   desc,
	}

	result, err := s.store.CreateDispute(ctx, dispute)
	if err != nil {
		return nil, fmt.Errorf("create dispute: %w", err)
	}

	slog.Info("dispute created", "dispute_id", result.ID, "user_id", userID, "transaction_id", transactionID)
	return result, nil
}

// ListUserDisputes returns disputes for a user with pagination.
func (s *Service) ListUserDisputes(ctx context.Context, userID uuid.UUID, limit, offset int) ([]postgres.Dispute, error) {
	return s.store.ListUserDisputes(ctx, userID, limit, offset)
}

// GetDispute returns a dispute by ID.
func (s *Service) GetDispute(ctx context.Context, disputeID uuid.UUID) (*postgres.Dispute, error) {
	return s.store.GetDispute(ctx, disputeID)
}

// ListOpenDisputes returns open disputes for admin view.
func (s *Service) ListOpenDisputes(ctx context.Context, limit, offset int) ([]postgres.Dispute, error) {
	return s.store.ListOpenDisputes(ctx, limit, offset)
}

// ResolveDispute updates a dispute's status (admin action).
func (s *Service) ResolveDispute(ctx context.Context, disputeID uuid.UUID, status, notes string, adminID uuid.UUID) error {
	err := s.store.ResolveDispute(ctx, disputeID, status, notes, adminID)
	if err != nil {
		return err
	}

	slog.Info("dispute resolved", "dispute_id", disputeID, "status", status, "admin_id", adminID)
	return nil
}

// ---------------------------------------------------------------------------
// Refunds
// ---------------------------------------------------------------------------

// ProcessRefund creates a refund, credits the user wallet, and logs a ledger entry.
func (s *Service) ProcessRefund(ctx context.Context, transactionID uuid.UUID, amountPaise int64, reason string, disputeID *uuid.UUID) (*postgres.Refund, error) {
	// Validate transaction exists
	txn, err := s.store.GetTransactionByID(ctx, transactionID)
	if err != nil {
		return nil, fmt.Errorf("lookup transaction: %w", err)
	}
	if txn == nil {
		return nil, fmt.Errorf("TRANSACTION_NOT_FOUND")
	}

	// Check for existing refund
	existingRefund, err := s.store.GetRefundByTransaction(ctx, transactionID)
	if err != nil {
		return nil, fmt.Errorf("check existing refund: %w", err)
	}
	if existingRefund != nil {
		return nil, fmt.Errorf("REFUND_ALREADY_EXISTS")
	}

	// Create refund record
	refund := &postgres.Refund{
		TransactionID: transactionID,
		DisputeID:     disputeID,
		AmountPaise:   amountPaise,
		Reason:        reason,
		Status:        "pending",
	}

	result, err := s.store.CreateRefund(ctx, refund)
	if err != nil {
		return nil, fmt.Errorf("create refund: %w", err)
	}

	// Credit user wallet
	if err := s.store.CreditWallet(ctx, txn.WalletID, amountPaise); err != nil {
		slog.Error("refund: failed to credit wallet", "error", err, "user_id", txn.WalletID, "amount_paise", amountPaise)
		return result, fmt.Errorf("credit wallet: %w", err)
	}

	// Create refund transaction record
	refundTxn := &postgres.Transaction{
		WalletID:      txn.WalletID,
		Type:          "refund",
		AmountPaise:   amountPaise,
		Currency:      txn.Currency,
		Status:        "completed",
		ReferenceType: "refund",
		ReferenceID:   result.ID.String(),
		Description:   fmt.Sprintf("Refund: %s", reason),
	}
	if err := s.store.CreateTransaction(ctx, refundTxn); err != nil {
		slog.Error("refund: failed to create refund transaction", "error", err)
	}

	slog.Info("refund processed", "refund_id", result.ID, "transaction_id", transactionID, "amount_paise", amountPaise)
	return result, nil
}

// ---------------------------------------------------------------------------
// Fraud reviews (delegated to store)
// ---------------------------------------------------------------------------

// ListPendingFraudReviews returns pending fraud reviews (admin view).
func (s *Service) ListPendingFraudReviews(ctx context.Context, limit, offset int) ([]postgres.FraudReview, error) {
	return s.store.ListPendingFraudReviews(ctx, limit, offset)
}

// ResolveFraudReview resolves a fraud review (admin action).
func (s *Service) ResolveFraudReview(ctx context.Context, reviewID uuid.UUID, status, notes string, reviewerID uuid.UUID) error {
	err := s.store.ResolveFraudReview(ctx, reviewID, status, notes, reviewerID)
	if err != nil {
		return err
	}
	slog.Info("fraud review resolved", "review_id", reviewID, "status", status, "reviewer_id", reviewerID)
	return nil
}
