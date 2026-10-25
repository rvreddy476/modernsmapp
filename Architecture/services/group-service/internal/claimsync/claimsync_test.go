package claimsync

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/atpost/group-service/internal/mediaclient"
	"github.com/google/uuid"
)

// fakeClaims is media-service's claim table.
type fakeClaims struct {
	assets map[uuid.UUID]bool
	held   map[string]map[uuid.UUID]bool
	err    error
	calls  int
}

func newClaims(assets ...uuid.UUID) *fakeClaims {
	f := &fakeClaims{assets: map[uuid.UUID]bool{}, held: map[string]map[uuid.UUID]bool{}}
	for _, a := range assets {
		f.assets[a] = true
	}
	return f
}

func key(kind string, ref uuid.UUID) string { return kind + "|" + ref.String() }

func (f *fakeClaims) Claim(_ context.Context, kind string, ref uuid.UUID, ids []uuid.UUID) error {
	f.calls++
	if f.err != nil {
		return f.err
	}
	for _, id := range ids {
		if !f.assets[id] {
			return mediaclient.ErrNotFound
		}
	}
	if f.held[key(kind, ref)] == nil {
		f.held[key(kind, ref)] = map[uuid.UUID]bool{}
	}
	for _, id := range ids {
		f.held[key(kind, ref)][id] = true
	}
	return nil
}

func (f *fakeClaims) Release(_ context.Context, kind string, ref uuid.UUID, ids []uuid.UUID) error {
	f.calls++
	if f.err != nil {
		return f.err
	}
	if len(ids) == 0 {
		delete(f.held, key(kind, ref))
	}
	for _, id := range ids {
		delete(f.held[key(kind, ref)], id)
	}
	return nil
}

func (f *fakeClaims) ReleaseExcept(_ context.Context, kind string, ref uuid.UUID, keep []uuid.UUID) error {
	f.calls++
	if f.err != nil {
		return f.err
	}
	if len(keep) == 0 {
		return errors.New("empty keep")
	}
	for id := range f.held[key(kind, ref)] {
		if !slices.Contains(keep, id) {
			delete(f.held[key(kind, ref)], id)
		}
	}
	return nil
}

func (f *fakeClaims) holds(kind string, ref, id uuid.UUID) bool { return f.held[key(kind, ref)][id] }

// fakeHolders is the service's tables: the CURRENT rows, read under the lock.
type fakeHolders struct {
	current map[string]Row
	locked  []uuid.UUID
	lockErr error
}

func (h *fakeHolders) LockMediaHolder(_ context.Context, ref uuid.UUID) (func(), error) {
	if h.lockErr != nil {
		return nil, h.lockErr
	}
	h.locked = append(h.locked, ref)
	return func() {}, nil
}

func (h *fakeHolders) MediaClaimRowFor(_ context.Context, kind string, ref uuid.UUID) (Row, error) {
	if r, ok := h.current[key(kind, ref)]; ok {
		return r, nil
	}
	return Row{Kind: kind, Ref: ref}, nil // gone: not live, holds nothing
}

func holders(rows ...Row) *fakeHolders {
	h := &fakeHolders{current: map[string]Row{}}
	for _, r := range rows {
		h.current[key(r.Kind, r.Ref)] = r
	}
	return h
}

func TestDryRunSendsNothing(t *testing.T) {
	live := Row{Kind: "a", Ref: uuid.New(), MediaIDs: []uuid.UUID{uuid.New()}, Live: true}
	dead := Row{Kind: "a", Ref: uuid.New(), MediaIDs: []uuid.UUID{uuid.New()}}
	var rep Report
	Reconcile(context.Background(), holders(), nil, []Row{live, dead}, false, &rep)
	if rep.Rows != 2 || rep.Claimed != 1 || rep.Released != 1 {
		t.Fatalf("dry run report %s", rep)
	}
}

func TestLiveRowIsClaimedAndStaleClaimsGo(t *testing.T) {
	ref, cur, stale := uuid.New(), uuid.New(), uuid.New()
	c := newClaims(cur, stale)
	_ = c.Claim(context.Background(), "a", ref, []uuid.UUID{stale}) // a lost release
	row := Row{Kind: "a", Ref: ref, MediaIDs: []uuid.UUID{cur}, Live: true}
	h := holders(row)
	var rep Report
	Reconcile(context.Background(), h, c, []Row{row}, true, &rep)
	if !c.holds("a", ref, cur) || c.holds("a", ref, stale) || rep.Claimed != 1 {
		t.Fatalf("held %v report %s", c.held, rep)
	}
	if !slices.Equal(h.locked, []uuid.UUID{ref}) {
		t.Fatal("the row was not locked before it was reconciled")
	}
}

func TestDeadRowIsReleased(t *testing.T) {
	ref, id := uuid.New(), uuid.New()
	c := newClaims(id)
	_ = c.Claim(context.Background(), "a", ref, []uuid.UUID{id})
	row := Row{Kind: "a", Ref: ref, MediaIDs: []uuid.UUID{id}, Live: false}
	var rep Report
	Reconcile(context.Background(), holders(row), c, []Row{row}, true, &rep)
	if c.holds("a", ref, id) || rep.Released != 1 {
		t.Fatalf("a deleted row still holds its media: %v", c.held)
	}
}

// The page is stale: the row changed after it was read. The pass acts on the
// row as it is under the lock, never on the page.
func TestApplyRereadsTheRowUnderTheLock(t *testing.T) {
	ref, before, now := uuid.New(), uuid.New(), uuid.New()
	c := newClaims(before, now)
	_ = c.Claim(context.Background(), "a", ref, []uuid.UUID{now}) // the edit's own claim
	page := Row{Kind: "a", Ref: ref, MediaIDs: []uuid.UUID{before}, Live: true}
	h := holders(Row{Kind: "a", Ref: ref, MediaIDs: []uuid.UUID{now}, Live: true})
	var rep Report
	Reconcile(context.Background(), h, c, []Row{page}, true, &rep)
	if !c.holds("a", ref, now) {
		t.Fatal("the pass released a claim the row's current media relies on")
	}
	if c.holds("a", ref, before) {
		t.Fatal("the pass claimed media from a stale read")
	}
}

func TestDanglingIDIsReportedAndTheRestStillClaimed(t *testing.T) {
	ref, ok, gone := uuid.New(), uuid.New(), uuid.New()
	c := newClaims(ok)
	row := Row{Kind: "a", Ref: ref, MediaIDs: []uuid.UUID{gone, ok}, Live: true}
	var rep Report
	Reconcile(context.Background(), holders(row), c, []Row{row}, true, &rep)
	if !c.holds("a", ref, ok) {
		t.Fatal("one dangling id left the row's other media unclaimed")
	}
	if len(rep.Dangling) != 1 || rep.Dangling[0].MediaID != gone || rep.Failed != 0 {
		t.Fatalf("report %s %v", rep, rep.Dangling)
	}
}

func TestFailuresAreCountedNotFatal(t *testing.T) {
	row := Row{Kind: "a", Ref: uuid.New(), MediaIDs: []uuid.UUID{uuid.New()}, Live: true}
	c := newClaims()
	c.err = mediaclient.ErrUnavailable
	var rep Report
	Reconcile(context.Background(), holders(row), c, []Row{row, row}, true, &rep)
	if rep.Failed != 2 || rep.Claimed != 0 {
		t.Fatalf("report %s", rep)
	}
	h := holders(row)
	h.lockErr = errors.New("db down")
	rep = Report{}
	Reconcile(context.Background(), h, newClaims(), []Row{row}, true, &rep)
	if rep.Failed != 1 {
		t.Fatalf("a lock failure was not counted: %s", rep)
	}
}

func TestRunPagesEveryTableUntilEmpty(t *testing.T) {
	refs := []uuid.UUID{uuid.New(), uuid.New(), uuid.New()}
	var asked []uuid.UUID
	pager := func(rows []Row) Pager {
		return func(_ context.Context, after uuid.UUID, _ int) ([]Row, uuid.UUID, error) {
			asked = append(asked, after)
			for i, r := range rows {
				if after == uuid.Nil || (i > 0 && rows[i-1].Ref == after) {
					return []Row{r}, r.Ref, nil
				}
			}
			return nil, after, nil
		}
	}
	a := pager([]Row{{Kind: "a", Ref: refs[0], Live: true, MediaIDs: []uuid.UUID{uuid.New()}}, {Kind: "a", Ref: refs[1]}})
	b := pager([]Row{{Kind: "b", Ref: refs[2]}})
	rep, err := Run(context.Background(), holders(), nil, []Pager{a, b}, false)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Rows != 3 || rep.Claimed != 1 || rep.Released != 2 {
		t.Fatalf("report %s (asked %v)", rep, asked)
	}
	boom := func(context.Context, uuid.UUID, int) ([]Row, uuid.UUID, error) {
		return nil, uuid.Nil, errors.New("read failed")
	}
	if _, err := Run(context.Background(), holders(), nil, []Pager{boom}, false); err == nil {
		t.Fatal("a read failure did not stop the pass")
	}
}
