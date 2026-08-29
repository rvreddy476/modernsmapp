package postgres

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"sort"
	"time"

	"github.com/atpost/chat-call-service/internal/domain"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type CallStore struct {
	db *pgxpool.Pool
}

// WithCallUsersLock serializes call creation across EVERY user who would
// participate in the new call (CALL-LB-5).
//
// The first fix locked only the initiator, which still let callers A and C
// place recipient B into two concurrent live calls: their two locks never
// contended and neither request examined B. Now the caller passes the
// complete direct-call user set (initiator + targets) and transaction-scoped
// advisory locks are taken for every one of them in deterministic UUID-byte
// order — NEVER request order — so any two creates that share a user always
// contend on that user's key, and A→B racing B→A sorts identically on both
// sides and cannot deadlock. pg_advisory_xact_lock releases on commit AND
// rollback, so the lock can never leak into the pool.
//
// Lock-versus-pool coverage proof: fn's writes go through the POOL, not this
// transaction. That is sound because the only ENABLED writer that can put a
// user into a new live call is createCallLocked, and every path into it
// holds these locks — every OTHER participant-add path (group create,
// ScheduleCall's group funnel, post-create InviteParticipants, open-mode
// JoinCall and its JoinCallByLink funnel) is refused server-side while
// Service.groupCallsEnabled is false, which is P0's fixed posture. The
// winner's pool writes are autocommitted per statement BEFORE its fn
// returns, and this lock releases only after fn returns; the loser acquires
// the contended key strictly afterwards, so its GetActiveCallForUser check
// reads the winner's committed rows. Enabling group calls without extending
// the lock to those paths would break this premise — that is why the flag
// has no config knob.
func (s *CallStore) WithCallUsersLock(ctx context.Context, userIDs []uuid.UUID, fn func() error) error {
	ids := append([]uuid.UUID(nil), userIDs...)
	sort.Slice(ids, func(i, j int) bool { return bytes.Compare(ids[i][:], ids[j][:]) < 0 })
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	for i, id := range ids {
		if i > 0 && id == ids[i-1] {
			continue // duplicate user (self-target): one lock suffices
		}
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, advisoryLockKey(id)); err != nil {
			return err
		}
	}
	if err := fn(); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// advisoryLockKey folds a UUID into the int64 keyspace pg advisory locks use.
func advisoryLockKey(id uuid.UUID) int64 {
	var key int64
	for i := 0; i < 8; i++ {
		key = key<<8 | int64(id[i])
	}
	return key
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
