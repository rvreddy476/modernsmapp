package service

import (
	"context"
	"encoding/base64"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/atpost/chat-call-service/internal/domain"
	"github.com/atpost/chat-call-service/internal/store/postgres"
	"github.com/google/uuid"
)

// fakeExtras is an in-memory extrasStore.
type fakeExtras struct {
	sessions     map[uuid.UUID]*domain.CallSession
	participants map[[2]uuid.UUID]*domain.CallParticipant
	reminders    []*postgres.CallReminder
	linkTokens   []string
	calls        int
}

func newFakeExtras() *fakeExtras {
	return &fakeExtras{
		sessions:     map[uuid.UUID]*domain.CallSession{},
		participants: map[[2]uuid.UUID]*domain.CallParticipant{},
	}
}

func (f *fakeExtras) addCall(initiator uuid.UUID) uuid.UUID {
	id := uuid.New()
	f.sessions[id] = &domain.CallSession{ID: id, InitiatorUserID: initiator, State: domain.CallStateActive}
	f.addParticipant(id, initiator, domain.JoinStateJoined)
	return id
}

func (f *fakeExtras) addParticipant(callID, userID uuid.UUID, joinState string) {
	f.participants[[2]uuid.UUID{callID, userID}] = &domain.CallParticipant{CallSessionID: callID, UserID: userID, JoinState: joinState}
}

func (f *fakeExtras) GetCallSession(_ context.Context, id uuid.UUID) (*domain.CallSession, error) {
	f.calls++
	return f.sessions[id], nil
}

func (f *fakeExtras) GetParticipant(_ context.Context, callID, userID uuid.UUID) (*domain.CallParticipant, error) {
	f.calls++
	return f.participants[[2]uuid.UUID{callID, userID}], nil
}

func (f *fakeExtras) CreateCallLink(_ context.Context, callID uuid.UUID, token string, _ time.Time) (string, error) {
	f.calls++
	if f.sessions[callID] == nil {
		return "", nil
	}
	f.linkTokens = append(f.linkTokens, token)
	return token, nil
}

func (f *fakeExtras) CreateCallReminder(_ context.Context, r *postgres.CallReminder) (*postgres.CallReminder, error) {
	f.calls++
	f.reminders = append(f.reminders, r)
	return r, nil
}

func (f *fakeExtras) DeleteCallReminder(context.Context, uuid.UUID, uuid.UUID) error {
	f.calls++
	return nil
}

func (f *fakeExtras) GetCallSummary(context.Context, uuid.UUID) (*postgres.CallSummary, error) {
	f.calls++
	return nil, nil
}

func (f *fakeExtras) UpsertCallSummary(_ context.Context, s *postgres.CallSummary) (*postgres.CallSummary, error) {
	f.calls++
	return s, nil
}

func extrasService(f *fakeExtras) *Service {
	return &Service{extras: f, log: slog.New(slog.NewTextHandler(io.Discard, nil))}
}

// Audit L3: link tokens carry 128 bits from crypto/rand, URL-safe.
func TestCallLinkTokenIs128BitURLSafe(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 2000; i++ {
		tok, err := newCallLinkToken()
		if err != nil {
			t.Fatal(err)
		}
		raw, err := base64.RawURLEncoding.DecodeString(tok)
		if err != nil || len(raw) < 16 {
			t.Fatalf("token %q does not carry 128 bits: %d bytes, err=%v", tok, len(raw), err)
		}
		if strings.ContainsAny(tok, "+/=") {
			t.Fatalf("token %q is not URL-safe", tok)
		}
		if !wellFormedCallLinkToken(tok) {
			t.Fatalf("minted token %q is refused by the format check", tok)
		}
		if seen[tok] {
			t.Fatalf("duplicate token %q", tok)
		}
		seen[tok] = true
	}
}

func TestWellFormedCallLinkTokenRefusesLegacyAndJunk(t *testing.T) {
	for _, tok := range []string{
		"", "1a2b3c4d", // legacy uuid[:8]
		uuid.NewString(),
		strings.Repeat("A", callLinkTokenLen-1),
		strings.Repeat("A", callLinkTokenLen+1),
		strings.Repeat("+", callLinkTokenLen),
	} {
		if wellFormedCallLinkToken(tok) {
			t.Errorf("token %q must be refused", tok)
		}
	}
}

func TestGenerateCallLinkStoresMintedToken(t *testing.T) {
	f := newFakeExtras()
	host := uuid.New()
	call := f.addCall(host)

	out, err := extrasService(f).GenerateCallLink(context.Background(), host, call, 0, false)
	if err != nil {
		t.Fatal(err)
	}
	tok, _ := out["link_token"].(string)
	if !wellFormedCallLinkToken(tok) || len(f.linkTokens) != 1 || f.linkTokens[0] != tok {
		t.Fatalf("link token %q was not the stored 128-bit token (stored %v)", tok, f.linkTokens)
	}
}

// Audit L3: lobby_enabled was stored and never enforced. It is refused
// outright, before anything is written.
func TestGenerateCallLinkRefusesLobby(t *testing.T) {
	f := newFakeExtras()
	host := uuid.New()
	call := f.addCall(host)

	_, err := extrasService(f).GenerateCallLink(context.Background(), host, call, 0, true)
	if !errors.Is(err, ErrLobbyNotSupported) {
		t.Fatalf("lobby_enabled=true: err=%v, want ErrLobbyNotSupported", err)
	}
	if len(f.linkTokens) != 0 {
		t.Fatal("a link was created for a lobby request")
	}
}

func TestGenerateCallLinkHostOnly(t *testing.T) {
	f := newFakeExtras()
	call := f.addCall(uuid.New())
	if _, err := extrasService(f).GenerateCallLink(context.Background(), uuid.New(), call, 0, false); !errors.Is(err, ErrNotHost) {
		t.Fatalf("non-host link: err=%v, want ErrNotHost", err)
	}
}

// A malformed (or legacy 8-character) token is refused before the rate
// limiter or the database is touched (both are nil here).
func TestJoinCallByLinkRefusesMalformedToken(t *testing.T) {
	s := extrasService(newFakeExtras())
	for _, tok := range []string{"1a2b3c4d", "", "../../etc"} {
		if _, err := s.JoinCallByLink(context.Background(), uuid.New(), tok); !errors.Is(err, ErrLinkNotFound) {
			t.Fatalf("token %q: err=%v, want ErrLinkNotFound", tok, err)
		}
	}
}

// Audit L4: reminders only for calls the user is part of.
func TestSetCallReminderRequiresParticipant(t *testing.T) {
	f := newFakeExtras()
	host, member, stranger, removed := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	call := f.addCall(host)
	f.addParticipant(call, member, domain.JoinStateNotJoined)
	f.addParticipant(call, removed, domain.JoinStateRemoved)
	s := extrasService(f)
	at := time.Now().Add(time.Hour)

	for name, user := range map[string]uuid.UUID{"stranger": stranger, "removed": removed} {
		if _, err := s.SetCallReminder(context.Background(), user, call, at); !errors.Is(err, ErrNotParticipant) {
			t.Fatalf("%s: err=%v, want ErrNotParticipant", name, err)
		}
	}
	if len(f.reminders) != 0 {
		t.Fatalf("a non-participant created %d reminder rows", len(f.reminders))
	}

	if _, err := s.SetCallReminder(context.Background(), member, call, at); err != nil {
		t.Fatalf("participant reminder: %v", err)
	}
	if len(f.reminders) != 1 || f.reminders[0].UserID != member {
		t.Fatalf("participant reminder not stored: %+v", f.reminders)
	}
}

func TestSetCallReminderUnknownCall(t *testing.T) {
	if _, err := extrasService(newFakeExtras()).SetCallReminder(context.Background(), uuid.New(), uuid.New(), time.Now()); !errors.Is(err, ErrCallNotFound) {
		t.Fatalf("unknown call: err=%v, want ErrCallNotFound", err)
	}
}
