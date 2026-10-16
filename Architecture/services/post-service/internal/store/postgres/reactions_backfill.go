package postgres

import (
	"context"
	"time"

	"github.com/google/uuid"
)

// BackfillReaction is one historical like to copy into reactions.
type BackfillReaction struct {
	PostID    uuid.UUID
	UserID    uuid.UUID
	Reaction  string
	CreatedAt time.Time
}

// BackfillPostReactions inserts historical post reactions (the likes
// backfill, internal/backfill). Rows for posts that no longer exist are
// dropped; a reaction already present for the same post and viewer is
// kept as it is. It returns how many input rows matched a post and how
// many were inserted. With apply=false the insert runs and is rolled back,
// so the counts are those an applied run would produce.
func (s *Store) BackfillPostReactions(ctx context.Context, rows []BackfillReaction, apply bool) (known, inserted int64, err error) {
	if len(rows) == 0 {
		return 0, 0, nil
	}
	posts := make([]uuid.UUID, len(rows))
	users := make([]uuid.UUID, len(rows))
	types := make([]string, len(rows))
	times := make([]time.Time, len(rows))
	for i, r := range rows {
		posts[i], users[i], types[i], times[i] = r.PostID, r.UserID, r.Reaction, r.CreatedAt
	}

	tx, err := s.db.Begin(ctx)
	if err != nil {
		return 0, 0, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	err = tx.QueryRow(ctx, `
		WITH input AS (
			SELECT * FROM unnest($1::uuid[], $2::uuid[], $3::text[], $4::timestamptz[])
				AS t(target_id, user_id, reaction_type, created_at)
		),
		known AS (
			SELECT i.* FROM input i WHERE EXISTS (SELECT 1 FROM posts p WHERE p.id = i.target_id)
		),
		ins AS (
			INSERT INTO reactions (id, target_type, target_id, user_id, reaction_type, created_at)
			SELECT gen_random_uuid(), 'post', target_id, user_id, reaction_type, created_at FROM known
			ON CONFLICT (target_type, target_id, user_id) DO NOTHING
			RETURNING 1
		)
		SELECT (SELECT count(*) FROM known), (SELECT count(*) FROM ins)
	`, posts, users, types, times).Scan(&known, &inserted)
	if err != nil {
		return 0, 0, err
	}
	if apply {
		if err := tx.Commit(ctx); err != nil {
			return 0, 0, err
		}
	}
	return known, inserted, nil
}
