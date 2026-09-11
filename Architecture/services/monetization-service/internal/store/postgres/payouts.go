package postgres

import (
	"context"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// The batch and statement store functions that used to live here were
// removed in plan Phase 3B: nothing called them, and the tables they
// wrote (payout_batches, payout_statements) are dropped by Phase 4's
// cleanup migration. The withdrawal record itself is in
// payout_requests.go.

// SetPayoutRequestProviderRef sets the provider reference on a payout request.
func (s *Store) SetPayoutRequestProviderRef(ctx context.Context, requestID uuid.UUID, ref string) error {
	_, err := s.db.Exec(ctx, `
		UPDATE payout_requests SET provider_reference = $2 WHERE id = $1
	`, requestID, ref)
	return err
}

// SetPayoutRequestFailure marks a payout request as failed with a reason.
func (s *Store) SetPayoutRequestFailure(ctx context.Context, requestID uuid.UUID, reason string) error {
	_, err := s.db.Exec(ctx, `
		UPDATE payout_requests SET status = 'failed', failure_reason = $2 WHERE id = $1
	`, requestID, reason)
	return err
}

// GetPayoutRequestByProviderRef returns a payout request by its provider reference.
func (s *Store) GetPayoutRequestByProviderRef(ctx context.Context, ref string) (*PayoutRequest, error) {
	var r PayoutRequest
	err := s.db.QueryRow(ctx, `
		SELECT id, user_id, transaction_id, amount, currency, status, payout_method_id, requested_at
		FROM payout_requests
		WHERE provider_reference = $1
	`, ref).Scan(
		&r.ID, &r.UserID, &r.TransactionID, &r.AmountPaise, &r.Currency, &r.Status,
		&r.payoutMethodID, &r.RequestedAt,
	)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	return &r, nil
}

// CheckKYCVerified checks whether a creator_tax_profiles row exists with verified_at IS NOT NULL.
func (s *Store) CheckKYCVerified(ctx context.Context, userID uuid.UUID) (bool, error) {
	var exists bool
	err := s.db.QueryRow(ctx, `
		SELECT EXISTS(
			SELECT 1 FROM creator_tax_profiles
			WHERE user_id = $1 AND verified_at IS NOT NULL
		)
	`, userID).Scan(&exists)
	return exists, err
}
