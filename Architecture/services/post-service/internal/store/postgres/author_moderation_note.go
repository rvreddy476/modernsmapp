package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// AuthorModerationNote is what the author of a rejected post is shown about
// why (2026-11-18): the latest base moderation decision's reason and source.
// Only post_moderation_decisions is read: its reason is the text a moderator
// writes for the author (the takedown notice carries the same text).
// post_review_audit's reason is a service or reviewer note and is never
// shown.
type AuthorModerationNote struct {
	Reason          string
	Source          string // "admin", "report" or "appeal"
	ResultingStatus string
}

// LatestModerationDecision returns the post's latest base decision, or nil
// when it has none.
func (s *Store) LatestModerationDecision(ctx context.Context, postID uuid.UUID) (*AuthorModerationNote, error) {
	var n AuthorModerationNote
	err := s.db.QueryRow(ctx, `
		SELECT reason, source, resulting_status FROM post_moderation_decisions
		WHERE post_id = $1 ORDER BY created_at DESC, decision_id DESC LIMIT 1`, postID).
		Scan(&n.Reason, &n.Source, &n.ResultingStatus)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("latest moderation decision: %w", err)
	}
	return &n, nil
}
