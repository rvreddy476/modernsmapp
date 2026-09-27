package postgres

import (
	"context"

	"github.com/google/uuid"
)

// Tunes are the viewer's PRIVATE negative signal on a post ("dislike").
// One row per (user, post); never counted, never exposed to anyone but the
// viewer as viewer_disliked (2026-09-27). The like lives in Redis + Scylla,
// so the two are made mutually exclusive by the service (tune.go): tuning
// removes the like, liking removes the tune.

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

// BatchHasTune answers viewer_disliked for a page of posts in one query.
// Posts without a tune are simply absent from the map.
func (s *Store) BatchHasTune(ctx context.Context, userID uuid.UUID, postIDs []uuid.UUID) (map[uuid.UUID]bool, error) {
	out := make(map[uuid.UUID]bool, len(postIDs))
	if len(postIDs) == 0 {
		return out, nil
	}
	rows, err := s.db.Query(ctx,
		`SELECT post_id FROM tunes WHERE user_id = $1 AND post_id = ANY($2)`, userID, postIDs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out[id] = true
	}
	return out, rows.Err()
}
