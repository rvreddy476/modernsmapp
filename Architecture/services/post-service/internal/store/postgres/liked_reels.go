package postgres

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
)

/*
	The viewer's likes, as rows of the reactions table (migration 063).

	The heart a viewer sees is Scylla reactions_by_post, which is keyed by
	post and cannot be listed by viewer. This table is the per-viewer index
	of the same fact: every like path writes it (service/liked_reels.go),
	and GET /v1/reels/liked reads it, newest like first.

	A row's created_at is when the viewer first reacted. A repeat tap or a
	change of type keeps it, so re-tapping a reel does not move it to the
	top of the list. (ToggleReaction, the multi-reaction toggle, stamps a
	change of type as it always has.)
*/

// pgReactionTypes is reactions_reaction_type_check (migration 063).
var pgReactionTypes = map[string]bool{
	"like": true, "spark": true, "supernova": true, "tune": true,
	"love": true, "haha": true, "wow": true, "sad": true, "angry": true,
}

// IndexReactionType is the reaction_type a like path stores: the caller's
// type when the table's check accepts it, else "like". The legacy routes
// take any string; the row only has to record that the viewer reacted.
func IndexReactionType(reaction string) string {
	if pgReactionTypes[reaction] {
		return reaction
	}
	return "like"
}

// UpsertPostReaction records that userID reacted to the post. Idempotent:
// a second call changes the type and keeps the first reaction's time.
func (s *Store) UpsertPostReaction(ctx context.Context, postID, userID uuid.UUID, reaction string) error {
	_, err := s.db.Exec(ctx, `
		INSERT INTO reactions (id, target_type, target_id, user_id, reaction_type, created_at)
		VALUES ($1, 'post', $2, $3, $4, now())
		ON CONFLICT (target_type, target_id, user_id)
		DO UPDATE SET reaction_type = EXCLUDED.reaction_type
	`, uuid.New(), postID, userID, IndexReactionType(reaction))
	return err
}

// DeletePostReaction removes userID's reaction on the post, of any type.
// Idempotent.
func (s *Store) DeletePostReaction(ctx context.Context, postID, userID uuid.UUID) error {
	_, err := s.db.Exec(ctx, `
		DELETE FROM reactions
		WHERE target_type = 'post' AND target_id = $1 AND user_id = $2
	`, postID, userID)
	return err
}

// LikedPost is one row of the viewer's liked list.
type LikedPost struct {
	PostID  uuid.UUID
	LikedAt time.Time
}

// LikedCursor is the keyset position after which a page starts: the
// (liked-at, post id) of the last row the previous page consumed.
type LikedCursor struct {
	LikedAt time.Time
	PostID  uuid.UUID
}

// EncodeLikedCursor is the opaque next_cursor of GET /v1/reels/liked:
// "<RFC3339Nano UTC>_<uuid>", the reel feed's cursor shape. UTC keeps a
// '+' offset out of a query string.
func EncodeLikedCursor(c LikedCursor) string {
	return encodeReelCursor(c.LikedAt.UTC(), c.PostID)
}

// ParseLikedCursor reads EncodeLikedCursor's form. An empty cursor is the
// first page (nil); an unreadable one is an error, never the first page.
func ParseLikedCursor(raw string) (*LikedCursor, error) {
	if raw == "" {
		return nil, nil
	}
	t, id, err := parseReelCursor(raw)
	if err != nil {
		return nil, fmt.Errorf("liked cursor: %w", err)
	}
	return &LikedCursor{LikedAt: t, PostID: id}, nil
}

// ListLikedShortFormPosts returns up to limit of userID's liked posts that
// are short-form (flick / reel) and live (not deleted, not scheduled,
// effectively approved), newest like first, strictly after `after`. The
// audience, the author's privacy and the age gate are the service's to
// judge on each id (it runs the post detail's read gate), so a page the
// service returns may be shorter than the rows read here.
func (s *Store) ListLikedShortFormPosts(ctx context.Context, userID uuid.UUID, limit int, after *LikedCursor) ([]LikedPost, error) {
	if limit < 1 {
		return nil, fmt.Errorf("liked posts limit %d: must be at least 1", limit)
	}
	args := []any{userID, limit}
	query := `
		SELECT r.target_id, r.created_at
		FROM reactions r
		JOIN posts p ON p.id = r.target_id
		WHERE r.user_id = $1
			AND r.target_type = 'post'
			AND p.content_type IN ('flick', 'reel')
			AND ` + ViewerEligibleSQL("p")
	if after != nil {
		args = append(args, after.LikedAt, after.PostID)
		query += fmt.Sprintf(` AND (r.created_at, r.target_id) < ($%d, $%d)`, len(args)-1, len(args))
	}
	query += ` ORDER BY r.created_at DESC, r.target_id DESC LIMIT $2`

	rows, err := s.db.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []LikedPost
	for rows.Next() {
		var lp LikedPost
		if err := rows.Scan(&lp.PostID, &lp.LikedAt); err != nil {
			return nil, err
		}
		out = append(out, lp)
	}
	return out, rows.Err()
}
