package claimsync

import (
	"context"
	"errors"
	"sort"
	"testing"
	"time"

	"github.com/atpost/user-service/internal/mediaclient"
	"github.com/atpost/user-service/internal/store"
	"github.com/google/uuid"
)

// fakeClaims is media-service's claim table. missing ids are refused the way
// media-service refuses an asset that does not exist.
type fakeClaims struct {
	held    map[string]map[uuid.UUID]bool
	missing map[uuid.UUID]bool
	err     error
	calls   int
}

func newClaims() *fakeClaims {
	return &fakeClaims{held: map[string]map[uuid.UUID]bool{}, missing: map[uuid.UUID]bool{}}
}

func k(kind string, ref uuid.UUID) string { return kind + "/" + ref.String() }

func (f *fakeClaims) Claim(_ context.Context, kind string, ref uuid.UUID, ids []uuid.UUID) error {
	f.calls++
	if f.err != nil {
		return f.err
	}
	for _, id := range ids {
		if f.missing[id] {
			return mediaclient.ErrNotFound
		}
	}
	if f.held[k(kind, ref)] == nil {
		f.held[k(kind, ref)] = map[uuid.UUID]bool{}
	}
	for _, id := range ids {
		f.held[k(kind, ref)][id] = true
	}
	return nil
}

func (f *fakeClaims) Release(_ context.Context, kind string, ref uuid.UUID, ids []uuid.UUID) error {
	f.calls++
	if f.err != nil {
		return f.err
	}
	if len(ids) == 0 {
		delete(f.held, k(kind, ref))
		return nil
	}
	for _, id := range ids {
		delete(f.held[k(kind, ref)], id)
	}
	return nil
}

func (f *fakeClaims) ReleaseExcept(_ context.Context, kind string, ref uuid.UUID, keep []uuid.UUID) error {
	f.calls++
	if f.err != nil {
		return f.err
	}
	if len(keep) == 0 {
		return errors.New("empty keep sent")
	}
	ks := map[uuid.UUID]bool{}
	for _, id := range keep {
		ks[id] = true
	}
	for id := range f.held[k(kind, ref)] {
		if !ks[id] {
			delete(f.held[k(kind, ref)], id)
		}
	}
	return nil
}

func (f *fakeClaims) holds(kind string, ref, id uuid.UUID) bool { return f.held[k(kind, ref)][id] }

// fakeSource is the holder tables: rows by kind/ref ("now"), and the pages
// the full pass reads (which may be stale relative to rows).
type fakeSource struct {
	now      map[string]store.MediaClaimRow
	channels []store.MediaClaimRow
	pages    []store.MediaClaimRow
	items    []store.MediaClaimRow
	pending  []store.PendingMediaClaimHolder
	locked   []uuid.UUID
}

func (s *fakeSource) LockMediaHolder(_ context.Context, ref uuid.UUID) (func(), error) {
	s.locked = append(s.locked, ref)
	return func() {}, nil
}

func (s *fakeSource) MediaClaimRowFor(_ context.Context, kind string, ref uuid.UUID) (store.MediaClaimRow, error) {
	if r, ok := s.now[k(kind, ref)]; ok {
		return r, nil
	}
	return store.MediaClaimRow{Kind: kind, Ref: ref}, nil
}

func (s *fakeSource) PendingMediaClaimHolders(_ context.Context, limit int) ([]store.PendingMediaClaimHolder, error) {
	out := append([]store.PendingMediaClaimHolder(nil), s.pending...)
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (s *fakeSource) ClearMediaClaimHolderPending(_ context.Context, p store.PendingMediaClaimHolder) error {
	for i, q := range s.pending {
		if q.Kind == p.Kind && q.Ref == p.Ref && q.EnqueuedAt.Equal(p.EnqueuedAt) {
			s.pending = append(s.pending[:i], s.pending[i+1:]...)
			return nil
		}
	}
	return nil
}

func page(rows []store.MediaClaimRow, after uuid.UUID, limit int) ([]store.MediaClaimRow, uuid.UUID, error) {
	sorted := append([]store.MediaClaimRow(nil), rows...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Ref.String() < sorted[j].Ref.String() })
	var out []store.MediaClaimRow
	last := after
	for _, r := range sorted {
		if after != uuid.Nil && r.Ref.String() <= after.String() {
			continue
		}
		if len(out) == limit {
			break
		}
		out = append(out, r)
		last = r.Ref
	}
	return out, last, nil
}

func (s *fakeSource) ChannelMediaClaimRows(_ context.Context, after uuid.UUID, limit int) ([]store.MediaClaimRow, uuid.UUID, error) {
	return page(s.channels, after, limit)
}
func (s *fakeSource) PortfolioMediaClaimRows(_ context.Context, after uuid.UUID, limit int) ([]store.MediaClaimRow, uuid.UUID, error) {
	return page(s.items, after, limit)
}
func (s *fakeSource) BusinessPageMediaClaimRows(_ context.Context, after uuid.UUID, limit int) ([]store.MediaClaimRow, uuid.UUID, error) {
	return page(s.pages, after, limit)
}

func row(kind string, live bool, ids ...uuid.UUID) store.MediaClaimRow {
	return store.MediaClaimRow{Kind: kind, Ref: uuid.New(), MediaIDs: ids, Live: live}
}

func sourceOf(rows ...store.MediaClaimRow) *fakeSource {
	s := &fakeSource{now: map[string]store.MediaClaimRow{}}
	for _, r := range rows {
		s.now[k(r.Kind, r.Ref)] = r
		switch r.Kind {
		case mediaclient.KindChannelAvatar, mediaclient.KindChannelBanner:
			s.channels = append(s.channels, r)
		case mediaclient.KindPortfolioItem:
			s.items = append(s.items, r)
		default:
			s.pages = append(s.pages, r)
		}
	}
	return s
}

func TestDryRunSendsNothing(t *testing.T) {
	live := row(mediaclient.KindChannelAvatar, true, uuid.New())
	dead := row(mediaclient.KindBusinessPageCover, false, uuid.New())
	src := sourceOf(live, dead)
	c := newClaims()
	rep, err := Run(context.Background(), src, c, false)
	if err != nil {
		t.Fatal(err)
	}
	if c.calls != 0 || len(src.locked) != 0 {
		t.Fatalf("a dry run sent %d calls / took %d locks", c.calls, len(src.locked))
	}
	if rep.Rows != 2 || rep.Claimed != 1 || rep.Released != 1 {
		t.Fatalf("report %s", rep)
	}
	// A dry run works without a claim client at all.
	if _, err := Run(context.Background(), src, nil, false); err != nil {
		t.Fatal(err)
	}
}

func TestApplyClaimsLiveRowsAndReleasesDeadOnes(t *testing.T) {
	media, stale := uuid.New(), uuid.New()
	live := row(mediaclient.KindPortfolioItem, true, media)
	dead := row(mediaclient.KindBusinessPageAvatar, false, uuid.New())
	src := sourceOf(live, dead)
	c := newClaims()
	_ = c.Claim(context.Background(), live.Kind, live.Ref, []uuid.UUID{stale}) // a release lost after an edit
	_ = c.Claim(context.Background(), dead.Kind, dead.Ref, dead.MediaIDs)

	rep, err := Run(context.Background(), src, c, true)
	if err != nil || rep.Failed != 0 {
		t.Fatalf("%v %s", err, rep)
	}
	if !c.holds(live.Kind, live.Ref, media) {
		t.Fatal("backfill did not claim what the live row shows")
	}
	if c.holds(live.Kind, live.Ref, stale) {
		t.Fatal("repair did not release what the live row no longer shows")
	}
	if len(c.held[k(dead.Kind, dead.Ref)]) != 0 {
		t.Fatal("a disabled page's claims were kept")
	}
}

// Applying re-reads each holder under its lock: the page may be stale.
func TestApplyActsOnTheRowAsItIsNow(t *testing.T) {
	old, now := uuid.New(), uuid.New()
	r := row(mediaclient.KindChannelBanner, true, old)
	src := sourceOf(r)
	cur := r
	cur.MediaIDs = []uuid.UUID{now}
	src.now[k(r.Kind, r.Ref)] = cur // an edit after the page was read
	c := newClaims()
	_ = c.Claim(context.Background(), r.Kind, r.Ref, []uuid.UUID{now})

	if _, err := Run(context.Background(), src, c, true); err != nil {
		t.Fatal(err)
	}
	if !c.holds(r.Kind, r.Ref, now) {
		t.Fatal("the pass released the claim the current row relies on")
	}
	if c.holds(r.Kind, r.Ref, old) {
		t.Fatal("the pass claimed a set the row no longer holds")
	}
	if len(src.locked) == 0 {
		t.Fatal("applied without the holder lock")
	}
}

func TestDanglingAndInvalidAreReportedNotClaimed(t *testing.T) {
	good, gone := uuid.New(), uuid.New()
	r := row(mediaclient.KindChannelAvatar, true, good, gone)
	inv := store.MediaClaimRow{Kind: mediaclient.KindBusinessPageCover, Ref: uuid.New(), Invalid: []string{"logo.png"}, Live: true}
	src := sourceOf(r, inv)
	c := newClaims()
	c.missing[gone] = true
	rep, err := Run(context.Background(), src, c, true)
	if err != nil {
		t.Fatal(err)
	}
	if !c.holds(r.Kind, r.Ref, good) {
		t.Fatal("one dangling id left the row's other media unprotected")
	}
	if len(rep.Dangling) != 1 || rep.Dangling[0].MediaID != gone {
		t.Fatalf("dangling %v", rep.Dangling)
	}
	if len(rep.Invalid) != 1 || rep.Invalid[0].Value != "logo.png" {
		t.Fatalf("invalid %v", rep.Invalid)
	}
}

func TestDrainClearsOnlySettledEntries(t *testing.T) {
	ref := uuid.New()
	src := sourceOf()
	at := time.Unix(1_700_000_000, 0)
	src.pending = []store.PendingMediaClaimHolder{{Kind: mediaclient.KindPortfolioItem, Ref: ref, EnqueuedAt: at}}
	c := newClaims()
	_ = c.Claim(context.Background(), mediaclient.KindPortfolioItem, ref, []uuid.UUID{uuid.New()})

	c.err = errors.New("down")
	var rep Report
	if n, err := Drain(context.Background(), src, c, 10, &rep); err != nil || n != 0 || len(src.pending) != 1 || rep.Failed != 1 {
		t.Fatalf("a failed settle cleared its entry: n=%d err=%v pending=%d %s", n, err, len(src.pending), rep)
	}
	c.err = nil
	rep = Report{}
	if n, err := Drain(context.Background(), src, c, 10, &rep); err != nil || n != 1 || len(src.pending) != 0 {
		t.Fatalf("n=%d err=%v pending=%d", n, err, len(src.pending))
	}
	if len(c.held[k(mediaclient.KindPortfolioItem, ref)]) != 0 {
		t.Fatal("the deleted holder's claims were not released")
	}
}

func TestRunPagesThroughEveryTable(t *testing.T) {
	var rows []store.MediaClaimRow
	for i := 0; i < PageSize+3; i++ {
		rows = append(rows, row(mediaclient.KindPortfolioItem, true, uuid.New()))
	}
	rows = append(rows, row(mediaclient.KindChannelAvatar, true, uuid.New()), row(mediaclient.KindBusinessPageAvatar, true, uuid.New()))
	src := sourceOf(rows...)
	rep, err := Run(context.Background(), src, newClaims(), false)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Rows != len(rows) {
		t.Fatalf("read %d of %d rows", rep.Rows, len(rows))
	}
}
