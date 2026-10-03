// Kind messages — mechanic M13 (DATING_KIND_CHECK_ENABLED).
//
// dating_comment_filters is each user's spark-comment filter: hide unkind
// comments (on unless switched off) and their own hidden words.
// dating_message_feedback records "did this message bother you?" answers:
// safety evidence, append-only, never deleted (a purge swaps the purged id
// for its subject token).
package store

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// CommentFilter is a user's spark-comment filter.
type CommentFilter struct {
	FilterUnkind bool
	Words        []string
}

// GetCommentFilter reads the user's filter; without a row, unkind comments
// are hidden and there are no words.
func (s *Store) GetCommentFilter(ctx context.Context, userID uuid.UUID) (CommentFilter, error) {
	f := CommentFilter{FilterUnkind: true, Words: []string{}}
	err := s.db.QueryRow(ctx, `SELECT filter_unkind, words FROM dating_comment_filters WHERE user_id = $1`, userID).Scan(&f.FilterUnkind, &f.Words)
	if errors.Is(err, pgx.ErrNoRows) {
		return f, nil
	}
	if err != nil {
		return CommentFilter{}, fmt.Errorf("comment filter: %w", err)
	}
	if f.Words == nil {
		f.Words = []string{}
	}
	return f, nil
}

// SetCommentFilter replaces the user's filter.
func (s *Store) SetCommentFilter(ctx context.Context, userID uuid.UUID, f CommentFilter) error {
	words := f.Words
	if words == nil {
		words = []string{}
	}
	_, err := s.db.Exec(ctx, `
        INSERT INTO dating_comment_filters (user_id, filter_unkind, words, updated_at) VALUES ($1, $2, $3, now())
        ON CONFLICT (user_id) DO UPDATE SET filter_unkind = EXCLUDED.filter_unkind, words = EXCLUDED.words, updated_at = now()`,
		userID, f.FilterUnkind, words)
	if err != nil {
		return fmt.Errorf("set comment filter: %w", err)
	}
	return nil
}

// RecordMessageFeedback appends a "did this bother you?" answer.
func (s *Store) RecordMessageFeedback(ctx context.Context, matchID, userID, otherID uuid.UUID, bothered bool) error {
	_, err := s.db.Exec(ctx, `
        INSERT INTO dating_message_feedback (match_id, user_id, other_id, bothered) VALUES ($1, $2, $3, $4)`,
		matchID, userID, otherID, bothered)
	if err != nil {
		return fmt.Errorf("record message feedback: %w", err)
	}
	return nil
}
