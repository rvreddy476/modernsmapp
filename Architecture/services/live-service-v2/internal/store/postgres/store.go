package postgres

import (
	"context"
	"errors"
	"fmt"
	"strings"
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
	ID        uuid.UUID  `json:"id"`
	StreamID  uuid.UUID  `json:"stream_id"`
	UserID    uuid.UUID  `json:"user_id"`
	Text      string     `json:"text"`
	IsPinned  bool       `json:"is_pinned"`
	PinnedAt  *time.Time `json:"pinned_at,omitempty"`
	CreatedAt time.Time  `json:"created_at"`
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
        SELECT id, stream_id, user_id, text, is_pinned, pinned_at, created_at
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
		if err := rows.Scan(&m.ID, &m.StreamID, &m.UserID, &m.Text, &m.IsPinned, &m.PinnedAt, &m.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// --- Chat moderation (Phase B) ---

// MuteUser inserts (or no-ops if already present) a per-stream mute.
// The PRIMARY KEY (stream_id, user_id) makes the UPSERT idempotent.
func (s *Store) MuteUser(ctx context.Context, streamID, userID, mutedBy uuid.UUID) error {
	const q = `
        INSERT INTO live_chat_mutes (stream_id, user_id, muted_by)
        VALUES ($1, $2, $3)
        ON CONFLICT (stream_id, user_id) DO NOTHING`
	_, err := s.db.Exec(ctx, q, streamID, userID, mutedBy)
	return err
}

// UnmuteUser removes a mute. No error if the row does not exist.
func (s *Store) UnmuteUser(ctx context.Context, streamID, userID uuid.UUID) error {
	const q = `DELETE FROM live_chat_mutes WHERE stream_id = $1 AND user_id = $2`
	_, err := s.db.Exec(ctx, q, streamID, userID)
	return err
}

// IsUserMuted is the per-message gate used by SendChat.
func (s *Store) IsUserMuted(ctx context.Context, streamID, userID uuid.UUID) (bool, error) {
	const q = `SELECT EXISTS(SELECT 1 FROM live_chat_mutes WHERE stream_id = $1 AND user_id = $2)`
	var exists bool
	if err := s.db.QueryRow(ctx, q, streamID, userID).Scan(&exists); err != nil {
		return false, err
	}
	return exists, nil
}

// ListMutedUsers returns the user IDs currently muted on a stream.
// Order is insertion-time DESC so the most-recent mutes show first
// in the host UI.
func (s *Store) ListMutedUsers(ctx context.Context, streamID uuid.UUID) ([]uuid.UUID, error) {
	const q = `
        SELECT user_id
        FROM live_chat_mutes
        WHERE stream_id = $1
        ORDER BY muted_at DESC`
	rows, err := s.db.Query(ctx, q, streamID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]uuid.UUID, 0)
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// AddWordFilter inserts a lowercased filter word for the stream.
// Idempotent on (stream_id, word).
func (s *Store) AddWordFilter(ctx context.Context, streamID uuid.UUID, word string, addedBy uuid.UUID) error {
	w := strings.ToLower(strings.TrimSpace(word))
	if w == "" {
		return fmt.Errorf("word is required")
	}
	if len(w) > 100 {
		return fmt.Errorf("word exceeds 100 chars")
	}
	const q = `
        INSERT INTO live_chat_word_filters (stream_id, word, added_by)
        VALUES ($1, $2, $3)
        ON CONFLICT (stream_id, word) DO NOTHING`
	_, err := s.db.Exec(ctx, q, streamID, w, addedBy)
	return err
}

// RemoveWordFilter deletes a filter word. No error if absent.
func (s *Store) RemoveWordFilter(ctx context.Context, streamID uuid.UUID, word string) error {
	w := strings.ToLower(strings.TrimSpace(word))
	const q = `DELETE FROM live_chat_word_filters WHERE stream_id = $1 AND word = $2`
	_, err := s.db.Exec(ctx, q, streamID, w)
	return err
}

// ListWordFilters returns the lowercased words configured for the
// stream, alphabetised for stable display in the host UI.
func (s *Store) ListWordFilters(ctx context.Context, streamID uuid.UUID) ([]string, error) {
	const q = `
        SELECT word
        FROM live_chat_word_filters
        WHERE stream_id = $1
        ORDER BY word ASC`
	rows, err := s.db.Query(ctx, q, streamID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]string, 0)
	for rows.Next() {
		var w string
		if err := rows.Scan(&w); err != nil {
			return nil, err
		}
		out = append(out, w)
	}
	return out, rows.Err()
}

// MatchesWordFilter reports true if any configured filter word for the
// stream is a substring of `text` (case-insensitive). Matches v1
// behaviour: ILIKE '%word%'. The DB-side check keeps the substring
// loop close to the filter rows and avoids shuttling the whole list
// to Go for each chat message.
func (s *Store) MatchesWordFilter(ctx context.Context, streamID uuid.UUID, text string) (bool, error) {
	const q = `
        SELECT EXISTS(
            SELECT 1 FROM live_chat_word_filters
            WHERE stream_id = $1
              AND $2 ILIKE '%' || word || '%'
        )`
	var exists bool
	if err := s.db.QueryRow(ctx, q, streamID, text).Scan(&exists); err != nil {
		return false, err
	}
	return exists, nil
}

// PinMessage atomically clears any existing pin on the stream and
// pins the target message. The two updates run inside a single
// transaction so a concurrent pin can never leave two messages
// flagged at once.
//
// Returns ErrNotFound if the message does not exist in the stream.
func (s *Store) PinMessage(ctx context.Context, streamID, messageID uuid.UUID) error {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	// Unpin any existing pinned message for this stream.
	if _, err := tx.Exec(ctx, `
        UPDATE live_chat_messages
        SET is_pinned = FALSE, pinned_at = NULL
        WHERE stream_id = $1 AND is_pinned = TRUE`, streamID); err != nil {
		return err
	}
	// Pin the target, scoped to the same stream so a wrong-stream id
	// fails closed rather than pinning across streams.
	tag, err := tx.Exec(ctx, `
        UPDATE live_chat_messages
        SET is_pinned = TRUE, pinned_at = NOW()
        WHERE id = $1 AND stream_id = $2`, messageID, streamID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return tx.Commit(ctx)
}

// UnpinMessage clears the pin on a specific message. No error if the
// message is not currently pinned.
func (s *Store) UnpinMessage(ctx context.Context, streamID, messageID uuid.UUID) error {
	const q = `
        UPDATE live_chat_messages
        SET is_pinned = FALSE, pinned_at = NULL
        WHERE id = $1 AND stream_id = $2`
	_, err := s.db.Exec(ctx, q, messageID, streamID)
	return err
}

// GetPinnedMessage returns the current pinned message for the stream,
// or (nil, nil) if no message is pinned. The partial index
// idx_live_chat_pinned keeps this lookup O(1) per stream.
func (s *Store) GetPinnedMessage(ctx context.Context, streamID uuid.UUID) (*ChatMessage, error) {
	const q = `
        SELECT id, stream_id, user_id, text, is_pinned, pinned_at, created_at
        FROM live_chat_messages
        WHERE stream_id = $1 AND is_pinned = TRUE
        ORDER BY pinned_at DESC
        LIMIT 1`
	row := s.db.QueryRow(ctx, q, streamID)
	m := &ChatMessage{}
	err := row.Scan(&m.ID, &m.StreamID, &m.UserID, &m.Text, &m.IsPinned, &m.PinnedAt, &m.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return m, nil
}
