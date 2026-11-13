package service

import (
	"context"
	"errors"
	"testing"
	"time"
)

// echo_notes pruning (notes_prune.go): the cutoffs are the timeline
// retention and the restore window, and a pass works in bounded batches.

type pruneRow struct {
	created   time.Time
	deleted   bool
	deletedAt time.Time
}

// fakePruneStore applies pruneEchoNotesSQL's predicate to in-memory rows.
type fakePruneStore struct {
	rows       []pruneRow
	calls      int
	gotCreated time.Time
	gotDeleted time.Time
	failAt     int // 1-based call that fails; 0 = never
}

func (f *fakePruneStore) PruneEchoNotes(_ context.Context, createdBefore, deletedBefore time.Time, batch int) (int64, error) {
	f.calls++
	f.gotCreated, f.gotDeleted = createdBefore, deletedBefore
	if f.failAt == f.calls {
		return 0, errors.New("postgres down")
	}
	var kept []pruneRow
	var n int64
	for _, r := range f.rows {
		if n < int64(batch) && (r.created.Before(createdBefore) || (r.deleted && r.deletedAt.Before(deletedBefore))) {
			n++
			continue
		}
		kept = append(kept, r)
	}
	f.rows = kept
	return n, nil
}

var pruneNow = time.Date(2026, 11, 13, 12, 0, 0, 0, time.UTC)

func testPruner(st *fakePruneStore, batch, maxBatches int) *EchoNotesPruner {
	p := NewEchoNotesPruner(st)
	p.batch, p.maxBatches = batch, maxBatches
	p.now = func() time.Time { return pruneNow }
	return p
}

func TestEchoNotesPrune_RetentionAndRestoreWindow(t *testing.T) {
	day := 24 * time.Hour
	st := &fakePruneStore{rows: []pruneRow{
		{created: pruneNow.Add(-361 * day)},                                                  // past retention: goes
		{created: pruneNow.Add(-359 * day)},                                                  // inside: stays
		{created: pruneNow.Add(-40 * day), deleted: true, deletedAt: pruneNow.Add(-32 * day)}, // purged upstream: goes
		{created: pruneNow.Add(-40 * day), deleted: true, deletedAt: pruneNow.Add(-10 * day)}, // still restorable: stays
		{created: pruneNow.Add(-day)},                                                        // live: stays
	}}
	n, err := testPruner(st, 100, 10).RunOnce(context.Background())
	if err != nil || n != 2 || len(st.rows) != 3 {
		t.Fatalf("pruned %d (err %v), %d left; want 2 pruned, 3 left", n, err, len(st.rows))
	}
	if want := pruneNow.Add(-360 * day); !st.gotCreated.Equal(want) {
		t.Fatalf("created cutoff %v, want the 360-day timeline retention %v", st.gotCreated, want)
	}
	if want := pruneNow.Add(-31 * day); !st.gotDeleted.Equal(want) {
		t.Fatalf("deleted cutoff %v, want the restore window plus a day %v", st.gotDeleted, want)
	}
}

func TestEchoNotesPrune_BatchesUntilShortAndStopsAtTheCap(t *testing.T) {
	old := pruneRow{created: pruneNow.Add(-400 * 24 * time.Hour)}
	mk := func(n int) []pruneRow {
		rows := make([]pruneRow, n)
		for i := range rows {
			rows[i] = old
		}
		return rows
	}
	st := &fakePruneStore{rows: mk(25)}
	n, err := testPruner(st, 10, 50).RunOnce(context.Background())
	if err != nil || n != 25 || st.calls != 3 {
		t.Fatalf("pruned %d in %d batches (err %v); want 25 in 3", n, st.calls, err)
	}

	st = &fakePruneStore{rows: mk(100)}
	n, err = testPruner(st, 10, 4).RunOnce(context.Background())
	if err != nil || n != 40 || st.calls != 4 || len(st.rows) != 60 {
		t.Fatalf("capped pass: pruned %d in %d batches, %d left (err %v); want 40 in 4, 60 left", n, st.calls, len(st.rows), err)
	}
}

func TestEchoNotesPrune_StopsOnErrorAndCancel(t *testing.T) {
	old := pruneRow{created: pruneNow.Add(-400 * 24 * time.Hour)}
	st := &fakePruneStore{rows: []pruneRow{old, old, old, old, old}, failAt: 2}
	n, err := testPruner(st, 2, 10).RunOnce(context.Background())
	if err == nil || n != 2 || st.calls != 2 {
		t.Fatalf("error pass: pruned %d in %d calls, err %v", n, st.calls, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	st = &fakePruneStore{rows: []pruneRow{old}}
	if _, err := testPruner(st, 2, 10).RunOnce(ctx); err == nil || st.calls != 0 {
		t.Fatalf("cancelled pass ran %d batches (err %v)", st.calls, err)
	}
}

func TestEchoNotesPrune_DeletedWindowNeverUnderADay(t *testing.T) {
	t.Setenv("ECHO_NOTES_DELETED_RETENTION", "1h")
	if p := NewEchoNotesPruner(&fakePruneStore{}); p.deletedRetention != defaultEchoDeletedRetention {
		t.Fatalf("a 1h window was accepted: %v", p.deletedRetention)
	}
	t.Setenv("ECHO_NOTES_DELETED_RETENTION", "720h")
	if p := NewEchoNotesPruner(&fakePruneStore{}); p.deletedRetention != 720*time.Hour {
		t.Fatalf("override ignored: %v", p.deletedRetention)
	}
}
