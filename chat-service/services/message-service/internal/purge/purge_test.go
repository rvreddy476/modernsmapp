package purge

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
)

type fakeStore struct {
	rows      map[uuid.UUID]int // rows remaining per user
	hidden    map[uuid.UUID]bool
	purges    int
	failPurge error
	log       *[]string
}

func (f *fakeStore) PurgeUser(_ context.Context, id uuid.UUID) error {
	f.purges++
	if f.failPurge != nil {
		return f.failPurge
	}
	delete(f.rows, id)
	delete(f.hidden, id)
	*f.log = append(*f.log, "erase-committed")
	return nil
}

func (f *fakeStore) SetUserHidden(_ context.Context, id uuid.UUID, hidden bool, _ string) error {
	if hidden {
		f.hidden[id] = true
	} else {
		delete(f.hidden, id)
	}
	return nil
}

type fakeAcks struct {
	acks []Ack
	log  *[]string
	fail error
}

func (a *fakeAcks) PublishPurgeAck(_ context.Context, ack Ack) error {
	if a.fail != nil {
		return a.fail
	}
	a.acks = append(a.acks, ack)
	*a.log = append(*a.log, "ack")
	return nil
}

func payload(id uuid.UUID) json.RawMessage {
	b, _ := json.Marshal(map[string]string{"user_id": id.String(), "requested_at": time.Now().Format(time.RFC3339)})
	return b
}

func TestPurgeIsIdempotentAndAcksEveryTime(t *testing.T) {
	id := uuid.New()
	var order []string
	st := &fakeStore{rows: map[uuid.UUID]int{id: 3}, hidden: map[uuid.UUID]bool{}, log: &order}
	acks := &fakeAcks{log: &order}
	fixed := time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)
	h := NewHandler("message", st, acks, st, nil).WithClock(func() time.Time { return fixed })

	if err := h.Handle(context.Background(), EventUserPurgeRequested, payload(id)); err != nil {
		t.Fatalf("first purge: %v", err)
	}
	if _, still := st.rows[id]; still {
		t.Fatal("rows must be gone after purge")
	}
	// Second delivery (auth re-emits every 24h): nothing to erase, still acks.
	if err := h.Handle(context.Background(), EventUserPurgeRequested, payload(id)); err != nil {
		t.Fatalf("second purge: %v", err)
	}
	if st.purges != 2 || len(acks.acks) != 2 {
		t.Fatalf("want 2 purges and 2 acks, got %d/%d", st.purges, len(acks.acks))
	}
	if acks.acks[0].Service != "message" || acks.acks[0].UserID != id.String() || !acks.acks[0].PurgedAt.Equal(fixed) {
		t.Fatalf("bad ack: %+v", acks.acks[0])
	}
	want := []string{"erase-committed", "ack", "erase-committed", "ack"}
	if len(order) != len(want) {
		t.Fatalf("order %v, want %v", order, want)
	}
	for i := range want {
		if order[i] != want[i] {
			t.Fatalf("ack must follow the committed erase: %v", order)
		}
	}
}

func TestNoAckWhenEraseFails(t *testing.T) {
	id := uuid.New()
	var order []string
	st := &fakeStore{rows: map[uuid.UUID]int{id: 1}, hidden: map[uuid.UUID]bool{}, log: &order, failPurge: errors.New("db down")}
	acks := &fakeAcks{log: &order}
	h := NewHandler("message", st, acks, st, nil)
	if err := h.Handle(context.Background(), EventUserPurgeRequested, payload(id)); err == nil {
		t.Fatal("expected error")
	}
	if len(acks.acks) != 0 {
		t.Fatal("ack must not be published when the erase did not commit")
	}
}

func TestAckFailureIsRetriedNotSwallowed(t *testing.T) {
	id := uuid.New()
	var order []string
	st := &fakeStore{rows: map[uuid.UUID]int{id: 1}, hidden: map[uuid.UUID]bool{}, log: &order}
	acks := &fakeAcks{log: &order, fail: errors.New("kafka down")}
	h := NewHandler("message", st, acks, st, nil)
	if err := h.Handle(context.Background(), EventUserPurgeRequested, payload(id)); err == nil {
		t.Fatal("expected error so the consumer holds the offset")
	}
	acks.fail = nil
	if err := h.Handle(context.Background(), EventUserPurgeRequested, payload(id)); err != nil {
		t.Fatalf("retry: %v", err)
	}
	if len(acks.acks) != 1 {
		t.Fatalf("want exactly one ack after retry, got %d", len(acks.acks))
	}
}

func TestHideAndUnhideToggle(t *testing.T) {
	id := uuid.New()
	var order []string
	st := &fakeStore{rows: map[uuid.UUID]int{}, hidden: map[uuid.UUID]bool{}, log: &order}
	h := NewHandler("message", st, &fakeAcks{log: &order}, st, nil)
	ctx := context.Background()
	for _, ev := range []string{EventUserDeactivated, EventUserDeletionScheduled} {
		st.hidden = map[uuid.UUID]bool{}
		if err := h.Handle(ctx, ev, payload(id)); err != nil {
			t.Fatal(err)
		}
		if !st.hidden[id] {
			t.Fatalf("%s must hide", ev)
		}
	}
	for _, ev := range []string{EventUserReactivated, EventUserDeletionCancelled} {
		st.hidden[id] = true
		if err := h.Handle(ctx, ev, payload(id)); err != nil {
			t.Fatal(err)
		}
		if st.hidden[id] {
			t.Fatalf("%s must unhide", ev)
		}
	}
	if st.purges != 0 || len(order) != 0 {
		t.Fatal("hide/unhide must never erase or ack")
	}
}

func TestMalformedPayloadIsPermanent(t *testing.T) {
	var order []string
	st := &fakeStore{rows: map[uuid.UUID]int{}, hidden: map[uuid.UUID]bool{}, log: &order}
	h := NewHandler("message", st, &fakeAcks{log: &order}, st, nil)
	err := h.Handle(context.Background(), EventUserPurgeRequested, json.RawMessage(`{"user_id":"nope"}`))
	if !errors.Is(err, ErrPermanent) {
		t.Fatalf("want ErrPermanent, got %v", err)
	}
	if !h.HandleUntilDurable(context.Background(), EventUserPurgeRequested, json.RawMessage(`{`)) {
		t.Fatal("permanent errors must be skipped, not held")
	}
	if err := h.Handle(context.Background(), "post.created", json.RawMessage(`{}`)); err != nil {
		t.Fatalf("unrelated events are a no-op: %v", err)
	}
}
