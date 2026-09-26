package store

import (
	"context"

	"github.com/google/uuid"
)

// RecordAuthorReveal writes the audit row for an admin or moderator learning
// who wrote an anonymous post. Append-only, like every group_admin_audit
// row; the metadata names the group and the author so the trail is
// complete without a join to a post that may later be deleted.
func (s *Store) RecordAuthorReveal(ctx context.Context, actorID, groupID, postID uuid.UUID, authorID string) error {
	_, err := s.db.Exec(ctx, `
		INSERT INTO group_admin_audit (actor_id, action, target_type, target_id, reason, metadata)
		VALUES ($1, 'group_post.author_revealed', 'group_post', $2, '', $3)`,
		actorID, postID, map[string]any{"group_id": groupID, "author_id": authorID})
	return err
}

// CountAuthorReveals is for tests and admin tooling: how many times a
// post's author has been revealed.
func (s *Store) CountAuthorReveals(ctx context.Context, postID uuid.UUID) (int, error) {
	var n int
	err := s.db.QueryRow(ctx, `
		SELECT COUNT(*) FROM group_admin_audit
		WHERE action = 'group_post.author_revealed' AND target_type = 'group_post' AND target_id = $1`, postID).Scan(&n)
	return n, err
}
