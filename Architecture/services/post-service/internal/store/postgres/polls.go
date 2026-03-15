package postgres

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
)

type PollVoteResult struct {
	OptionID   uuid.UUID `json:"option_id"`
	OptionText string    `json:"option_text"`
	VoteCount  int64     `json:"vote_count"`
}

func (s *Store) CastPollVote(ctx context.Context, postID, optionID, userID uuid.UUID) error {
	// Verify poll is still open
	var endsAt *time.Time
	err := s.db.QueryRow(ctx,
		`SELECT ends_at FROM polls WHERE post_id = $1`, postID).Scan(&endsAt)
	if err != nil {
		return fmt.Errorf("poll not found: %w", err)
	}
	if endsAt != nil && time.Now().After(*endsAt) {
		return fmt.Errorf("poll has ended")
	}

	_, err = s.db.Exec(ctx,
		`INSERT INTO poll_votes (post_id, option_id, user_id, created_at) VALUES ($1, $2, $3, NOW())`,
		postID, optionID, userID)
	if err != nil {
		return fmt.Errorf("already voted or invalid option: %w", err)
	}
	return nil
}

func (s *Store) GetPollResults(ctx context.Context, postID uuid.UUID) ([]PollVoteResult, error) {
	rows, err := s.db.Query(ctx, `
		SELECT po.id, po.label, COUNT(pv.user_id) as vote_count
		FROM poll_options po
		LEFT JOIN poll_votes pv ON pv.option_id = po.id AND pv.post_id = $1
		WHERE po.post_id = $1
		GROUP BY po.id, po.label
		ORDER BY po.sort_order ASC`,
		postID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var results []PollVoteResult
	for rows.Next() {
		var r PollVoteResult
		if err := rows.Scan(&r.OptionID, &r.OptionText, &r.VoteCount); err != nil {
			return nil, err
		}
		results = append(results, r)
	}
	return results, rows.Err()
}
