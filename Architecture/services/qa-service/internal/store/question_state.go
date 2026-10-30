package store

import (
	"context"
	"encoding/json"

	"github.com/google/uuid"
)

// Question state changes a user (not the admin console) makes (audit H2).
// The service decides who may ask (service/guards.go); each statement here
// re-checks the state it moves from, so a stale read cannot close a merged
// question or reopen a moderator's lock.

// lockedByModerationSQL: a platform moderator locked the question (the lock
// is written by both admin paths as a moderation_actions row; there is no
// unlock route). Only the admin console may lift it.
const lockedByModerationSQL = `EXISTS (SELECT 1 FROM moderation_actions lk
	WHERE lk.target_type = 'question' AND lk.target_id = q.id AND lk.action_type = 'lock')`

// CloseQuestion closes an open question. False: it was not open.
func (s *Store) CloseQuestion(ctx context.Context, questionID, closedBy uuid.UUID, reason string) (bool, error) {
	tag, err := s.db.Exec(ctx, `
		UPDATE questions SET status = 'closed', closed_by = $2, closed_reason = $3, updated_at = now()
		WHERE id = $1 AND deleted_at IS NULL AND status = 'open'`, questionID, closedBy, reason)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() == 1, nil
}

// ReopenQuestion reopens a closed question that no platform moderator
// locked. A community moderator (asModerator) may reopen any such question;
// the author only one they closed themselves. Merged, pending and deleted
// questions never reopen. False: nothing matched.
func (s *Store) ReopenQuestion(ctx context.Context, questionID, actorID uuid.UUID, asModerator bool) (bool, error) {
	tag, err := s.db.Exec(ctx, `
		UPDATE questions q SET status = 'open', closed_by = NULL, closed_reason = NULL, updated_at = now()
		WHERE q.id = $1 AND q.deleted_at IS NULL AND q.status = 'closed'
		  AND NOT `+lockedByModerationSQL+`
		  AND ($3 OR (q.author_id = $2 AND q.closed_by = $2))`, questionID, actorID, asModerator)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() == 1, nil
}

// Community moderation action types (moderation_actions.action_type).
const (
	ActionCommunityApprove = "community_approve"
	ActionCommunityReject  = "community_reject"
)

// DecidePendingQuestion approves (opens) or rejects (deletes) one question
// of communityID awaiting approval, and records who did it. False: no such
// pending question in that community.
func (s *Store) DecidePendingQuestion(ctx context.Context, communityID, questionID, actorID uuid.UUID, approve bool, reason string) (bool, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	query := `UPDATE questions SET status = 'open', updated_at = now()`
	action := ActionCommunityApprove
	if !approve {
		query = `UPDATE questions SET status = 'deleted', deleted_at = now(), updated_at = now()`
		action = ActionCommunityReject
	}
	tag, err := tx.Exec(ctx, query+`
		WHERE id = $1 AND community_id = $2 AND status = 'pending_approval' AND deleted_at IS NULL`, questionID, communityID)
	if err != nil {
		return false, err
	}
	if tag.RowsAffected() == 0 {
		return false, nil
	}
	meta, err := json.Marshal(map[string]string{"source": "community-moderator", "community_id": communityID.String()})
	if err != nil {
		return false, err
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO moderation_actions (actor_id, action_type, target_type, target_id, reason, metadata)
		VALUES ($1, $2, 'question', $3, $4, $5::jsonb)`, actorID, action, questionID, reason, string(meta)); err != nil {
		return false, err
	}
	return true, tx.Commit(ctx)
}
