package postgres

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ErrNotFound is returned by store methods when a requested entity does not exist.
var ErrNotFound = errors.New("record not found")

type Stream struct {
	ID           uuid.UUID  `json:"id"`
	HostID       uuid.UUID  `json:"host_id"`
	Title        string     `json:"title"`
	Description  string     `json:"description"`
	ThumbnailURL *string    `json:"thumbnail_url,omitempty"`
	StreamKey    string     `json:"stream_key,omitempty"`
	Status       string     `json:"status"`
	Visibility   string     `json:"visibility"`
	PeakViewers  int        `json:"peak_viewers"`
	TotalViewers int        `json:"total_viewers"`
	LikeCount    int        `json:"like_count"`
	StartedAt    *time.Time `json:"started_at,omitempty"`
	EndedAt      *time.Time `json:"ended_at,omitempty"`
	DurationSecs int        `json:"duration_secs"`
	ReplayURL    *string    `json:"replay_url,omitempty"`
	CreatedAt    time.Time  `json:"created_at"`
	UpdatedAt    time.Time  `json:"updated_at"`
}

type ChatMessage struct {
	ID        uuid.UUID `json:"id"`
	StreamID  uuid.UUID `json:"stream_id"`
	UserID    uuid.UUID `json:"user_id"`
	Message   string    `json:"message"`
	IsPinned  bool      `json:"is_pinned"`
	CreatedAt time.Time `json:"created_at"`
}

type ViewerSession struct {
	ID           uuid.UUID  `json:"id"`
	StreamID     uuid.UUID  `json:"stream_id"`
	UserID       uuid.UUID  `json:"user_id"`
	JoinedAt     time.Time  `json:"joined_at"`
	LeftAt       *time.Time `json:"left_at,omitempty"`
	DurationSecs int        `json:"duration_secs"`
}

type ScheduledStream struct {
	ID           uuid.UUID  `json:"id"`
	HostID       uuid.UUID  `json:"host_id"`
	Title        string     `json:"title"`
	Description  string     `json:"description"`
	ScheduledAt  time.Time  `json:"scheduled_at"`
	ReminderSent bool       `json:"reminder_sent"`
	StreamID     *uuid.UUID `json:"stream_id,omitempty"`
	CreatedAt    time.Time  `json:"created_at"`
}

type Store struct {
	db *pgxpool.Pool
}

func New(db *pgxpool.Pool) *Store {
	return &Store{db: db}
}

// --- Streams ---

func (s *Store) CreateStream(ctx context.Context, st *Stream) error {
	_, err := s.db.Exec(ctx, `
		INSERT INTO live.streams (id, host_id, title, description, thumbnail_url, stream_key, status, visibility, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $9)
	`, st.ID, st.HostID, st.Title, st.Description, st.ThumbnailURL, st.StreamKey, st.Status, st.Visibility, st.CreatedAt)
	return err
}

func (s *Store) GetStream(ctx context.Context, id uuid.UUID) (*Stream, error) {
	var st Stream
	err := s.db.QueryRow(ctx, `
		SELECT id, host_id, title, description, thumbnail_url, stream_key, status, visibility,
		       peak_viewers, total_viewers, like_count, started_at, ended_at, duration_secs, replay_url, created_at, updated_at
		FROM live.streams WHERE id = $1
	`, id).Scan(&st.ID, &st.HostID, &st.Title, &st.Description, &st.ThumbnailURL, &st.StreamKey,
		&st.Status, &st.Visibility, &st.PeakViewers, &st.TotalViewers, &st.LikeCount,
		&st.StartedAt, &st.EndedAt, &st.DurationSecs, &st.ReplayURL, &st.CreatedAt, &st.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return &st, nil
}

func (s *Store) GetStreamByKey(ctx context.Context, streamKey string) (*Stream, error) {
	var st Stream
	err := s.db.QueryRow(ctx, `
		SELECT id, host_id, title, description, thumbnail_url, stream_key, status, visibility,
		       peak_viewers, total_viewers, like_count, started_at, ended_at, duration_secs, replay_url, created_at, updated_at
		FROM live.streams WHERE stream_key = $1
	`, streamKey).Scan(&st.ID, &st.HostID, &st.Title, &st.Description, &st.ThumbnailURL, &st.StreamKey,
		&st.Status, &st.Visibility, &st.PeakViewers, &st.TotalViewers, &st.LikeCount,
		&st.StartedAt, &st.EndedAt, &st.DurationSecs, &st.ReplayURL, &st.CreatedAt, &st.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return &st, nil
}

func (s *Store) ListLiveStreams(ctx context.Context, limit, offset int) ([]Stream, error) {
	rows, err := s.db.Query(ctx, `
		SELECT id, host_id, title, description, thumbnail_url, '', status, visibility,
		       peak_viewers, total_viewers, like_count, started_at, ended_at, duration_secs, replay_url, created_at, updated_at
		FROM live.streams WHERE status = 'live'
		ORDER BY total_viewers DESC, started_at DESC LIMIT $1 OFFSET $2
	`, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanStreams(rows)
}

func (s *Store) ListHostStreams(ctx context.Context, hostID uuid.UUID, limit, offset int) ([]Stream, error) {
	rows, err := s.db.Query(ctx, `
		SELECT id, host_id, title, description, thumbnail_url, '', status, visibility,
		       peak_viewers, total_viewers, like_count, started_at, ended_at, duration_secs, replay_url, created_at, updated_at
		FROM live.streams WHERE host_id = $1
		ORDER BY created_at DESC LIMIT $2 OFFSET $3
	`, hostID, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanStreams(rows)
}

func scanStreams(rows interface {
	Next() bool
	Scan(dest ...interface{}) error
}) ([]Stream, error) {
	var streams []Stream
	for rows.Next() {
		var st Stream
		if err := rows.Scan(&st.ID, &st.HostID, &st.Title, &st.Description, &st.ThumbnailURL, &st.StreamKey,
			&st.Status, &st.Visibility, &st.PeakViewers, &st.TotalViewers, &st.LikeCount,
			&st.StartedAt, &st.EndedAt, &st.DurationSecs, &st.ReplayURL, &st.CreatedAt, &st.UpdatedAt); err != nil {
			return nil, err
		}
		st.StreamKey = "" // Never expose stream key in list responses
		streams = append(streams, st)
	}
	return streams, nil
}

func (s *Store) GoLive(ctx context.Context, id uuid.UUID) error {
	_, err := s.db.Exec(ctx, `
		UPDATE live.streams SET status = 'live', started_at = NOW(), updated_at = NOW() WHERE id = $1
	`, id)
	return err
}

func (s *Store) EndStream(ctx context.Context, id uuid.UUID) error {
	_, err := s.db.Exec(ctx, `
		UPDATE live.streams SET status = 'ended', ended_at = NOW(),
		       duration_secs = EXTRACT(EPOCH FROM (NOW() - started_at))::int,
		       updated_at = NOW()
		WHERE id = $1
	`, id)
	return err
}

func (s *Store) UpdateViewerCount(ctx context.Context, id uuid.UUID, currentViewers int) error {
	_, err := s.db.Exec(ctx, `
		UPDATE live.streams SET
			total_viewers = total_viewers + 1,
			peak_viewers = GREATEST(peak_viewers, $2),
			updated_at = NOW()
		WHERE id = $1
	`, id, currentViewers)
	return err
}

func (s *Store) IncrementLikes(ctx context.Context, id uuid.UUID) error {
	_, err := s.db.Exec(ctx, `UPDATE live.streams SET like_count = like_count + 1 WHERE id = $1`, id)
	return err
}

// --- Chat ---

func (s *Store) SendChatMessage(ctx context.Context, msg *ChatMessage) error {
	_, err := s.db.Exec(ctx, `
		INSERT INTO live.chat_messages (id, stream_id, user_id, message, created_at)
		VALUES ($1, $2, $3, $4, $5)
	`, msg.ID, msg.StreamID, msg.UserID, msg.Message, msg.CreatedAt)
	return err
}

func (s *Store) GetChatMessages(ctx context.Context, streamID uuid.UUID, limit int, before *time.Time) ([]ChatMessage, error) {
	var query string
	var args []interface{}
	if before != nil {
		query = `SELECT id, stream_id, user_id, message, is_pinned, created_at
			FROM live.chat_messages WHERE stream_id = $1 AND created_at < $2
			ORDER BY created_at DESC LIMIT $3`
		args = []interface{}{streamID, *before, limit}
	} else {
		query = `SELECT id, stream_id, user_id, message, is_pinned, created_at
			FROM live.chat_messages WHERE stream_id = $1
			ORDER BY created_at DESC LIMIT $2`
		args = []interface{}{streamID, limit}
	}

	rows, err := s.db.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var messages []ChatMessage
	for rows.Next() {
		var m ChatMessage
		if err := rows.Scan(&m.ID, &m.StreamID, &m.UserID, &m.Message, &m.IsPinned, &m.CreatedAt); err != nil {
			return nil, err
		}
		messages = append(messages, m)
	}
	return messages, nil
}

func (s *Store) PinMessage(ctx context.Context, messageID uuid.UUID) error {
	_, err := s.db.Exec(ctx, `UPDATE live.chat_messages SET is_pinned = TRUE WHERE id = $1`, messageID)
	return err
}

// --- Viewer Sessions ---

func (s *Store) JoinStream(ctx context.Context, streamID, userID uuid.UUID) error {
	_, err := s.db.Exec(ctx, `
		INSERT INTO live.viewer_sessions (id, stream_id, user_id, joined_at)
		VALUES ($1, $2, $3, NOW())
		ON CONFLICT (stream_id, user_id) WHERE left_at IS NULL DO NOTHING
	`, uuid.New(), streamID, userID)
	return err
}

func (s *Store) LeaveStream(ctx context.Context, streamID, userID uuid.UUID) error {
	_, err := s.db.Exec(ctx, `
		UPDATE live.viewer_sessions SET left_at = NOW(),
		       duration_secs = EXTRACT(EPOCH FROM (NOW() - joined_at))::int
		WHERE stream_id = $1 AND user_id = $2 AND left_at IS NULL
	`, streamID, userID)
	return err
}

func (s *Store) GetActiveViewerCount(ctx context.Context, streamID uuid.UUID) (int, error) {
	var count int
	err := s.db.QueryRow(ctx, `
		SELECT COUNT(*) FROM live.viewer_sessions WHERE stream_id = $1 AND left_at IS NULL
	`, streamID).Scan(&count)
	return count, err
}

// --- Scheduled Streams ---

func (s *Store) CreateScheduledStream(ctx context.Context, ss *ScheduledStream) error {
	_, err := s.db.Exec(ctx, `
		INSERT INTO live.scheduled_streams (id, host_id, title, description, scheduled_at, created_at)
		VALUES ($1, $2, $3, $4, $5, $6)
	`, ss.ID, ss.HostID, ss.Title, ss.Description, ss.ScheduledAt, ss.CreatedAt)
	return err
}

func (s *Store) ListUpcomingStreams(ctx context.Context, limit int) ([]ScheduledStream, error) {
	rows, err := s.db.Query(ctx, `
		SELECT id, host_id, title, description, scheduled_at, reminder_sent, stream_id, created_at
		FROM live.scheduled_streams WHERE stream_id IS NULL AND scheduled_at > NOW()
		ORDER BY scheduled_at ASC LIMIT $1
	`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var scheduled []ScheduledStream
	for rows.Next() {
		var ss ScheduledStream
		if err := rows.Scan(&ss.ID, &ss.HostID, &ss.Title, &ss.Description, &ss.ScheduledAt, &ss.ReminderSent, &ss.StreamID, &ss.CreatedAt); err != nil {
			return nil, err
		}
		scheduled = append(scheduled, ss)
	}
	return scheduled, nil
}
