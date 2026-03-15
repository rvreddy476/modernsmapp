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

func (s *Service) InviteGuest(ctx context.Context, streamID, userID uuid.UUID, role string) error {
	return s.store.InviteGuest(ctx, streamID, userID, role)
}

func (s *Service) UpdateGuestStatus(ctx context.Context, streamID, userID uuid.UUID, status string) error {
	return s.store.UpdateGuestStatus(ctx, streamID, userID, status)
}

func (s *Service) GetStreamGuests(ctx context.Context, streamID uuid.UUID) ([]postgres.LiveGuest, error) {
	return s.store.GetStreamGuests(ctx, streamID)
}

// --- Live Polls ---

type CreatePollInput struct {
	StreamID uuid.UUID
	Question string
	Options  json.RawMessage
	EndsAt   *time.Time
}

func (s *Service) CreateLivePoll(ctx context.Context, input *CreatePollInput) (*postgres.LivePoll, error) {
	if input.Question == "" {
		return nil, fmt.Errorf("question is required")
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

func (s *Service) VoteOnPoll(ctx context.Context, pollID, userID uuid.UUID, optionID string) error {
	if optionID == "" {
		return fmt.Errorf("option_id is required")
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

func (s *Service) SendGift(ctx context.Context, input *SendGiftInput) (*postgres.LiveGift, error) {
	if input.GiftCount <= 0 {
		input.GiftCount = 1
	}
	g := &postgres.LiveGift{
		StreamID:  input.StreamID,
		SenderID:  input.SenderID,
		GiftType:  input.GiftType,
		GiftCount: input.GiftCount,
		ValueINR:  input.ValueINR,
		Message:   input.Message,
	}
	return s.store.SendGift(ctx, g)
}

func (s *Service) GetStreamGifts(ctx context.Context, streamID uuid.UUID, limit int) ([]postgres.LiveGift, error) {
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	return s.store.GetStreamGifts(ctx, streamID, limit)
}

// --- Moderation ---

func (s *Service) MuteUser(ctx context.Context, streamID, userID, mutedBy uuid.UUID) error {
	return s.store.MuteUser(ctx, streamID, userID, mutedBy)
}

func (s *Service) UnmuteUser(ctx context.Context, streamID, userID uuid.UUID) error {
	return s.store.UnmuteUser(ctx, streamID, userID)
}

func (s *Service) GetMutedUsers(ctx context.Context, streamID uuid.UUID) ([]postgres.LiveMute, error) {
	return s.store.GetMutedUsers(ctx, streamID)
}

func (s *Service) AddWordFilter(ctx context.Context, streamID uuid.UUID, word string, addedBy uuid.UUID) error {
	if word == "" {
		return fmt.Errorf("word is required")
	}
	return s.store.AddWordFilter(ctx, streamID, word, addedBy)
}

func (s *Service) RemoveWordFilter(ctx context.Context, streamID uuid.UUID, word string) error {
	return s.store.RemoveWordFilter(ctx, streamID, word)
}

func (s *Service) GetWordFilters(ctx context.Context, streamID uuid.UUID) ([]postgres.LiveWordFilter, error) {
	return s.store.GetWordFilters(ctx, streamID)
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
