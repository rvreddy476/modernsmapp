package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// LiveStream mirrors the live_streams row. Pointers cover NULLable cols.
type LiveStream struct {
	ID                       uuid.UUID  `json:"id"`
	CreatorUserID            uuid.UUID  `json:"creator_user_id"`
	LiveKitRoom              string     `json:"livekit_room"`
	Title                    string     `json:"title"`
	Description              string     `json:"description"`
	CoverMediaID             *uuid.UUID `json:"cover_media_id,omitempty"`
	Status                   string     `json:"status"`
	Visibility               string     `json:"visibility"`
	ScheduledAt              *time.Time `json:"scheduled_at,omitempty"`
	StartedAt                *time.Time `json:"started_at,omitempty"`
	EndedAt                  *time.Time `json:"ended_at,omitempty"`
	ViewerPeak               int        `json:"viewer_peak"`
	RecordingURL             *string    `json:"recording_url,omitempty"`
	RecordingDurationSeconds *int       `json:"recording_duration_seconds,omitempty"`
	EgressID                 *string    `json:"-"`
	CreatedAt                time.Time  `json:"created_at"`
	UpdatedAt                time.Time  `json:"updated_at"`
}

var ErrNotFound = errors.New("live stream not found")

type Store struct {
	db *pgxpool.Pool
}

func New(db *pgxpool.Pool) *Store { return &Store{db: db} }

func scanStream(row pgx.Row) (*LiveStream, error) {
	var s LiveStream
	err := row.Scan(
		&s.ID,
		&s.CreatorUserID,
		&s.LiveKitRoom,
		&s.Title,
		&s.Description,
		&s.CoverMediaID,
		&s.Status,
		&s.Visibility,
		&s.ScheduledAt,
		&s.StartedAt,
		&s.EndedAt,
		&s.ViewerPeak,
		&s.RecordingURL,
		&s.RecordingDurationSeconds,
		&s.EgressID,
		&s.CreatedAt,
		&s.UpdatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &s, nil
}

const selectColumns = `
    id, creator_user_id, livekit_room, title, description, cover_media_id,
    status, visibility, scheduled_at, started_at, ended_at,
    viewer_peak, recording_url, recording_duration_seconds, egress_id,
    created_at, updated_at`

type CreateStreamParams struct {
	CreatorUserID uuid.UUID
	LiveKitRoom   string
	Title         string
	Description   string
	CoverMediaID  *uuid.UUID
	Visibility    string
	ScheduledAt   *time.Time
}

func (s *Store) CreateStream(ctx context.Context, p CreateStreamParams) (*LiveStream, error) {
	const q = `
        INSERT INTO live_streams
            (creator_user_id, livekit_room, title, description, cover_media_id,
             visibility, scheduled_at, status)
        VALUES ($1, $2, $3, $4, $5, $6, $7, 'scheduled')
        RETURNING ` + selectColumns
	return scanStream(s.db.QueryRow(ctx, q,
		p.CreatorUserID,
		p.LiveKitRoom,
		p.Title,
		p.Description,
		p.CoverMediaID,
		p.Visibility,
		p.ScheduledAt,
	))
}

func (s *Store) GetByID(ctx context.Context, id uuid.UUID) (*LiveStream, error) {
	const q = `SELECT ` + selectColumns + ` FROM live_streams WHERE id = $1`
	return scanStream(s.db.QueryRow(ctx, q, id))
}

// MarkLive flips status to 'live' and stamps started_at. Idempotent: if
// the row is already live, started_at is preserved.
func (s *Store) MarkLive(ctx context.Context, id uuid.UUID, egressID string) (*LiveStream, error) {
	const q = `
        UPDATE live_streams
        SET status = 'live',
            started_at = COALESCE(started_at, NOW()),
            egress_id = COALESCE(NULLIF($2, ''), egress_id),
            updated_at = NOW()
        WHERE id = $1
          AND status IN ('scheduled', 'live')
        RETURNING ` + selectColumns
	row := s.db.QueryRow(ctx, q, id, egressID)
	st, err := scanStream(row)
	if errors.Is(err, ErrNotFound) {
		return nil, fmt.Errorf("stream is not in a startable state")
	}
	return st, err
}

// MarkEnded flips status to 'ended' and stamps ended_at. peakViewers
// is materialised from the Redis hot counter by the caller.
func (s *Store) MarkEnded(ctx context.Context, id uuid.UUID, peakViewers int) (*LiveStream, error) {
	const q = `
        UPDATE live_streams
        SET status = 'ended',
            ended_at = COALESCE(ended_at, NOW()),
            viewer_peak = GREATEST(viewer_peak, $2),
            updated_at = NOW()
        WHERE id = $1
          AND status = 'live'
        RETURNING ` + selectColumns
	row := s.db.QueryRow(ctx, q, id, peakViewers)
	st, err := scanStream(row)
	if errors.Is(err, ErrNotFound) {
		return nil, fmt.Errorf("stream is not currently live")
	}
	return st, err
}

// SetRecording is called from the Egress webhook once the file lands.
func (s *Store) SetRecording(ctx context.Context, id uuid.UUID, url string, durationSec int) (*LiveStream, error) {
	const q = `
        UPDATE live_streams
        SET recording_url = $2,
            recording_duration_seconds = $3,
            updated_at = NOW()
        WHERE id = $1
        RETURNING ` + selectColumns
	return scanStream(s.db.QueryRow(ctx, q, id, url, durationSec))
}

type ListLiveParams struct {
	Limit         int
	StartedBefore *time.Time
	IDBefore      *uuid.UUID
}

// ListLive returns streams currently in status='live', ordered by
// started_at DESC. Cursor pagination uses (started_at, id) as the
// keyset; if either StartedBefore/IDBefore is nil we return the head.
func (s *Store) ListLive(ctx context.Context, p ListLiveParams) ([]*LiveStream, error) {
	limit := p.Limit
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	var (
		rows pgx.Rows
		err  error
	)
	if p.StartedBefore != nil && p.IDBefore != nil {
		const q = `
            SELECT ` + selectColumns + `
            FROM live_streams
            WHERE status = 'live'
              AND (started_at, id) < ($1, $2)
            ORDER BY started_at DESC, id DESC
            LIMIT $3`
		rows, err = s.db.Query(ctx, q, *p.StartedBefore, *p.IDBefore, limit)
	} else {
		const q = `
            SELECT ` + selectColumns + `
            FROM live_streams
            WHERE status = 'live'
            ORDER BY started_at DESC NULLS LAST, id DESC
            LIMIT $1`
		rows, err = s.db.Query(ctx, q, limit)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]*LiveStream, 0, limit)
	for rows.Next() {
		st, err := scanStream(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, st)
	}
	return out, rows.Err()
}

// RecordViewerEvent inserts a join/leave row for analytics. Best-effort:
// callers log errors but do not fail user-visible operations.
func (s *Store) RecordViewerEvent(ctx context.Context, streamID, userID uuid.UUID, eventType string) error {
	const q = `
        INSERT INTO live_viewer_events (stream_id, user_id, event_type)
        VALUES ($1, $2, $3)`
	_, err := s.db.Exec(ctx, q, streamID, userID, eventType)
	return err
}

// ChatMessage mirrors live_chat_messages. The text field is bounded
// 1-500 chars by the schema CHECK so service-layer validation is
// belt-and-braces.
type ChatMessage struct {
	ID        uuid.UUID `json:"id"`
	StreamID  uuid.UUID `json:"stream_id"`
	UserID    uuid.UUID `json:"user_id"`
	Text      string    `json:"text"`
	CreatedAt time.Time `json:"created_at"`
}

// InsertChatMessage persists a message + returns the generated id +
// timestamp the caller broadcasts via Redis pub/sub.
func (s *Store) InsertChatMessage(ctx context.Context, streamID, userID uuid.UUID, text string) (*ChatMessage, error) {
	out := &ChatMessage{StreamID: streamID, UserID: userID, Text: text}
	const q = `
        INSERT INTO live_chat_messages (stream_id, user_id, text)
        VALUES ($1, $2, $3)
        RETURNING id, created_at`
	if err := s.db.QueryRow(ctx, q, streamID, userID, text).Scan(&out.ID, &out.CreatedAt); err != nil {
		return nil, fmt.Errorf("insert chat: %w", err)
	}
	return out, nil
}

// ListRecentChatMessages returns the last `limit` messages for a
// stream, newest first. Used by viewers landing mid-stream to replay
// the conversation buffer; live messages thereafter arrive via the
// Redis pub/sub channel.
func (s *Store) ListRecentChatMessages(ctx context.Context, streamID uuid.UUID, limit int) ([]*ChatMessage, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	const q = `
        SELECT id, stream_id, user_id, text, created_at
        FROM live_chat_messages
        WHERE stream_id = $1
        ORDER BY created_at DESC
        LIMIT $2`
	rows, err := s.db.Query(ctx, q, streamID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]*ChatMessage, 0, limit)
	for rows.Next() {
		m := &ChatMessage{}
		if err := rows.Scan(&m.ID, &m.StreamID, &m.UserID, &m.Text, &m.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}
