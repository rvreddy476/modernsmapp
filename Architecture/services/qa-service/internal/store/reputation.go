package store

import (
	"context"
	"fmt"

	"github.com/google/uuid"
)

// Reputation points. service/reputation.go re-exports them.
const (
	RepQuestionAsked           = 2
	RepAnswerPosted            = 5
	RepAnswerUpvoted           = 10
	RepAnswerDownvoted         = -2
	RepQuestionUpvoted         = 5
	RepQuestionDownvoted       = -1
	RepBestAnswerSelected      = 25
	RepBestAnswerSelectorBonus = 2
)

// RepEvent is one reputation_events row a change writes for the target's
// author.
type RepEvent struct {
	EventType string
	Points    int
}

// VoteReputation is what one vote transition pays the target's author
// (audit H1): nothing when the vote does not change, the reversal of the old
// vote's award when it is removed or flipped, and the new vote's award when
// one is cast. A flip from up to down on an answer is -10 then -2. Comment
// votes carry no reputation.
func VoteReputation(target, oldVote, newVote string) []RepEvent {
	if oldVote == newVote {
		return nil
	}
	var up, down int
	switch target {
	case "question":
		up, down = RepQuestionUpvoted, RepQuestionDownvoted
	case "answer":
		up, down = RepAnswerUpvoted, RepAnswerDownvoted
	default:
		return nil
	}
	var out []RepEvent
	switch oldVote {
	case "up":
		out = append(out, RepEvent{target + "_upvote_removed", -up})
	case "down":
		out = append(out, RepEvent{target + "_downvote_removed", -down})
	}
	switch newVote {
	case "up":
		out = append(out, RepEvent{target + "_upvoted", up})
	case "down":
		out = append(out, RepEvent{target + "_downvoted", down})
	}
	return out
}

func (s *Store) AddReputationEvent(ctx context.Context, userID uuid.UUID, eventType string, points int, sourceType string, sourceID *uuid.UUID) error {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if err := addReputationTx(ctx, tx, userID, eventType, points, sourceType, sourceID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// addReputationTx writes one reputation event and moves the user's score by
// its points, inside the caller's transaction, so a change and what it pays
// commit or roll back together.
func addReputationTx(ctx context.Context, exec dbtx, userID uuid.UUID, eventType string, points int, sourceType string, sourceID *uuid.UUID) error {
	if _, err := exec.Exec(ctx, `
		INSERT INTO reputation_events (user_id, event_type, points, source_type, source_id)
		VALUES ($1, $2, $3, $4, $5)`, userID, eventType, points, sourceType, sourceID); err != nil {
		return fmt.Errorf("insert reputation event: %w", err)
	}
	if _, err := exec.Exec(ctx, `
		INSERT INTO qa_profiles (user_id, reputation_score) VALUES ($1, $2)
		ON CONFLICT (user_id) DO UPDATE SET reputation_score = qa_profiles.reputation_score + $2, updated_at = now()`,
		userID, points); err != nil {
		return fmt.Errorf("update reputation score: %w", err)
	}
	return nil
}

// GetReputationHistory lists a user's reputation events. The owner sees them
// all. Anyone else never sees an event whose source is an anonymous question
// or answer (audit H4): its source id, or even its time, would tie the
// anonymous post to the user.
func (s *Store) GetReputationHistory(ctx context.Context, userID uuid.UUID, viewerID *uuid.UUID, limit, offset int) ([]ReputationEvent, error) {
	if limit <= 0 {
		limit = 50
	}
	owner := viewerID != nil && *viewerID == userID
	rows, err := s.db.Query(ctx, `
		SELECT e.id, e.user_id, e.event_type, e.points, e.source_type, e.source_id, e.created_at
		FROM reputation_events e
		WHERE e.user_id = $1
		  AND ($4 OR NOT (
			(e.source_type = 'question' AND EXISTS (SELECT 1 FROM questions q WHERE q.id = e.source_id AND q.is_anonymous))
			OR (e.source_type = 'answer' AND EXISTS (SELECT 1 FROM answers a WHERE a.id = e.source_id AND a.is_anonymous))))
		ORDER BY e.created_at DESC LIMIT $2 OFFSET $3`, userID, limit, offset, owner)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var results []ReputationEvent
	for rows.Next() {
		var e ReputationEvent
		if err := rows.Scan(&e.ID, &e.UserID, &e.EventType, &e.Points, &e.SourceType, &e.SourceID, &e.CreatedAt); err != nil {
			return nil, err
		}
		results = append(results, e)
	}
	return results, rows.Err()
}

func (s *Store) GetReputationScore(ctx context.Context, userID uuid.UUID) (int, error) {
	var score int
	err := s.db.QueryRow(ctx, `SELECT COALESCE(reputation_score, 0) FROM qa_profiles WHERE user_id = $1`, userID).Scan(&score)
	return score, err
}

func (s *Store) AwardBadge(ctx context.Context, userID uuid.UUID, badgeType, badgeName string) error {
	_, err := s.db.Exec(ctx, `INSERT INTO contributor_badges (user_id, badge_type, badge_name) VALUES ($1, $2, $3)`,
		userID, badgeType, badgeName)
	return err
}

func (s *Store) GetBadges(ctx context.Context, userID uuid.UUID) ([]ContributorBadge, error) {
	rows, err := s.db.Query(ctx, `
		SELECT id, user_id, badge_type, badge_name, awarded_at
		FROM contributor_badges WHERE user_id = $1 ORDER BY awarded_at DESC`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var results []ContributorBadge
	for rows.Next() {
		var b ContributorBadge
		if err := rows.Scan(&b.ID, &b.UserID, &b.BadgeType, &b.BadgeName, &b.AwardedAt); err != nil {
			return nil, err
		}
		results = append(results, b)
	}
	return results, nil
}

func (s *Store) GetLeaderboard(ctx context.Context, topicID *uuid.UUID, limit int) ([]QAProfile, error) {
	if limit <= 0 {
		limit = 20
	}
	if topicID != nil {
		rows, err := s.db.Query(ctx, `
			SELECT DISTINCT p.user_id, p.display_name, p.bio, p.expertise_areas, p.reputation_score,
			       p.question_count, p.answer_count, p.best_answer_count, p.is_verified, p.created_at, p.updated_at
			FROM qa_profiles p
			JOIN answers a ON a.author_id = p.user_id
			JOIN question_topics qt ON qt.question_id = a.question_id
			WHERE qt.topic_id = $1
			ORDER BY p.reputation_score DESC LIMIT $2`, *topicID, limit)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		return scanProfiles(rows)
	}
	rows, err := s.db.Query(ctx, `
		SELECT user_id, display_name, bio, expertise_areas, reputation_score,
		       question_count, answer_count, best_answer_count, is_verified, created_at, updated_at
		FROM qa_profiles ORDER BY reputation_score DESC LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanProfiles(rows)
}
