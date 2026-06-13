package store

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// HideRideRating soft-hides a customer rating + comment. visibility
// must be 'hidden' or 'flagged'. Used by admin moderation.
func (s *Store) HideRideRating(ctx context.Context, adminID, rideID uuid.UUID, visibility string) error {
	if visibility != "hidden" && visibility != "flagged" && visibility != "public" {
		return fmt.Errorf("invalid visibility: %s", visibility)
	}
	tag, err := s.db.Exec(ctx, `
		UPDATE rider_rides
		SET rating_visibility = $2,
			rating_hidden_by  = CASE WHEN $2 = 'public' THEN NULL ELSE $3 END,
			rating_hidden_at  = CASE WHEN $2 = 'public' THEN NULL ELSE NOW() END
		WHERE id = $1 AND rating IS NOT NULL
	`, rideID, visibility, adminID)
	if err != nil {
		return fmt.Errorf("hide rating: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return pgx.ErrNoRows
	}
	return nil
}

// AddPartnerResponse lets a partner reply to their rating. The store
// verifies the ride was indeed assigned to that partner.
func (s *Store) AddPartnerResponse(ctx context.Context, partnerID, rideID uuid.UUID, response string) error {
	tag, err := s.db.Exec(ctx, `
		UPDATE rider_rides
		SET partner_response = $3, partner_responded_at = NOW()
		WHERE id = $1 AND partner_id = $2 AND rating IS NOT NULL
	`, rideID, partnerID, response)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return pgx.ErrNoRows
	}
	return nil
}
