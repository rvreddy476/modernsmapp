package postgres

import (
	"context"

	"github.com/google/uuid"
)

func (s *Store) CreateTune(ctx context.Context, userID, postID uuid.UUID) error {
	_, err := s.db.Exec(ctx,
		`INSERT INTO tunes (user_id, post_id) VALUES ($1, $2) ON CONFLICT DO NOTHING`,
		userID, postID)
	return err
}

func (s *Store) DeleteTune(ctx context.Context, userID, postID uuid.UUID) error {
	_, err := s.db.Exec(ctx,
		`DELETE FROM tunes WHERE user_id = $1 AND post_id = $2`,
		userID, postID)
	return err
}

func (s *Store) HasTune(ctx context.Context, userID, postID uuid.UUID) (bool, error) {
	var exists bool
	err := s.db.QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM tunes WHERE user_id = $1 AND post_id = $2)`,
		userID, postID).Scan(&exists)
	return exists, err
}
