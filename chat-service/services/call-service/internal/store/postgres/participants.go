package postgres

import (
	"context"
	"errors"
	"time"

	"github.com/atpost/chat-call-service/internal/domain"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func (s *CallStore) AddParticipant(ctx context.Context, p *domain.CallParticipant) error {
	_, err := s.db.Exec(ctx, `
		INSERT INTO calls.call_participants
			(id, call_session_id, user_id, role, invite_state, join_state,
			 audio_muted, video_muted, created_at, updated_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$9)`,
		p.ID, p.CallSessionID, p.UserID, p.Role, p.InviteState, p.JoinState,
		p.AudioMuted, p.VideoMuted, p.CreatedAt,
	)
	return err
}

func (s *CallStore) GetParticipant(ctx context.Context, callID, userID uuid.UUID) (*domain.CallParticipant, error) {
	row := s.db.QueryRow(ctx, `
		SELECT id, call_session_id, user_id, role, invite_state, join_state,
		       audio_muted, video_muted, hand_raised, is_screen_sharing,
		       joined_at, left_at, last_quality_score, duration_seconds, created_at, updated_at
		FROM calls.call_participants
		WHERE call_session_id = $1 AND user_id = $2`, callID, userID)

	var p domain.CallParticipant
	err := row.Scan(
		&p.ID, &p.CallSessionID, &p.UserID, &p.Role, &p.InviteState, &p.JoinState,
		&p.AudioMuted, &p.VideoMuted, &p.HandRaised, &p.IsScreenSharing,
		&p.JoinedAt, &p.LeftAt, &p.LastQualityScore, &p.DurationSeconds,
		&p.CreatedAt, &p.UpdatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &p, nil
}

func (s *CallStore) GetParticipants(ctx context.Context, callID uuid.UUID) ([]domain.CallParticipant, error) {
	rows, err := s.db.Query(ctx, `
		SELECT id, call_session_id, user_id, role, invite_state, join_state,
		       audio_muted, video_muted, hand_raised, is_screen_sharing,
		       joined_at, left_at, last_quality_score, duration_seconds, created_at, updated_at
		FROM calls.call_participants
		WHERE call_session_id = $1
		ORDER BY created_at ASC`, callID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var participants []domain.CallParticipant
	for rows.Next() {
		var p domain.CallParticipant
		if err := rows.Scan(
			&p.ID, &p.CallSessionID, &p.UserID, &p.Role, &p.InviteState, &p.JoinState,
			&p.AudioMuted, &p.VideoMuted, &p.HandRaised, &p.IsScreenSharing,
			&p.JoinedAt, &p.LeftAt, &p.LastQualityScore, &p.DurationSeconds,
			&p.CreatedAt, &p.UpdatedAt,
		); err != nil {
			return nil, err
		}
		participants = append(participants, p)
	}
	return participants, nil
}

func (s *CallStore) UpdateParticipantJoinState(ctx context.Context, callID, userID uuid.UUID, joinState string) error {
	now := time.Now()
	var joinedAt, leftAt *time.Time

	switch joinState {
	case domain.JoinStateJoined:
		joinedAt = &now
	case domain.JoinStateLeft, domain.JoinStateRemoved:
		leftAt = &now
	}

	_, err := s.db.Exec(ctx, `
		UPDATE calls.call_participants
		SET join_state = $3, joined_at = COALESCE($4, joined_at),
		    left_at = COALESCE($5, left_at), updated_at = $6
		WHERE call_session_id = $1 AND user_id = $2`,
		callID, userID, joinState, joinedAt, leftAt, now,
	)
	return err
}

func (s *CallStore) UpdateParticipantInviteState(ctx context.Context, callID, userID uuid.UUID, inviteState string) error {
	_, err := s.db.Exec(ctx, `
		UPDATE calls.call_participants
		SET invite_state = $3, updated_at = $4
		WHERE call_session_id = $1 AND user_id = $2`,
		callID, userID, inviteState, time.Now(),
	)
	return err
}

func (s *CallStore) UpdateMediaState(ctx context.Context, callID, userID uuid.UUID, audioMuted, videoMuted bool) error {
	_, err := s.db.Exec(ctx, `
		UPDATE calls.call_participants
		SET audio_muted = $3, video_muted = $4, updated_at = $5
		WHERE call_session_id = $1 AND user_id = $2`,
		callID, userID, audioMuted, videoMuted, time.Now(),
	)
	return err
}

func (s *CallStore) CountActiveParticipants(ctx context.Context, callID uuid.UUID) (int, error) {
	var count int
	err := s.db.QueryRow(ctx, `
		SELECT COUNT(*) FROM calls.call_participants
		WHERE call_session_id = $1 AND join_state = 'joined'`, callID).Scan(&count)
	return count, err
}

func (s *CallStore) SetParticipantDuration(ctx context.Context, callID, userID uuid.UUID, durationSeconds int) error {
	_, err := s.db.Exec(ctx, `
		UPDATE calls.call_participants
		SET duration_seconds = $3, updated_at = $4
		WHERE call_session_id = $1 AND user_id = $2`,
		callID, userID, durationSeconds, time.Now(),
	)
	return err
}

// MarkAllParticipantsLeft sets all joined participants to left state.
func (s *CallStore) MarkAllParticipantsLeft(ctx context.Context, callID uuid.UUID) error {
	now := time.Now()
	_, err := s.db.Exec(ctx, `
		UPDATE calls.call_participants
		SET join_state = 'left', left_at = $2, updated_at = $2
		WHERE call_session_id = $1 AND join_state IN ('joined', 'joining', 'reconnecting')`,
		callID, now,
	)
	return err
}
