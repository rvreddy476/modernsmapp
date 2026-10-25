package claimsync

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/atpost/identity-profile-service/internal/mediaclient"
	"github.com/atpost/identity-profile-service/internal/store"
	"github.com/google/uuid"
)

// fakeClaims journals calls; ids in missing are answered ErrNotFound.
type fakeClaims struct {
	journal []string
	missing map[uuid.UUID]bool
	err     error
}

func (f *fakeClaims) Claim(_ context.Context, kind string, ref uuid.UUID, ids []uuid.UUID) error {
	f.journal = append(f.journal, fmt.Sprintf("claim %s %s %v", kind, ref, ids))
	if f.err != nil {
		return f.err
	}
	for _, id := range ids {
		if f.missing[id] {
			return mediaclient.ErrNotFound
		}
	}
	return nil
}

func (f *fakeClaims) Release(_ context.Context, kind string, ref uuid.UUID, ids []uuid.UUID) error {
	f.journal = append(f.journal, fmt.Sprintf("release %s %s %v", kind, ref, ids))
	return f.err
}

func (f *fakeClaims) ReleaseExcept(_ context.Context, kind string, ref uuid.UUID, keep []uuid.UUID) error {
	f.journal = append(f.journal, fmt.Sprintf("release-except %s %s %v", kind, ref, keep))
	return f.err
}

func want(t *testing.T, got []string, lines ...string) {
	t.Helper()
	if strings.Join(got, "\n") != strings.Join(lines, "\n") {
		t.Fatalf("journal:\n  %s\nwant:\n  %s", strings.Join(got, "\n  "), strings.Join(lines, "\n  "))
	}
}

type holderKey struct {
	kind string
	ref  uuid.UUID
}

// fakeHolders journals lock/unlock into the claims journal and answers the
// re-read from current (keyed by kind and ref: one profile holds two kinds).
type fakeHolders struct {
	f       *fakeClaims
	current map[holderKey]store.MediaClaimRow
	lockErr error
	readErr error
}

func (h *fakeHolders) LockMediaHolder(_ context.Context, ref uuid.UUID) (func(), error) {
	if h.lockErr != nil {
		return nil, h.lockErr
	}
	h.f.journal = append(h.f.journal, "lock "+ref.String())
	return func() { h.f.journal = append(h.f.journal, "unlock "+ref.String()) }, nil
}

func (h *fakeHolders) MediaClaimRowFor(_ context.Context, kind string, ref uuid.UUID) (store.MediaClaimRow, error) {
	if h.readErr != nil {
		return store.MediaClaimRow{}, h.readErr
	}
	r, ok := h.current[holderKey{kind, ref}]
	if !ok {
		return store.MediaClaimRow{Kind: kind, Ref: ref}, nil
	}
	return r, nil
}

func holdersFor(f *fakeClaims, rows []store.MediaClaimRow) *fakeHolders {
	h := &fakeHolders{f: f, current: map[holderKey]store.MediaClaimRow{}}
	for _, r := range rows {
		h.current[holderKey{r.Kind, r.Ref}] = r
	}
	return h
}

// reconcileWith runs Reconcile where the re-read returns the page row, and
// strips the lock lines so a test can assert the claim calls alone.
func reconcileWith(f *fakeClaims, rows []store.MediaClaimRow, apply bool, rep *Report) {
	Reconcile(context.Background(), holdersFor(f, rows), f, rows, apply, rep)
	kept := f.journal[:0]
	for _, l := range f.journal {
		if !strings.HasPrefix(l, "lock ") && !strings.HasPrefix(l, "unlock ") {
			kept = append(kept, l)
		}
	}
	f.journal = kept
}

func avatarRow(ref uuid.UUID, live bool, ids ...uuid.UUID) store.MediaClaimRow {
	return store.MediaClaimRow{Kind: mediaclient.KindProfileAvatar, Ref: ref, MediaIDs: ids, Live: live}
}

// A live row: claim its image FIRST, then drop anything else it holds.
func TestLiveRowIsClaimedBeforeItsStaleClaimsGo(t *testing.T) {
	f := &fakeClaims{}
	ref, a := uuid.New(), uuid.New()
	var rep Report
	reconcileWith(f, []store.MediaClaimRow{avatarRow(ref, true, a, uuid.Nil)}, true, &rep)
	want(t, f.journal,
		fmt.Sprintf("claim profile_avatar %s [%s]", ref, a),
		fmt.Sprintf("release-except profile_avatar %s [%s]", ref, a))
	if rep.Claimed != 1 || rep.Released != 0 || rep.Failed != 0 {
		t.Fatalf("report %s", rep)
	}
}

// A profile gone (or its column cleared) holds nothing.
func TestGoneOrEmptyRowIsReleased(t *testing.T) {
	for name, row := range map[string]store.MediaClaimRow{
		"gone":  {Kind: mediaclient.KindProfileCover, Ref: uuid.New(), MediaIDs: []uuid.UUID{uuid.New()}, Live: false},
		"empty": {Kind: mediaclient.KindProfileCover, Ref: uuid.New(), Live: true},
	} {
		f := &fakeClaims{}
		var rep Report
		reconcileWith(f, []store.MediaClaimRow{row}, true, &rep)
		want(t, f.journal, fmt.Sprintf("release profile_cover %s []", row.Ref))
		if rep.Released != 1 || rep.Claimed != 0 {
			t.Fatalf("%s: report %s", name, rep)
		}
	}
}

// The image is gone from media-service: reported, and the profile keeps no
// claim of that kind.
func TestDanglingImageIsReportedAndTheRowReleased(t *testing.T) {
	ref, gone := uuid.New(), uuid.New()
	f := &fakeClaims{missing: map[uuid.UUID]bool{gone: true}}
	var rep Report
	reconcileWith(f, []store.MediaClaimRow{avatarRow(ref, true, gone)}, true, &rep)
	if len(rep.Dangling) != 1 || rep.Dangling[0].MediaID != gone || rep.Dangling[0].Ref != ref || rep.Dangling[0].Kind != mediaclient.KindProfileAvatar {
		t.Fatalf("dangling %+v", rep.Dangling)
	}
	if last := f.journal[len(f.journal)-1]; last != fmt.Sprintf("release profile_avatar %s []", ref) {
		t.Fatalf("journal %v", f.journal)
	}
}

// media-service down: nothing is released (a release without its claim
// would be the unsafe direction) and the row is counted as failed.
func TestUnavailableNeverReleasesALiveRow(t *testing.T) {
	f := &fakeClaims{err: mediaclient.ErrUnavailable}
	var rep Report
	reconcileWith(f, []store.MediaClaimRow{avatarRow(uuid.New(), true, uuid.New())}, true, &rep)
	for _, line := range f.journal {
		if strings.HasPrefix(line, "release") {
			t.Fatalf("released a live row after its claim failed: %v", f.journal)
		}
	}
	if rep.Failed != 1 || rep.Claimed != 0 {
		t.Fatalf("report %s", rep)
	}
}

func TestDryRunSendsNothing(t *testing.T) {
	f := &fakeClaims{}
	var rep Report
	reconcileWith(f, []store.MediaClaimRow{
		avatarRow(uuid.New(), true, uuid.New()),
		avatarRow(uuid.New(), false, uuid.New()),
	}, false, &rep)
	if len(f.journal) != 0 || rep.Claimed != 1 || rep.Released != 1 || rep.Rows != 2 {
		t.Fatalf("journal %v report %s", f.journal, rep)
	}
}

// The page was read earlier; the row is re-read under the writers' lock and
// the CURRENT image is what gets claimed and kept.
func TestReconcileActsOnTheRowAsItIsNowUnderTheLock(t *testing.T) {
	ref, old, now := uuid.New(), uuid.New(), uuid.New()
	f := &fakeClaims{}
	h := &fakeHolders{f: f, current: map[holderKey]store.MediaClaimRow{
		{mediaclient.KindProfileAvatar, ref}: avatarRow(ref, true, now),
	}}
	var rep Report
	Reconcile(context.Background(), h, f, []store.MediaClaimRow{avatarRow(ref, true, old)}, true, &rep)
	want(t, f.journal,
		"lock "+ref.String(),
		fmt.Sprintf("claim profile_avatar %s [%s]", ref, now),
		fmt.Sprintf("release-except profile_avatar %s [%s]", ref, now),
		"unlock "+ref.String())
}

// No lock (or no re-read), no action: acting unlocked is how a stale release
// happens.
func TestReconcileWithoutTheLockOrReReadSendsNothing(t *testing.T) {
	for name, h := range map[string]*fakeHolders{
		"lock":    {lockErr: errors.New("db down")},
		"re-read": {readErr: errors.New("db down")},
	} {
		f := &fakeClaims{}
		h.f = f
		var rep Report
		Reconcile(context.Background(), h, f, []store.MediaClaimRow{avatarRow(uuid.New(), true, uuid.New())}, true, &rep)
		for _, l := range f.journal {
			if !strings.HasPrefix(l, "lock ") && !strings.HasPrefix(l, "unlock ") {
				t.Fatalf("%s failure: sent %q", name, l)
			}
		}
		if rep.Failed != 1 {
			t.Fatalf("%s failure: report %s", name, rep)
		}
	}
}

// fakeSource serves fixed pages and records cursors.
type fakeSource struct {
	fakeHolders
	pages   [][]store.MediaClaimRow
	cursors []uuid.UUID
	err     error
}

func (s *fakeSource) ProfileMediaClaimRows(_ context.Context, after uuid.UUID, _ int) ([]store.MediaClaimRow, uuid.UUID, error) {
	s.cursors = append(s.cursors, after)
	if s.err != nil {
		return nil, after, s.err
	}
	if len(s.pages) == 0 {
		return nil, after, nil
	}
	page := s.pages[0]
	s.pages = s.pages[1:]
	return page, page[len(page)-1].Ref, nil
}

func TestRunWalksEveryPage(t *testing.T) {
	a, b := uuid.New(), uuid.New()
	first := []store.MediaClaimRow{
		avatarRow(a, true, uuid.New()),
		{Kind: mediaclient.KindProfileCover, Ref: a, MediaIDs: []uuid.UUID{uuid.New()}, Live: true},
	}
	second := []store.MediaClaimRow{avatarRow(b, true, uuid.New())}
	fc := &fakeClaims{}
	src := &fakeSource{
		fakeHolders: *holdersFor(fc, append(append([]store.MediaClaimRow{}, first...), second...)),
		pages:       [][]store.MediaClaimRow{first, second},
	}
	rep, err := Run(context.Background(), src, fc, true)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Rows != 3 || rep.Claimed != 3 || rep.Failed != 0 {
		t.Fatalf("report %s", rep)
	}
	if len(src.cursors) != 3 || src.cursors[0] != uuid.Nil || src.cursors[1] != a || src.cursors[2] != b {
		t.Fatalf("cursors %v", src.cursors)
	}
}

func TestRunStopsOnAReadFailure(t *testing.T) {
	if _, err := Run(context.Background(), &fakeSource{fakeHolders: fakeHolders{f: &fakeClaims{}}, err: errors.New("db down")}, &fakeClaims{}, true); err == nil {
		t.Fatal("a read failure must stop the pass")
	}
}
