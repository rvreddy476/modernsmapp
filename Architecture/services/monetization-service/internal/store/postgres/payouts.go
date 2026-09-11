package postgres

import (
	"context"

	"github.com/google/uuid"
)

// The batch and statement store functions that used to live here were
// removed in plan Phase 3B: nothing called them, and the tables they
// wrote (payout_batches, payout_statements) are dropped by migration
// 022. The ad-hoc status setters that followed them
// (SetPayoutRequestProviderRef, SetPayoutRequestFailure,
// GetPayoutRequestByProviderRef) went in Phase 4A: every write of
// payout_requests.status is TransitionPayoutRequest in payout_rail.go,
// and the withdrawal record itself is in payout_requests.go.

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
