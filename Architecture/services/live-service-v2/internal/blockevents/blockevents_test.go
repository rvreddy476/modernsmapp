package blockevents

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"

	"github.com/google/uuid"
)

type recHandler struct {
	calls []Block
	fail  int // fail this many calls first
}

func (h *recHandler) OnUserBlocked(_ context.Context, blocker, blocked uuid.UUID) error {
	h.calls = append(h.calls, Block{BlockerID: blocker, BlockedID: blocked})
	if h.fail > 0 {
		h.fail--
		return errors.New("store down")
	}
	return nil
}

var quiet = slog.New(slog.NewTextHandler(io.Discard, nil))

func blockMsg(blocker, blocked string) []byte {
	return []byte(`{"event_id":"e1","event_type":"UserBlocked","payload":{"blocker_id":"` + blocker +
		`","blocked_id":"` + blocked + `","blocked_at":"2026-11-07T10:00:00Z"}}`)
}

func TestDecode(t *testing.T) {
	a, b := uuid.New(), uuid.New()
	got, ok, err := Decode(blockMsg(a.String(), b.String()))
	if !ok || err != nil || got.BlockerID != a || got.BlockedID != b {
		t.Fatalf("UserBlocked: %+v %v %v", got, ok, err)
	}
	for name, raw := range map[string]string{
		"other event":  `{"event_type":"UserFollowed","payload":{"follower_id":"x"}}`,
		"unblock":      `{"event_type":"UserUnblocked","payload":{"blocker_id":"` + a.String() + `","blocked_id":"` + b.String() + `"}}`,
		"not json":     `not json`,
		"bare payload": `{"blocker_id":"` + a.String() + `","blocked_id":"` + b.String() + `"}`,
	} {
		if _, ok, err := Decode([]byte(raw)); ok || err != nil {
			t.Fatalf("%s: ok=%v err=%v, want skipped", name, ok, err)
		}
	}
	for name, raw := range map[string][]byte{
		"bad blocker": blockMsg("nope", b.String()),
		"nil blocked": blockMsg(a.String(), uuid.Nil.String()),
		"self block":  blockMsg(a.String(), a.String()),
		"bad payload": []byte(`{"event_type":"UserBlocked","payload":"x"}`),
	} {
		if _, ok, err := Decode(raw); !ok || !errors.Is(err, ErrMalformed) {
			t.Fatalf("%s: ok=%v err=%v, want malformed", name, ok, err)
		}
	}
}

func TestApplyHandsTheBlockOverAndRetries(t *testing.T) {
	a, b := uuid.New(), uuid.New()
	h := &recHandler{fail: Attempts - 1}
	if !Apply(context.Background(), h, blockMsg(a.String(), b.String()), 0, quiet) {
		t.Fatal("not applied after transient failures")
	}
	if len(h.calls) != Attempts || h.calls[0] != (Block{BlockerID: a, BlockedID: b}) {
		t.Fatalf("calls = %+v", h.calls)
	}

	// Given up after Attempts, never forever.
	h = &recHandler{fail: 100}
	if Apply(context.Background(), h, blockMsg(a.String(), b.String()), 0, quiet) {
		t.Fatal("reported applied")
	}
	if len(h.calls) != Attempts {
		t.Fatalf("calls = %d, want %d", len(h.calls), Attempts)
	}

	// Anything else never reaches the handler.
	h = &recHandler{}
	Apply(context.Background(), h, []byte(`{"event_type":"PostCreated","payload":{}}`), 0, quiet)
	Apply(context.Background(), h, blockMsg("x", b.String()), 0, quiet)
	if len(h.calls) != 0 {
		t.Fatalf("calls = %+v", h.calls)
	}
}
