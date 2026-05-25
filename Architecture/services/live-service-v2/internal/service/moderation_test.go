package service

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/atpost/live-service-v2/internal/store/postgres"
)

// fakeStore is an in-memory stand-in for *postgres.Store. Only the
// behaviour the Phase B moderation tests exercise is implemented;
// the rest panics so a misuse is loud.
type fakeStore struct {
	streams     map[uuid.UUID]*postgres.LiveStream
	mutes       map[uuid.UUID]map[uuid.UUID]bool
	wordFilters map[uuid.UUID]map[string]bool
	messages    map[uuid.UUID]*postgres.ChatMessage
	pinned      map[uuid.UUID]uuid.UUID
}

func newFakeStore() *fakeStore {
	return &fakeStore{
		streams:     map[uuid.UUID]*postgres.LiveStream{},
		mutes:       map[uuid.UUID]map[uuid.UUID]bool{},
		wordFilters: map[uuid.UUID]map[string]bool{},
		messages:    map[uuid.UUID]*postgres.ChatMessage{},
		pinned:      map[uuid.UUID]uuid.UUID{},
	}
}

func (f *fakeStore) addStream(creator uuid.UUID) *postgres.LiveStream {
	st := &postgres.LiveStream{
		ID:            uuid.New(),
		CreatorUserID: creator,
		Status:        "live",
		Visibility:    visibilityPublic,
		LiveKitRoom:   "stream_" + uuid.NewString(),
	}
	f.streams[st.ID] = st
	return st
}

func (f *fakeStore) GetByID(_ context.Context, id uuid.UUID) (*postgres.LiveStream, error) {
	st, ok := f.streams[id]
	if !ok {
		return nil, postgres.ErrNotFound
	}
	return st, nil
}

func (f *fakeStore) MuteUser(_ context.Context, streamID, userID, _ uuid.UUID) error {
	if _, ok := f.mutes[streamID]; !ok {
		f.mutes[streamID] = map[uuid.UUID]bool{}
	}
	f.mutes[streamID][userID] = true
	return nil
}

func (f *fakeStore) UnmuteUser(_ context.Context, streamID, userID uuid.UUID) error {
	if m, ok := f.mutes[streamID]; ok {
		delete(m, userID)
	}
	return nil
}

func (f *fakeStore) IsUserMuted(_ context.Context, streamID, userID uuid.UUID) (bool, error) {
	if m, ok := f.mutes[streamID]; ok {
		return m[userID], nil
	}
	return false, nil
}

func (f *fakeStore) ListMutedUsers(_ context.Context, streamID uuid.UUID) ([]uuid.UUID, error) {
	out := []uuid.UUID{}
	for u := range f.mutes[streamID] {
		out = append(out, u)
	}
	return out, nil
}

func (f *fakeStore) AddWordFilter(_ context.Context, streamID uuid.UUID, word string, _ uuid.UUID) error {
	if _, ok := f.wordFilters[streamID]; !ok {
		f.wordFilters[streamID] = map[string]bool{}
	}
	f.wordFilters[streamID][strings.ToLower(strings.TrimSpace(word))] = true
	return nil
}

func (f *fakeStore) RemoveWordFilter(_ context.Context, streamID uuid.UUID, word string) error {
	if m, ok := f.wordFilters[streamID]; ok {
		delete(m, strings.ToLower(strings.TrimSpace(word)))
	}
	return nil
}

func (f *fakeStore) ListWordFilters(_ context.Context, streamID uuid.UUID) ([]string, error) {
	out := []string{}
	for w := range f.wordFilters[streamID] {
		out = append(out, w)
	}
	return out, nil
}

func (f *fakeStore) MatchesWordFilter(_ context.Context, streamID uuid.UUID, text string) (bool, error) {
	lt := strings.ToLower(text)
	for w := range f.wordFilters[streamID] {
		if strings.Contains(lt, w) {
			return true, nil
		}
	}
	return false, nil
}

func (f *fakeStore) InsertChatMessage(_ context.Context, streamID, userID uuid.UUID, text string) (*postgres.ChatMessage, error) {
	msg := &postgres.ChatMessage{
		ID:        uuid.New(),
		StreamID:  streamID,
		UserID:    userID,
		Text:      text,
		CreatedAt: time.Now(),
	}
	f.messages[msg.ID] = msg
	return msg, nil
}

func (f *fakeStore) ListRecentChatMessages(_ context.Context, streamID uuid.UUID, _ int) ([]*postgres.ChatMessage, error) {
	out := []*postgres.ChatMessage{}
	for _, m := range f.messages {
		if m.StreamID == streamID {
			out = append(out, m)
		}
	}
	return out, nil
}

func (f *fakeStore) PinMessage(_ context.Context, streamID, messageID uuid.UUID) error {
	msg, ok := f.messages[messageID]
	if !ok || msg.StreamID != streamID {
		return postgres.ErrNotFound
	}
	// Unpin any existing.
	if prev, ok := f.pinned[streamID]; ok {
		if pm, ok := f.messages[prev]; ok {
			pm.IsPinned = false
			pm.PinnedAt = nil
		}
	}
	now := time.Now()
	msg.IsPinned = true
	msg.PinnedAt = &now
	f.pinned[streamID] = messageID
	return nil
}

func (f *fakeStore) UnpinMessage(_ context.Context, streamID, messageID uuid.UUID) error {
	if msg, ok := f.messages[messageID]; ok && msg.StreamID == streamID {
		msg.IsPinned = false
		msg.PinnedAt = nil
	}
	if f.pinned[streamID] == messageID {
		delete(f.pinned, streamID)
	}
	return nil
}

func (f *fakeStore) GetPinnedMessage(_ context.Context, streamID uuid.UUID) (*postgres.ChatMessage, error) {
	id, ok := f.pinned[streamID]
	if !ok {
		return nil, nil
	}
	return f.messages[id], nil
}

// Unused-by-tests but required by the Store interface.
func (f *fakeStore) CreateStream(_ context.Context, _ postgres.CreateStreamParams) (*postgres.LiveStream, error) {
	panic("not implemented")
}
func (f *fakeStore) MarkLive(_ context.Context, _ uuid.UUID, _ string) (*postgres.LiveStream, error) {
	panic("not implemented")
}
func (f *fakeStore) MarkEnded(_ context.Context, _ uuid.UUID, _ int) (*postgres.LiveStream, error) {
	panic("not implemented")
}
func (f *fakeStore) SetRecording(_ context.Context, _ uuid.UUID, _ string, _ int) (*postgres.LiveStream, error) {
	panic("not implemented")
}
func (f *fakeStore) ListLive(_ context.Context, _ postgres.ListLiveParams) ([]*postgres.LiveStream, error) {
	panic("not implemented")
}
func (f *fakeStore) RecordViewerEvent(_ context.Context, _, _ uuid.UUID, _ string) error {
	return nil
}

// Compile-time guard so a future change to the Store interface fails
// here loudly instead of at the call site.
var _ Store = (*fakeStore)(nil)

func newModerationService(store Store) *Service {
	return &Service{
		store:    store,
		livekit:  fakeLiveKit{},
		graph:    &fakeGraph{},
		producer: nil,
		redis:    nil,
	}
}

// TestSendChat_Muted — once a user is muted, SendChat returns
// ErrChatMuted before persisting or fanning out.
func TestSendChat_Muted(t *testing.T) {
	store := newFakeStore()
	creator := uuid.New()
	viewer := uuid.New()
	st := store.addStream(creator)
	svc := newModerationService(store)

	// Mute the viewer (creator action).
	if err := svc.Mute(context.Background(), st.ID, creator, viewer); err != nil {
		t.Fatalf("Mute: %v", err)
	}
	// Viewer now tries to chat.
	_, err := svc.SendChat(context.Background(), st.ID, viewer, "hello world")
	if !errors.Is(err, ErrChatMuted) {
		t.Fatalf("expected ErrChatMuted, got %v", err)
	}
	if len(store.messages) != 0 {
		t.Fatalf("muted SendChat must not persist; got %d messages", len(store.messages))
	}
}

// TestSendChat_WordFilter — word filter substring match blocks the
// message with ErrChatBlockedWord (case-insensitive).
func TestSendChat_WordFilter(t *testing.T) {
	store := newFakeStore()
	creator := uuid.New()
	viewer := uuid.New()
	st := store.addStream(creator)
	svc := newModerationService(store)

	if err := svc.AddWordFilter(context.Background(), st.ID, creator, "Spam"); err != nil {
		t.Fatalf("AddWordFilter: %v", err)
	}
	_, err := svc.SendChat(context.Background(), st.ID, viewer, "this is SPAMMY content")
	if !errors.Is(err, ErrChatBlockedWord) {
		t.Fatalf("expected ErrChatBlockedWord, got %v", err)
	}
	if len(store.messages) != 0 {
		t.Fatalf("filtered SendChat must not persist; got %d messages", len(store.messages))
	}
	// A clean message still goes through.
	if _, err := svc.SendChat(context.Background(), st.ID, viewer, "clean message"); err != nil {
		t.Fatalf("clean SendChat: %v", err)
	}
}

// TestPin_ServiceFlow — pinning a message stores it and GetPinnedMessage
// returns the same row. A subsequent pin replaces the prior one.
func TestPin_ServiceFlow(t *testing.T) {
	store := newFakeStore()
	creator := uuid.New()
	viewer := uuid.New()
	st := store.addStream(creator)
	svc := newModerationService(store)

	msg1, err := svc.SendChat(context.Background(), st.ID, viewer, "first")
	if err != nil {
		t.Fatalf("SendChat 1: %v", err)
	}
	msg2, err := svc.SendChat(context.Background(), st.ID, viewer, "second")
	if err != nil {
		t.Fatalf("SendChat 2: %v", err)
	}

	if err := svc.PinMessage(context.Background(), st.ID, creator, msg1.ID); err != nil {
		t.Fatalf("PinMessage 1: %v", err)
	}
	got, err := svc.GetPinnedMessage(context.Background(), st.ID)
	if err != nil {
		t.Fatalf("GetPinnedMessage: %v", err)
	}
	if got == nil || got.ID != msg1.ID || !got.IsPinned {
		t.Fatalf("expected msg1 pinned; got %+v", got)
	}

	// Pinning msg2 must replace msg1 as the active pin.
	if err := svc.PinMessage(context.Background(), st.ID, creator, msg2.ID); err != nil {
		t.Fatalf("PinMessage 2: %v", err)
	}
	got2, err := svc.GetPinnedMessage(context.Background(), st.ID)
	if err != nil {
		t.Fatalf("GetPinnedMessage 2: %v", err)
	}
	if got2 == nil || got2.ID != msg2.ID {
		t.Fatalf("expected msg2 pinned; got %+v", got2)
	}
	if store.messages[msg1.ID].IsPinned {
		t.Fatalf("expected msg1 to be unpinned after re-pin")
	}
}

// TestMute_NonCreator — only the creator may mute. A different user
// gets ErrNotCreator and no mute is recorded.
func TestMute_NonCreator(t *testing.T) {
	store := newFakeStore()
	creator := uuid.New()
	attacker := uuid.New()
	victim := uuid.New()
	st := store.addStream(creator)
	svc := newModerationService(store)

	err := svc.Mute(context.Background(), st.ID, attacker, victim)
	if !errors.Is(err, ErrNotCreator) {
		t.Fatalf("expected ErrNotCreator, got %v", err)
	}
	if m, ok := store.mutes[st.ID]; ok && m[victim] {
		t.Fatalf("non-creator must not be able to mute")
	}
}

// TestPinMessage_WrongStream — PinMessage on a message that doesn't
// belong to the stream returns ErrMessageNotFound.
func TestPinMessage_WrongStream(t *testing.T) {
	store := newFakeStore()
	creator := uuid.New()
	st := store.addStream(creator)
	svc := newModerationService(store)

	err := svc.PinMessage(context.Background(), st.ID, creator, uuid.New())
	if !errors.Is(err, ErrMessageNotFound) {
		t.Fatalf("expected ErrMessageNotFound, got %v", err)
	}
}
