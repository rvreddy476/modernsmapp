package events

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/atpost/search-service/internal/purge"
	"github.com/google/uuid"
	"github.com/segmentio/kafka-go"
)

// fakeSearchEraser is a fake of the searchEraser interface (the subset of
// *search.Store PurgeStore uses) so PurgeStore's composition can be unit
// tested without a live OpenSearch cluster.
type fakeSearchEraser struct {
	erasedAuthors []string
	deletedUsers  []string
	hiddenUsers   map[string]bool
	hiddenAuthors map[string]bool

	eraseErr, deleteErr, hideUserErr, hideAuthorErr error
}

func newFakeSearchEraser() *fakeSearchEraser {
	return &fakeSearchEraser{hiddenUsers: map[string]bool{}, hiddenAuthors: map[string]bool{}}
}

func (f *fakeSearchEraser) EraseAuthorContent(_ context.Context, authorID string) error {
	if f.eraseErr != nil {
		return f.eraseErr
	}
	f.erasedAuthors = append(f.erasedAuthors, authorID)
	return nil
}

func (f *fakeSearchEraser) DeleteUser(_ context.Context, userID string) error {
	if f.deleteErr != nil {
		return f.deleteErr
	}
	f.deletedUsers = append(f.deletedUsers, userID)
	return nil
}

func (f *fakeSearchEraser) UpdateUserHidden(_ context.Context, userID string, hidden bool) error {
	if f.hideUserErr != nil {
		return f.hideUserErr
	}
	f.hiddenUsers[userID] = hidden
	return nil
}

func (f *fakeSearchEraser) UpdatePostsAuthorHidden(_ context.Context, authorID string, hidden bool) error {
	if f.hideAuthorErr != nil {
		return f.hideAuthorErr
	}
	f.hiddenAuthors[authorID] = hidden
	return nil
}

// fakePgEraser is a fake of pgEraser, standing in for either
// *postgres.AnalyticsStore or *postgres.SearchExtrasStore.
type fakePgEraser struct {
	purged []uuid.UUID
	err    error
}

func (f *fakePgEraser) PurgeUser(_ context.Context, userID uuid.UUID) error {
	if f.err != nil {
		return f.err
	}
	f.purged = append(f.purged, userID)
	return nil
}

// fakeAckPublisher records acks in order, standing in for
// purge.KafkaAckPublisher.
type fakeAckPublisher struct {
	acks []purge.Ack
	err  error
}

func (a *fakeAckPublisher) PublishPurgeAck(_ context.Context, ack purge.Ack) error {
	if a.err != nil {
		return a.err
	}
	a.acks = append(a.acks, ack)
	return nil
}

func TestPurgeStore_PurgeUser_ErasesEverythingAndIsIdempotent(t *testing.T) {
	se := newFakeSearchEraser()
	analytics := &fakePgEraser{}
	extras := &fakePgEraser{}
	p := &PurgeStore{search: se, analytics: analytics, extras: extras}

	id := uuid.New()
	// auth-service re-emits user.purge_requested every 24h until it sees
	// our ack, so a redelivery must be a safe no-op-equivalent re-run, not
	// an error.
	if err := p.PurgeUser(context.Background(), id); err != nil {
		t.Fatalf("first purge: %v", err)
	}
	if err := p.PurgeUser(context.Background(), id); err != nil {
		t.Fatalf("second purge (idempotent redelivery): %v", err)
	}

	if len(se.erasedAuthors) != 2 || se.erasedAuthors[0] != id.String() {
		t.Fatalf("expected EraseAuthorContent called twice with %s, got %v", id, se.erasedAuthors)
	}
	if len(se.deletedUsers) != 2 || se.deletedUsers[0] != id.String() {
		t.Fatalf("expected DeleteUser called twice with %s, got %v", id, se.deletedUsers)
	}
	if len(analytics.purged) != 2 || analytics.purged[0] != id {
		t.Fatalf("expected optional analytics store purged on every delivery, got %v", analytics.purged)
	}
	if len(extras.purged) != 2 || extras.purged[0] != id {
		t.Fatalf("expected optional extras store purged on every delivery, got %v", extras.purged)
	}
}

func TestPurgeStore_PurgeUser_OptionalPostgresStoresSkippedWhenNil(t *testing.T) {
	se := newFakeSearchEraser()
	// analytics and extras left nil, mirroring POSTGRES_DSN unset.
	p := &PurgeStore{search: se}
	if err := p.PurgeUser(context.Background(), uuid.New()); err != nil {
		t.Fatalf("purge without optional postgres stores must succeed: %v", err)
	}
}

func TestNewPurgeStore_NilConcretePostgresPointersStayNil(t *testing.T) {
	// Regression guard for the classic Go nil-interface trap: passing a
	// nil *postgres.AnalyticsStore / *postgres.SearchExtrasStore into
	// NewPurgeStore must leave the interface-typed fields genuinely nil,
	// not a non-nil interface wrapping a nil pointer.
	p := NewPurgeStore(nil, nil, nil)
	if p.analytics != nil {
		t.Fatal("nil *postgres.AnalyticsStore must produce a nil analytics field")
	}
	if p.extras != nil {
		t.Fatal("nil *postgres.SearchExtrasStore must produce a nil extras field")
	}
}

func TestPurgeStore_PurgeUser_PropagatesSearchErasureFailure(t *testing.T) {
	se := newFakeSearchEraser()
	se.eraseErr = errors.New("opensearch down")
	analytics := &fakePgEraser{}
	p := &PurgeStore{search: se, analytics: analytics}

	if err := p.PurgeUser(context.Background(), uuid.New()); err == nil {
		t.Fatal("expected the erase failure to propagate so the caller never acks")
	}
	if len(analytics.purged) != 0 {
		t.Fatal("a failed opensearch erase must not fall through to the postgres erase")
	}
}

func TestPurgeStore_SetUserHidden_TogglesUserAndPosts(t *testing.T) {
	se := newFakeSearchEraser()
	p := &PurgeStore{search: se}
	id := uuid.New()

	if err := p.SetUserHidden(context.Background(), id, true, "user.deactivated"); err != nil {
		t.Fatal(err)
	}
	if !se.hiddenUsers[id.String()] {
		t.Fatal("hide must flip the user document")
	}
	if !se.hiddenAuthors[id.String()] {
		t.Fatal("hide must flip every post by the author")
	}

	if err := p.SetUserHidden(context.Background(), id, false, "user.reactivated"); err != nil {
		t.Fatal(err)
	}
	if se.hiddenUsers[id.String()] {
		t.Fatal("unhide must flip the user document back")
	}
	if se.hiddenAuthors[id.String()] {
		t.Fatal("unhide must flip every post by the author back")
	}
}

// End-to-end through the exact composition main.go wires: purge.NewHandler
// driving our PurgeStore adapter, verifying the ack carries service="search"
// and that erase happens before the ack (mirrored from purge_test.go's
// generic assertion, at the search-service integration point).
func TestPurgeHandler_EndToEndWithSearchServiceAdapter(t *testing.T) {
	se := newFakeSearchEraser()
	acks := &fakeAckPublisher{}
	store := &PurgeStore{search: se}
	fixed := time.Date(2026, 9, 3, 12, 0, 0, 0, time.UTC)
	h := purge.NewHandler("search", store, acks, store, nil).WithClock(func() time.Time { return fixed })

	id := uuid.New()
	payload, _ := json.Marshal(map[string]string{"user_id": id.String()})

	if err := h.Handle(context.Background(), purge.EventUserDeactivated, payload); err != nil {
		t.Fatalf("deactivate: %v", err)
	}
	if !se.hiddenUsers[id.String()] {
		t.Fatal("user.deactivated must hide the account")
	}
	if len(acks.acks) != 0 {
		t.Fatal("hide must never ack")
	}

	if err := h.Handle(context.Background(), purge.EventUserPurgeRequested, payload); err != nil {
		t.Fatalf("purge: %v", err)
	}
	if len(se.erasedAuthors) != 1 || se.erasedAuthors[0] != id.String() {
		t.Fatal("purge_requested must erase the author's content")
	}
	if len(acks.acks) != 1 {
		t.Fatalf("expected exactly one ack, got %d", len(acks.acks))
	}
	if acks.acks[0].Service != "search" || acks.acks[0].UserID != id.String() || !acks.acks[0].PurgedAt.Equal(fixed) {
		t.Fatalf("bad ack: %+v", acks.acks[0])
	}
}

// The consumer's default dispatch branch (processMessage in consumer.go)
// must route lifecycle events to the wired purge.Handler rather than
// falling through as a silent no-op — this is the exact wiring main.go
// performs via WithLifecycleHandler.
func TestConsumer_DispatchesLifecycleEventToPurgeHandler(t *testing.T) {
	se := newFakeSearchEraser()
	acks := &fakeAckPublisher{}
	store := &PurgeStore{search: se}
	h := purge.NewHandler("search", store, acks, store, nil)
	c := &Consumer{lifecycle: h}

	id := uuid.New()
	payload, _ := json.Marshal(map[string]string{"user_id": id.String()})
	envelope, err := json.Marshal(map[string]any{
		"event_type": purge.EventUserPurgeRequested,
		"payload":    json.RawMessage(payload),
	})
	if err != nil {
		t.Fatal(err)
	}

	if err := c.processMessage(context.Background(), kafka.Message{Value: envelope}); err != nil {
		t.Fatalf("processMessage: %v", err)
	}
	if len(acks.acks) != 1 {
		t.Fatalf("expected the purge to be handled and acked, got %d acks", len(acks.acks))
	}
	if len(se.erasedAuthors) != 1 || se.erasedAuthors[0] != id.String() {
		t.Fatal("purge_requested routed through the consumer must erase the author's content")
	}
}

// Without a wired lifecycle handler (nil, e.g. before main.go wires it), a
// lifecycle event must fall through as a harmless no-op rather than panic.
func TestConsumer_LifecycleEventIsNoOpWithoutHandler(t *testing.T) {
	c := &Consumer{}
	envelope, _ := json.Marshal(map[string]any{
		"event_type": purge.EventUserDeactivated,
		"payload":    json.RawMessage(`{"user_id":"` + uuid.New().String() + `"}`),
	})
	if err := c.processMessage(context.Background(), kafka.Message{Value: envelope}); err != nil {
		t.Fatalf("expected no-op, got error: %v", err)
	}
}
