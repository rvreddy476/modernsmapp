package service

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/atpost/chat-call-service/internal/store/postgres"
	"github.com/atpost/chat-shared/callauth"
	"github.com/google/uuid"
)

type fakePairs struct {
	pairs []postgres.SignalingPair
	err   error
}

func (f fakePairs) ListActiveSignalingPairs(context.Context) ([]postgres.SignalingPair, error) {
	return f.pairs, f.err
}

type refreshCall struct {
	a, b, call uuid.UUID
	ttl        time.Duration
}

func quietLogger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

// Audit M6: every pair the database lists as active is re-armed for a full
// callauth.DefaultTTL, so a call longer than an hour keeps its signaling.
func TestSignalingRefreshReArmsEveryActivePair(t *testing.T) {
	p1 := postgres.SignalingPair{CallID: uuid.New(), InitiatorID: uuid.New(), ParticipantID: uuid.New()}
	p2 := postgres.SignalingPair{CallID: uuid.New(), InitiatorID: uuid.New(), ParticipantID: uuid.New()}
	var got []refreshCall
	refresh := func(_ context.Context, a, b, call uuid.UUID, ttl time.Duration) (bool, error) {
		got = append(got, refreshCall{a, b, call, ttl})
		return call != p2.CallID, nil // p2's key is already gone
	}
	w := newSignalingAuthRefresher(fakePairs{pairs: []postgres.SignalingPair{p1, p2}}, refresh, quietLogger(), 0)

	if n := w.sweep(context.Background()); n != 1 {
		t.Fatalf("extended %d keys, want 1", n)
	}
	want := []refreshCall{
		{p1.InitiatorID, p1.ParticipantID, p1.CallID, callauth.DefaultTTL},
		{p2.InitiatorID, p2.ParticipantID, p2.CallID, callauth.DefaultTTL},
	}
	if len(got) != len(want) {
		t.Fatalf("refresh calls = %+v, want %+v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("refresh %d = %+v, want %+v", i, got[i], want[i])
		}
	}
	if w.interval != DefaultSignalingRefreshInterval || w.interval >= callauth.DefaultTTL {
		t.Fatalf("refresh interval %v must be shorter than the key TTL %v", w.interval, callauth.DefaultTTL)
	}
}

// When the call list cannot be read nothing is refreshed: an outage lets
// keys expire rather than extending pairs nobody vouched for.
func TestSignalingRefreshListFailureRefreshesNothing(t *testing.T) {
	called := false
	refresh := func(context.Context, uuid.UUID, uuid.UUID, uuid.UUID, time.Duration) (bool, error) {
		called = true
		return true, nil
	}
	w := newSignalingAuthRefresher(fakePairs{
		pairs: []postgres.SignalingPair{{CallID: uuid.New()}},
		err:   errors.New("db down"),
	}, refresh, quietLogger(), time.Minute)
	if n := w.sweep(context.Background()); n != 0 || called {
		t.Fatalf("refreshed %d pairs (called=%v) after a listing failure", n, called)
	}
}

func TestSignalingRefreshContinuesPastOneFailure(t *testing.T) {
	bad := postgres.SignalingPair{CallID: uuid.New(), InitiatorID: uuid.New(), ParticipantID: uuid.New()}
	good := postgres.SignalingPair{CallID: uuid.New(), InitiatorID: uuid.New(), ParticipantID: uuid.New()}
	refresh := func(_ context.Context, _, _, call uuid.UUID, _ time.Duration) (bool, error) {
		if call == bad.CallID {
			return false, errors.New("redis blip")
		}
		return true, nil
	}
	w := newSignalingAuthRefresher(fakePairs{pairs: []postgres.SignalingPair{bad, good}}, refresh, quietLogger(), time.Minute)
	if n := w.sweep(context.Background()); n != 1 {
		t.Fatalf("extended %d keys, want 1 (the good pair)", n)
	}
}
