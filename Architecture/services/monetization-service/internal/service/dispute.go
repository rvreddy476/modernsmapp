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

// GetUserDispute returns a dispute only to the user it concerns: the one
// who opened it, over their own transaction (CreateDispute refuses any
// other). Anyone else gets nil, exactly as for an id that does not exist,
// so the route cannot be used to learn which dispute ids are real. Admin
// reads go through the admin-service token family, not this.
func (s *Service) GetUserDispute(ctx context.Context, userID, disputeID uuid.UUID) (*postgres.Dispute, error) {
	d, err := s.store.GetDispute(ctx, disputeID)
	if err != nil {
		return nil, err
	}
	return disputeVisibleTo(d, userID), nil
}

// disputeVisibleTo is the ownership gate: d when userID opened it, else nil.
func disputeVisibleTo(d *postgres.Dispute, userID uuid.UUID) *postgres.Dispute {
	if d == nil || userID == uuid.Nil || d.UserID != userID {
		return nil
	}
	return d
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
