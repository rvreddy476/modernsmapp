// Who liked you store — mechanic M4.
package store

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// incomingSparkVisibleWhere is the rule ListIncomingSparks applies, for the
// spark aliased sp: not declined, the pair not blocked either way, the
// sender neither deleted nor suspended.
func incomingSparkVisibleWhere() string {
	return `sp.declined_at IS NULL
          AND NOT ` + blockedPairPredicate("sp.from_user_id", "sp.to_user_id") + `
          AND ` + visibleProfilePredicate("sp.from_user_id")
}

// CountIncomingSparks counts the sparks ListIncomingSparks would show the
// user, across every page.
func (s *Store) CountIncomingSparks(ctx context.Context, userID uuid.UUID) (int, error) {
	var n int
	err := s.db.QueryRow(ctx, `
        SELECT COUNT(*)::int FROM dating_sparks sp
        WHERE sp.to_user_id = $1
          AND `+incomingSparkVisibleWhere(), userID).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("count incoming sparks: %w", err)
	}
	return n, nil
}

// GetVisibleIncomingSpark returns the spark only when it is aimed at
// recipientID and ListIncomingSparks would show it (ErrSparkNotFound
// otherwise, the same answer for every cause).
func (s *Store) GetVisibleIncomingSpark(ctx context.Context, sparkID, recipientID uuid.UUID) (*Spark, error) {
	row := s.db.QueryRow(ctx, `
        SELECT `+sparkSelectCols+` FROM dating_sparks sp
        WHERE sp.id = $1 AND sp.to_user_id = $2
          AND `+incomingSparkVisibleWhere(), sparkID, recipientID)
	return scanSpark(row)
}

// PrimaryApprovedPhoto returns the user's approved primary photo, or
// ErrPhotoNotFound.
func (s *Store) PrimaryApprovedPhoto(ctx context.Context, userID uuid.UUID) (*Photo, error) {
	var id uuid.UUID
	err := s.db.QueryRow(ctx, `
        SELECT id FROM dating_photos
        WHERE user_id = $1 AND is_primary = true AND moderation_status = 'approved'
        LIMIT 1`, userID).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrPhotoNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("primary approved photo: %w", err)
	}
	return s.GetPhotoByID(ctx, id)
}
