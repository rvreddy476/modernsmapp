package postgres

import (
	"context"
	"time"

	"github.com/google/uuid"
)

type Suspension struct {
	UserID    uuid.UUID `json:"user_id"`
	Until     time.Time `json:"until"`
	Reason    string    `json:"reason"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

func (s *Store) SuspendUser(ctx context.Context, userID uuid.UUID, until time.Time, reason string) error {
	query := `
		INSERT INTO admin.suspensions (user_id, until, reason, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $4)
		ON CONFLICT (user_id) DO UPDATE 
		SET until = EXCLUDED.until, reason = EXCLUDED.reason, updated_at = EXCLUDED.updated_at
	`
	_, err := s.db.Exec(ctx, query, userID, until, reason, time.Now())
	return err
}
