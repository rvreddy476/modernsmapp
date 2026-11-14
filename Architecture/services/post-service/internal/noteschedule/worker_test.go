package noteschedule

import (
	"context"
	"errors"
	"testing"

	"github.com/atpost/post-service/internal/service"
)

type fakePub struct {
	passes []service.NotePublishReport
	err    error
	calls  int
}

func (f *fakePub) PublishDueNotes(_ context.Context, limit int) (service.NotePublishReport, error) {
	if f.calls >= len(f.passes) {
		f.calls++
		return service.NotePublishReport{}, f.err
	}
	r := f.passes[f.calls]
	f.calls++
	return r, nil
}

// A tick drains a backlog: it runs passes while each claims a full batch.
func TestTickDrainsFullBatches(t *testing.T) {
	f := &fakePub{passes: []service.NotePublishReport{{Claimed: 2, Published: 2}, {Claimed: 2, Published: 1, Deferred: 1}, {Claimed: 1, Published: 1}}}
	rep := NewWorker(f, Config{Batch: 2}, nil).Tick(context.Background())
	if f.calls != 3 || rep.Claimed != 5 || rep.Published != 4 || rep.Deferred != 1 {
		t.Fatalf("calls %d report %+v", f.calls, rep)
	}
}

func TestTickStopsOnError(t *testing.T) {
	f := &fakePub{err: errors.New("db down")}
	NewWorker(f, Config{Batch: 2}, nil).Tick(context.Background())
	if f.calls != 1 {
		t.Fatalf("calls %d", f.calls)
	}
}
