package service

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/atpost/post-service/internal/store/postgres"
	"github.com/atpost/shared/events"
	"github.com/google/uuid"
	"github.com/segmentio/kafka-go"
)

// feed-07: one pass of the outbox relay over a fake store and a fake
// publisher. The advisory lock itself is Postgres's and is not exercised
// here (no database in unit tests); what is pinned is how the pass uses it.

type fakeOutboxStore struct {
	rows []postgres.OutboxEvent // unpublished, in read order

	lockHeldElsewhere bool
	lockErr           error
	readErr           error
	markErr           map[uuid.UUID]error

	lockTries   int
	released    int
	badReleases int // release called without holding the lock
	reads       int
	marked      []uuid.UUID
}

func (f *fakeOutboxStore) TryOutboxRelayLock(context.Context) (func(), bool, error) {
	f.lockTries++
	if f.lockErr != nil {
		return nil, false, f.lockErr
	}
	if f.lockHeldElsewhere {
		return func() { f.badReleases++ }, false, nil
	}
	return func() { f.released++ }, true, nil
}

func (f *fakeOutboxStore) GetUnpublishedOutboxEvents(context.Context, int) ([]postgres.OutboxEvent, error) {
	f.reads++
	if f.readErr != nil {
		return nil, f.readErr
	}
	return append([]postgres.OutboxEvent(nil), f.rows...), nil
}

func (f *fakeOutboxStore) MarkOutboxEventPublished(_ context.Context, id uuid.UUID) error {
	if err := f.markErr[id]; err != nil {
		return err
	}
	f.marked = append(f.marked, id)
	kept := f.rows[:0]
	for _, r := range f.rows {
		if r.ID != id {
			kept = append(kept, r)
		}
	}
	f.rows = kept
	return nil
}

type sentOutbox struct {
	eventID, eventType, key string
}

type fakeOutboxPublisher struct {
	sent []sentOutbox
	fail map[string]error // by event id
}

func (p *fakeOutboxPublisher) publish(_ context.Context, msg kafka.Message) error {
	var env events.EventEnvelope
	if err := json.Unmarshal(msg.Value, &env); err != nil {
		return err
	}
	if err := p.fail[env.EventID]; err != nil {
		return err
	}
	p.sent = append(p.sent, sentOutbox{eventID: env.EventID, eventType: env.EventType, key: string(msg.Key)})
	return nil
}

func outboxRow(eventType string, aggregate uuid.UUID, at time.Time) postgres.OutboxEvent {
	return postgres.OutboxEvent{
		ID: uuid.New(), EventType: eventType, AggregateType: "post", AggregateID: aggregate,
		Payload: json.RawMessage(`{"post_id":"` + aggregate.String() + `"}`), CreatedAt: at,
	}
}

// The audit's acceptance case: one unpublished row, published, the
// mark-published fails, the next pass publishes it again. Both publishes
// carry the row id as the event id and the aggregate id as the key.
func TestRelayOutboxPass_RepublishKeepsEventIDAndAggregateKey(t *testing.T) {
	ctx := context.Background()
	post := uuid.New()
	row := outboxRow(events.PostReacted, post, time.Now())
	store := &fakeOutboxStore{rows: []postgres.OutboxEvent{row}, markErr: map[uuid.UUID]error{row.ID: errors.New("postgres went away")}}
	pub := &fakeOutboxPublisher{}

	if err := relayOutboxPass(ctx, store, pub.publish); err != nil {
		t.Fatal(err)
	}
	store.markErr = nil
	if err := relayOutboxPass(ctx, store, pub.publish); err != nil {
		t.Fatal(err)
	}

	if len(pub.sent) != 2 {
		t.Fatalf("publishes = %d, want 2 (the unmarked row goes out again)", len(pub.sent))
	}
	for i, s := range pub.sent {
		if s.eventID != row.ID.String() {
			t.Errorf("publish %d: event id = %s, want the outbox row id %s", i+1, s.eventID, row.ID)
		}
		if s.key != post.String() {
			t.Errorf("publish %d: key = %s, want the aggregate id %s", i+1, s.key, post)
		}
	}
	if len(store.marked) != 1 || store.marked[0] != row.ID {
		t.Fatalf("marked = %v, want the row once", store.marked)
	}
	// A third pass has nothing left to send.
	if err := relayOutboxPass(ctx, store, pub.publish); err != nil || len(pub.sent) != 2 {
		t.Fatalf("third pass: err=%v publishes=%d, want nothing new", err, len(pub.sent))
	}
}

// A replica that does not get the lock reads nothing, publishes nothing and
// releases nothing: the other replica's pass owns the rows.
func TestRelayOutboxPass_LockHeldElsewhere_Skips(t *testing.T) {
	store := &fakeOutboxStore{rows: []postgres.OutboxEvent{outboxRow(events.PostCreated, uuid.New(), time.Now())}, lockHeldElsewhere: true}
	pub := &fakeOutboxPublisher{}

	if err := relayOutboxPass(context.Background(), store, pub.publish); err != nil {
		t.Fatalf("a busy lock is not an error: %v", err)
	}
	if store.lockTries != 1 {
		t.Fatalf("lock tries = %d, want 1", store.lockTries)
	}
	if store.reads != 0 || len(pub.sent) != 0 || len(store.marked) != 0 {
		t.Fatalf("without the lock: reads=%d publishes=%d marks=%d, want none", store.reads, len(pub.sent), len(store.marked))
	}
	if store.released != 0 || store.badReleases != 0 {
		t.Fatalf("released=%d badReleases=%d; a lock that was not taken is not released", store.released, store.badReleases)
	}
}

// When the lock cannot be asked for at all, the pass fails closed: an error,
// and nothing published without the claim.
func TestRelayOutboxPass_LockError_PublishesNothing(t *testing.T) {
	store := &fakeOutboxStore{rows: []postgres.OutboxEvent{outboxRow(events.PostCreated, uuid.New(), time.Now())}, lockErr: errors.New("pool exhausted")}
	pub := &fakeOutboxPublisher{}

	if err := relayOutboxPass(context.Background(), store, pub.publish); err == nil {
		t.Fatal("lock error swallowed")
	}
	if store.reads != 0 || len(pub.sent) != 0 {
		t.Fatalf("reads=%d publishes=%d after a lock error, want none", store.reads, len(pub.sent))
	}
}

// The lock is released exactly once on every way out of a pass that took it.
func TestRelayOutboxPass_ReleasesTheLockOnEveryPath(t *testing.T) {
	post := uuid.New()
	row := outboxRow(events.PostCreated, post, time.Now())
	cases := map[string]struct {
		store   *fakeOutboxStore
		pub     *fakeOutboxPublisher
		wantErr bool
	}{
		"all published": {
			store: &fakeOutboxStore{rows: []postgres.OutboxEvent{row}}, pub: &fakeOutboxPublisher{},
		},
		"nothing to publish": {
			store: &fakeOutboxStore{}, pub: &fakeOutboxPublisher{},
		},
		"read fails": {
			store: &fakeOutboxStore{readErr: errors.New("query failed")}, pub: &fakeOutboxPublisher{}, wantErr: true,
		},
		"publish fails": {
			store: &fakeOutboxStore{rows: []postgres.OutboxEvent{row}},
			pub:   &fakeOutboxPublisher{fail: map[string]error{row.ID.String(): errors.New("broker down")}},
		},
		"mark fails": {
			store: &fakeOutboxStore{rows: []postgres.OutboxEvent{row}, markErr: map[uuid.UUID]error{row.ID: errors.New("update failed")}},
			pub:   &fakeOutboxPublisher{},
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			err := relayOutboxPass(context.Background(), tc.store, tc.pub.publish)
			if (err != nil) != tc.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tc.wantErr)
			}
			if tc.store.released != 1 {
				t.Fatalf("released %d times, want exactly 1", tc.store.released)
			}
		})
	}
}

// A publisher that panics still leaves the lock released (the release is
// deferred), so a crashing pass cannot wedge the relay on every replica.
func TestRelayOutboxPass_ReleasesTheLockOnPanic(t *testing.T) {
	store := &fakeOutboxStore{rows: []postgres.OutboxEvent{outboxRow(events.PostCreated, uuid.New(), time.Now())}}
	func() {
		defer func() { _ = recover() }()
		_ = relayOutboxPass(context.Background(), store, func(context.Context, kafka.Message) error { panic("writer blew up") })
	}()
	if store.released != 1 {
		t.Fatalf("released %d times after a panic, want 1", store.released)
	}
}

// A row that cannot be published holds back the later rows of the SAME post
// (a restore must not overtake its delete); other posts carry on. The next
// pass sends the held rows in their original order.
func TestRelayOutboxPass_PublishFailureHoldsLaterRowsOfThatAggregate(t *testing.T) {
	ctx := context.Background()
	postA, postB := uuid.New(), uuid.New()
	now := time.Now()
	del := outboxRow(events.PostDeleted, postA, now)
	other := outboxRow(events.PostCreated, postB, now.Add(time.Millisecond))
	res := outboxRow(events.PostRestored, postA, now.Add(2*time.Millisecond))
	store := &fakeOutboxStore{rows: []postgres.OutboxEvent{del, other, res}}
	pub := &fakeOutboxPublisher{fail: map[string]error{del.ID.String(): errors.New("broker down")}}

	if err := relayOutboxPass(ctx, store, pub.publish); err != nil {
		t.Fatal(err)
	}
	if len(pub.sent) != 1 || pub.sent[0].eventID != other.ID.String() {
		t.Fatalf("first pass sent %+v, want only the other post's row (the restore waits behind its delete)", pub.sent)
	}

	pub.fail = nil
	if err := relayOutboxPass(ctx, store, pub.publish); err != nil {
		t.Fatal(err)
	}
	if len(pub.sent) != 3 || pub.sent[1].eventID != del.ID.String() || pub.sent[2].eventID != res.ID.String() {
		t.Fatalf("after the retry sent %+v, want delete then restore", pub.sent)
	}
}

// A row that was published but could not be marked will go out again; the
// later rows of the same post wait for that, so the repeat arrives before
// them rather than after.
func TestRelayOutboxPass_MarkFailureHoldsLaterRowsOfThatAggregate(t *testing.T) {
	ctx := context.Background()
	post := uuid.New()
	now := time.Now()
	del := outboxRow(events.PostDeleted, post, now)
	res := outboxRow(events.PostRestored, post, now.Add(time.Millisecond))
	store := &fakeOutboxStore{rows: []postgres.OutboxEvent{del, res}, markErr: map[uuid.UUID]error{del.ID: errors.New("update failed")}}
	pub := &fakeOutboxPublisher{}

	if err := relayOutboxPass(ctx, store, pub.publish); err != nil {
		t.Fatal(err)
	}
	if len(pub.sent) != 1 || pub.sent[0].eventID != del.ID.String() {
		t.Fatalf("first pass sent %+v, want the delete only", pub.sent)
	}

	store.markErr = nil
	if err := relayOutboxPass(ctx, store, pub.publish); err != nil {
		t.Fatal(err)
	}
	want := []string{del.ID.String(), del.ID.String(), res.ID.String()}
	if len(pub.sent) != len(want) {
		t.Fatalf("sent %+v, want delete, delete (repeat), restore", pub.sent)
	}
	for i, id := range want {
		if pub.sent[i].eventID != id {
			t.Fatalf("publish %d = %s, want %s (delete, its repeat, then the restore)", i+1, pub.sent[i].eventID, id)
		}
	}
}

// No producer, no relay: the worker's call is a no-op, as before.
func TestPublishOutboxEvents_NoProducer(t *testing.T) {
	if err := (&Service{}).PublishOutboxEvents(context.Background()); err != nil {
		t.Fatal(err)
	}
}
