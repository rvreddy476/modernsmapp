package paymentevents

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
)

// fakeTx stands in for a database transaction: writes are staged and only
// become visible to the inbox on commit, so a rolled-back claim is forgotten.
type fakeTx struct {
	staged []string
}

type fakeInbox struct {
	committed map[string]bool
	claims    []Claim
	err       error
}

func (f *fakeInbox) Claim(_ context.Context, tx *fakeTx, c Claim) (bool, error) {
	f.claims = append(f.claims, c)
	if f.err != nil {
		return false, f.err
	}
	if f.committed[c.EventID] {
		return false, nil
	}
	for _, id := range tx.staged {
		if id == c.EventID {
			return false, nil
		}
	}
	tx.staged = append(tx.staged, c.EventID)
	return true, nil
}

func (f *fakeInbox) commit(tx *fakeTx) {
	for _, id := range tx.staged {
		f.committed[id] = true
	}
}

func TestApplyOnce_RunsTheEffectOncePerEvent(t *testing.T) {
	inbox := &fakeInbox{committed: map[string]bool{}}
	claim := Claim{EventID: "evt-1", EventType: TypeSucceeded, IntentID: "i-1", ReferenceID: uuid.New(), AmountMinor: 25000, Currency: "INR"}
	effects := 0
	effect := func(context.Context, *fakeTx) error { effects++; return nil }

	// First delivery: claimed, effect runs, caller commits.
	tx := &fakeTx{}
	if err := ApplyOnce(context.Background(), tx, Inbox[*fakeTx](inbox), claim, effect); err != nil {
		t.Fatalf("first delivery: %v", err)
	}
	inbox.commit(tx)

	// Redeliveries: duplicate, effect does not run.
	for i := 0; i < 3; i++ {
		tx := &fakeTx{}
		if err := ApplyOnce(context.Background(), tx, Inbox[*fakeTx](inbox), claim, effect); !errors.Is(err, ErrDuplicate) {
			t.Fatalf("redelivery %d: err = %v, want ErrDuplicate", i, err)
		}
	}
	if effects != 1 {
		t.Fatalf("effect ran %d times, want exactly 1", effects)
	}
	if len(inbox.claims) != 4 || inbox.claims[0] != claim {
		t.Fatalf("claims = %+v", inbox.claims)
	}
}

func TestApplyOnce_FailedEffectLeavesTheEventRetryable(t *testing.T) {
	inbox := &fakeInbox{committed: map[string]bool{}}
	claim := Claim{EventID: "evt-2", ReferenceID: uuid.New()}
	boom := errors.New("stock commit failed")

	// The effect fails; the caller rolls back (does not commit), so the
	// claim is forgotten with it.
	if err := ApplyOnce(context.Background(), &fakeTx{}, Inbox[*fakeTx](inbox), claim,
		func(context.Context, *fakeTx) error { return boom }); !errors.Is(err, boom) {
		t.Fatalf("err = %v, want the effect's error", err)
	}

	ran := false
	tx := &fakeTx{}
	if err := ApplyOnce(context.Background(), tx, Inbox[*fakeTx](inbox), claim,
		func(context.Context, *fakeTx) error { ran = true; return nil }); err != nil || !ran {
		t.Fatalf("retry after rollback: err = %v ran = %v", err, ran)
	}
}

func TestApplyOnce_RefusesBeforeClaiming(t *testing.T) {
	inbox := &fakeInbox{committed: map[string]bool{}}
	for _, id := range []string{"", "   "} {
		err := ApplyOnce(context.Background(), &fakeTx{}, Inbox[*fakeTx](inbox), Claim{EventID: id},
			func(context.Context, *fakeTx) error { t.Fatal("effect ran without a dedupe key"); return nil })
		if !errors.Is(err, ErrNoEventID) {
			t.Fatalf("event id %q: err = %v", id, err)
		}
	}
	if len(inbox.claims) != 0 {
		t.Fatal("a blank event id reached the inbox")
	}

	// A claim that errors runs no effect.
	inbox.err = errors.New("connection reset")
	err := ApplyOnce(context.Background(), &fakeTx{}, Inbox[*fakeTx](inbox), Claim{EventID: "evt-3"},
		func(context.Context, *fakeTx) error { t.Fatal("effect ran after a failed claim"); return nil })
	if !errors.Is(err, inbox.err) {
		t.Fatalf("err = %v, want the claim's error", err)
	}
}
