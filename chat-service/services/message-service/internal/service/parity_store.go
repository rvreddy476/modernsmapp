package service

import (
	"context"
	"time"

	"github.com/atpost/chat-message-service/internal/store/postgres"
	"github.com/google/uuid"
)

// parityStore is the chat-parity surface of the Postgres store (lane B1).
// It is reached by a checked type assertion, so the in-memory fakes the
// older tests use keep compiling and simply skip the parity behaviour.
type parityStore interface {
	ListInboxConversations(ctx context.Context, userID uuid.UUID, lane string, pinned bool, limit int, cursorTs *time.Time, cursorID *uuid.UUID) ([]postgres.Conversation, error)
	GetInboxMeta(ctx context.Context, userID uuid.UUID, convIDs []uuid.UUID) (map[uuid.UUID]postgres.InboxMeta, error)
	GetOtherMembersReadCursors(ctx context.Context, viewerID uuid.UUID, convIDs []uuid.UUID) (map[uuid.UUID]map[uuid.UUID]postgres.ReadCursor, error)
	ClearForMe(ctx context.Context, conversationID, userID uuid.UUID, at time.Time, hide bool) error
	GetMemberCutoff(ctx context.Context, conversationID, userID uuid.UUID) (*time.Time, error)
	ResetUnread(ctx context.Context, conversationID, userID uuid.UUID) error
	HideMessageForMe(ctx context.Context, userID, conversationID, messageID uuid.UUID) error
	HiddenMessageIDs(ctx context.Context, userID, conversationID uuid.UUID, ids []uuid.UUID) (map[uuid.UUID]bool, error)
	LookupMessageTS(ctx context.Context, conversationID, messageID uuid.UUID) (string, time.Time, uuid.UUID, bool, error)
	ApplyDeliveryParity(ctx context.Context, d postgres.DeliveryParity) error
	GetStreaks(ctx context.Context, convIDs []uuid.UUID) (map[uuid.UUID]postgres.StreakState, error)
	RemoveSharedItem(ctx context.Context, conversationID, messageID uuid.UUID) error
	ListSharedItems(ctx context.Context, conversationID, viewerID uuid.UUID, kind string, cutoff *time.Time, beforeTs *time.Time, beforeID *uuid.UUID, limit int) ([]postgres.SharedItem, error)
	ReplaceNote(ctx context.Context, n postgres.Note) error
	DeleteNotes(ctx context.Context, userID uuid.UUID) error
	GetNote(ctx context.Context, id uuid.UUID, now time.Time) (*postgres.Note, error)
	ListLiveNotes(ctx context.Context, userIDs []uuid.UUID, now time.Time, limit int) ([]postgres.Note, error)
	DirectPeerIDs(ctx context.Context, userID uuid.UUID, limit int) ([]uuid.UUID, error)
	ListStickerCatalogue(ctx context.Context) ([]postgres.StickerPack, error)
	GetActiveSticker(ctx context.Context, id uuid.UUID) (*postgres.Sticker, error)
	UpsertStickerPack(ctx context.Context, id uuid.UUID, name string, sortOrder int, active bool) error
	UpsertSticker(ctx context.Context, st postgres.Sticker, active bool) error
	SearchMyConversations(ctx context.Context, userID uuid.UUID, q string, limit int) ([]uuid.UUID, error)
	GetMemberSearchStates(ctx context.Context, userID uuid.UUID, convIDs []uuid.UUID) (map[uuid.UUID]postgres.MemberSearchState, error)
	HiddenAmong(ctx context.Context, userID uuid.UUID, messageIDs []uuid.UUID) (map[uuid.UUID]bool, error)
	ListRequestEnvelopes(ctx context.Context, userID uuid.UUID, limit int) ([]postgres.RequestEnvelope, error)
	GetProfileExtras(ctx context.Context, ids []uuid.UUID) (map[uuid.UUID]postgres.ProfileExtras, error)
	SetProfileExtras(ctx context.Context, userID uuid.UUID, username string, createdAt *time.Time) error
}

func (s *Service) parity() (parityStore, bool) {
	ps, ok := s.convStore.(parityStore)
	return ps, ok
}

// clock is the service's notion of now (tests pin it).
func (s *Service) clock() time.Time {
	if s.nowFn != nil {
		return s.nowFn()
	}
	return time.Now()
}

// SetClock pins the service clock (tests and fixtures only).
func (s *Service) SetClock(now func() time.Time) { s.nowFn = now }
