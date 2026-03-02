package store

import (
	"context"
	"time"

	"github.com/gocql/gocql"
	"github.com/google/uuid"
)

// PairSignals holds precomputed pair-level signals from ScyllaDB.
type PairSignals struct {
	ViewerID              uuid.UUID
	CandidateID           uuid.UUID
	MutualFriendCount     int
	MutualStrongCount     int
	CommonGroupCount      int
	SameCity              bool
	SameSchool            bool
	SameCompany           bool
	MutualFollow          bool
	ProfileView7d         int
	ReverseView7d         int
	ChatInteraction30d    int
	ContentCoengagement7d int
	TriadicClosureScore   float32
	InterestSimilarity    float32
	LastUpdatedAt         time.Time
}

// ScyllaStore wraps ScyllaDB operations for pair signals.
type ScyllaStore struct {
	session *gocql.Session
}

// NewScyllaStore creates a new ScyllaDB store.
func NewScyllaStore(session *gocql.Session) *ScyllaStore {
	return &ScyllaStore{session: session}
}

// UpsertPairSignals writes or updates pair-level signals.
func (s *ScyllaStore) UpsertPairSignals(_ context.Context, ps *PairSignals) error {
	return s.session.Query(`
		INSERT INTO user_pair_signals (
			viewer_id, candidate_id,
			mutual_friend_count, mutual_strong_count, common_group_count,
			same_city, same_school, same_company, mutual_follow,
			profile_view_7d, reverse_view_7d, chat_interaction_30d,
			content_coengagement_7d, triadic_closure_score, interest_similarity,
			last_updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		ps.ViewerID, ps.CandidateID,
		ps.MutualFriendCount, ps.MutualStrongCount, ps.CommonGroupCount,
		ps.SameCity, ps.SameSchool, ps.SameCompany, ps.MutualFollow,
		ps.ProfileView7d, ps.ReverseView7d, ps.ChatInteraction30d,
		ps.ContentCoengagement7d, ps.TriadicClosureScore, ps.InterestSimilarity,
		time.Now(),
	).Exec()
}

// GetPairSignals reads pair signals for a single viewer-candidate pair.
func (s *ScyllaStore) GetPairSignals(_ context.Context, viewerID, candidateID uuid.UUID) (*PairSignals, error) {
	ps := &PairSignals{ViewerID: viewerID, CandidateID: candidateID}
	err := s.session.Query(`
		SELECT mutual_friend_count, mutual_strong_count, common_group_count,
		       same_city, same_school, same_company, mutual_follow,
		       profile_view_7d, reverse_view_7d, chat_interaction_30d,
		       content_coengagement_7d, triadic_closure_score, interest_similarity,
		       last_updated_at
		FROM user_pair_signals
		WHERE viewer_id = ? AND candidate_id = ?`,
		viewerID, candidateID,
	).Scan(
		&ps.MutualFriendCount, &ps.MutualStrongCount, &ps.CommonGroupCount,
		&ps.SameCity, &ps.SameSchool, &ps.SameCompany, &ps.MutualFollow,
		&ps.ProfileView7d, &ps.ReverseView7d, &ps.ChatInteraction30d,
		&ps.ContentCoengagement7d, &ps.TriadicClosureScore, &ps.InterestSimilarity,
		&ps.LastUpdatedAt,
	)
	if err == gocql.ErrNotFound {
		return ps, nil
	}
	return ps, err
}

// GetPairSignalsBatch reads pair signals for multiple candidates.
func (s *ScyllaStore) GetPairSignalsBatch(_ context.Context, viewerID uuid.UUID, candidateIDs []uuid.UUID) (map[uuid.UUID]*PairSignals, error) {
	result := make(map[uuid.UUID]*PairSignals, len(candidateIDs))
	for _, cid := range candidateIDs {
		result[cid] = &PairSignals{ViewerID: viewerID, CandidateID: cid}
	}

	if len(candidateIDs) == 0 {
		return result, nil
	}

	iter := s.session.Query(`
		SELECT candidate_id,
		       mutual_friend_count, mutual_strong_count, common_group_count,
		       same_city, same_school, same_company, mutual_follow,
		       profile_view_7d, reverse_view_7d, chat_interaction_30d,
		       content_coengagement_7d, triadic_closure_score, interest_similarity,
		       last_updated_at
		FROM user_pair_signals
		WHERE viewer_id = ? AND candidate_id IN ?`,
		viewerID, candidateIDs,
	).Iter()

	var ps PairSignals
	for iter.Scan(
		&ps.CandidateID,
		&ps.MutualFriendCount, &ps.MutualStrongCount, &ps.CommonGroupCount,
		&ps.SameCity, &ps.SameSchool, &ps.SameCompany, &ps.MutualFollow,
		&ps.ProfileView7d, &ps.ReverseView7d, &ps.ChatInteraction30d,
		&ps.ContentCoengagement7d, &ps.TriadicClosureScore, &ps.InterestSimilarity,
		&ps.LastUpdatedAt,
	) {
		copy := ps
		copy.ViewerID = viewerID
		result[copy.CandidateID] = &copy
	}

	return result, iter.Close()
}

// DeletePairSignals removes pair signals for a viewer-candidate pair.
func (s *ScyllaStore) DeletePairSignals(_ context.Context, viewerID, candidateID uuid.UUID) error {
	return s.session.Query(`
		DELETE FROM user_pair_signals WHERE viewer_id = ? AND candidate_id = ?`,
		viewerID, candidateID,
	).Exec()
}
