package claimsync

import (
	"context"
	"errors"
	"sort"
	"testing"

	"github.com/google/uuid"

	"github.com/atpost/dating-service/internal/mediaclient"
)

// fakeClaims keeps claims per (kind, ref) like media-service.
type fakeClaims struct {
	held  map[string]map[uuid.UUID]bool
	gone  map[uuid.UUID]bool
	down  bool
	calls []string
}

func newFakeClaims() *fakeClaims {
	return &fakeClaims{held: map[string]map[uuid.UUID]bool{}, gone: map[uuid.UUID]bool{}}
}

func key(kind, ref string) string { return kind + "|" + ref }

func (f *fakeClaims) Claim(_ context.Context, kind, ref string, ids []uuid.UUID) error {
	f.calls = append(f.calls, "claim "+ref)
	if f.down {
		return mediaclient.ErrUnavailable
	}
	for _, id := range ids {
		if f.gone[id] {
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

func (f *fakeClaims) Release(_ context.Context, kind, ref string, ids []uuid.UUID) error {
	f.calls = append(f.calls, "release "+ref)
	if f.down {
		return mediaclient.ErrUnavailable
	}
	if len(ids) == 0 {
		delete(f.held, key(kind, ref))
	}
	for _, id := range ids {
		delete(f.held[key(kind, ref)], id)
	}
	return nil
}

func (f *fakeClaims) ReleaseExcept(_ context.Context, kind, ref string, keep []uuid.UUID) error {
	f.calls = append(f.calls, "release-except "+ref)
	if f.down {
		return mediaclient.ErrUnavailable
	}
	if len(keep) == 0 {
		return errors.New("empty keep")
	}
	k := map[uuid.UUID]bool{}
	for _, id := range keep {
		k[id] = true
	}
	for id := range f.held[key(kind, ref)] {
		if !k[id] {
			delete(f.held[key(kind, ref)], id)
		}
	}
	return nil
}

func (f *fakeClaims) holds(kind, ref string, id uuid.UUID) bool { return f.held[key(kind, ref)][id] }

// fakeSource is a holder table: what each row names NOW (rereads see it),
// and the page the pass read earlier (which may be stale).
type fakeSource struct {
	now     map[string]*uuid.UUID
	page    []Row
	locked  []string
	pageErr error
}

func (s *fakeSource) Kinds() []string { return []string{"k"} }

func (s *fakeSource) Page(_ context.Context, _ string, after string, limit int) ([]Row, string, error) {
	if s.pageErr != nil {
		return nil, "", s.pageErr
	}
	var out []Row
	for _, r := range s.page {
		if r.Ref > after {
			out = append(out, r)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Ref < out[j].Ref })
	if len(out) > limit {
		out = out[:limit]
		return out, out[len(out)-1].Ref, nil
	}
	return out, "", nil
}

func (s *fakeSource) Lock(_ context.Context, row Row) (func(), error) {
	s.locked = append(s.locked, row.Ref)
	return func() {}, nil
}

func (s *fakeSource) Reread(_ context.Context, row Row) (Row, error) {
	return Row{Kind: row.Kind, Ref: row.Ref, MediaID: s.now[row.Ref]}, nil
}

func ptr(id uuid.UUID) *uuid.UUID { return &id }

func TestDryRunSendsNothing(t *testing.T) {
	a := uuid.New()
	src := &fakeSource{now: map[string]*uuid.UUID{"1": &a}, page: []Row{{Kind: "k", Ref: "1", MediaID: &a}, {Kind: "k", Ref: "2"}}}
	c := newFakeClaims()
	rep, err := Run(context.Background(), src, c, false)
	if err != nil || len(c.calls) != 0 || len(src.locked) != 0 {
		t.Fatalf("dry run: err=%v calls=%v locked=%v", err, c.calls, src.locked)
	}
	if rep.Rows != 2 || rep.Claimed != 1 || rep.Released != 1 {
		t.Fatalf("dry run report %s", rep)
	}
}

func TestApplyClaimsTheCurrentAssetAndDropsTheRest(t *testing.T) {
	a, stale := uuid.New(), uuid.New()
	src := &fakeSource{now: map[string]*uuid.UUID{"1": &a}, page: []Row{{Kind: "k", Ref: "1", MediaID: &a}}}
	c := newFakeClaims()
	c.held[key("k", "1")] = map[uuid.UUID]bool{stale: true}
	rep, err := Run(context.Background(), src, c, true)
	if err != nil || rep.Claimed != 1 || rep.Failed != 0 {
		t.Fatalf("apply: %s %v", rep, err)
	}
	if !c.holds("k", "1", a) || c.holds("k", "1", stale) {
		t.Fatalf("held %v; want only the current asset", c.held)
	}
	if c.calls[0] != "claim 1" {
		t.Fatalf("calls %v: the claim must come before any release", c.calls)
	}
}

// The page said one asset; the row was edited since. The pass acts on the
// re-read under the lock, never on the stale page.
func TestApplyUsesTheReReadNotTheStalePage(t *testing.T) {
	old, edited := uuid.New(), uuid.New()
	src := &fakeSource{now: map[string]*uuid.UUID{"1": &edited}, page: []Row{{Kind: "k", Ref: "1", MediaID: &old}}}
	c := newFakeClaims()
	c.held[key("k", "1")] = map[uuid.UUID]bool{edited: true}
	if _, err := Run(context.Background(), src, c, true); err != nil {
		t.Fatal(err)
	}
	if !c.holds("k", "1", edited) || c.holds("k", "1", old) {
		t.Fatalf("held %v; the pass must keep what the row holds now", c.held)
	}
	if len(src.locked) != 1 {
		t.Fatal("the row was not locked before it was re-read")
	}
}

func TestARowThatNamesNothingIsReleased(t *testing.T) {
	stale := uuid.New()
	src := &fakeSource{now: map[string]*uuid.UUID{}, page: []Row{{Kind: "k", Ref: "1"}}}
	c := newFakeClaims()
	c.held[key("k", "1")] = map[uuid.UUID]bool{stale: true}
	rep, _ := Run(context.Background(), src, c, true)
	if rep.Released != 1 || len(c.held[key("k", "1")]) != 0 {
		t.Fatalf("report %s held %v", rep, c.held)
	}
}

func TestADanglingAssetIsReportedAndFailuresCounted(t *testing.T) {
	gone := uuid.New()
	src := &fakeSource{now: map[string]*uuid.UUID{"1": &gone}, page: []Row{{Kind: "k", Ref: "1", MediaID: &gone}}}
	c := newFakeClaims()
	c.gone[gone] = true
	rep, _ := Run(context.Background(), src, c, true)
	if len(rep.Dangling) != 1 || rep.Dangling[0].MediaID != gone || rep.Dangling[0].Ref != "1" || rep.Claimed != 0 {
		t.Fatalf("report %s %+v", rep, rep.Dangling)
	}

	c = newFakeClaims()
	c.down = true
	a := uuid.New()
	src = &fakeSource{now: map[string]*uuid.UUID{"1": &a}, page: []Row{{Kind: "k", Ref: "1", MediaID: &a}}}
	if rep, _ := Run(context.Background(), src, c, true); rep.Failed != 1 || rep.Claimed != 0 {
		t.Fatalf("media-service down: %s", rep)
	}
}

func TestRunPagesThroughEveryRowAndStopsOnAReadError(t *testing.T) {
	src := &fakeSource{now: map[string]*uuid.UUID{}}
	for i := 0; i < PageSize+3; i++ {
		ref := uuid.New().String()
		id := uuid.New()
		src.now[ref] = ptr(id)
		src.page = append(src.page, Row{Kind: "k", Ref: ref, MediaID: ptr(id)})
	}
	rep, err := Run(context.Background(), src, newFakeClaims(), false)
	if err != nil || rep.Rows != PageSize+3 {
		t.Fatalf("paged %d rows of %d (err %v)", rep.Rows, PageSize+3, err)
	}
	src.pageErr = errors.New("db down")
	if _, err := Run(context.Background(), src, newFakeClaims(), false); err == nil {
		t.Fatal("a read failure must stop the pass")
	}
}
