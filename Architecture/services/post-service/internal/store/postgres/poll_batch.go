package postgres

import (
	"context"
	"time"

	"github.com/google/uuid"
)

// Batched poll reads for listed pages (2026-10-30 audit B9). GetPoll and
// GetUserPollVotes answer one post each; a page of polls used to make two
// round trips per row. These answer a page in a fixed number of queries,
// with the same rules: options in sort order, votes counted only when they
// name one of the poll's own options (the poll_options join, see GetPoll),
// percentages of the counted total.

// GetPollsByPostIDs returns the poll of every listed post that has one,
// keyed by post id. A post with no poll is absent.
func (s *Store) GetPollsByPostIDs(ctx context.Context, postIDs []uuid.UUID) (map[uuid.UUID]*PollData, error) {
	out := make(map[uuid.UUID]*PollData, len(postIDs))
	if len(postIDs) == 0 {
		return out, nil
	}
	rows, err := s.db.Query(ctx, `
		SELECT post_id, question, allows_multiple, ends_at FROM polls WHERE post_id = ANY($1)
	`, postIDs)
	if err != nil {
		return nil, err
	}
	now := time.Now()
	for rows.Next() {
		var id uuid.UUID
		poll := &PollData{}
		if err := rows.Scan(&id, &poll.Question, &poll.AllowsMultiple, &poll.EndsAt); err != nil {
			rows.Close()
			return nil, err
		}
		poll.HasEnded = poll.EndsAt != nil && poll.EndsAt.Before(now)
		out[id] = poll
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(out) == 0 {
		return out, nil
	}

	optRows, err := s.db.Query(ctx, `
		SELECT post_id, id, label, sort_order FROM poll_options
		WHERE post_id = ANY($1) ORDER BY post_id, sort_order
	`, postIDs)
	if err != nil {
		return nil, err
	}
	for optRows.Next() {
		var postID uuid.UUID
		var opt PollOption
		if err := optRows.Scan(&postID, &opt.ID, &opt.Label, &opt.SortOrder); err != nil {
			optRows.Close()
			return nil, err
		}
		if poll := out[postID]; poll != nil {
			poll.Options = append(poll.Options, opt)
		}
	}
	optRows.Close()
	if err := optRows.Err(); err != nil {
		return nil, err
	}

	countRows, err := s.db.Query(ctx, `
		SELECT v.post_id, v.option_id, COUNT(*)
		FROM poll_votes v
		JOIN poll_options o ON o.id = v.option_id AND o.post_id = v.post_id
		WHERE v.post_id = ANY($1)
		GROUP BY v.post_id, v.option_id
	`, postIDs)
	if err != nil {
		return nil, err
	}
	counts := make(map[uuid.UUID]map[uuid.UUID]int64, len(out))
	for countRows.Next() {
		var postID, optID uuid.UUID
		var n int64
		if err := countRows.Scan(&postID, &optID, &n); err != nil {
			countRows.Close()
			return nil, err
		}
		if counts[postID] == nil {
			counts[postID] = map[uuid.UUID]int64{}
		}
		counts[postID][optID] = n
	}
	countRows.Close()
	if err := countRows.Err(); err != nil {
		return nil, err
	}
	for postID, poll := range out {
		applyPollCounts(poll, counts[postID])
	}
	return out, nil
}

// applyPollCounts sets each option's vote count and percentage, and the
// poll's total, from the counted votes (GetPoll's arithmetic).
func applyPollCounts(poll *PollData, voteCounts map[uuid.UUID]int64) {
	poll.TotalVotes = 0
	for _, n := range voteCounts {
		poll.TotalVotes += n
	}
	for i := range poll.Options {
		poll.Options[i].VoteCount = voteCounts[poll.Options[i].ID]
		poll.Options[i].Percentage = 0
		if poll.TotalVotes > 0 {
			poll.Options[i].Percentage = float64(poll.Options[i].VoteCount) / float64(poll.TotalVotes) * 100
		}
	}
}

// GetUserPollVotesBatch returns, per post, the option ids userID voted for
// (joined to poll_options, as GetUserPollVotes is). Posts without a vote are
// absent.
func (s *Store) GetUserPollVotesBatch(ctx context.Context, postIDs []uuid.UUID, userID uuid.UUID) (map[uuid.UUID][]uuid.UUID, error) {
	out := make(map[uuid.UUID][]uuid.UUID)
	if len(postIDs) == 0 {
		return out, nil
	}
	rows, err := s.db.Query(ctx, `
		SELECT v.post_id, v.option_id
		FROM poll_votes v
		JOIN poll_options o ON o.id = v.option_id AND o.post_id = v.post_id
		WHERE v.post_id = ANY($1) AND v.user_id = $2
	`, postIDs, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var postID, optID uuid.UUID
		if err := rows.Scan(&postID, &optID); err != nil {
			return nil, err
		}
		out[postID] = append(out[postID], optID)
	}
	return out, rows.Err()
}
