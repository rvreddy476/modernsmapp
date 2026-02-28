package store

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// UserReputation stores a user's trust score and endorsement count.
type UserReputation struct {
	UserID              uuid.UUID       `json:"user_id"`
	TrustScore          float64         `json:"trust_score"`          // 0.00 – 1.00
	EndorsementCount    int             `json:"endorsement_count"`
	CrossPlatformProofs json.RawMessage `json:"cross_platform_proofs,omitempty"`
}

// Endorsement represents one user endorsing another's skill.
type Endorsement struct {
	ID         uuid.UUID `json:"id"`
	FromUserID uuid.UUID `json:"from_user_id"`
	ToUserID   uuid.UUID `json:"to_user_id"`
	SkillTag   string    `json:"skill_tag"`
	Message    string    `json:"message,omitempty"`
	CreatedAt  time.Time `json:"created_at"`
}

// GetReputation returns the reputation record for a user.
func (s *Store) GetReputation(ctx context.Context, userID uuid.UUID) (*UserReputation, error) {
	var r UserReputation
	err := s.db.QueryRow(ctx, `
		SELECT user_id, trust_score, endorsement_count, cross_platform_proofs
		FROM user_reputation WHERE user_id = $1
	`, userID).Scan(&r.UserID, &r.TrustScore, &r.EndorsementCount, &r.CrossPlatformProofs)
	if err != nil {
		// Return default reputation if none exists
		return &UserReputation{
			UserID:              userID,
			TrustScore:          0.50,
			EndorsementCount:    0,
			CrossPlatformProofs: json.RawMessage("{}"),
		}, nil
	}
	return &r, nil
}

// UpsertReputation creates or updates a user's reputation.
func (s *Store) UpsertReputation(ctx context.Context, r *UserReputation) error {
	_, err := s.db.Exec(ctx, `
		INSERT INTO user_reputation (user_id, trust_score, endorsement_count, cross_platform_proofs)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (user_id) DO UPDATE SET
			trust_score = $2, endorsement_count = $3, cross_platform_proofs = $4
	`, r.UserID, r.TrustScore, r.EndorsementCount, r.CrossPlatformProofs)
	return err
}

// CreateEndorsement adds an endorsement and increments the reputation counter.
func (s *Store) CreateEndorsement(ctx context.Context, e *Endorsement) error {
	e.ID = uuid.New()
	e.CreatedAt = time.Now()

	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	_, err = tx.Exec(ctx, `
		INSERT INTO endorsements (id, from_user_id, to_user_id, skill_tag, message, created_at)
		VALUES ($1, $2, $3, $4, $5, $6)
	`, e.ID, e.FromUserID, e.ToUserID, e.SkillTag, e.Message, e.CreatedAt)
	if err != nil {
		return err
	}

	// Upsert reputation and increment endorsement count
	_, err = tx.Exec(ctx, `
		INSERT INTO user_reputation (user_id, trust_score, endorsement_count, cross_platform_proofs)
		VALUES ($1, 0.50, 1, '{}')
		ON CONFLICT (user_id) DO UPDATE SET
			endorsement_count = user_reputation.endorsement_count + 1
	`, e.ToUserID)
	if err != nil {
		return err
	}

	return tx.Commit(ctx)
}

// DeleteEndorsement removes an endorsement.
func (s *Store) DeleteEndorsement(ctx context.Context, fromUserID, toUserID uuid.UUID, skillTag string) error {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	tag, err := tx.Exec(ctx, `
		DELETE FROM endorsements WHERE from_user_id = $1 AND to_user_id = $2 AND skill_tag = $3
	`, fromUserID, toUserID, skillTag)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("ENDORSEMENT_NOT_FOUND")
	}

	_, err = tx.Exec(ctx, `
		UPDATE user_reputation SET endorsement_count = GREATEST(endorsement_count - 1, 0)
		WHERE user_id = $1
	`, toUserID)
	if err != nil {
		return err
	}

	return tx.Commit(ctx)
}

// GetEndorsements returns all endorsements received by a user.
func (s *Store) GetEndorsements(ctx context.Context, userID uuid.UUID) ([]Endorsement, error) {
	rows, err := s.db.Query(ctx, `
		SELECT id, from_user_id, to_user_id, skill_tag, message, created_at
		FROM endorsements WHERE to_user_id = $1
		ORDER BY created_at DESC
	`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var endorsements []Endorsement
	for rows.Next() {
		var e Endorsement
		if err := rows.Scan(&e.ID, &e.FromUserID, &e.ToUserID, &e.SkillTag, &e.Message, &e.CreatedAt); err != nil {
			return nil, err
		}
		endorsements = append(endorsements, e)
	}
	return endorsements, rows.Err()
}

// GetEndorsementsBySkill groups endorsements by skill tag with counts.
type SkillEndorsementSummary struct {
	SkillTag string `json:"skill_tag"`
	Count    int    `json:"count"`
}

func (s *Store) GetEndorsementSummary(ctx context.Context, userID uuid.UUID) ([]SkillEndorsementSummary, error) {
	rows, err := s.db.Query(ctx, `
		SELECT skill_tag, COUNT(*) as cnt
		FROM endorsements WHERE to_user_id = $1
		GROUP BY skill_tag ORDER BY cnt DESC
	`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var summaries []SkillEndorsementSummary
	for rows.Next() {
		var s SkillEndorsementSummary
		if err := rows.Scan(&s.SkillTag, &s.Count); err != nil {
			return nil, err
		}
		summaries = append(summaries, s)
	}
	return summaries, rows.Err()
}

// UpdateStatus updates a user's status/mood.
func (s *Store) UpdateStatus(ctx context.Context, userID uuid.UUID, statusText, statusEmoji string, expiresAt *time.Time) error {
	_, err := s.db.Exec(ctx, `
		UPDATE users SET status_text = $2, status_emoji = $3, status_expires_at = $4, updated_at = NOW()
		WHERE id = $1
	`, userID, statusText, statusEmoji, expiresAt)
	return err
}

// ClearExpiredStatuses clears statuses that have expired.
func (s *Store) ClearExpiredStatuses(ctx context.Context) (int64, error) {
	tag, err := s.db.Exec(ctx, `
		UPDATE users SET status_text = '', status_emoji = '', status_expires_at = NULL, updated_at = NOW()
		WHERE status_expires_at IS NOT NULL AND status_expires_at < NOW()
	`)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}

// TrackLinkClick increments click count for a user link.
func (s *Store) TrackLinkClick(ctx context.Context, userID uuid.UUID, platform string) error {
	_, err := s.db.Exec(ctx, `
		UPDATE user_links SET click_count = click_count + 1
		WHERE user_id = $1 AND platform = $2
	`, userID, platform)
	return err
}

// GetLinkAnalytics returns click counts for all of a user's links.
type LinkAnalytics struct {
	Platform   string `json:"platform"`
	URL        string `json:"url"`
	ClickCount int    `json:"click_count"`
}

func (s *Store) GetLinkAnalytics(ctx context.Context, userID uuid.UUID) ([]LinkAnalytics, error) {
	rows, err := s.db.Query(ctx, `
		SELECT platform, url, COALESCE(click_count, 0)
		FROM user_links WHERE user_id = $1 ORDER BY sort_order
	`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var analytics []LinkAnalytics
	for rows.Next() {
		var a LinkAnalytics
		if err := rows.Scan(&a.Platform, &a.URL, &a.ClickCount); err != nil {
			return nil, err
		}
		analytics = append(analytics, a)
	}
	return analytics, rows.Err()
}
