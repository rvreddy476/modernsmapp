package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/atpost/chat-call-service/internal/domain"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// CallReminder represents a scheduled reminder for an upcoming call.
type CallReminder struct {
	ID            uuid.UUID `json:"id"`
	CallSessionID uuid.UUID `json:"call_session_id"`
	UserID        uuid.UUID `json:"user_id"`
	RemindAt      time.Time `json:"remind_at"`
	Sent          bool      `json:"sent"`
	CreatedAt     time.Time `json:"created_at"`
}

// CallSummary represents an AI-generated post-call summary.
type CallSummary struct {
	ID            uuid.UUID       `json:"id"`
	CallSessionID uuid.UUID       `json:"call_session_id"`
	TranscriptURL *string         `json:"transcript_url,omitempty"`
	SummaryText   *string         `json:"summary_text,omitempty"`
	KeyPoints     json.RawMessage `json:"key_points,omitempty"`
	ActionItems   json.RawMessage `json:"action_items,omitempty"`
	Participants  []uuid.UUID     `json:"participants,omitempty"`
	DurationMs    *int            `json:"duration_ms,omitempty"`
	Language      string          `json:"language"`
	GeneratedAt   time.Time       `json:"generated_at"`
}

// ---------------------------------------------------------------------------
// Call Links
// ---------------------------------------------------------------------------

// CreateCallLink generates a shareable token and sets it on the call session.
// Returns the generated token.
func (s *CallStore) CreateCallLink(ctx context.Context, callSessionID uuid.UUID, expiresAt time.Time, lobbyEnabled bool) (string, error) {
	token := uuid.New().String()[:8]
	var result string
	err := s.db.QueryRow(ctx, `
		UPDATE calls.call_sessions
		SET link_token = $1, link_expires_at = $2, lobby_enabled = $3
		WHERE id = $4
		RETURNING link_token`,
		token, expiresAt, lobbyEnabled, callSessionID,
	).Scan(&result)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil
	}
	return result, err
}

// GetCallSessionByLinkToken looks up a session by its shareable link token,
// returning nil if not found or if the token has expired.
func (s *CallStore) GetCallSessionByLinkToken(ctx context.Context, token string) (*domain.CallSession, error) {
	row := s.db.QueryRow(ctx, `
		SELECT id, call_type, source_type, source_id, initiator_user_id, room_id, state,
		       region_code, audio_only, recording_enabled, max_participants, join_mode,
		       started_at, answered_at, ended_at, ended_reason, metadata_json, created_at, updated_at
		FROM calls.call_sessions
		WHERE link_token = $1
		  AND (link_expires_at IS NULL OR link_expires_at > NOW())`, token)

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

// ---------------------------------------------------------------------------
// Scheduled Calls
// ---------------------------------------------------------------------------

// SetCallScheduledAt persists the scheduled_at timestamp on a call session.
func (s *CallStore) SetCallScheduledAt(ctx context.Context, callSessionID uuid.UUID, scheduledAt time.Time) error {
	_, err := s.db.Exec(ctx, `
		UPDATE calls.call_sessions SET scheduled_at = $1 WHERE id = $2`,
		scheduledAt, callSessionID,
	)
	return err
}

// ListScheduledCalls returns upcoming scheduled calls (after `after`) for the
// given user, ordered by scheduled_at ascending.
func (s *CallStore) ListScheduledCalls(ctx context.Context, userID uuid.UUID, after time.Time, limit int) ([]domain.CallSession, error) {
	if limit <= 0 || limit > 50 {
		limit = 20
	}
	rows, err := s.db.Query(ctx, `
		SELECT cs.id, cs.call_type, cs.source_type, cs.source_id, cs.initiator_user_id,
		       cs.room_id, cs.state, cs.region_code, cs.audio_only, cs.recording_enabled,
		       cs.max_participants, cs.join_mode, cs.started_at, cs.answered_at, cs.ended_at,
		       cs.ended_reason, cs.metadata_json, cs.created_at, cs.updated_at
		FROM calls.call_sessions cs
		JOIN calls.call_participants cp ON cp.call_session_id = cs.id
		WHERE cp.user_id = $1 AND cs.scheduled_at > $2
		ORDER BY cs.scheduled_at ASC
		LIMIT $3`, userID, after, limit)
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

// ---------------------------------------------------------------------------
// Reminders
// ---------------------------------------------------------------------------

// CreateCallReminder upserts a reminder for (callSessionID, userID).
func (s *CallStore) CreateCallReminder(ctx context.Context, r *CallReminder) (*CallReminder, error) {
	if r.ID == uuid.Nil {
		r.ID = uuid.New()
	}
	now := time.Now()
	r.CreatedAt = now

	row := s.db.QueryRow(ctx, `
		INSERT INTO calls.call_reminders (id, call_session_id, user_id, remind_at, sent, created_at)
		VALUES ($1, $2, $3, $4, FALSE, $5)
		ON CONFLICT (call_session_id, user_id)
		DO UPDATE SET remind_at = $4, sent = FALSE
		RETURNING id, call_session_id, user_id, remind_at, sent, created_at`,
		r.ID, r.CallSessionID, r.UserID, r.RemindAt, r.CreatedAt,
	)

	var out CallReminder
	if err := row.Scan(&out.ID, &out.CallSessionID, &out.UserID, &out.RemindAt, &out.Sent, &out.CreatedAt); err != nil {
		return nil, err
	}
	return &out, nil
}

// GetDueReminders returns unsent reminders whose remind_at <= before.
func (s *CallStore) GetDueReminders(ctx context.Context, before time.Time, limit int) ([]CallReminder, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := s.db.Query(ctx, `
		SELECT id, call_session_id, user_id, remind_at, sent, created_at
		FROM calls.call_reminders
		WHERE sent = FALSE AND remind_at <= $1
		LIMIT $2`, before, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var reminders []CallReminder
	for rows.Next() {
		var r CallReminder
		if err := rows.Scan(&r.ID, &r.CallSessionID, &r.UserID, &r.RemindAt, &r.Sent, &r.CreatedAt); err != nil {
			return nil, err
		}
		reminders = append(reminders, r)
	}
	return reminders, nil
}

// MarkReminderSent marks a single reminder as sent.
func (s *CallStore) MarkReminderSent(ctx context.Context, id uuid.UUID) error {
	_, err := s.db.Exec(ctx, `UPDATE calls.call_reminders SET sent = TRUE WHERE id = $1`, id)
	return err
}

// DeleteCallReminder removes a reminder for the given (callSessionID, userID) pair.
func (s *CallStore) DeleteCallReminder(ctx context.Context, callSessionID, userID uuid.UUID) error {
	_, err := s.db.Exec(ctx, `
		DELETE FROM calls.call_reminders
		WHERE call_session_id = $1 AND user_id = $2`, callSessionID, userID)
	return err
}

// ---------------------------------------------------------------------------
// Summaries
// ---------------------------------------------------------------------------

// UpsertCallSummary inserts or updates an AI-generated call summary.
func (s *CallStore) UpsertCallSummary(ctx context.Context, sum *CallSummary) (*CallSummary, error) {
	if sum.ID == uuid.Nil {
		sum.ID = uuid.New()
	}
	sum.GeneratedAt = time.Now()

	row := s.db.QueryRow(ctx, `
		INSERT INTO calls.call_summaries
			(id, call_session_id, transcript_url, summary_text, key_points, action_items,
			 participants, duration_ms, language, generated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
		ON CONFLICT (call_session_id) DO UPDATE SET
			transcript_url = EXCLUDED.transcript_url,
			summary_text   = EXCLUDED.summary_text,
			key_points     = EXCLUDED.key_points,
			action_items   = EXCLUDED.action_items,
			participants   = EXCLUDED.participants,
			duration_ms    = EXCLUDED.duration_ms,
			language       = EXCLUDED.language,
			generated_at   = EXCLUDED.generated_at
		RETURNING id, call_session_id, transcript_url, summary_text, key_points, action_items,
		          participants, duration_ms, language, generated_at`,
		sum.ID, sum.CallSessionID, sum.TranscriptURL, sum.SummaryText,
		sum.KeyPoints, sum.ActionItems, sum.Participants, sum.DurationMs,
		sum.Language, sum.GeneratedAt,
	)

	var out CallSummary
	if err := row.Scan(
		&out.ID, &out.CallSessionID, &out.TranscriptURL, &out.SummaryText,
		&out.KeyPoints, &out.ActionItems, &out.Participants, &out.DurationMs,
		&out.Language, &out.GeneratedAt,
	); err != nil {
		return nil, err
	}
	return &out, nil
}

// GetCallSummary retrieves the summary for a call session.
func (s *CallStore) GetCallSummary(ctx context.Context, callSessionID uuid.UUID) (*CallSummary, error) {
	row := s.db.QueryRow(ctx, `
		SELECT id, call_session_id, transcript_url, summary_text, key_points, action_items,
		       participants, duration_ms, language, generated_at
		FROM calls.call_summaries
		WHERE call_session_id = $1`, callSessionID)

	var out CallSummary
	if err := row.Scan(
		&out.ID, &out.CallSessionID, &out.TranscriptURL, &out.SummaryText,
		&out.KeyPoints, &out.ActionItems, &out.Participants, &out.DurationMs,
		&out.Language, &out.GeneratedAt,
	); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return &out, nil
}
