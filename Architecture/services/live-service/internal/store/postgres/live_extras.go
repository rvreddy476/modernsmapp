package postgres

import (
	"context"
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

// --- Types ---

type LiveGuest struct {
	StreamID  uuid.UUID  `json:"stream_id"`
	UserID    uuid.UUID  `json:"user_id"`
	Role      string     `json:"role"`
	Status    string     `json:"status"`
	InvitedAt time.Time  `json:"invited_at"`
	JoinedAt  *time.Time `json:"joined_at,omitempty"`
}

type LivePoll struct {
	ID        uuid.UUID       `json:"id"`
	StreamID  uuid.UUID       `json:"stream_id"`
	Question  string          `json:"question"`
	Options   json.RawMessage `json:"options"`
	Status    string          `json:"status"`
	CreatedAt time.Time       `json:"created_at"`
	EndsAt    *time.Time      `json:"ends_at,omitempty"`
}

type LivePollVote struct {
	PollID   uuid.UUID `json:"poll_id"`
	UserID   uuid.UUID `json:"user_id"`
	OptionID string    `json:"option_id"`
	VotedAt  time.Time `json:"voted_at"`
}

type LiveGift struct {
	ID        uuid.UUID `json:"id"`
	StreamID  uuid.UUID `json:"stream_id"`
	SenderID  uuid.UUID `json:"sender_id"`
	GiftType  string    `json:"gift_type"`
	GiftCount int       `json:"gift_count"`
	ValueINR  float64   `json:"value_inr"`
	Message   *string   `json:"message,omitempty"`
	SentAt    time.Time `json:"sent_at"`
}

type LiveMute struct {
	StreamID uuid.UUID `json:"stream_id"`
	UserID   uuid.UUID `json:"user_id"`
	MutedBy  uuid.UUID `json:"muted_by"`
	MutedAt  time.Time `json:"muted_at"`
}

type LiveWordFilter struct {
	StreamID uuid.UUID `json:"stream_id"`
	Word     string    `json:"word"`
	AddedBy  uuid.UUID `json:"added_by"`
}

type LiveDVRSegment struct {
	ID         uuid.UUID `json:"id"`
	StreamID   uuid.UUID `json:"stream_id"`
	SegmentURL string    `json:"segment_url"`
	StartTS    time.Time `json:"start_ts"`
	DurationMs int       `json:"duration_ms"`
	SegmentNum int       `json:"segment_num"`
}

type AudioRoom struct {
	ID               uuid.UUID  `json:"id"`
	HostID           uuid.UUID  `json:"host_id"`
	Topic            string     `json:"topic"`
	Description      string     `json:"description"`
	Type             string     `json:"type"`
	CommunityID      *uuid.UUID `json:"community_id,omitempty"`
	Status           string     `json:"status"`
	ScheduledAt      *time.Time `json:"scheduled_at,omitempty"`
	StartedAt        *time.Time `json:"started_at,omitempty"`
	EndedAt          *time.Time `json:"ended_at,omitempty"`
	ListenerCount    int        `json:"listener_count"`
	RecordingEnabled bool       `json:"recording_enabled"`
	CreatedAt        time.Time  `json:"created_at"`
}

type AudioRoomMember struct {
	RoomID     uuid.UUID  `json:"room_id"`
	UserID     uuid.UUID  `json:"user_id"`
	Role       string     `json:"role"`
	HandRaised bool       `json:"hand_raised"`
	IsMuted    bool       `json:"is_muted"`
	JoinedAt   time.Time  `json:"joined_at"`
	LeftAt     *time.Time `json:"left_at,omitempty"`
}

// --- Live Guests ---

func (s *Store) InviteGuest(ctx context.Context, streamID, userID uuid.UUID, role string) error {
	_, err := s.db.Exec(ctx, `
		INSERT INTO live_guests (stream_id, user_id, role, status, invited_at)
		VALUES ($1, $2, $3, 'invited', NOW())
		ON CONFLICT (stream_id, user_id) DO UPDATE SET role = $3, status = 'invited', invited_at = NOW()
	`, streamID, userID, role)
	return err
}

func (s *Store) UpdateGuestStatus(ctx context.Context, streamID, userID uuid.UUID, status string) error {
	_, err := s.db.Exec(ctx, `
		UPDATE live_guests SET status = $3,
			joined_at = CASE WHEN $3 = 'accepted' THEN NOW() ELSE joined_at END
		WHERE stream_id = $1 AND user_id = $2
	`, streamID, userID, status)
	return err
}

func (s *Store) GetStreamGuests(ctx context.Context, streamID uuid.UUID) ([]LiveGuest, error) {
	rows, err := s.db.Query(ctx, `
		SELECT stream_id, user_id, role, status, invited_at, joined_at
		FROM live_guests WHERE stream_id = $1
	`, streamID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var guests []LiveGuest
	for rows.Next() {
		var g LiveGuest
		if err := rows.Scan(&g.StreamID, &g.UserID, &g.Role, &g.Status, &g.InvitedAt, &g.JoinedAt); err != nil {
			return nil, err
		}
		guests = append(guests, g)
	}
	return guests, nil
}

// --- Live Polls ---

func (s *Store) CreateLivePoll(ctx context.Context, p *LivePoll) (*LivePoll, error) {
	err := s.db.QueryRow(ctx, `
		INSERT INTO live_polls (stream_id, question, options, status, created_at, ends_at)
		VALUES ($1, $2, $3, $4, $5, $6)
		RETURNING id, created_at
	`, p.StreamID, p.Question, p.Options, p.Status, p.CreatedAt, p.EndsAt).
		Scan(&p.ID, &p.CreatedAt)
	if err != nil {
		return nil, err
	}
	return p, nil
}

func (s *Store) VoteOnPoll(ctx context.Context, pollID, userID uuid.UUID, optionID string) error {
	_, err := s.db.Exec(ctx, `
		INSERT INTO live_poll_votes (poll_id, user_id, option_id, voted_at)
		VALUES ($1, $2, $3, NOW())
		ON CONFLICT (poll_id, user_id) DO NOTHING
	`, pollID, userID, optionID)
	if err != nil {
		return err
	}

	_, err = s.db.Exec(ctx, `
		UPDATE live_polls
		SET options = (
			SELECT jsonb_agg(CASE WHEN e->>'id' = $3 THEN jsonb_set(e, '{votes}', ((COALESCE(e->>'votes','0'))::int + 1)::text::jsonb) ELSE e END)
			FROM jsonb_array_elements(options) e
		)
		WHERE id = $1 AND status = 'open'
	`, pollID, userID, optionID)
	return err
}

func (s *Store) GetLivePolls(ctx context.Context, streamID uuid.UUID) ([]LivePoll, error) {
	rows, err := s.db.Query(ctx, `
		SELECT id, stream_id, question, options, status, created_at, ends_at
		FROM live_polls WHERE stream_id = $1
		ORDER BY created_at DESC
	`, streamID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var polls []LivePoll
	for rows.Next() {
		var p LivePoll
		if err := rows.Scan(&p.ID, &p.StreamID, &p.Question, &p.Options, &p.Status, &p.CreatedAt, &p.EndsAt); err != nil {
			return nil, err
		}
		polls = append(polls, p)
	}
	return polls, nil
}

// --- Live Gifts ---

func (s *Store) SendGift(ctx context.Context, g *LiveGift) (*LiveGift, error) {
	err := s.db.QueryRow(ctx, `
		INSERT INTO live_gifts (stream_id, sender_id, gift_type, gift_count, value_inr, message, sent_at)
		VALUES ($1, $2, $3, $4, $5, $6, NOW())
		RETURNING id, sent_at
	`, g.StreamID, g.SenderID, g.GiftType, g.GiftCount, g.ValueINR, g.Message).
		Scan(&g.ID, &g.SentAt)
	if err != nil {
		return nil, err
	}
	return g, nil
}

func (s *Store) GetStreamGifts(ctx context.Context, streamID uuid.UUID, limit int) ([]LiveGift, error) {
	rows, err := s.db.Query(ctx, `
		SELECT id, stream_id, sender_id, gift_type, gift_count, value_inr, message, sent_at
		FROM live_gifts WHERE stream_id = $1
		ORDER BY sent_at DESC LIMIT $2
	`, streamID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var gifts []LiveGift
	for rows.Next() {
		var g LiveGift
		if err := rows.Scan(&g.ID, &g.StreamID, &g.SenderID, &g.GiftType, &g.GiftCount, &g.ValueINR, &g.Message, &g.SentAt); err != nil {
			return nil, err
		}
		gifts = append(gifts, g)
	}
	return gifts, nil
}

// --- Live Moderation ---

func (s *Store) MuteUser(ctx context.Context, streamID, userID, mutedBy uuid.UUID) error {
	_, err := s.db.Exec(ctx, `
		INSERT INTO live_mutes (stream_id, user_id, muted_by, muted_at)
		VALUES ($1, $2, $3, NOW())
		ON CONFLICT (stream_id, user_id) DO NOTHING
	`, streamID, userID, mutedBy)
	return err
}

func (s *Store) UnmuteUser(ctx context.Context, streamID, userID uuid.UUID) error {
	_, err := s.db.Exec(ctx, `DELETE FROM live_mutes WHERE stream_id = $1 AND user_id = $2`, streamID, userID)
	return err
}

func (s *Store) GetMutedUsers(ctx context.Context, streamID uuid.UUID) ([]LiveMute, error) {
	rows, err := s.db.Query(ctx, `
		SELECT stream_id, user_id, muted_by, muted_at FROM live_mutes WHERE stream_id = $1
	`, streamID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var mutes []LiveMute
	for rows.Next() {
		var m LiveMute
		if err := rows.Scan(&m.StreamID, &m.UserID, &m.MutedBy, &m.MutedAt); err != nil {
			return nil, err
		}
		mutes = append(mutes, m)
	}
	return mutes, nil
}

func (s *Store) AddWordFilter(ctx context.Context, streamID uuid.UUID, word string, addedBy uuid.UUID) error {
	_, err := s.db.Exec(ctx, `
		INSERT INTO live_word_filters (stream_id, word, added_by)
		VALUES ($1, $2, $3)
		ON CONFLICT (stream_id, word) DO NOTHING
	`, streamID, word, addedBy)
	return err
}

func (s *Store) RemoveWordFilter(ctx context.Context, streamID uuid.UUID, word string) error {
	_, err := s.db.Exec(ctx, `DELETE FROM live_word_filters WHERE stream_id = $1 AND word = $2`, streamID, word)
	return err
}

func (s *Store) GetWordFilters(ctx context.Context, streamID uuid.UUID) ([]LiveWordFilter, error) {
	rows, err := s.db.Query(ctx, `
		SELECT stream_id, word, added_by FROM live_word_filters WHERE stream_id = $1
	`, streamID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var filters []LiveWordFilter
	for rows.Next() {
		var f LiveWordFilter
		if err := rows.Scan(&f.StreamID, &f.Word, &f.AddedBy); err != nil {
			return nil, err
		}
		filters = append(filters, f)
	}
	return filters, nil
}

// --- Gift Leaderboard ---

type GiftLeaderboardEntry struct {
	SenderID   uuid.UUID `json:"sender_id"`
	TotalValue float64   `json:"total_value"`
	GiftCount  int       `json:"gift_count"`
}

func (s *Store) GetGiftLeaderboard(ctx context.Context, streamID uuid.UUID, limit int) ([]GiftLeaderboardEntry, error) {
	rows, err := s.db.Query(ctx, `
		SELECT sender_id, SUM(value_inr * gift_count) AS total_value, SUM(gift_count) AS gift_count
		FROM live_gifts WHERE stream_id = $1
		GROUP BY sender_id
		ORDER BY total_value DESC
		LIMIT $2
	`, streamID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var entries []GiftLeaderboardEntry
	for rows.Next() {
		var e GiftLeaderboardEntry
		if err := rows.Scan(&e.SenderID, &e.TotalValue, &e.GiftCount); err != nil {
			return nil, err
		}
		entries = append(entries, e)
	}
	return entries, nil
}

// --- Moderation checks ---

func (s *Store) IsUserMuted(ctx context.Context, streamID, userID uuid.UUID) (bool, error) {
	var exists bool
	err := s.db.QueryRow(ctx, `
		SELECT EXISTS(SELECT 1 FROM live_mutes WHERE stream_id = $1 AND user_id = $2)
	`, streamID, userID).Scan(&exists)
	return exists, err
}

func (s *Store) MatchesWordFilter(ctx context.Context, streamID uuid.UUID, message string) (bool, error) {
	var exists bool
	err := s.db.QueryRow(ctx, `
		SELECT EXISTS(
			SELECT 1 FROM live_word_filters
			WHERE stream_id = $1
			  AND $2 ILIKE '%' || word || '%'
		)
	`, streamID, message).Scan(&exists)
	return exists, err
}

// --- DVR Segments ---

func (s *Store) ExpireDVRSegments(ctx context.Context, olderThan time.Time) (int64, error) {
	tag, err := s.db.Exec(ctx, `
		DELETE FROM live_dvr_segments WHERE start_ts < $1
	`, olderThan)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}

// --- Worker helpers ---

// FindScheduledStreamsForReminder returns scheduled streams whose start_time is within
// the next windowMinutes and for which a reminder has not yet been sent.
func (s *Store) FindScheduledStreamsForReminder(ctx context.Context, windowMinutes int) ([]ScheduledStream, error) {
	rows, err := s.db.Query(ctx, `
		SELECT id, host_id, title, description, scheduled_at, reminder_sent, stream_id, created_at
		FROM live.scheduled_streams
		WHERE stream_id IS NULL
		  AND reminder_sent = FALSE
		  AND scheduled_at > NOW()
		  AND scheduled_at <= NOW() + ($1 || ' minutes')::INTERVAL
	`, windowMinutes)
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

// MarkReminderSent marks the reminder_sent flag on a scheduled stream.
func (s *Store) MarkReminderSent(ctx context.Context, id uuid.UUID) error {
	_, err := s.db.Exec(ctx, `
		UPDATE live.scheduled_streams SET reminder_sent = TRUE WHERE id = $1
	`, id)
	return err
}

// FindScheduledStreamsToActivate returns scheduled streams whose scheduled_at has passed
// but which have not yet been linked to a live stream (host never manually went live).
func (s *Store) FindScheduledStreamsToActivate(ctx context.Context) ([]ScheduledStream, error) {
	rows, err := s.db.Query(ctx, `
		SELECT id, host_id, title, description, scheduled_at, reminder_sent, stream_id, created_at
		FROM live.scheduled_streams
		WHERE stream_id IS NULL
		  AND scheduled_at <= NOW()
	`)
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

// LinkScheduledStreamToLive associates a scheduled stream record with the created live stream.
func (s *Store) LinkScheduledStreamToLive(ctx context.Context, scheduledID, streamID uuid.UUID) error {
	_, err := s.db.Exec(ctx, `
		UPDATE live.scheduled_streams SET stream_id = $2 WHERE id = $1
	`, scheduledID, streamID)
	return err
}

// EndStaleViewerSessions closes any viewer sessions that have been open longer than maxAgeHours.
func (s *Store) EndStaleViewerSessions(ctx context.Context, maxAgeHours int) (int64, error) {
	tag, err := s.db.Exec(ctx, `
		UPDATE live.viewer_sessions
		SET left_at = NOW(),
		    duration_secs = EXTRACT(EPOCH FROM (NOW() - joined_at))::int
		WHERE left_at IS NULL
		  AND joined_at < NOW() - ($1 || ' hours')::INTERVAL
	`, maxAgeHours)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}

// EndIdleAudioRooms ends audio rooms that have had 0 active members for longer than idleMinutes.
func (s *Store) EndIdleAudioRooms(ctx context.Context, idleMinutes int) ([]uuid.UUID, error) {
	rows, err := s.db.Query(ctx, `
		UPDATE audio_rooms
		SET status = 'ended', ended_at = NOW()
		WHERE status = 'live'
		  AND listener_count = 0
		  AND started_at < NOW() - ($1 || ' minutes')::INTERVAL
		RETURNING id
	`, idleMinutes)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var ids []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, nil
}

// --- DVR Segments ---

func (s *Store) AddDVRSegment(ctx context.Context, seg *LiveDVRSegment) error {
	_, err := s.db.Exec(ctx, `
		INSERT INTO live_dvr_segments (stream_id, segment_url, start_ts, duration_ms, segment_num)
		VALUES ($1, $2, $3, $4, $5)
	`, seg.StreamID, seg.SegmentURL, seg.StartTS, seg.DurationMs, seg.SegmentNum)
	return err
}

func (s *Store) GetDVRSegments(ctx context.Context, streamID uuid.UUID) ([]LiveDVRSegment, error) {
	rows, err := s.db.Query(ctx, `
		SELECT id, stream_id, segment_url, start_ts, duration_ms, segment_num
		FROM live_dvr_segments WHERE stream_id = $1
		ORDER BY segment_num
	`, streamID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var segments []LiveDVRSegment
	for rows.Next() {
		var seg LiveDVRSegment
		if err := rows.Scan(&seg.ID, &seg.StreamID, &seg.SegmentURL, &seg.StartTS, &seg.DurationMs, &seg.SegmentNum); err != nil {
			return nil, err
		}
		segments = append(segments, seg)
	}
	return segments, nil
}

// --- Audio Rooms ---

func (s *Store) CreateAudioRoom(ctx context.Context, r *AudioRoom) (*AudioRoom, error) {
	err := s.db.QueryRow(ctx, `
		INSERT INTO audio_rooms (host_id, topic, description, type, community_id, status, scheduled_at, recording_enabled, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, NOW())
		RETURNING id, created_at
	`, r.HostID, r.Topic, r.Description, r.Type, r.CommunityID, r.Status, r.ScheduledAt, r.RecordingEnabled).
		Scan(&r.ID, &r.CreatedAt)
	if err != nil {
		return nil, err
	}
	return r, nil
}

func (s *Store) GetAudioRoom(ctx context.Context, id uuid.UUID) (*AudioRoom, error) {
	var r AudioRoom
	err := s.db.QueryRow(ctx, `
		SELECT id, host_id, topic, description, type, community_id, status,
		       scheduled_at, started_at, ended_at, listener_count, recording_enabled, created_at
		FROM audio_rooms WHERE id = $1
	`, id).Scan(&r.ID, &r.HostID, &r.Topic, &r.Description, &r.Type, &r.CommunityID, &r.Status,
		&r.ScheduledAt, &r.StartedAt, &r.EndedAt, &r.ListenerCount, &r.RecordingEnabled, &r.CreatedAt)
	if err != nil {
		return nil, err
	}
	return &r, nil
}

func (s *Store) UpdateAudioRoomStatus(ctx context.Context, id uuid.UUID, status string, startedAt, endedAt *time.Time) error {
	_, err := s.db.Exec(ctx, `
		UPDATE audio_rooms SET status = $2, started_at = $3, ended_at = $4 WHERE id = $1
	`, id, status, startedAt, endedAt)
	return err
}

func (s *Store) JoinAudioRoom(ctx context.Context, roomID, userID uuid.UUID, role string) error {
	_, err := s.db.Exec(ctx, `
		INSERT INTO audio_room_members (room_id, user_id, role, joined_at)
		VALUES ($1, $2, $3, NOW())
		ON CONFLICT (room_id, user_id) DO UPDATE SET left_at = NULL, joined_at = NOW(), role = $3
	`, roomID, userID, role)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(ctx, `
		UPDATE audio_rooms SET listener_count = listener_count + 1 WHERE id = $1
	`, roomID)
	return err
}

func (s *Store) LeaveAudioRoom(ctx context.Context, roomID, userID uuid.UUID) error {
	_, err := s.db.Exec(ctx, `
		UPDATE audio_room_members SET left_at = NOW() WHERE room_id = $1 AND user_id = $2 AND left_at IS NULL
	`, roomID, userID)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(ctx, `
		UPDATE audio_rooms SET listener_count = GREATEST(0, listener_count - 1) WHERE id = $1
	`, roomID)
	return err
}

func (s *Store) GetAudioRoomMembers(ctx context.Context, roomID uuid.UUID) ([]AudioRoomMember, error) {
	rows, err := s.db.Query(ctx, `
		SELECT room_id, user_id, role, hand_raised, is_muted, joined_at, left_at
		FROM audio_room_members WHERE room_id = $1 AND left_at IS NULL
	`, roomID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var members []AudioRoomMember
	for rows.Next() {
		var m AudioRoomMember
		if err := rows.Scan(&m.RoomID, &m.UserID, &m.Role, &m.HandRaised, &m.IsMuted, &m.JoinedAt, &m.LeftAt); err != nil {
			return nil, err
		}
		members = append(members, m)
	}
	return members, nil
}

func (s *Store) ListLiveAudioRooms(ctx context.Context, limit int) ([]AudioRoom, error) {
	rows, err := s.db.Query(ctx, `
		SELECT id, host_id, topic, description, type, community_id, status,
		       scheduled_at, started_at, ended_at, listener_count, recording_enabled, created_at
		FROM audio_rooms WHERE status = 'live'
		ORDER BY listener_count DESC, started_at DESC LIMIT $1
	`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var rooms []AudioRoom
	for rows.Next() {
		var r AudioRoom
		if err := rows.Scan(&r.ID, &r.HostID, &r.Topic, &r.Description, &r.Type, &r.CommunityID, &r.Status,
			&r.ScheduledAt, &r.StartedAt, &r.EndedAt, &r.ListenerCount, &r.RecordingEnabled, &r.CreatedAt); err != nil {
			return nil, err
		}
		rooms = append(rooms, r)
	}
	return rooms, nil
}
