package service

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/atpost/live-service/internal/store/postgres"
	"github.com/google/uuid"
)

// --- Live Guests ---

// InviteGuest validates stream ownership then creates/updates the guest record.
// Only the stream host can invite guests.
func (s *Service) InviteGuest(ctx context.Context, streamID, hostID, guestUserID uuid.UUID, role string) error {
	st, err := s.store.GetStream(ctx, streamID)
	if err != nil {
		return fmt.Errorf("stream not found")
	}
	if st.HostID != hostID {
		return fmt.Errorf("only the stream host can invite guests")
	}
	if st.Status == "ended" {
		return fmt.Errorf("cannot invite guests to an ended stream")
	}
	if role == "" {
		role = "guest"
	}
	return s.store.InviteGuest(ctx, streamID, guestUserID, role)
}

// UpdateGuestStatus validates status transitions: pending/invited → accepted/declined/removed.
// The guest themselves can accept/decline; the host can remove.
func (s *Service) UpdateGuestStatus(ctx context.Context, streamID, callerID, guestUserID uuid.UUID, status string) error {
	validStatuses := map[string]bool{
		"accepted": true,
		"declined": true,
		"removed":  true,
	}
	if !validStatuses[status] {
		return fmt.Errorf("status must be one of: accepted, declined, removed")
	}

	st, err := s.store.GetStream(ctx, streamID)
	if err != nil {
		return fmt.Errorf("stream not found")
	}

	// Only the guest can accept/decline; only the host (or guest) can remove
	switch status {
	case "accepted", "declined":
		if callerID != guestUserID {
			return fmt.Errorf("only the invited guest can accept or decline")
		}
	case "removed":
		if callerID != st.HostID && callerID != guestUserID {
			return fmt.Errorf("only the host or the guest can remove a guest")
		}
	}

	return s.store.UpdateGuestStatus(ctx, streamID, guestUserID, status)
}

func (s *Service) GetStreamGuests(ctx context.Context, streamID uuid.UUID) ([]postgres.LiveGuest, error) {
	return s.store.GetStreamGuests(ctx, streamID)
}

// --- Live Polls ---

type CreatePollInput struct {
	StreamID uuid.UUID
	HostID   uuid.UUID
	Question string
	Options  json.RawMessage
	EndsAt   *time.Time
}

// CreateLivePoll validates that only the host can create a poll, the stream is live,
// and the options array has at most 4 entries.
func (s *Service) CreateLivePoll(ctx context.Context, input *CreatePollInput) (*postgres.LivePoll, error) {
	if input.Question == "" {
		return nil, fmt.Errorf("question is required")
	}

	st, err := s.store.GetStream(ctx, input.StreamID)
	if err != nil {
		return nil, fmt.Errorf("stream not found")
	}
	if st.HostID != input.HostID {
		return nil, fmt.Errorf("only the stream host can create polls")
	}
	if st.Status != "live" {
		return nil, fmt.Errorf("polls can only be created on a live stream")
	}

	// Validate option count (max 4)
	var opts []any
	if err := json.Unmarshal(input.Options, &opts); err != nil {
		return nil, fmt.Errorf("options must be a JSON array")
	}
	if len(opts) < 2 || len(opts) > 4 {
		return nil, fmt.Errorf("polls must have between 2 and 4 options")
	}

	p := &postgres.LivePoll{
		StreamID:  input.StreamID,
		Question:  input.Question,
		Options:   input.Options,
		Status:    "open",
		CreatedAt: time.Now(),
		EndsAt:    input.EndsAt,
	}
	return s.store.CreateLivePoll(ctx, p)
}

// VoteOnPoll enforces that the stream must be live and prevents duplicate votes.
// The duplicate-vote prevention is handled at the DB layer (PK on poll_id, user_id),
// but we also verify the stream is still live.
func (s *Service) VoteOnPoll(ctx context.Context, streamID, pollID, userID uuid.UUID, optionID string) error {
	if optionID == "" {
		return fmt.Errorf("option_id is required")
	}
	st, err := s.store.GetStream(ctx, streamID)
	if err != nil {
		return fmt.Errorf("stream not found")
	}
	if st.Status != "live" {
		return fmt.Errorf("voting is only allowed on live streams")
	}
	return s.store.VoteOnPoll(ctx, pollID, userID, optionID)
}

func (s *Service) GetLivePolls(ctx context.Context, streamID uuid.UUID) ([]postgres.LivePoll, error) {
	return s.store.GetLivePolls(ctx, streamID)
}

// --- Gifts ---

type SendGiftInput struct {
	StreamID  uuid.UUID
	SenderID  uuid.UUID
	GiftType  string
	GiftCount int
	ValueINR  float64
	Message   *string
}

// SendGift validates the stream is live, records the gift, and publishes a Kafka event
// so the wallet/payments service can credit the host.
func (s *Service) SendGift(ctx context.Context, input *SendGiftInput) (*postgres.LiveGift, error) {
	st, err := s.store.GetStream(ctx, input.StreamID)
	if err != nil {
		return nil, fmt.Errorf("stream not found")
	}
	if st.Status != "live" {
		return nil, fmt.Errorf("gifts can only be sent to live streams")
	}
	if input.GiftCount <= 0 {
		input.GiftCount = 1
	}
	if input.ValueINR <= 0 {
		return nil, fmt.Errorf("gift value must be positive")
	}

	g := &postgres.LiveGift{
		StreamID:  input.StreamID,
		SenderID:  input.SenderID,
		GiftType:  input.GiftType,
		GiftCount: input.GiftCount,
		ValueINR:  input.ValueINR,
		Message:   input.Message,
	}
	gift, err := s.store.SendGift(ctx, g)
	if err != nil {
		return nil, err
	}

	// Publish gift event for wallet/payments service to process
	senderStr := input.SenderID.String()
	go s.publishEvent(ctx, "LiveGiftSent", &input.SenderID, map[string]any{
		"gift_id":    gift.ID.String(),
		"stream_id":  input.StreamID.String(),
		"host_id":    st.HostID.String(),
		"sender_id":  senderStr,
		"gift_type":  input.GiftType,
		"gift_count": input.GiftCount,
		"value_inr":  input.ValueINR,
		"sent_at":    gift.SentAt,
	})

	return gift, nil
}

func (s *Service) GetStreamGifts(ctx context.Context, streamID uuid.UUID, limit int) ([]postgres.LiveGift, error) {
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	return s.store.GetStreamGifts(ctx, streamID, limit)
}

func (s *Service) GetGiftLeaderboard(ctx context.Context, streamID uuid.UUID, limit int) ([]postgres.GiftLeaderboardEntry, error) {
	if limit <= 0 || limit > 50 {
		limit = 10
	}
	return s.store.GetGiftLeaderboard(ctx, streamID, limit)
}

// --- Moderation ---

// MuteUser enforces that only the stream host can mute users.
func (s *Service) MuteUser(ctx context.Context, streamID, userID, mutedBy uuid.UUID) error {
	st, err := s.store.GetStream(ctx, streamID)
	if err != nil {
		return fmt.Errorf("stream not found")
	}
	if st.HostID != mutedBy {
		return fmt.Errorf("only the stream host can mute users")
	}
	return s.store.MuteUser(ctx, streamID, userID, mutedBy)
}

// UnmuteUser enforces that only the stream host can unmute users.
func (s *Service) UnmuteUser(ctx context.Context, streamID, userID, callerID uuid.UUID) error {
	st, err := s.store.GetStream(ctx, streamID)
	if err != nil {
		return fmt.Errorf("stream not found")
	}
	if st.HostID != callerID {
		return fmt.Errorf("only the stream host can unmute users")
	}
	return s.store.UnmuteUser(ctx, streamID, userID)
}

func (s *Service) GetMutedUsers(ctx context.Context, streamID uuid.UUID) ([]postgres.LiveMute, error) {
	return s.store.GetMutedUsers(ctx, streamID)
}

// AddWordFilter validates that the caller is the host, then adds the word filter.
func (s *Service) AddWordFilter(ctx context.Context, streamID uuid.UUID, word string, addedBy uuid.UUID) error {
	if word == "" {
		return fmt.Errorf("word is required")
	}
	st, err := s.store.GetStream(ctx, streamID)
	if err != nil {
		return fmt.Errorf("stream not found")
	}
	if st.HostID != addedBy {
		return fmt.Errorf("only the stream host can manage word filters")
	}
	return s.store.AddWordFilter(ctx, streamID, word, addedBy)
}

// RemoveWordFilter validates that the caller is the host, then removes the word filter.
func (s *Service) RemoveWordFilter(ctx context.Context, streamID uuid.UUID, word string, callerID uuid.UUID) error {
	st, err := s.store.GetStream(ctx, streamID)
	if err != nil {
		return fmt.Errorf("stream not found")
	}
	if st.HostID != callerID {
		return fmt.Errorf("only the stream host can manage word filters")
	}
	return s.store.RemoveWordFilter(ctx, streamID, word)
}

func (s *Service) GetWordFilters(ctx context.Context, streamID uuid.UUID) ([]postgres.LiveWordFilter, error) {
	return s.store.GetWordFilters(ctx, streamID)
}

func (s *Service) IsUserMuted(ctx context.Context, streamID, userID uuid.UUID) (bool, error) {
	return s.store.IsUserMuted(ctx, streamID, userID)
}

// --- DVR ---

func (s *Service) AddDVRSegment(ctx context.Context, seg *postgres.LiveDVRSegment) error {
	return s.store.AddDVRSegment(ctx, seg)
}

func (s *Service) GetDVRSegments(ctx context.Context, streamID uuid.UUID) ([]postgres.LiveDVRSegment, error) {
	return s.store.GetDVRSegments(ctx, streamID)
}

// --- Audio Rooms ---

type CreateAudioRoomInput struct {
	HostID           uuid.UUID
	Topic            string
	Description      string
	Type             string
	CommunityID      *uuid.UUID
	ScheduledAt      *time.Time
	RecordingEnabled bool
}

func (s *Service) CreateAudioRoom(ctx context.Context, input *CreateAudioRoomInput) (*postgres.AudioRoom, error) {
	if input.Topic == "" {
		return nil, fmt.Errorf("topic is required")
	}
	roomType := input.Type
	if roomType == "" {
		roomType = "open"
	}
	r := &postgres.AudioRoom{
		HostID:           input.HostID,
		Topic:            input.Topic,
		Description:      input.Description,
		Type:             roomType,
		CommunityID:      input.CommunityID,
		Status:           "scheduled",
		ScheduledAt:      input.ScheduledAt,
		RecordingEnabled: input.RecordingEnabled,
	}
	return s.store.CreateAudioRoom(ctx, r)
}

func (s *Service) GetAudioRoom(ctx context.Context, id uuid.UUID) (*postgres.AudioRoom, error) {
	return s.store.GetAudioRoom(ctx, id)
}

func (s *Service) StartAudioRoom(ctx context.Context, roomID, hostID uuid.UUID) error {
	r, err := s.store.GetAudioRoom(ctx, roomID)
	if err != nil {
		return fmt.Errorf("audio room not found")
	}
	if r.HostID != hostID {
		return fmt.Errorf("not the room host")
	}
	if r.Status != "scheduled" {
		return fmt.Errorf("room is already %s", r.Status)
	}
	now := time.Now()
	return s.store.UpdateAudioRoomStatus(ctx, roomID, "live", &now, nil)
}

func (s *Service) EndAudioRoom(ctx context.Context, roomID, hostID uuid.UUID) error {
	r, err := s.store.GetAudioRoom(ctx, roomID)
	if err != nil {
		return fmt.Errorf("audio room not found")
	}
	if r.HostID != hostID {
		return fmt.Errorf("not the room host")
	}
	if r.Status != "live" {
		return fmt.Errorf("room is not live")
	}
	now := time.Now()
	return s.store.UpdateAudioRoomStatus(ctx, roomID, "ended", r.StartedAt, &now)
}

func (s *Service) JoinAudioRoom(ctx context.Context, roomID, userID uuid.UUID) error {
	return s.store.JoinAudioRoom(ctx, roomID, userID, "listener")
}

func (s *Service) LeaveAudioRoom(ctx context.Context, roomID, userID uuid.UUID) error {
	return s.store.LeaveAudioRoom(ctx, roomID, userID)
}

func (s *Service) GetAudioRoomMembers(ctx context.Context, roomID uuid.UUID) ([]postgres.AudioRoomMember, error) {
	return s.store.GetAudioRoomMembers(ctx, roomID)
}

func (s *Service) ListLiveAudioRooms(ctx context.Context, limit int) ([]postgres.AudioRoom, error) {
	if limit <= 0 || limit > 50 {
		limit = 20
	}
	return s.store.ListLiveAudioRooms(ctx, limit)
}
