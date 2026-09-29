// The hold reconciliation sweep without a database (plan section 9.5):
// every drift kind is found and counted, only the RECORDED acked decision
// is ever re-queued, a case that says active is never released, a
// parked or pending command is a human's or the dispatcher's, and a
// failing read fails the sweep instead of guessing.
package reconcile

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/atpost/trust-safety-service/internal/restriction"
	"github.com/atpost/trust-safety-service/internal/store/postgres"
	"github.com/google/uuid"
)

type fakeHoldStore struct {
	cases    []postgres.ReconcileCase
	pageSize int
	requeued []uuid.UUID
	acked    map[uuid.UUID]bool
}

func (f *fakeHoldStore) ListCasesForReconcile(_ context.Context, after postgres.ReconcilePage, limit int, _ time.Duration) ([]postgres.ReconcileCase, postgres.ReconcilePage, error) {
	start := 0
	if after.AfterID != uuid.Nil {
		for i, c := range f.cases {
			if c.Case.ID == after.AfterID {
				start = i + 1
			}
		}
	}
	end := start + limit
	if f.pageSize > 0 && start+f.pageSize < end {
		end = start + f.pageSize
	}
	if end > len(f.cases) {
		end = len(f.cases)
	}
	if start >= end {
		return nil, after, nil
	}
	page := f.cases[start:end]
	return page, postgres.ReconcilePage{AfterID: page[len(page)-1].Case.ID, AfterCreatedAt: page[len(page)-1].Case.CreatedAt}, nil
}

func (f *fakeHoldStore) RequeueAckedCommand(_ context.Context, id uuid.UUID) (bool, error) {
	f.requeued = append(f.requeued, id)
	return f.acked[id], nil
}

type fakeReader struct {
	rows map[uuid.UUID]restriction.Row
	err  error
	asks int
}

func (r *fakeReader) ListByCase(_ context.Context, ids []uuid.UUID) ([]restriction.Row, error) {
	r.asks++
	if r.err != nil {
		return nil, r.err
	}
	var out []restriction.Row
	for _, id := range ids {
		if row, ok := r.rows[id]; ok {
			out = append(out, row)
		}
	}
	return out, nil
}

func mkCase(state string, rev int64, cmdStatus string, cmdRev int64, age time.Duration) postgres.ReconcileCase {
	c := postgres.CopyrightCase{ID: uuid.New(), SubjectPostID: uuid.New(), SubjectAuthorID: uuid.New(), State: state, CaseRevision: rev, CreatedAt: time.Now().Add(-age)}
	rc := postgres.ReconcileCase{Case: c}
	if cmdStatus != "" {
		action := postgres.RestrictionActionPlaceHold
		if state == postgres.CopyrightCaseHoldReleased {
			action = postgres.RestrictionActionReleaseHold
		}
		rc.Command = &postgres.RestrictionCommand{DecisionID: uuid.New(), CaseID: c.ID, CaseRevision: cmdRev, Action: action, Status: cmdStatus, CreatedAt: time.Now().Add(-age)}
		if cmdStatus == postgres.RestrictionCommandParked {
			r := "refused"
			rc.Command.ParkReason = &r
		}
	}
	return rc
}

func row(c postgres.ReconcileCase, state string, rev int64) restriction.Row {
	return restriction.Row{CaseID: c.Case.ID, PostID: c.Case.SubjectPostID, State: state, CaseRevision: rev}
}

func newTestReconciler(store *fakeHoldStore, reader *fakeReader) (*HoldReconciler, *int) {
	kicks := 0
	r := NewHoldReconciler(store, reader, nil, func() { kicks++ }, slog.New(slog.NewTextHandler(&strings.Builder{}, nil)))
	return r, &kicks
}

func TestHoldReconcile_DriftTable(t *testing.T) {
	const active, released = postgres.CopyrightCaseHoldActive, postgres.CopyrightCaseHoldReleased
	const acked, pending, parked = postgres.RestrictionCommandAcked, postgres.RestrictionCommandPending, postgres.RestrictionCommandParked
	type tc struct {
		name    string
		c       postgres.ReconcileCase
		row     *restriction.Row
		kind    string
		requeue bool
	}
	ok1 := mkCase(active, 1, acked, 1, time.Hour)
	missing := mkCase(active, 1, acked, 1, time.Hour)
	releasedRow := mkCase(active, 3, acked, 3, time.Hour)
	stale := mkCase(active, 3, acked, 3, time.Hour)
	relOK := mkCase(released, 2, acked, 2, time.Hour)
	relActive := mkCase(released, 2, acked, 2, time.Hour)
	relStale := mkCase(released, 4, acked, 4, time.Hour)
	relMissing := mkCase(released, 2, acked, 2, time.Hour)
	prk := mkCase(active, 1, parked, 1, time.Hour)
	pendFresh := mkCase(active, 1, pending, 1, time.Minute)
	pendOld := mkCase(active, 1, pending, 1, 2*time.Hour)
	noCmd := mkCase(active, 1, "", 0, time.Hour)
	otherPost := mkCase(active, 1, acked, 1, time.Hour)
	otherRow := row(otherPost, "active", 1)
	otherRow.PostID = uuid.New()
	r1, r2, r3, r4, r5, r6, r7 := row(ok1, "active", 1), row(releasedRow, "released", 3), row(stale, "active", 2), row(relOK, "released", 2), row(relActive, "active", 1), row(relStale, "released", 3), row(prk, "released", 1)
	r8 := row(pendFresh, "absent", 0)
	cases := []tc{
		{"active acked consistent", ok1, &r1, "", false},
		{"active acked no row", missing, nil, DriftMissingHold, true},
		{"active acked row released", releasedRow, &r2, DriftMissingHold, true},
		{"active acked row behind", stale, &r3, DriftStaleRevision, true},
		{"released acked consistent", relOK, &r4, "", false},
		{"released acked still active", relActive, &r5, DriftUnexpectedActive, true},
		{"released acked row behind", relStale, &r6, DriftStaleRevision, true},
		{"released acked no row", relMissing, nil, DriftMissingRow, false},
		{"parked", prk, &r7, DriftParked, false},
		{"pending fresh", pendFresh, &r8, "", false},
		{"pending overdue", pendOld, nil, DriftPendingOverdue, false},
		{"no command", noCmd, nil, DriftNoCommand, false},
		{"row names another post", otherPost, &otherRow, DriftMissingRow, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			store := &fakeHoldStore{cases: []postgres.ReconcileCase{c.c}, acked: map[uuid.UUID]bool{}}
			if c.c.Command != nil && c.c.Command.Status == acked {
				store.acked[c.c.Command.DecisionID] = true
			}
			reader := &fakeReader{rows: map[uuid.UUID]restriction.Row{}}
			if c.row != nil {
				reader.rows[c.c.Case.ID] = *c.row
			}
			r, kicks := newTestReconciler(store, reader)
			rep, err := r.Reconcile(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if rep.Checked != 1 {
				t.Fatalf("checked=%d", rep.Checked)
			}
			if c.kind == "" {
				if rep.Drift() != 0 {
					t.Fatalf("want no drift, got %v", rep.Findings)
				}
			} else if rep.Findings[c.kind] != 1 || rep.Drift() != 1 {
				t.Fatalf("want one %s, got %v", c.kind, rep.Findings)
			}
			if c.requeue {
				if len(store.requeued) != 1 || store.requeued[0] != c.c.Command.DecisionID || rep.Requeued != 1 || *kicks != 1 {
					t.Fatalf("want the recorded decision re-queued once and a kick: requeued=%v report=%d kicks=%d", store.requeued, rep.Requeued, *kicks)
				}
			} else if len(store.requeued) != 0 || rep.Requeued != 0 || *kicks != 0 {
				t.Fatalf("nothing may be re-queued: %v kicks=%d", store.requeued, *kicks)
			}
		})
	}
}

// The sweep never releases a hold a case says is active, and never mints a
// command of its own: the only ids it ever hands the store are the
// recorded decisions of the cases it checked.
func TestHoldReconcile_NeverReleasesAndNeverInvents(t *testing.T) {
	activeMissing := mkCase(postgres.CopyrightCaseHoldActive, 1, postgres.RestrictionCommandAcked, 1, time.Hour)
	activeReleasedRow := mkCase(postgres.CopyrightCaseHoldActive, 1, postgres.RestrictionCommandAcked, 1, time.Hour)
	noCmd := mkCase(postgres.CopyrightCaseHoldActive, 1, "", 0, time.Hour)
	store := &fakeHoldStore{cases: []postgres.ReconcileCase{activeMissing, activeReleasedRow, noCmd},
		acked: map[uuid.UUID]bool{activeMissing.Command.DecisionID: true, activeReleasedRow.Command.DecisionID: true}}
	reader := &fakeReader{rows: map[uuid.UUID]restriction.Row{activeReleasedRow.Case.ID: row(activeReleasedRow, "released", 1)}}
	r, _ := newTestReconciler(store, reader)
	rep, err := r.Reconcile(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	known := map[uuid.UUID]string{activeMissing.Command.DecisionID: activeMissing.Command.Action, activeReleasedRow.Command.DecisionID: activeReleasedRow.Command.Action}
	if len(store.requeued) != 2 {
		t.Fatalf("requeued=%v", store.requeued)
	}
	for _, id := range store.requeued {
		action, ok := known[id]
		if !ok {
			t.Fatalf("re-queued an id that is not a recorded decision: %s", id)
		}
		if action != postgres.RestrictionActionPlaceHold {
			t.Fatalf("an active case re-queued a %s", action)
		}
	}
	if rep.Findings[DriftNoCommand] != 1 || rep.Findings[DriftMissingHold] != 2 {
		t.Fatalf("findings=%v", rep.Findings)
	}
}

func TestHoldReconcile_PagesAndFailsClosedOnReadError(t *testing.T) {
	var cases []postgres.ReconcileCase
	acked := map[uuid.UUID]bool{}
	for i := 0; i < 7; i++ {
		c := mkCase(postgres.CopyrightCaseHoldActive, 1, postgres.RestrictionCommandAcked, 1, time.Hour)
		acked[c.Command.DecisionID] = true
		cases = append(cases, c)
	}
	reader := &fakeReader{rows: map[uuid.UUID]restriction.Row{}}
	for _, c := range cases {
		reader.rows[c.Case.ID] = row(c, "active", 1)
	}
	store := &fakeHoldStore{cases: cases, pageSize: 3, acked: acked}
	r, _ := newTestReconciler(store, reader)
	r.PageSize = 3
	rep, err := r.Reconcile(context.Background())
	if err != nil || rep.Checked != 7 || rep.Drift() != 0 || reader.asks != 3 {
		t.Fatalf("paged: checked=%d drift=%d asks=%d err=%v", rep.Checked, rep.Drift(), reader.asks, err)
	}

	reader.err = errors.New("post-service down")
	store.requeued = nil
	if _, err := r.Reconcile(context.Background()); err == nil || len(store.requeued) != 0 {
		t.Fatalf("a failed read must fail the sweep and change nothing: err=%v requeued=%v", err, store.requeued)
	}
}

// A requeue that the store refuses (the command was not acked any more) is
// not counted as a repair.
func TestHoldReconcile_RequeueRefusedIsNotARepair(t *testing.T) {
	c := mkCase(postgres.CopyrightCaseHoldActive, 1, postgres.RestrictionCommandAcked, 1, time.Hour)
	store := &fakeHoldStore{cases: []postgres.ReconcileCase{c}, acked: map[uuid.UUID]bool{}}
	r, kicks := newTestReconciler(store, &fakeReader{rows: map[uuid.UUID]restriction.Row{}})
	rep, err := r.Reconcile(context.Background())
	if err != nil || rep.Requeued != 0 || *kicks != 0 || rep.Findings[DriftMissingHold] != 1 {
		t.Fatalf("report=%+v kicks=%d err=%v", rep, *kicks, err)
	}
}
