package postgres

import (
	"context"

	"github.com/google/uuid"
)

type PollVoteResult struct {
	OptionID   uuid.UUID `json:"option_id"`
	OptionText string    `json:"option_text"`
	VoteCount  int64     `json:"vote_count"`
}

// InsertPollVote records one vote, and is the ONLY place a poll_votes row is
// written. It reports whether a row was actually inserted; false means the
// vote was refused, not that it silently succeeded.
//
// Every rule the service checked in Go is restated here in SQL, because the
// service's checks are reads that happened earlier:
//
//   - the FROM poll_options clause makes the insert produce no row at all
//     unless option_id is an option of THIS post's poll. A fabricated UUID,
//     or a real option id borrowed from another poll, inserts nothing.
//
//   - the NOT EXISTS clause is the allows_multiple rule. For a single-choice
//     poll the row appears only if this voter has no vote on this poll yet.
//
//   - ON CONFLICT DO NOTHING absorbs the same option twice on a multi-choice
//     poll, which the primary key would otherwise raise as 23505.
//
// The advisory lock is what makes the NOT EXISTS trustworthy. Under READ
// COMMITTED the subquery sees the snapshot taken when the statement began, so
// two of the same voter's requests arriving together on a single-choice poll
// would both find "no vote yet" and both insert — for DIFFERENT options, so
// the primary key does not collide and nothing stops them. That is exactly
// the double-vote this function exists to prevent. The lock is keyed on
// (poll, voter), so it serializes a voter against themselves and never makes
// two different people on the same poll wait for each other.
func (s *Store) InsertPollVote(ctx context.Context, postID, optionID, userID uuid.UUID, allowsMultiple bool) (bool, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if _, err := tx.Exec(ctx,
		`SELECT pg_advisory_xact_lock(hashtextextended($1::text || ':' || $2::text, 0))`,
		postID, userID); err != nil {
		return false, err
	}

	tag, err := tx.Exec(ctx, `
		INSERT INTO poll_votes (post_id, option_id, user_id, created_at)
		SELECT $1, $2, $3, NOW()
		FROM poll_options o
		WHERE o.id = $2
		  AND o.post_id = $1
		  AND ($4 OR NOT EXISTS (
		        SELECT 1 FROM poll_votes v
		        WHERE v.post_id = $1 AND v.user_id = $3))
		ON CONFLICT (post_id, user_id, option_id) DO NOTHING
	`, postID, optionID, userID, allowsMultiple)
	if err != nil {
		return false, err
	}
	inserted := tag.RowsAffected() > 0
	if err := tx.Commit(ctx); err != nil {
		return false, err
	}
	return inserted, nil
}

// GetPollResults counts votes per option for a poll.
//
// It drives from poll_options, so an option with no votes still appears with
// zero and a vote row naming an option that does not exist can never appear
// at all. Migration 045 makes the latter impossible to create; the join shape
// keeps this read correct against a database that predates it.
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
