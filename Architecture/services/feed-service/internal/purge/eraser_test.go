package purge

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
)

type fakeTimelineStore struct {
	deleted []uuid.UUID
	log     *[]string
	fail    error
}

func (f *fakeTimelineStore) DeleteTimelineEntriesByAuthor(_ context.Context, authorID uuid.UUID) error {
	if f.fail != nil {
		return f.fail
	}
	f.deleted = append(f.deleted, authorID)
	if f.log != nil {
		*f.log = append(*f.log, "scylla-deleted")
	}
	return nil
}

type fakePGStore struct {
	purged []uuid.UUID
	log    *[]string
	fail   error
}

func (f *fakePGStore) PurgeUser(_ context.Context, userID uuid.UUID) error {
	if f.fail != nil {
		return f.fail
	}
	f.purged = append(f.purged, userID)
	if f.log != nil {
		*f.log = append(*f.log, "pg-purged")
	}
	return nil
}

// The Scylla author-timeline delete must run before the Postgres
// transaction — mirrors notification-service's NewEraser ordering
// (inbox first, prefs second).
func TestStoreEraser_RunsScyllaBeforePostgres(t *testing.T) {
	id := uuid.New()
	var order []string
	ts := &fakeTimelineStore{log: &order}
	pg := &fakePGStore{log: &order}
	e := NewEraser(ts, pg)

	if err := e.PurgeUser(context.Background(), id); err != nil {
		t.Fatalf("PurgeUser: %v", err)
	}
	if len(ts.deleted) != 1 || ts.deleted[0] != id {
		t.Fatalf("scylla delete not called with %s: %v", id, ts.deleted)
	}
	if len(pg.purged) != 1 || pg.purged[0] != id {
		t.Fatalf("postgres purge not called with %s: %v", id, pg.purged)
	}
	want := []string{"scylla-deleted", "pg-purged"}
	if len(order) != len(want) || order[0] != want[0] || order[1] != want[1] {
		t.Fatalf("order = %v, want %v", order, want)
	}
}

// A second call (auth-service re-emits user.purge_requested every 24h
// until acked) must still succeed — the fakes have nothing left to erase,
// but PurgeUser must not error.
func TestStoreEraser_IdempotentOnRedelivery(t *testing.T) {
	id := uuid.New()
	ts := &fakeTimelineStore{}
	pg := &fakePGStore{}
	e := NewEraser(ts, pg)

	if err := e.PurgeUser(context.Background(), id); err != nil {
		t.Fatalf("first purge: %v", err)
	}
	if err := e.PurgeUser(context.Background(), id); err != nil {
		t.Fatalf("second (redelivered) purge must also succeed: %v", err)
	}
	if len(ts.deleted) != 2 || len(pg.purged) != 2 {
		t.Fatalf("expected both stores called twice, got scylla=%d pg=%d", len(ts.deleted), len(pg.purged))
	}
}

// A Scylla failure must stop before Postgres runs — a partial erase must
// not be reported as committed.
func TestStoreEraser_ScyllaFailureStopsBeforePostgres(t *testing.T) {
	id := uuid.New()
	ts := &fakeTimelineStore{fail: errors.New("scylla down")}
	pg := &fakePGStore{}
	e := NewEraser(ts, pg)

	if err := e.PurgeUser(context.Background(), id); err == nil {
		t.Fatal("expected error when the scylla delete fails")
	}
	if len(pg.purged) != 0 {
		t.Fatal("postgres purge must not run after a scylla failure")
	}
}

// Either store may be nil (mirrors notification-service: "either store may
// be nil").
func TestStoreEraser_NilStoresAreNoOps(t *testing.T) {
	id := uuid.New()
	pg := &fakePGStore{}
	e := NewEraser(nil, pg)
	if err := e.PurgeUser(context.Background(), id); err != nil {
		t.Fatalf("nil timeline store: %v", err)
	}
	if len(pg.purged) != 1 {
		t.Fatal("postgres purge should still run when timeline store is nil")
	}

	e2 := NewEraser(&fakeTimelineStore{}, nil)
	if err := e2.PurgeUser(context.Background(), id); err != nil {
		t.Fatalf("nil pg store: %v", err)
	}
}
