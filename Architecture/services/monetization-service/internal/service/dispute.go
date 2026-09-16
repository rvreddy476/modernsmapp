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

// Admin dispute updates, refunds and fraud decisions are in
// admin_console.go: each commits its audit row with the change.

// ListPendingFraudReviews returns pending fraud reviews (admin view).
func (s *Service) ListPendingFraudReviews(ctx context.Context, limit, offset int) ([]postgres.FraudReview, error) {
	return s.store.ListPendingFraudReviews(ctx, limit, offset)
}
