package store

import (
	"context"
	"fmt"

	"github.com/google/uuid"
)

// RideMessage mirrors rider_ride_messages.
type RideMessage struct {
	ID         uuid.UUID `json:"id"`
	RideID     uuid.UUID `json:"ride_id"`
	AuthorID   uuid.UUID `json:"author_id"`
	AuthorRole string    `json:"author_role"`
	Body       string    `json:"body"`
	ReadBy     []byte    `json:"read_by"`
	CreatedAt  string    `json:"created_at"`
}

// AppendRideMessage authorizes via role check at the service layer.
func (s *Store) AppendRideMessage(ctx context.Context, rideID, authorID uuid.UUID, authorRole, body string) (*RideMessage, error) {
	if authorRole != "customer" && authorRole != "partner" && authorRole != "admin" {
		return nil, fmt.Errorf("invalid role: %s", authorRole)
	}
	var m RideMessage
	if err := s.db.QueryRow(ctx, `
		INSERT INTO rider_ride_messages (ride_id, author_id, author_role, body)
		VALUES ($1, $2, $3, $4)
		RETURNING id, ride_id, author_id, author_role, body, read_by, created_at::text
	`, rideID, authorID, authorRole, body).Scan(
		&m.ID, &m.RideID, &m.AuthorID, &m.AuthorRole, &m.Body, &m.ReadBy, &m.CreatedAt,
	); err != nil {
		return nil, err
	}
	return &m, nil
}

// ListRideMessages returns the conversation in chronological order.
func (s *Store) ListRideMessages(ctx context.Context, rideID uuid.UUID) ([]RideMessage, error) {
	rows, err := s.db.Query(ctx, `
		SELECT id, ride_id, author_id, author_role, body, read_by, created_at::text
		FROM rider_ride_messages
		WHERE ride_id = $1
		ORDER BY created_at
	`, rideID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []RideMessage
	for rows.Next() {
		var m RideMessage
		if err := rows.Scan(&m.ID, &m.RideID, &m.AuthorID, &m.AuthorRole,
			&m.Body, &m.ReadBy, &m.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// MarkRideMessageRead is idempotent via NOT EXISTS on user_id.
func (s *Store) MarkRideMessageRead(ctx context.Context, messageID, userID uuid.UUID, role string) error {
	_, err := s.db.Exec(ctx, `
		UPDATE rider_ride_messages
		SET read_by = read_by || jsonb_build_array(
			jsonb_build_object('role', $2::text, 'user_id', $3::text, 'at', NOW()::text)
		)
		WHERE id = $1
		  AND NOT EXISTS (
			SELECT 1 FROM jsonb_array_elements(read_by) e
			WHERE e->>'user_id' = $3::text
		  )
	`, messageID, role, userID.String())
	return err
}

// RidePartyMembership reports the user's role on the ride.
type RidePartyMembership struct {
	IsCustomer bool
	IsPartner  bool
}

func (s *Store) RidePartyMembership(ctx context.Context, rideID, userID uuid.UUID) (*RidePartyMembership, error) {
	var m RidePartyMembership
	if err := s.db.QueryRow(ctx, `
		SELECT
			EXISTS(SELECT 1 FROM rider_rides WHERE id = $1 AND customer_user_id = $2),
			EXISTS(
				SELECT 1 FROM rider_rides rr
				JOIN rider_partners rp ON rp.id = rr.partner_id
				WHERE rr.id = $1 AND rp.user_id = $2
			)
	`, rideID, userID).Scan(&m.IsCustomer, &m.IsPartner); err != nil {
		return nil, err
	}
	return &m, nil
}
