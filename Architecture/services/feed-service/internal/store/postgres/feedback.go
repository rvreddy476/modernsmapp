package postgres

import (
	"context"
	"time"

	"github.com/google/uuid"
)

// PostFeedback is one viewer's latest answer about one post — the "Interested"
// / "Not interested" rows of the post "more" sheet. See migration 006.
type PostFeedback struct {
	UserID    uuid.UUID `json:"user_id"`
	PostID    uuid.UUID `json:"post_id"`
	AuthorID  uuid.UUID `json:"author_id"`
	Category  string    `json:"category,omitempty"`
	Signal    string    `json:"signal"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// UpsertFeedback stores the viewer's answer for a post, replacing any earlier
// answer for the same post. Idempotent: repeating the same signal only bumps
// updated_at. created_at / updated_at are filled from the row on return.
func (s *MetaStore) UpsertFeedback(ctx context.Context, f *PostFeedback) error {
	return s.db.QueryRow(ctx, `
		INSERT INTO feed_feedback (user_id, post_id, author_id, category, signal)
		VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (user_id, post_id) DO UPDATE
		SET signal = EXCLUDED.signal,
		    author_id = EXCLUDED.author_id,
		    category = EXCLUDED.category,
		    updated_at = NOW()
		RETURNING created_at, updated_at`,
		f.UserID, f.PostID, f.AuthorID, f.Category, f.Signal,
	).Scan(&f.CreatedAt, &f.UpdatedAt)
}

// ExcludedPostIDs returns, out of `postIDs`, the ones this viewer must not be
// shown: posts they marked not_interested, plus posts they hid through
// POST /v1/feed/hide (feed_hides — written since migration 002, read nowhere
// until this filter). One query per page; the caller fails closed on error.
func (s *MetaStore) ExcludedPostIDs(ctx context.Context, viewerID uuid.UUID, postIDs []uuid.UUID) (map[uuid.UUID]struct{}, error) {
	out := make(map[uuid.UUID]struct{})
	if len(postIDs) == 0 {
		return out, nil
	}
	rows, err := s.db.Query(ctx, `
		SELECT post_id FROM feed_feedback
		WHERE user_id = $1 AND signal = 'not_interested' AND post_id = ANY($2)
		UNION
		SELECT post_id FROM feed_hides
		WHERE user_id = $1 AND post_id = ANY($2)`,
		viewerID, postIDs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out[id] = struct{}{}
	}
	return out, rows.Err()
}

// AuthorFeedbackNet is the viewer's net answer about an author across every
// post they answered on: +1 per interested, -1 per not_interested. This is
// the value mirrored into Redis for the ranker's author penalty.
func (s *MetaStore) AuthorFeedbackNet(ctx context.Context, viewerID, authorID uuid.UUID) (int, error) {
	var net int
	err := s.db.QueryRow(ctx, `
		SELECT COALESCE(SUM(CASE signal WHEN 'interested' THEN 1 ELSE -1 END), 0)
		FROM feed_feedback WHERE user_id = $1 AND author_id = $2`,
		viewerID, authorID).Scan(&net)
	return net, err
}
