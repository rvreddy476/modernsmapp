package service

import (
	"context"
	"io"
	"log/slog"
	"testing"

	"github.com/atpost/chat-call-service/internal/domain"
	"github.com/google/uuid"
)

type fakeMute struct {
	muted     bool
	gotCallee uuid.UUID
	gotCaller uuid.UUID
	gotSource *uuid.UUID
	calls     int
}

func (f *fakeMute) CallsMuted(_ context.Context, callee, caller uuid.UUID, source *uuid.UUID) bool {
	f.calls++
	f.gotCallee, f.gotCaller, f.gotSource = callee, caller, source
	return f.muted
}

func muteTestService(c CallMuteChecker) *Service {
	s := &Service{log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	if c != nil {
		s.WithCallMuteChecker(c)
	}
	return s
}

func TestShouldRingSkipsMutedMember(t *testing.T) {
	caller, callee, conv := uuid.New(), uuid.New(), uuid.New()
	f := &fakeMute{muted: true}
	if muteTestService(f).shouldRing(context.Background(), caller, callee, domain.SourceTypeChat, &conv) {
		t.Fatal("a member with mute_calls on must not be rung")
	}
	if f.gotCallee != callee || f.gotCaller != caller || f.gotSource == nil || *f.gotSource != conv {
		t.Fatalf("checker asked the wrong question: %+v", f)
	}
}

func TestShouldRingRingsUnmutedAndWithoutChecker(t *testing.T) {
	caller, callee := uuid.New(), uuid.New()
	if !muteTestService(&fakeMute{}).shouldRing(context.Background(), caller, callee, domain.SourceTypeChat, nil) {
		t.Fatal("unmuted member must ring")
	}
	if !muteTestService(nil).shouldRing(context.Background(), caller, callee, domain.SourceTypeChat, nil) {
		t.Fatal("no checker configured must ring")
	}
}

// Only a chat-sourced call names a source conversation; any other source id
// (a profile, a circle) is not a conversation and is not passed on. The DM
// between the pair is still checked by the reader.
func TestShouldRingPassesSourceConversationOnlyForChat(t *testing.T) {
	f := &fakeMute{}
	other := uuid.New()
	muteTestService(f).shouldRing(context.Background(), uuid.New(), uuid.New(), domain.SourceTypeProfile, &other)
	if f.calls != 1 || f.gotSource != nil {
		t.Fatalf("non-chat source leaked as a conversation id: %+v", f)
	}
}
