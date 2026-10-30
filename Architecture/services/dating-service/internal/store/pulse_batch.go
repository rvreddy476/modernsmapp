// Batched candidate reads for Nebula (audit 2026-10-30 L3).
package store

import (
	"context"
	"fmt"

	"github.com/google/uuid"
)

// GetCandidatesForViewer is GetCandidateForViewer for many users in one
// query (Nebula used to run one query per pass, up to 100 per call), in the
// order of userIDs. It applies the same visibility rule as
// GetCandidateForViewer; users the viewer may not see are left out
// (TestGetCandidatesForViewer_MatchesSingleRead keeps the two in step).
func (s *Store) GetCandidatesForViewer(ctx context.Context, viewerID uuid.UUID, userIDs []uuid.UUID) ([]*CandidateProfile, error) {
	if len(userIDs) == 0 {
		return nil, nil
	}
	rows, err := s.db.Query(ctx, `
        SELECT `+s.candidateCols()+`
        FROM dating_profiles p
        LEFT JOIN dating_tunes t ON t.user_id = p.user_id
        WHERE p.user_id = ANY($2::uuid[])
          AND `+andClauses(s.candidateVisibleClauses("p", "$1::uuid", viewerGenderFromDB("$1::uuid"))), viewerID, userIDs)
	if err != nil {
		return nil, fmt.Errorf("query candidates for viewer: %w", err)
	}
	defer rows.Close()
	byID := make(map[uuid.UUID]*CandidateProfile, len(userIDs))
	for rows.Next() {
		c, err := s.scanCandidate(ctx, rows)
		if err != nil {
			return nil, err
		}
		byID[c.UserID] = c
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return orderCandidates(userIDs, byID), nil
}

// orderCandidates returns the found candidates in the order of ids, once each.
func orderCandidates(ids []uuid.UUID, byID map[uuid.UUID]*CandidateProfile) []*CandidateProfile {
	out := make([]*CandidateProfile, 0, len(byID))
	for _, id := range ids {
		if c, ok := byID[id]; ok {
			out = append(out, c)
			delete(byID, id)
		}
	}
	return out
}
