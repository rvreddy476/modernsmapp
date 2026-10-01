package service

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
)

// fakeStore is the in-memory store in fakes_test.go (memStore).

func newModerationService(store Store) *Service {
	return &Service{
		store:   store,
		livekit: fakeLiveKit{},
		graph:   &fakeGraph{},
		redis:   nil,
	}
}

// TestSendChat_Muted — once a user is muted, SendChat returns
// ErrChatMuted before persisting or fanning out.
func TestSendChat_Muted(t *testing.T) {
	store := newFakeStore()
	creator := uuid.New()
	viewer := uuid.New()
	st := store.AddStream(creator)
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
	if len(store.Messages) != 0 {
		t.Fatalf("muted SendChat must not persist; got %d messages", len(store.Messages))
	}
}

// TestSendChat_WordFilter — word filter substring match blocks the
// message with ErrChatBlockedWord (case-insensitive).
func TestSendChat_WordFilter(t *testing.T) {
	store := newFakeStore()
	creator := uuid.New()
	viewer := uuid.New()
	st := store.AddStream(creator)
	svc := newModerationService(store)

	if err := svc.AddWordFilter(context.Background(), st.ID, creator, "Spam"); err != nil {
		t.Fatalf("AddWordFilter: %v", err)
	}
	_, err := svc.SendChat(context.Background(), st.ID, viewer, "this is SPAMMY content")
	if !errors.Is(err, ErrChatBlockedWord) {
		t.Fatalf("expected ErrChatBlockedWord, got %v", err)
	}
	if len(store.Messages) != 0 {
		t.Fatalf("filtered SendChat must not persist; got %d messages", len(store.Messages))
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
	st := store.AddStream(creator)
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
	if store.Messages[msg1.ID].IsPinned {
		t.Fatalf("expected msg1 to be unpinned after re-pin")
	}
}

// TestMute_NonCreator — only the host or a stream moderator may mute. Any
// other user gets ErrNotModerator and no mute is recorded.
func TestMute_NonCreator(t *testing.T) {
	store := newFakeStore()
	creator := uuid.New()
	attacker := uuid.New()
	victim := uuid.New()
	st := store.AddStream(creator)
	svc := newModerationService(store)

	err := svc.Mute(context.Background(), st.ID, attacker, victim)
	if !errors.Is(err, ErrNotModerator) {
		t.Fatalf("expected ErrNotModerator, got %v", err)
	}
	if m, ok := store.Mutes[st.ID]; ok && m[victim] {
		t.Fatalf("non-creator must not be able to mute")
	}
}

// TestPinMessage_WrongStream — PinMessage on a message that doesn't
// belong to the stream returns ErrMessageNotFound.
func TestPinMessage_WrongStream(t *testing.T) {
	store := newFakeStore()
	creator := uuid.New()
	st := store.AddStream(creator)
	svc := newModerationService(store)

	err := svc.PinMessage(context.Background(), st.ID, creator, uuid.New())
	if !errors.Is(err, ErrMessageNotFound) {
		t.Fatalf("expected ErrMessageNotFound, got %v", err)
	}
}
