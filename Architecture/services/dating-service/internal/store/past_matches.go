// Past matches — mechanic M19 (DATING_PAST_MATCH_REPORT_ENABLED).
//
// The caller's matches that ended in the last few weeks, so someone who was
// treated badly can still report the person after an unmatch, an expiry or
// a block. Unlike ListMatchesForUser this keeps blocked pairs and
// suspended or deleted counterparts: they are exactly the ones worth
// reporting. Only the first name is read; no photo, no profile.
package store

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// PastMatch is one ended match from the caller's side.
type PastMatch struct {
	MatchID     uuid.UUID
	OtherID     uuid.UUID
	FirstName   *string
	MatchedAt   time.Time
	ClosedAt    time.Time
	Status      string
	CloseReason *string
	// Reported: the caller has reported the other person since the match
	// formed.
	Reported bool
}

// PastMatches lists userID's matches that ended after since, newest first.
// A match has ended once closed_at is set (unmatch, block, expiry, close),
// so that is what marks one.
// Rows of a purged person, or already anonymised, are left out: there is no
// one left to report.
func (s *Store) PastMatches(ctx context.Context, userID uuid.UUID, since time.Time, limit int) ([]PastMatch, error) {
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	rows, err := s.db.Query(ctx, `
        SELECT m.id, o.other_id, p.first_name, m.matched_at, m.closed_at, m.status, m.close_reason,
               EXISTS (SELECT 1 FROM dating_reports r
                   WHERE r.reporter_id = $1 AND r.target_id = o.other_id AND r.created_at >= m.matched_at)
        FROM dating_matches m
        CROSS JOIN LATERAL (SELECT CASE WHEN m.user_a = $1 THEN m.user_b ELSE m.user_a END AS other_id) o
        JOIN dating_profiles p ON p.user_id = o.other_id
        WHERE (m.user_a = $1 OR m.user_b = $1)
          AND m.closed_at IS NOT NULL AND m.closed_at > $2
          AND m.anonymised_at IS NULL
          AND m.close_reason IS DISTINCT FROM 'account_purged'
        ORDER BY m.closed_at DESC
        LIMIT $3`, userID, since, limit)
	if err != nil {
		return nil, fmt.Errorf("past matches: %w", err)
	}
	defer rows.Close()
	var out []PastMatch
	for rows.Next() {
		var pm PastMatch
		if err := rows.Scan(&pm.MatchID, &pm.OtherID, &pm.FirstName, &pm.MatchedAt, &pm.ClosedAt, &pm.Status, &pm.CloseReason, &pm.Reported); err != nil {
			return nil, err
		}
		out = append(out, pm)
	}
	return out, rows.Err()
}
