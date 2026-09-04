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

// AuthorFeedback is one viewer's latest answer about one AUTHOR — "Don't
// recommend this account" on the post "more" sheet. not_interested is the
// mute; interested clears it. See migration 007.
type AuthorFeedback struct {
	UserID    uuid.UUID `json:"user_id"`
	AuthorID  uuid.UUID `json:"author_id"`
	Signal    string    `json:"signal"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// MutedAuthor is one row of GET /v1/feed/feedback/authors: an author the
// viewer currently has muted and when the mute was set.
type MutedAuthor struct {
	AuthorID  uuid.UUID `json:"author_id"`
	CreatedAt time.Time `json:"created_at"`
}

// UpsertAuthorFeedback stores the viewer's answer about an author, replacing
// any earlier answer. Latest wins, same as UpsertFeedback: a not_interested
// after an interested re-mutes, an interested after a not_interested clears
// the mute. created_at is reset when the answer changes so the listing shows
// when the CURRENT mute was set, not when the viewer first ever muted.
func (s *MetaStore) UpsertAuthorFeedback(ctx context.Context, f *AuthorFeedback) error {
	return s.db.QueryRow(ctx, `
		INSERT INTO feed_author_feedback (user_id, author_id, signal)
		VALUES ($1, $2, $3)
		ON CONFLICT (user_id, author_id) DO UPDATE
		SET signal = EXCLUDED.signal,
		    created_at = CASE WHEN feed_author_feedback.signal = EXCLUDED.signal
		                      THEN feed_author_feedback.created_at ELSE NOW() END,
		    updated_at = NOW()
		RETURNING created_at, updated_at`,
		f.UserID, f.AuthorID, f.Signal,
	).Scan(&f.CreatedAt, &f.UpdatedAt)
}

// MutedAuthorIDs returns every author this viewer has answered
// not_interested about at the author level. One query per page; the caller
// fails closed on error.
func (s *MetaStore) MutedAuthorIDs(ctx context.Context, viewerID uuid.UUID) (map[uuid.UUID]struct{}, error) {
	rows, err := s.db.Query(ctx, `
		SELECT author_id FROM feed_author_feedback
		WHERE user_id = $1 AND signal = 'not_interested'`,
		viewerID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make(map[uuid.UUID]struct{})
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out[id] = struct{}{}
	}
	return out, rows.Err()
}

// ListMutedAuthors is MutedAuthorIDs with the timestamp the client shows,
// newest mute first. Never returns nil.
func (s *MetaStore) ListMutedAuthors(ctx context.Context, viewerID uuid.UUID) ([]MutedAuthor, error) {
	rows, err := s.db.Query(ctx, `
		SELECT author_id, created_at FROM feed_author_feedback
		WHERE user_id = $1 AND signal = 'not_interested'
		ORDER BY created_at DESC, author_id`,
		viewerID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []MutedAuthor{}
	for rows.Next() {
		var m MutedAuthor
		if err := rows.Scan(&m.AuthorID, &m.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// AuthorFeedbackNet is the viewer's net answer about an author across every
// post they answered on: +1 per interested, -1 per not_interested. `muted`
// reports whether the viewer also has an author-level not_interested (a
// "Don't recommend this account") for them. ranking.NetWithMute combines
// the two into the value mirrored into Redis for the author penalty. One
// query.
func (s *MetaStore) AuthorFeedbackNet(ctx context.Context, viewerID, authorID uuid.UUID) (net int, muted bool, err error) {
	err = s.db.QueryRow(ctx, `
		SELECT
		    COALESCE((SELECT SUM(CASE signal WHEN 'interested' THEN 1 ELSE -1 END)
		              FROM feed_feedback WHERE user_id = $1 AND author_id = $2), 0),
		    EXISTS (SELECT 1 FROM feed_author_feedback
		            WHERE user_id = $1 AND author_id = $2 AND signal = 'not_interested')`,
		viewerID, authorID).Scan(&net, &muted)
	return net, muted, err
}
