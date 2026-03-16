package service

import (
	"context"
	"time"

	"github.com/atpost/live-service/internal/store/postgres"
	"github.com/google/uuid"
)

// Storer abstracts the data-access layer so that the Service can be tested
// with an in-memory fake and so the concrete *postgres.Store can be swapped
// without changing the Service struct.
type Storer interface {
	// Stream lifecycle
	CreateStream(ctx context.Context, st *postgres.Stream) error
	GetStream(ctx context.Context, id uuid.UUID) (*postgres.Stream, error)
	GetStreamByKey(ctx context.Context, streamKey string) (*postgres.Stream, error)
	ListLiveStreams(ctx context.Context, limit, offset int) ([]postgres.Stream, error)
	ListHostStreams(ctx context.Context, hostID uuid.UUID, limit, offset int) ([]postgres.Stream, error)
	GoLive(ctx context.Context, id uuid.UUID) error
	EndStream(ctx context.Context, id uuid.UUID) error
	UpdateViewerCount(ctx context.Context, id uuid.UUID, currentViewers int) error
	IncrementLikes(ctx context.Context, id uuid.UUID) error

	// Chat
	SendChatMessage(ctx context.Context, msg *postgres.ChatMessage) error
	GetChatMessages(ctx context.Context, streamID uuid.UUID, limit int, before *time.Time) ([]postgres.ChatMessage, error)
	PinMessage(ctx context.Context, messageID uuid.UUID) error

	// Viewer sessions
	JoinStream(ctx context.Context, streamID, userID uuid.UUID) error
	LeaveStream(ctx context.Context, streamID, userID uuid.UUID) error
	GetActiveViewerCount(ctx context.Context, streamID uuid.UUID) (int, error)

	// Scheduled streams
	CreateScheduledStream(ctx context.Context, ss *postgres.ScheduledStream) error
	ListUpcomingStreams(ctx context.Context, limit int) ([]postgres.ScheduledStream, error)

	// Guests
	InviteGuest(ctx context.Context, streamID, userID uuid.UUID, role string) error
	UpdateGuestStatus(ctx context.Context, streamID, userID uuid.UUID, status string) error
	GetStreamGuests(ctx context.Context, streamID uuid.UUID) ([]postgres.LiveGuest, error)

	// Polls
	CreateLivePoll(ctx context.Context, p *postgres.LivePoll) (*postgres.LivePoll, error)
	GetLivePolls(ctx context.Context, streamID uuid.UUID) ([]postgres.LivePoll, error)
	VoteOnPoll(ctx context.Context, pollID, userID uuid.UUID, optionID string) error

	// Gifts
	SendGift(ctx context.Context, g *postgres.LiveGift) (*postgres.LiveGift, error)
	GetStreamGifts(ctx context.Context, streamID uuid.UUID, limit int) ([]postgres.LiveGift, error)
	GetGiftLeaderboard(ctx context.Context, streamID uuid.UUID, limit int) ([]postgres.GiftLeaderboardEntry, error)

	// Moderation
	MuteUser(ctx context.Context, streamID, userID, mutedBy uuid.UUID) error
	UnmuteUser(ctx context.Context, streamID, userID uuid.UUID) error
	GetMutedUsers(ctx context.Context, streamID uuid.UUID) ([]postgres.LiveMute, error)
	IsUserMuted(ctx context.Context, streamID, userID uuid.UUID) (bool, error)
	AddWordFilter(ctx context.Context, streamID uuid.UUID, word string, addedBy uuid.UUID) error
	RemoveWordFilter(ctx context.Context, streamID uuid.UUID, word string) error
	GetWordFilters(ctx context.Context, streamID uuid.UUID) ([]postgres.LiveWordFilter, error)
	MatchesWordFilter(ctx context.Context, streamID uuid.UUID, message string) (bool, error)

	// DVR
	AddDVRSegment(ctx context.Context, seg *postgres.LiveDVRSegment) error
	GetDVRSegments(ctx context.Context, streamID uuid.UUID) ([]postgres.LiveDVRSegment, error)
	ExpireDVRSegments(ctx context.Context, olderThan time.Time) (int64, error)

	// Audio rooms
	CreateAudioRoom(ctx context.Context, r *postgres.AudioRoom) (*postgres.AudioRoom, error)
	GetAudioRoom(ctx context.Context, id uuid.UUID) (*postgres.AudioRoom, error)
	UpdateAudioRoomStatus(ctx context.Context, id uuid.UUID, status string, startedAt, endedAt *time.Time) error
	JoinAudioRoom(ctx context.Context, roomID, userID uuid.UUID, role string) error
	LeaveAudioRoom(ctx context.Context, roomID, userID uuid.UUID) error
	GetAudioRoomMembers(ctx context.Context, roomID uuid.UUID) ([]postgres.AudioRoomMember, error)
	ListLiveAudioRooms(ctx context.Context, limit int) ([]postgres.AudioRoom, error)
}
