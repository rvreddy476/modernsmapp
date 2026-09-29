package outbox

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/atpost/trust-safety-service/internal/store/postgres"
	"github.com/google/uuid"
)

type fakeStore struct {
	rows       []postgres.OutboxEvent
	published  map[int64]bool
	retryAfter map[int64]time.Duration
	failMark   error
}

func newFakeStore(n int) *fakeStore {
	f := &fakeStore{published: map[int64]bool{}, retryAfter: map[int64]time.Duration{}}
	for i := 1; i <= n; i++ {
		f.rows = append(f.rows, postgres.OutboxEvent{ID: int64(i), EventID: uuid.New(), EventType: "StrikeIssued",
			Topic: "social.events.v1", PartitionKey: "u", Payload: []byte(`{"n":` + string(rune('0'+i)) + `}`)})
	}
	return f
}

func (f *fakeStore) PendingOutbox(_ context.Context, limit int) ([]postgres.OutboxEvent, error) {
	var out []postgres.OutboxEvent
	for _, r := range f.rows {
		if f.published[r.ID] || f.retryAfter[r.ID] > 0 {
			continue
		}
		out = append(out, r)
		if len(out) == limit {
			break
		}
	}
	return out, nil
}

func (f *fakeStore) MarkOutboxPublished(_ context.Context, id int64) error {
	if f.failMark != nil {
		return f.failMark
	}
	f.published[id] = true
	return nil
}

func (f *fakeStore) RecordOutboxFailure(_ context.Context, id int64, _ error, retryAfter time.Duration) error {
	for i := range f.rows {
		if f.rows[i].ID == id {
			f.rows[i].Attempts++
		}
	}
	f.retryAfter[id] = retryAfter
	return nil
}

type fakePublisher struct {
	sent    []Message
	failIDs map[string]error // by payload
}

func (p *fakePublisher) Publish(_ context.Context, m Message) error {
	if err := p.failIDs[string(m.Value)]; err != nil {
		return err
	}
	p.sent = append(p.sent, m)
	return nil
}

func TestDispatcher_PublishesInOrderAndMarks(t *testing.T) {
	st, pub := newFakeStore(3), &fakePublisher{}
	d := New(st, pub, nil)
	n, err := d.Sweep(context.Background())
	if err != nil || n != 3 {
		t.Fatalf("n=%d err=%v", n, err)
	}
	if len(pub.sent) != 3 || string(pub.sent[0].Value) != `{"n":1}` || string(pub.sent[2].Value) != `{"n":3}` {
		t.Fatalf("sent=%v", pub.sent)
	}
	for id := int64(1); id <= 3; id++ {
		if !st.published[id] {
			t.Fatalf("row %d not marked", id)
		}
	}
	if pub.sent[0].Topic != "social.events.v1" || pub.sent[0].Key != "u" || pub.sent[0].EventType != "StrikeIssued" || pub.sent[0].EventID != st.rows[0].EventID.String() {
		t.Fatalf("message shape: %+v", pub.sent[0])
	}
	// A drained outbox publishes nothing more.
	if n, _ := d.Sweep(context.Background()); n != 0 {
		t.Fatalf("second sweep published %d", n)
	}
}

// A failing row holds the rows behind it until it has failed
// MaxOrderedAttempts times; after that the sweep continues past it and the
// row is retried with backoff, never dropped.
func TestDispatcher_RetryHoldsOrderThenContinuesPast(t *testing.T) {
	st := newFakeStore(3)
	pub := &fakePublisher{failIDs: map[string]error{`{"n":2}`: errors.New("broker down")}}
	d := New(st, pub, nil)
	d.MaxOrderedAttempts = 3
	ctx := context.Background()

	for attempt := 1; attempt <= 2; attempt++ {
		n, err := d.Sweep(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if attempt == 1 && n != 1 {
			t.Fatalf("first sweep published %d, want 1 (row 1 only)", n)
		}
		if attempt == 2 && n != 0 {
			t.Fatalf("second sweep published %d, want 0 (row 2 still blocks)", n)
		}
		if st.published[3] {
			t.Fatal("row 3 leapfrogged the failing row 2")
		}
		if st.retryAfter[2] != 0 {
			t.Fatalf("a blocking row must stay immediately retryable, got %v", st.retryAfter[2])
		}
	}
	// Third failure reaches MaxOrderedAttempts: row 2 is parked with backoff
	// and row 3 goes through.
	n, err := d.Sweep(ctx)
	if err != nil || n != 1 || !st.published[3] {
		t.Fatalf("third sweep: n=%d err=%v published3=%v", n, err, st.published[3])
	}
	if st.rows[1].Attempts != 3 || st.retryAfter[2] != time.Second {
		t.Fatalf("row 2 attempts=%d retryAfter=%v, want 3 and 1s", st.rows[1].Attempts, st.retryAfter[2])
	}
	if st.published[2] {
		t.Fatal("row 2 must not be marked published")
	}
	// The broker comes back: the parked row is retried and published.
	delete(pub.failIDs, `{"n":2}`)
	st.retryAfter[2] = 0
	if n, _ := d.Sweep(ctx); n != 1 || !st.published[2] {
		t.Fatalf("recovery sweep: n=%d published2=%v", n, st.published[2])
	}
}

func TestDispatcher_BackoffGrowsAndCaps(t *testing.T) {
	d := New(newFakeStore(0), &fakePublisher{}, nil)
	d.MaxOrderedAttempts = 5
	d.MaxBackoff = 8 * time.Second
	want := map[int]time.Duration{5: time.Second, 6: 2 * time.Second, 7: 4 * time.Second, 8: 8 * time.Second, 9: 8 * time.Second, 40: 8 * time.Second}
	for attempts, w := range want {
		if got := d.backoff(attempts); got != w {
			t.Fatalf("backoff(%d)=%v, want %v", attempts, got, w)
		}
	}
}

// Publish succeeded but the mark failed: the sweep stops (the next one
// re-sends the same event_id, a duplicate consumers dedupe) and reports the
// store error.
func TestDispatcher_MarkFailureStopsWithoutLoss(t *testing.T) {
	st, pub := newFakeStore(2), &fakePublisher{}
	st.failMark = errors.New("db down")
	d := New(st, pub, nil)
	n, err := d.Sweep(context.Background())
	if err == nil || n != 0 || len(pub.sent) != 1 {
		t.Fatalf("n=%d err=%v sent=%d, want 0, error, 1", n, err, len(pub.sent))
	}
	st.failMark = nil
	if n, err := d.Sweep(context.Background()); err != nil || n != 2 || len(pub.sent) != 3 || pub.sent[1].EventID != pub.sent[0].EventID {
		t.Fatalf("recovery: n=%d err=%v sent=%d (the re-send must carry the same event_id)", n, err, len(pub.sent))
	}
}
