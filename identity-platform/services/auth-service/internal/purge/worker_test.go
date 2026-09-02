package purge

import (
	"context"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/atpost/identity-auth-service/internal/store"
	"github.com/google/uuid"
)

// fakePurgeStore mirrors the real guards in store/lifecycle.go: RequestPurge
// throttles to 24h, CompletePurge only flips a pending row once.
type fakePurgeStore struct {
	now   time.Time
	users map[uuid.UUID]*fakeUser
	acks  map[uuid.UUID]map[string]struct{}
	// outbox records every event emitted, in order, as "<type>:<user_id>".
	outbox []string
}

type fakeUser struct {
	status      string
	purgeDate   time.Time
	requestedAt *time.Time
	completedAt *time.Time
	anonymised  bool
}

func newFakePurgeStore() *fakePurgeStore {
	return &fakePurgeStore{
		now:   time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC),
		users: map[uuid.UUID]*fakeUser{},
		acks:  map[uuid.UUID]map[string]struct{}{},
	}
}

func (f *fakePurgeStore) addDue(daysPast int) uuid.UUID {
	id := uuid.New()
	f.users[id] = &fakeUser{
		status:    store.AccountStatusPendingDeletion,
		purgeDate: f.now.Add(-time.Duration(daysPast) * 24 * time.Hour),
	}
	return id
}

func (f *fakePurgeStore) ack(id uuid.UUID, services ...string) {
	if f.acks[id] == nil {
		f.acks[id] = map[string]struct{}{}
	}
	for _, s := range services {
		f.acks[id][s] = struct{}{}
	}
}

func (f *fakePurgeStore) ListPurgeDue(_ context.Context, _ int) ([]store.PurgeCandidate, error) {
	var out []store.PurgeCandidate
	for id, u := range f.users {
		if u.status == store.AccountStatusPendingDeletion && !u.purgeDate.After(f.now) && u.completedAt == nil {
			out = append(out, store.PurgeCandidate{UserID: id, ScheduledPurgeDate: u.purgeDate, PurgeRequestedAt: u.requestedAt})
		}
	}
	return out, nil
}

func (f *fakePurgeStore) RequestPurge(_ context.Context, id uuid.UUID) (bool, error) {
	u := f.users[id]
	if u == nil || u.status != store.AccountStatusPendingDeletion || u.completedAt != nil {
		return false, nil
	}
	if u.requestedAt != nil && u.requestedAt.After(f.now.Add(-24*time.Hour)) {
		return false, nil
	}
	at := f.now
	u.requestedAt = &at
	f.outbox = append(f.outbox, store.EventUserPurgeRequested+":"+id.String())
	return true, nil
}

func (f *fakePurgeStore) GetPurgeAcks(_ context.Context, id uuid.UUID) (map[string]struct{}, error) {
	out := map[string]struct{}{}
	for s := range f.acks[id] {
		out[s] = struct{}{}
	}
	return out, nil
}

func (f *fakePurgeStore) CompletePurge(_ context.Context, id uuid.UUID) error {
	u := f.users[id]
	if u == nil || u.status != store.AccountStatusPendingDeletion || u.completedAt != nil {
		return store.ErrLifecycleConflict
	}
	at := f.now
	u.completedAt = &at
	u.status = store.AccountStatusPurged
	u.anonymised = true
	f.outbox = append(f.outbox, store.EventUserPurged+":"+id.String())
	return nil
}

func (f *fakePurgeStore) InsertPurgeAck(_ context.Context, id uuid.UUID, svc string, _ time.Time) error {
	f.ack(id, svc)
	return nil
}

func newTestWorker(f *fakePurgeStore, required string) *Worker {
	w := NewWorker(f, slog.Default(), Config{
		TickInterval:     time.Minute,
		RequiredServices: ParseRequiredServices(required),
	})
	w.now = func() time.Time { return f.now }
	return w
}

func hasEvent(outbox []string, prefix string) int {
	n := 0
	for _, e := range outbox {
		if strings.HasPrefix(e, prefix) {
			n++
		}
	}
	return n
}

// THE central invariant: N-1 acks is not enough. Ever.
func TestTick_PartialAcksNeverPurge(t *testing.T) {
	f := newFakePurgeStore()
	id := f.addDue(1)
	required := ParseRequiredServices("")
	// Every required service but one.
	f.ack(id, required[:len(required)-1]...)

	rep := newTestWorker(f, "").Tick(context.Background())

	if len(rep.Purged) != 0 {
		t.Fatalf("purged with %d/%d acks: %v", len(required)-1, len(required), rep.Purged)
	}
	u := f.users[id]
	if u.status != store.AccountStatusPendingDeletion || u.anonymised || u.completedAt != nil {
		t.Fatalf("row mutated on partial acks: %+v", u)
	}
	if hasEvent(f.outbox, store.EventUserPurged) != 0 {
		t.Fatalf("user.purged emitted on partial acks: %v", f.outbox)
	}
	if len(rep.Requested) != 1 || hasEvent(f.outbox, store.EventUserPurgeRequested) != 1 {
		t.Fatalf("expected exactly one user.purge_requested: %v", f.outbox)
	}
}

func TestTick_FullAcksPurgeAndAnonymise(t *testing.T) {
	f := newFakePurgeStore()
	id := f.addDue(1)
	f.ack(id, ParseRequiredServices("")...)

	rep := newTestWorker(f, "").Tick(context.Background())

	if len(rep.Purged) != 1 || rep.Purged[0] != id {
		t.Fatalf("purged = %v, want [%s]", rep.Purged, id)
	}
	u := f.users[id]
	if u.status != store.AccountStatusPurged || !u.anonymised || u.completedAt == nil {
		t.Fatalf("row not finalised: %+v", u)
	}
	if hasEvent(f.outbox, store.EventUserPurged) != 1 {
		t.Fatalf("user.purged not emitted exactly once: %v", f.outbox)
	}
	if hasEvent(f.outbox, store.EventUserPurgeRequested) != 0 {
		t.Fatalf("a fully-acked user must not be re-requested: %v", f.outbox)
	}

	// A second tick finds nothing (the row is no longer due) and is a no-op.
	rep2 := newTestWorker(f, "").Tick(context.Background())
	if rep2.Due != 0 || len(rep2.Purged) != 0 || hasEvent(f.outbox, store.EventUserPurged) != 1 {
		t.Fatalf("second tick was not idempotent: %+v / %v", rep2, f.outbox)
	}
}

// Acks are matched against the configured set, so extra acks from services
// outside it do not count and an operator-narrowed set completes sooner.
func TestTick_RequiredSetIsConfigurable(t *testing.T) {
	f := newFakePurgeStore()
	id := f.addDue(1)
	f.ack(id, "graph", "post", "search")

	rep := newTestWorker(f, "graph,post").Tick(context.Background())
	if len(rep.Purged) != 1 {
		t.Fatalf("expected purge with narrowed set graph,post; got %+v", rep)
	}

	// Case-insensitive, whitespace-tolerant, de-duplicated parsing.
	got := ParseRequiredServices(" Graph, post ,graph, ")
	if strings.Join(got, ",") != "graph,post" {
		t.Fatalf("ParseRequiredServices = %v", got)
	}
	if len(ParseRequiredServices("")) == 0 {
		t.Fatal("an empty REQUIRED_PURGE_SERVICES must fall back to the default set, never to zero")
	}
}

// user.purge_requested is re-emitted at most once per 24h until acked.
func TestTick_RequestIsThrottledAndRepeats(t *testing.T) {
	f := newFakePurgeStore()
	id := f.addDue(0)
	w := newTestWorker(f, "")

	w.Tick(context.Background())
	w.Tick(context.Background())
	f.now = f.now.Add(23 * time.Hour)
	w.Tick(context.Background())
	if n := hasEvent(f.outbox, store.EventUserPurgeRequested); n != 1 {
		t.Fatalf("purge_requested emitted %d times within 24h, want 1", n)
	}

	f.now = f.now.Add(2 * time.Hour) // 25h after the first request
	rep := w.Tick(context.Background())
	if n := hasEvent(f.outbox, store.EventUserPurgeRequested); n != 2 {
		t.Fatalf("purge_requested emitted %d times after 24h, want 2", n)
	}
	if len(rep.Requested) != 1 || rep.Requested[0] != id {
		t.Fatalf("report.Requested = %v", rep.Requested)
	}
	if f.users[id].status != store.AccountStatusPendingDeletion {
		t.Fatal("status changed without acks")
	}
}

func TestTick_OverdueWarnsAfterSevenDays(t *testing.T) {
	f := newFakePurgeStore()
	fresh := f.addDue(2)
	stale := f.addDue(8)
	f.ack(stale, "graph")

	rep := newTestWorker(f, "").Tick(context.Background())

	if len(rep.Overdue) != 1 || rep.Overdue[0] != stale {
		t.Fatalf("overdue = %v, want [%s]", rep.Overdue, stale)
	}
	for _, id := range rep.Overdue {
		if id == fresh {
			t.Fatal("a 2-day-old candidate was reported overdue")
		}
	}
	if len(rep.Purged) != 0 {
		t.Fatal("overdue must never mean purge")
	}
}

// ── Acks consumer parsing ───────────────────────────────────────────────────

func TestParseAck(t *testing.T) {
	id := uuid.New()

	uid, svc, at, err := ParseAck([]byte(`{"user_id":"` + id.String() + `","service":"Graph","purged_at":"2026-10-02T12:00:00Z"}`))
	if err != nil || uid != id || svc != "graph" || !at.Equal(time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)) {
		t.Fatalf("bare ack: uid=%s svc=%q at=%v err=%v", uid, svc, at, err)
	}

	uid, svc, _, err = ParseAck([]byte(`{"event_id":"x","event_type":"purge.ack","payload":{"user_id":"` + id.String() + `","service":"post"}}`))
	if err != nil || uid != id || svc != "post" {
		t.Fatalf("enveloped ack: uid=%s svc=%q err=%v", uid, svc, err)
	}

	for _, bad := range []string{`not json`, `{}`, `{"user_id":"nope","service":"graph"}`, `{"user_id":"` + id.String() + `"}`} {
		if _, _, _, err := ParseAck([]byte(bad)); err == nil {
			t.Fatalf("accepted malformed ack %q", bad)
		}
	}
}
