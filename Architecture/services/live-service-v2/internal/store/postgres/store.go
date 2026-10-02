package postgres

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// LiveStream mirrors the live_streams row. Pointers cover NULLable cols.
type LiveStream struct {
	ID            uuid.UUID  `json:"id"`
	CreatorUserID uuid.UUID  `json:"creator_user_id"`
	LiveKitRoom   string     `json:"livekit_room"`
	Title         string     `json:"title"`
	Description   string     `json:"description"`
	CoverMediaID  *uuid.UUID `json:"cover_media_id,omitempty"`
	Status        string     `json:"status"`
	Visibility    string     `json:"visibility"`
	ScheduledAt   *time.Time `json:"scheduled_at,omitempty"`
	StartedAt     *time.Time `json:"started_at,omitempty"`
	EndedAt       *time.Time `json:"ended_at,omitempty"`
	ViewerPeak    int        `json:"viewer_peak"`
	// ViewerCount is the current number of viewers in the LiveKit room,
	// from participant webhooks, never counting the host.
	ViewerCount int `json:"viewer_count"`
	// EndedReason is set on ended/failed: host_ended | host_lost |
	// room_finished | admin_stopped | no_media. null otherwise.
	EndedReason *string `json:"ended_reason"`
	// ModeratorUserIDs is filled only for the host and the stream's
	// moderators (service layer); absent for everyone else.
	ModeratorUserIDs *[]uuid.UUID `json:"moderator_user_ids,omitempty"`
	// StatusChangedAt is when the current status began (database clock).
	StatusChangedAt          time.Time `json:"status_changed_at"`
	RecordingURL             *string   `json:"recording_url,omitempty"`
	RecordingDurationSeconds *int      `json:"recording_duration_seconds,omitempty"`
	EgressID                 *string   `json:"-"`
	// Source is where the host's media comes from: 'device' (the browser or
	// phone camera) or 'encoder' (OBS / an encoder / a camera through a
	// LiveKit ingress).
	Source string `json:"source"`
	// HasIngress is filled only for the host and the stream's moderators
	// (service layer); absent for everyone else.
	HasIngress *bool `json:"has_ingress,omitempty"`
	// IngressID is the LiveKit ingress issued for the stream (nil = none).
	// EncoderIdentity is the participant identity it publishes as. Neither is
	// on the wire, and the stream key is not stored at all.
	IngressID       *string `json:"-"`
	EncoderIdentity *string `json:"-"`
	// Orientation is 'landscape' (PostTube) or 'portrait' (the Reels Live
	// tab). Category is a slug of post-service's video taxonomy, "" = none.
	Orientation string `json:"orientation"`
	Category    string `json:"category"`
	// HeartCount is the total of free hearts sent on the stream.
	HeartCount int64 `json:"heart_count"`
	// RecordingPostID is the video post the recording became (nil until
	// post-service reports it; see migration 005).
	RecordingPostID *uuid.UUID `json:"recording_post_id,omitempty"`
	// Creator is the host card (service layer): user_id always; name, handle
	// and avatar_url when the profile lookup answered.
	Creator *UserCard `json:"creator,omitempty"`
	// ReminderSet / ReminderCount are filled on upcoming rows (service
	// layer): the count for everyone, reminder_set for a signed-in caller.
	ReminderSet   *bool `json:"reminder_set,omitempty"`
	ReminderCount *int  `json:"reminder_count,omitempty"`
	// ViewerCap is the new-streamer limit on concurrent viewers (service
	// layer): on the host's own row of a stream that has not ended, while the
	// cap applies to them; absent for everyone else.
	ViewerCap *int      `json:"viewer_cap,omitempty"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

var ErrNotFound = errors.New("live stream not found")

type Store struct {
	db *pgxpool.Pool
	// founding decides the founding creator badge (surfaces.go).
	founding FoundingRule
}

func New(db *pgxpool.Pool) *Store { return &Store{db: db} }

func scanStream(row pgx.Row) (*LiveStream, error) {
	return scanStreamExtra(row)
}

// scanStreamExtra scans selectColumns followed by extra destinations (a
// computed column appended after selectColumns).
func scanStreamExtra(row pgx.Row, extra ...any) (*LiveStream, error) {
	var s LiveStream
	err := row.Scan(append([]any{
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
		&s.ViewerCount,
		&s.EndedReason,
		&s.StatusChangedAt,
		&s.RecordingURL,
		&s.RecordingDurationSeconds,
		&s.EgressID,
		&s.Source,
		&s.IngressID,
		&s.EncoderIdentity,
		&s.Orientation,
		&s.Category,
		&s.HeartCount,
		&s.RecordingPostID,
		&s.CreatedAt,
		&s.UpdatedAt,
	}, extra...)...)
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
    viewer_peak, viewer_count, ended_reason, status_changed_at,
    recording_url, recording_duration_seconds, egress_id,
    source, ingress_id, encoder_identity,
    orientation, category, heart_count, recording_post_id,
    created_at, updated_at`

type CreateStreamParams struct {
	CreatorUserID uuid.UUID
	LiveKitRoom   string
	Title         string
	Description   string
	CoverMediaID  *uuid.UUID
	Visibility    string
	ScheduledAt   *time.Time
	// Source is SourceDevice or SourceEncoder; empty means SourceDevice.
	Source string
	// Orientation is OrientationLandscape or OrientationPortrait; empty
	// means OrientationLandscape. Category is "" or a taxonomy slug.
	Orientation string
	Category    string
}

// Stream orientations.
const (
	OrientationLandscape = "landscape"
	OrientationPortrait  = "portrait"
)

// Stream sources.
const (
	SourceDevice  = "device"
	SourceEncoder = "encoder"
)

func (s *Store) CreateStream(ctx context.Context, p CreateStreamParams) (*LiveStream, error) {
	const q = `
        INSERT INTO live_streams
            (creator_user_id, livekit_room, title, description, cover_media_id,
             visibility, scheduled_at, status, source, orientation, category)
        VALUES ($1, $2, $3, $4, $5, $6, $7, 'scheduled', $8, $9, $10)
        RETURNING ` + selectColumns
	source := p.Source
	if source == "" {
		source = SourceDevice
	}
	orientation := p.Orientation
	if orientation == "" {
		orientation = OrientationLandscape
	}
	return scanStream(s.db.QueryRow(ctx, q,
		p.CreatorUserID,
		p.LiveKitRoom,
		p.Title,
		p.Description,
		p.CoverMediaID,
		p.Visibility,
		p.ScheduledAt,
		source,
		orientation,
		p.Category,
	))
}

func (s *Store) GetByID(ctx context.Context, id uuid.UUID) (*LiveStream, error) {
	const q = `SELECT ` + selectColumns + ` FROM live_streams WHERE id = $1`
	return scanStream(s.db.QueryRow(ctx, q, id))
}

// GetByRoom finds a stream by its LiveKit room name (livekit_room is
// UNIQUE). Webhooks name the room, not the stream, and the room name is NOT
// "stream_<id>" for rows created before 2 Oct 2026: the id was assigned by
// the database, not the one the name was built from.
func (s *Store) GetByRoom(ctx context.Context, room string) (*LiveStream, error) {
	const q = `SELECT ` + selectColumns + ` FROM live_streams WHERE livekit_room = $1`
	return scanStream(s.db.QueryRow(ctx, q, room))
}

// Status changes (start, live, reconnecting, ended, failed), the recording
// pointer and their outbox events are in lifecycle.go: every one of them is
// a locked read-decide-write in one transaction.

type ListLiveParams struct {
	Limit         int
	StartedBefore *time.Time
	IDBefore      *uuid.UUID
	// Sort is SortRecent (the zero value) or SortViewers. With SortViewers
	// the keyset is (viewer_count, started_at, id) and ViewersBefore carries
	// its first part.
	Sort          string
	ViewersBefore *int
	Filter        StreamFilter
}

// ListLive returns streams on air (status live or reconnecting), newest
// first or most-watched first. Cursor pagination uses the order's keyset;
// without a complete cursor it returns the head.
func (s *Store) ListLive(ctx context.Context, p ListLiveParams) ([]*LiveStream, error) {
	limit := p.Limit
	if limit <= 0 || limit > 500 {
		limit = 20
	}
	conds := []string{"status IN ('live', 'reconnecting')"}
	var args []any
	conds, args = p.Filter.where(conds, args)
	order := "started_at DESC NULLS LAST, id DESC"
	hasCursor := p.StartedBefore != nil && p.IDBefore != nil
	if p.Sort == SortViewers {
		order = "viewer_count DESC, started_at DESC NULLS LAST, id DESC"
		if hasCursor && p.ViewersBefore != nil {
			args = append(args, *p.ViewersBefore, *p.StartedBefore, *p.IDBefore)
			n := len(args)
			conds = append(conds, fmt.Sprintf("(viewer_count, started_at, id) < ($%d, $%d, $%d)", n-2, n-1, n))
		}
	} else if hasCursor {
		args = append(args, *p.StartedBefore, *p.IDBefore)
		n := len(args)
		conds = append(conds, fmt.Sprintf("(started_at, id) < ($%d, $%d)", n-1, n))
	}
	args = append(args, limit)
	q := `SELECT ` + selectColumns + ` FROM live_streams WHERE ` + strings.Join(conds, " AND ") +
		` ORDER BY ` + order + fmt.Sprintf(" LIMIT $%d", len(args))
	return s.queryStreams(ctx, q, args...)
}

type ListScheduledParams struct {
	Limit int
	// Now is the "upcoming" boundary: only rows with scheduled_at > Now.
	Now            time.Time
	ScheduledAfter *time.Time
	IDAfter        *uuid.UUID
	Filter         StreamFilter
	// Unstarted lists EVERY 'scheduled' stream instead (Now is ignored):
	// overdue ones and ones with no scheduled_at too, soonest first with the
	// timeless ones last. A creator's own list (surfaces.go).
	Unstarted bool
}

// ListScheduled returns upcoming streams — status='scheduled' with a
// scheduled_at still in the future — soonest first. Keyset on
// (scheduled_at, id) ascending; a stream with no scheduled_at is not
// "upcoming" and never listed. Visibility is the caller's filter, the same
// authorizeViewer gate ListLive's caller applies.
func (s *Store) ListScheduled(ctx context.Context, p ListScheduledParams) ([]*LiveStream, error) {
	limit := p.Limit
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	if p.Unstarted {
		return s.listUnstarted(ctx, p, limit)
	}
	conds := []string{"status = 'scheduled'", "scheduled_at > $1"}
	args := []any{p.Now}
	conds, args = p.Filter.where(conds, args)
	if p.ScheduledAfter != nil && p.IDAfter != nil {
		args = append(args, *p.ScheduledAfter, *p.IDAfter)
		n := len(args)
		conds = append(conds, fmt.Sprintf("(scheduled_at, id) > ($%d, $%d)", n-1, n))
	}
	args = append(args, limit)
	q := `SELECT ` + selectColumns + ` FROM live_streams WHERE ` + strings.Join(conds, " AND ") +
		fmt.Sprintf(" ORDER BY scheduled_at ASC, id ASC LIMIT $%d", len(args))
	return s.queryStreams(ctx, q, args...)
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
	// RemovedAt is set once a host, moderator or admin removed the message.
	// Viewer reads never return removed rows, so it is not on the wire.
	RemovedAt *time.Time `json:"-"`
	// Author is who wrote the message (service layer): user_id and role
	// always, the rest when the profile lookup answered.
	Author *ChatAuthor `json:"author,omitempty"`
}

// Chat author roles.
const (
	ChatRoleHost      = "host"
	ChatRoleModerator = "moderator"
	ChatRoleViewer    = "viewer"
)

// ChatAuthor is the writer of a chat message as every chat row shows them.
// Only user_id and role are guaranteed; name, handle and avatar_url are left
// off when the profile lookup did not answer. badges is always a list.
type ChatAuthor struct {
	UserID    uuid.UUID `json:"user_id"`
	Name      string    `json:"name,omitempty"`
	Handle    string    `json:"handle,omitempty"`
	AvatarURL string    `json:"avatar_url,omitempty"`
	Badges    []string  `json:"badges"`
	// Role is the author's role in the stream when the row was read:
	// host | moderator | viewer.
	Role string `json:"role"`
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
        WHERE stream_id = $1 AND removed_at IS NULL
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
	// Characters, not bytes: the column's CHECK is char_length, and a word in
	// Telugu or Hindi is three bytes a character.
	if utf8.RuneCountInString(w) > MaxWordFilterChars {
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

// MaxWordFilterChars is the longest filter word, in characters.
const MaxWordFilterChars = 100

// WordFilterMatches reports whether any of words (stored lowercased) is a
// substring of text, ignoring case. It is the one rule both stores use.
func WordFilterMatches(words []string, text string) bool {
	lt := strings.ToLower(text)
	for _, w := range words {
		if w != "" && strings.Contains(lt, w) {
			return true
		}
	}
	return false
}

// MatchesWordFilter reports true if any configured filter word for the
// stream is a substring of `text` (case-insensitive).
//
// The words are read and compared in Go (WordFilterMatches). The comparison
// used to be `$2 ILIKE '%' || word || '%'` in SQL, which read `%` and `_` in
// a filter word as wildcards — a host who blocked "_" or "%" blocked every
// message, emoji and all — and lowercased by the database's collation rather
// than the way the words were lowercased when they were stored.
func (s *Store) MatchesWordFilter(ctx context.Context, streamID uuid.UUID, text string) (bool, error) {
	words, err := s.ListWordFilters(ctx, streamID)
	if err != nil {
		return false, err
	}
	return WordFilterMatches(words, text), nil
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
        WHERE id = $1 AND stream_id = $2 AND removed_at IS NULL`, messageID, streamID)
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
        WHERE stream_id = $1 AND is_pinned = TRUE AND removed_at IS NULL
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
