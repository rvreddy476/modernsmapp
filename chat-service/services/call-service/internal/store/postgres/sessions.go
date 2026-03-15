package postgres

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"time"

	"github.com/atpost/chat-call-service/internal/domain"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type CallStore struct {
	db *pgxpool.Pool
}

func NewCallStore(db *pgxpool.Pool) *CallStore {
	return &CallStore{db: db}
}

func (s *CallStore) CreateCallSession(ctx context.Context, session *domain.CallSession) error {
	_, err := s.db.Exec(ctx, `
		INSERT INTO calls.call_sessions
			(id, call_type, source_type, source_id, initiator_user_id, room_id, state,
			 region_code, audio_only, recording_enabled, max_participants, join_mode,
			 started_at, metadata_json, created_at, updated_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$15)`,
		session.ID, session.CallType, session.SourceType, session.SourceID,
		session.InitiatorUserID, session.RoomID, session.State,
		session.RegionCode, session.AudioOnly, session.RecordingEnabled,
		session.MaxParticipants, session.JoinMode,
		session.StartedAt, session.MetadataJSON, session.CreatedAt,
	)
	return err
}

func (s *CallStore) GetCallSession(ctx context.Context, id uuid.UUID) (*domain.CallSession, error) {
	row := s.db.QueryRow(ctx, `
		SELECT id, call_type, source_type, source_id, initiator_user_id, room_id, state,
		       region_code, audio_only, recording_enabled, max_participants, join_mode,
		       started_at, answered_at, ended_at, ended_reason, metadata_json, created_at, updated_at
		FROM calls.call_sessions WHERE id = $1`, id)

	var cs domain.CallSession
	err := row.Scan(
		&cs.ID, &cs.CallType, &cs.SourceType, &cs.SourceID,
		&cs.InitiatorUserID, &cs.RoomID, &cs.State,
		&cs.RegionCode, &cs.AudioOnly, &cs.RecordingEnabled,
		&cs.MaxParticipants, &cs.JoinMode,
		&cs.StartedAt, &cs.AnsweredAt, &cs.EndedAt, &cs.EndedReason,
		&cs.MetadataJSON, &cs.CreatedAt, &cs.UpdatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &cs, nil
}

func (s *CallStore) UpdateCallState(ctx context.Context, id uuid.UUID, newState string, endedReason *string) error {
	now := time.Now()
	var answeredAt, endedAt *time.Time

	switch newState {
	case domain.CallStateActive:
		answeredAt = &now
	case domain.CallStateEnded, domain.CallStateCanceled, domain.CallStateFailed, domain.CallStateExpired:
		endedAt = &now
	}

	_, err := s.db.Exec(ctx, `
		UPDATE calls.call_sessions
		SET state = $2, ended_reason = $3, answered_at = COALESCE($4, answered_at),
		    ended_at = COALESCE($5, ended_at), updated_at = $6
		WHERE id = $1`,
		id, newState, endedReason, answeredAt, endedAt, now,
	)
	return err
}

// HistoryCursor is used for cursor-based pagination of call history.
type HistoryCursor struct {
	CreatedAt time.Time `json:"c"`
	ID        uuid.UUID `json:"i"`
}

func (s *CallStore) ListCallHistory(ctx context.Context, userID uuid.UUID, limit int, cursor string) ([]domain.CallSession, string, error) {
	var cur *HistoryCursor
	if cursor != "" {
		raw, err := base64.RawURLEncoding.DecodeString(cursor)
		if err == nil {
			var hc HistoryCursor
			if json.Unmarshal(raw, &hc) == nil {
				cur = &hc
			}
		}
	}

	if limit <= 0 || limit > 50 {
		limit = 20
	}

	var rows pgx.Rows
	var err error
	if cur != nil {
		rows, err = s.db.Query(ctx, `
			SELECT cs.id, cs.call_type, cs.source_type, cs.source_id, cs.initiator_user_id,
			       cs.room_id, cs.state, cs.region_code, cs.audio_only, cs.recording_enabled,
			       cs.max_participants, cs.join_mode, cs.started_at, cs.answered_at, cs.ended_at,
			       cs.ended_reason, cs.metadata_json, cs.created_at, cs.updated_at
			FROM calls.call_sessions cs
			JOIN calls.call_participants cp ON cp.call_session_id = cs.id
			WHERE cp.user_id = $1 AND (cs.created_at, cs.id) < ($2, $3)
			ORDER BY cs.created_at DESC, cs.id DESC
			LIMIT $4`, userID, cur.CreatedAt, cur.ID, limit)
	} else {
		rows, err = s.db.Query(ctx, `
			SELECT cs.id, cs.call_type, cs.source_type, cs.source_id, cs.initiator_user_id,
			       cs.room_id, cs.state, cs.region_code, cs.audio_only, cs.recording_enabled,
			       cs.max_participants, cs.join_mode, cs.started_at, cs.answered_at, cs.ended_at,
			       cs.ended_reason, cs.metadata_json, cs.created_at, cs.updated_at
			FROM calls.call_sessions cs
			JOIN calls.call_participants cp ON cp.call_session_id = cs.id
			WHERE cp.user_id = $1
			ORDER BY cs.created_at DESC, cs.id DESC
			LIMIT $2`, userID, limit)
	}
	if err != nil {
		return nil, "", err
	}
	defer rows.Close()

	var sessions []domain.CallSession
	for rows.Next() {
		var cs domain.CallSession
		if err := rows.Scan(
			&cs.ID, &cs.CallType, &cs.SourceType, &cs.SourceID,
			&cs.InitiatorUserID, &cs.RoomID, &cs.State,
			&cs.RegionCode, &cs.AudioOnly, &cs.RecordingEnabled,
			&cs.MaxParticipants, &cs.JoinMode,
			&cs.StartedAt, &cs.AnsweredAt, &cs.EndedAt, &cs.EndedReason,
			&cs.MetadataJSON, &cs.CreatedAt, &cs.UpdatedAt,
		); err != nil {
			return nil, "", err
		}
		sessions = append(sessions, cs)
	}

	var nextCursor string
	if len(sessions) == limit {
		last := sessions[len(sessions)-1]
		raw, _ := json.Marshal(HistoryCursor{CreatedAt: last.CreatedAt, ID: last.ID})
		nextCursor = base64.RawURLEncoding.EncodeToString(raw)
	}

	return sessions, nextCursor, nil
}

// GetActiveCallForUser returns an active or ringing call the user is part of.
func (s *CallStore) GetActiveCallForUser(ctx context.Context, userID uuid.UUID) (*domain.CallSession, error) {
	row := s.db.QueryRow(ctx, `
		SELECT cs.id, cs.call_type, cs.source_type, cs.source_id, cs.initiator_user_id,
		       cs.room_id, cs.state, cs.region_code, cs.audio_only, cs.recording_enabled,
		       cs.max_participants, cs.join_mode, cs.started_at, cs.answered_at, cs.ended_at,
		       cs.ended_reason, cs.metadata_json, cs.created_at, cs.updated_at
		FROM calls.call_sessions cs
		JOIN calls.call_participants cp ON cp.call_session_id = cs.id
		WHERE cp.user_id = $1 AND cs.state IN ('initiated', 'ringing', 'active')
		  AND cp.join_state IN ('not_joined', 'joining', 'joined', 'reconnecting')
		ORDER BY cs.created_at DESC
		LIMIT 1`, userID)

	var cs domain.CallSession
	err := row.Scan(
		&cs.ID, &cs.CallType, &cs.SourceType, &cs.SourceID,
		&cs.InitiatorUserID, &cs.RoomID, &cs.State,
		&cs.RegionCode, &cs.AudioOnly, &cs.RecordingEnabled,
		&cs.MaxParticipants, &cs.JoinMode,
		&cs.StartedAt, &cs.AnsweredAt, &cs.EndedAt, &cs.EndedReason,
		&cs.MetadataJSON, &cs.CreatedAt, &cs.UpdatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &cs, nil
}

// GetRingingCallsOlderThan returns calls in ringing state older than the given time.
func (s *CallStore) GetRingingCallsOlderThan(ctx context.Context, olderThan time.Time) ([]domain.CallSession, error) {
	rows, err := s.db.Query(ctx, `
		SELECT id, call_type, source_type, source_id, initiator_user_id,
		       room_id, state, region_code, audio_only, recording_enabled,
		       max_participants, join_mode, started_at, answered_at, ended_at,
		       ended_reason, metadata_json, created_at, updated_at
		FROM calls.call_sessions
		WHERE state IN ('initiated', 'ringing') AND created_at < $1
		ORDER BY created_at ASC
		LIMIT 100`, olderThan)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var sessions []domain.CallSession
	for rows.Next() {
		var cs domain.CallSession
		if err := rows.Scan(
			&cs.ID, &cs.CallType, &cs.SourceType, &cs.SourceID,
			&cs.InitiatorUserID, &cs.RoomID, &cs.State,
			&cs.RegionCode, &cs.AudioOnly, &cs.RecordingEnabled,
			&cs.MaxParticipants, &cs.JoinMode,
			&cs.StartedAt, &cs.AnsweredAt, &cs.EndedAt, &cs.EndedReason,
			&cs.MetadataJSON, &cs.CreatedAt, &cs.UpdatedAt,
		); err != nil {
			return nil, err
		}
		sessions = append(sessions, cs)
	}
	return sessions, nil
}
