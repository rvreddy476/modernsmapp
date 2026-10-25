package mediaclaims

import (
	"context"
	"errors"
	"slices"
	"sync"
	"testing"

	"github.com/atpost/group-service/internal/mediaclient"
	"github.com/google/uuid"
)

// fakeMedia is media-service: an asset table, and the claims it keeps.
type fakeMedia struct {
	mu       sync.Mutex
	assets   map[uuid.UUID]*mediaclient.Asset
	held     map[string]map[uuid.UUID]bool // kind|ref -> media
	gets     int
	getErr   error
	claimErr error
	relErr   error
	calls    []string // "claim kind ref" / "release kind ref", in order
}

func newFake(assets ...*mediaclient.Asset) *fakeMedia {
	f := &fakeMedia{assets: map[uuid.UUID]*mediaclient.Asset{}, held: map[string]map[uuid.UUID]bool{}}
	for _, a := range assets {
		f.assets[a.ID] = a
	}
	return f
}

func holder(kind string, ref uuid.UUID) string { return kind + "|" + ref.String() }

func (f *fakeMedia) Get(_ context.Context, id uuid.UUID) (*mediaclient.Asset, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.gets++
	if f.getErr != nil {
		return nil, f.getErr
	}
	a, ok := f.assets[id]
	if !ok {
		return nil, mediaclient.ErrNotFound
	}
	return a, nil
}

func (f *fakeMedia) Claim(_ context.Context, kind string, ref uuid.UUID, ids []uuid.UUID) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, "claim "+holder(kind, ref))
	if f.claimErr != nil {
		return f.claimErr
	}
	for _, id := range ids { // all or nothing
		if _, ok := f.assets[id]; !ok {
			return mediaclient.ErrNotFound
		}
	}
	h := f.held[holder(kind, ref)]
	if h == nil {
		h = map[uuid.UUID]bool{}
		f.held[holder(kind, ref)] = h
	}
	for _, id := range ids {
		h[id] = true
	}
	return nil
}

func (f *fakeMedia) Release(_ context.Context, kind string, ref uuid.UUID, ids []uuid.UUID) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, "release "+holder(kind, ref))
	if f.relErr != nil {
		return f.relErr
	}
	if len(ids) == 0 {
		delete(f.held, holder(kind, ref))
		return nil
	}
	for _, id := range ids {
		delete(f.held[holder(kind, ref)], id)
	}
	return nil
}

func (f *fakeMedia) holds(kind string, ref, id uuid.UUID) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.held[holder(kind, ref)][id]
}

func asset(owner uuid.UUID, processing, moderation string) *mediaclient.Asset {
	return &mediaclient.Asset{ID: uuid.New(), UploaderID: owner, ProcessingStatus: processing, ModerationStatus: moderation}
}

const kind = "k"

func TestCheckAsset(t *testing.T) {
	me := uuid.New()
	for name, tc := range map[string]struct {
		a    *mediaclient.Asset
		want error
	}{
		"ready":            {asset(me, "ready", "passed"), nil},
		"uploaded":         {asset(me, "uploaded", "pending"), nil},
		"processing":       {asset(me, "processing", "passed"), nil},
		"someone else's":   {asset(uuid.New(), "ready", "passed"), ErrMediaNotOwned},
		"pending upload":   {asset(me, "pending_upload", "passed"), ErrMediaNotReady},
		"failed":           {asset(me, "failed", "passed"), ErrMediaNotReady},
		"deleted":          {asset(me, "deleted", "passed"), ErrMediaNotReady},
		"unknown status":   {asset(me, "weird", "passed"), ErrMediaNotReady},
		"empty status":     {asset(me, "", "passed"), ErrMediaNotReady},
		"rejected":         {asset(me, "ready", "rejected"), ErrMediaNotReady},
		"empty moderation": {asset(me, "ready", ""), ErrMediaNotReady},
	} {
		if err := CheckAsset(tc.a, me); !errors.Is(err, tc.want) || (tc.want == nil && err != nil) {
			t.Errorf("%s: got %v, want %v", name, err, tc.want)
		}
	}
}

func TestAttachClaimsBeforeTheWrite(t *testing.T) {
	me, ref := uuid.New(), uuid.New()
	a := asset(me, "ready", "passed")
	f := newFake(a)
	g := New(f, f)
	wrote := false
	err := g.Attach(context.Background(), me, []Change{{Kind: kind, Ref: ref, Add: []uuid.UUID{a.ID}}}, func() error {
		if !f.holds(kind, ref, a.ID) {
			t.Fatal("the row was written before its media was claimed")
		}
		wrote = true
		return nil
	})
	if err != nil || !wrote {
		t.Fatalf("attach: %v (wrote %v)", err, wrote)
	}
	if !f.holds(kind, ref, a.ID) {
		t.Fatal("the claim did not survive a successful write")
	}
}

// Every refusal of the gate stops the write and claims nothing.
func TestAttachRefusalsWriteNothing(t *testing.T) {
	me := uuid.New()
	mine := asset(me, "ready", "passed")
	theirs := asset(uuid.New(), "ready", "passed")
	pending := asset(me, "pending_upload", "passed")
	rejected := asset(me, "ready", "rejected")
	f := newFake(mine, theirs, pending, rejected)
	tooMany := make([]uuid.UUID, MaxPerCall+1)
	for i := range tooMany {
		tooMany[i] = mine.ID
	}
	for name, tc := range map[string]struct {
		ids  []uuid.UUID
		want error
	}{
		"someone else's":    {[]uuid.UUID{mine.ID, theirs.ID}, ErrMediaNotOwned},
		"unconfirmed":       {[]uuid.UUID{pending.ID}, ErrMediaNotReady},
		"rejected":          {[]uuid.UUID{rejected.ID}, ErrMediaNotReady},
		"missing":           {[]uuid.UUID{uuid.New()}, ErrMediaNotFound},
		"nil id":            {[]uuid.UUID{uuid.Nil}, ErrMediaNotFound},
		"too many (bounds)": {tooMany, nil},
	} {
		ref := uuid.New()
		err := New(f, f).Attach(context.Background(), me, []Change{{Kind: kind, Ref: ref, Add: tc.ids}}, func() error {
			t.Fatalf("%s: wrote", name)
			return nil
		})
		if err == nil || (tc.want != nil && !errors.Is(err, tc.want)) {
			t.Errorf("%s: got %v, want %v", name, err, tc.want)
		}
		if len(f.held[holder(kind, ref)]) != 0 {
			t.Errorf("%s: a refused attach left a claim", name)
		}
	}
}

// uuid.Nil is refused without asking media-service.
func TestNilIDNeverCallsOut(t *testing.T) {
	f := newFake()
	err := New(f, f).Attach(context.Background(), uuid.New(), []Change{{Kind: kind, Ref: uuid.New(), Add: []uuid.UUID{uuid.Nil}}},
		func() error { t.Fatal("wrote"); return nil })
	if !errors.Is(err, ErrMediaNotFound) || f.gets != 0 {
		t.Fatalf("nil id: %v after %d lookups", err, f.gets)
	}
}

// Fail closed: no gate, no lookup, no claims, or media-service down refuses a
// write that adds media; a write that adds nothing never calls out.
func TestAttachFailsClosed(t *testing.T) {
	me, ref := uuid.New(), uuid.New()
	a := asset(me, "ready", "passed")
	add := []Change{{Kind: kind, Ref: ref, Add: []uuid.UUID{a.ID}}}
	noWrite := func() error { t.Fatal("wrote without a claim"); return nil }

	var none *Gate
	if err := none.Attach(context.Background(), me, add, noWrite); !errors.Is(err, ErrMediaUnavailable) {
		t.Fatalf("nil gate = %v", err)
	}
	if err := FromClient(nil).Attach(context.Background(), me, add, noWrite); !errors.Is(err, ErrMediaUnavailable) {
		t.Fatalf("unconfigured client = %v", err)
	}
	f := newFake(a)
	if err := New(f, nil).Attach(context.Background(), me, add, noWrite); !errors.Is(err, ErrMediaUnavailable) {
		t.Fatalf("lookup without claims = %v", err)
	}
	down := newFake(a)
	down.getErr = mediaclient.ErrUnavailable
	if err := New(down, down).Attach(context.Background(), me, add, noWrite); !errors.Is(err, ErrMediaUnavailable) {
		t.Fatalf("lookup down = %v", err)
	}
	claimDown := newFake(a)
	claimDown.claimErr = mediaclient.ErrUnavailable
	if err := New(claimDown, claimDown).Attach(context.Background(), me, add, noWrite); !errors.Is(err, ErrMediaUnavailable) {
		t.Fatalf("claim down = %v", err)
	}

	// Removing media, or touching none, needs no media-service at all.
	wrote := false
	drop := []Change{{Kind: kind, Ref: ref, Drop: []uuid.UUID{a.ID}}}
	if err := none.Attach(context.Background(), me, drop, func() error { wrote = true; return nil }); err != nil || !wrote {
		t.Fatalf("a removal was refused for want of media-service: %v", err)
	}
}

// The owner's delete won the race: the second asset is gone by the time of
// its claim. Nothing is written, and the claim made for the first change goes.
func TestClaimRefusedCompensatesAndWritesNothing(t *testing.T) {
	me, ref := uuid.New(), uuid.New()
	a, gone := asset(me, "ready", "passed"), asset(me, "ready", "passed")
	f := newFake(a) // gone was deleted after the gate checked it
	err := New(f, f).Write(context.Background(), []Change{
		{Kind: "avatar", Ref: ref, Add: []uuid.UUID{a.ID}},
		{Kind: "banner", Ref: ref, Add: []uuid.UUID{gone.ID}},
	}, func() error { t.Fatal("wrote"); return nil })
	if !errors.Is(err, ErrMediaNotFound) {
		t.Fatalf("got %v", err)
	}
	if f.holds("avatar", ref, a.ID) {
		t.Fatal("a claim made before a refused one was not released")
	}
}

func TestFailedWriteReleasesExactlyWhatItClaimed(t *testing.T) {
	me, ref := uuid.New(), uuid.New()
	kept, added := asset(me, "ready", "passed"), asset(me, "ready", "passed")
	f := newFake(kept, added)
	g := New(f, f)
	if err := g.Write(context.Background(), []Change{{Kind: kind, Ref: ref, Add: []uuid.UUID{kept.ID}}}, func() error { return nil }); err != nil {
		t.Fatal(err)
	}
	boom := errors.New("write failed")
	err := g.Attach(context.Background(), me, []Change{{Kind: kind, Ref: ref, Add: []uuid.UUID{added.ID}, Drop: []uuid.UUID{kept.ID}}},
		func() error { return boom })
	if !errors.Is(err, boom) {
		t.Fatalf("got %v", err)
	}
	if f.holds(kind, ref, added.ID) {
		t.Fatal("a failed write left its new claim behind")
	}
	if !f.holds(kind, ref, kept.ID) {
		t.Fatal("a failed write released media the row still holds")
	}
}

func TestDropIsReleasedOnlyAfterTheWrite(t *testing.T) {
	me, ref := uuid.New(), uuid.New()
	old, next := asset(me, "ready", "passed"), asset(me, "ready", "passed")
	f := newFake(old, next)
	g := New(f, f)
	_ = g.Write(context.Background(), []Change{{Kind: kind, Ref: ref, Add: []uuid.UUID{old.ID}}}, func() error { return nil })
	err := g.Attach(context.Background(), me, []Change{{Kind: kind, Ref: ref, Add: []uuid.UUID{next.ID}, Drop: []uuid.UUID{old.ID}}},
		func() error {
			if !f.holds(kind, ref, old.ID) {
				t.Fatal("the replaced media was released before the row stopped naming it")
			}
			return nil
		})
	if err != nil {
		t.Fatal(err)
	}
	if f.holds(kind, ref, old.ID) || !f.holds(kind, ref, next.ID) {
		t.Fatalf("after the replace: %v", f.held)
	}
}

// A release that fails is logged and reported, never an error of the write.
func TestReleaseIsBestEffort(t *testing.T) {
	f := newFake()
	f.relErr = mediaclient.ErrUnavailable
	if New(f, f).Release(context.Background(), kind, uuid.New(), nil) {
		t.Fatal("a failed release reported success")
	}
	var none *Gate
	if none.Release(context.Background(), kind, uuid.New(), nil) {
		t.Fatal("a release with no client reported success")
	}
	ok := newFake()
	if !New(ok, ok).Release(context.Background(), kind, uuid.New(), nil) {
		t.Fatal("a release failed")
	}
}

// A release outlives the request that triggered it.
func TestReleaseSurvivesACancelledRequest(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	f := &ctxCheckingClaims{}
	if !New(nil, f).Release(ctx, kind, uuid.New(), nil) || f.sawCancelled {
		t.Fatal("the release ran on the cancelled request context")
	}
}

type ctxCheckingClaims struct{ sawCancelled bool }

func (c *ctxCheckingClaims) Claim(context.Context, string, uuid.UUID, []uuid.UUID) error { return nil }
func (c *ctxCheckingClaims) Release(ctx context.Context, _ string, _ uuid.UUID, _ []uuid.UUID) error {
	c.sawCancelled = ctx.Err() != nil
	return ctx.Err()
}

func TestDiff(t *testing.T) {
	a, b, c := uuid.New(), uuid.New(), uuid.New()
	added, removed := Diff([]uuid.UUID{a, b, b, uuid.Nil}, []uuid.UUID{b, c, c, uuid.Nil})
	if !slices.Equal(added, []uuid.UUID{c}) || !slices.Equal(removed, []uuid.UUID{a}) {
		t.Fatalf("added %v removed %v", added, removed)
	}
	added, removed = OptionalDiff(&a, &a)
	if len(added)+len(removed) != 0 {
		t.Fatal("an unchanged column is not a change")
	}
	added, removed = OptionalDiff(nil, &a)
	if !slices.Equal(added, []uuid.UUID{a}) || len(removed) != 0 {
		t.Fatalf("set: %v %v", added, removed)
	}
	added, removed = OptionalDiff(&a, nil)
	if len(added) != 0 || !slices.Equal(removed, []uuid.UUID{a}) {
		t.Fatalf("clear: %v %v", added, removed)
	}
	if got := Unique([]uuid.UUID{a, uuid.Nil, a, b}); !slices.Equal(got, []uuid.UUID{a, b}) {
		t.Fatalf("unique %v", got)
	}
}
