package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/atpost/chat-message-service/internal/store/postgres"
	"github.com/google/uuid"
)

func TestWithIdempotency_RequiresKey(t *testing.T) {
	svc := &Service{convStore: &idempotencyConvStoreStub{}}
	_, err := withIdempotency(context.Background(), svc, "", map[string]string{"a": "b"}, func() (*MessageResponse, error) {
		return &MessageResponse{}, nil
	})
	if !errors.Is(err, ErrIdempotencyKeyRequired) {
		t.Fatalf("expected ErrIdempotencyKeyRequired, got %v", err)
	}
}

func TestWithIdempotency_ReturnsCachedResponse(t *testing.T) {
	payload := map[string]string{"hello": "world"}
	hash, err := hashRequestPayload(payload)
	if err != nil {
		t.Fatalf("hashRequestPayload: %v", err)
	}
	cached := []byte(`{"conversation_id":"7d16ea6b-8799-4289-a4dc-fd77fb2d9dd8","msg_id":"f27483da-5e8b-4307-9046-17b7f6622db7","sender_id":"92a2c71e-b8fd-4328-b0aa-df5ac3e7f6e7","type":"text","text":"hello","created_at":"2026-02-16T00:00:00Z"}`)
	store := &idempotencyConvStoreStub{
		createResult: false,
		checkResult: &postgres.IdempotencyResult{
			RequestHash: hash,
			Response:    cached,
		},
	}
	svc := &Service{convStore: store}
	called := false
	resp, err := withIdempotency(context.Background(), svc, "idem-1", payload, func() (*MessageResponse, error) {
		called = true
		return nil, nil
	})
	if err != nil {
		t.Fatalf("withIdempotency returned error: %v", err)
	}
	if called {
		t.Fatal("expected exec not to be called when cached response exists")
	}
	if resp == nil || resp.MsgID.String() != "f27483da-5e8b-4307-9046-17b7f6622db7" {
		t.Fatalf("unexpected cached response: %+v", resp)
	}
}

func TestWithIdempotency_ConflictOnDifferentRequest(t *testing.T) {
	payload := map[string]string{"hello": "world"}
	store := &idempotencyConvStoreStub{
		createResult: false,
		checkResult: &postgres.IdempotencyResult{
			RequestHash: "different-hash",
			Response:    []byte(`{"ok":true}`),
		},
	}
	svc := &Service{convStore: store}
	_, err := withIdempotency(context.Background(), svc, "idem-1", payload, func() (*MessageResponse, error) {
		return &MessageResponse{}, nil
	})
	if !errors.Is(err, ErrIdempotencyConflict) {
		t.Fatalf("expected ErrIdempotencyConflict, got %v", err)
	}
}

type idempotencyConvStoreStub struct {
	createResult bool
	createErr    error
	checkResult  *postgres.IdempotencyResult
	checkErr     error
	saveErr      error
	releaseErr   error

	releaseCalled int
}

func (s *idempotencyConvStoreStub) CreateDirectConversation(ctx context.Context, userA, userB, createdBy uuid.UUID) (uuid.UUID, error) {
	return uuid.Nil, errors.New("not implemented")
}
func (s *idempotencyConvStoreStub) CreateGroupConversation(ctx context.Context, creatorID uuid.UUID, title string, memberIDs []uuid.UUID) (uuid.UUID, error) {
	return uuid.Nil, errors.New("not implemented")
}
func (s *idempotencyConvStoreStub) GetConversation(ctx context.Context, id uuid.UUID) (*postgres.Conversation, error) {
	return nil, errors.New("not implemented")
}
func (s *idempotencyConvStoreStub) TouchConversation(ctx context.Context, id uuid.UUID, ts time.Time) error {
	return errors.New("not implemented")
}
func (s *idempotencyConvStoreStub) ListConversationsByUser(ctx context.Context, userID uuid.UUID, limit int, cursorUpdatedAt *time.Time, cursorID *uuid.UUID) ([]postgres.Conversation, error) {
	return nil, errors.New("not implemented")
}
func (s *idempotencyConvStoreStub) CheckMembership(ctx context.Context, conversationID, userID uuid.UUID) (bool, error) {
	return false, errors.New("not implemented")
}
func (s *idempotencyConvStoreStub) GetMembers(ctx context.Context, conversationID uuid.UUID) ([]postgres.Member, error) {
	return nil, errors.New("not implemented")
}
func (s *idempotencyConvStoreStub) GetMemberRole(ctx context.Context, conversationID, userID uuid.UUID) (string, error) {
	return "", errors.New("not implemented")
}
func (s *idempotencyConvStoreStub) AddMember(ctx context.Context, conversationID, userID uuid.UUID, role string) error {
	return errors.New("not implemented")
}
func (s *idempotencyConvStoreStub) RemoveMember(ctx context.Context, conversationID, userID uuid.UUID) (bool, error) {
	return false, errors.New("not implemented")
}
func (s *idempotencyConvStoreStub) UpdateTitle(ctx context.Context, conversationID uuid.UUID, title string) error {
	return errors.New("not implemented")
}
func (s *idempotencyConvStoreStub) InsertOutboxEvent(ctx context.Context, eventType string, payload interface{}) error {
	return errors.New("not implemented")
}
func (s *idempotencyConvStoreStub) FetchUnpublishedOutboxEvents(ctx context.Context, limit int) ([]postgres.OutboxEvent, error) {
	return nil, errors.New("not implemented")
}
func (s *idempotencyConvStoreStub) MarkOutboxEventPublished(ctx context.Context, id int64) error {
	return errors.New("not implemented")
}
func (s *idempotencyConvStoreStub) CheckIdempotencyKey(ctx context.Context, key string) (*postgres.IdempotencyResult, error) {
	return s.checkResult, s.checkErr
}
func (s *idempotencyConvStoreStub) CreateIdempotencyKey(ctx context.Context, key, requestHash string) (bool, error) {
	return s.createResult, s.createErr
}
func (s *idempotencyConvStoreStub) SaveIdempotencyResponse(ctx context.Context, key, requestHash string, response interface{}) error {
	return s.saveErr
}
func (s *idempotencyConvStoreStub) ReleaseIdempotencyKey(ctx context.Context, key, requestHash string) error {
	s.releaseCalled++
	return s.releaseErr
}
func (s *idempotencyConvStoreStub) UpsertUserProfile(ctx context.Context, userID uuid.UUID, displayName string, avatarMediaID *uuid.UUID) error {
	return errors.New("not implemented")
}
func (s *idempotencyConvStoreStub) GetUserProfiles(ctx context.Context, userIDs []uuid.UUID) (map[uuid.UUID]postgres.UserProfile, error) {
	return nil, errors.New("not implemented")
}
