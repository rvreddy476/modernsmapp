package claimsync

import (
	"context"
	"errors"
	"sort"
	"strings"
	"testing"

	"github.com/atpost/post-service/internal/mediaclaims"
	"github.com/atpost/post-service/internal/store/postgres"
	"github.com/google/uuid"
)

type key struct {
	kind string
	ref  uuid.UUID
}

type fakeClaims struct {
	held       map[key]map[uuid.UUID]bool
	missing    map[uuid.UUID]bool
	claimErr   error
	releaseErr error
	log        *[]string
}

func (f *fakeClaims) Claim(_ context.Context, kind string, ref uuid.UUID, ids []uuid.UUID) error {
	*f.log = append(*f.log, "claim:"+kind)
	if f.claimErr != nil {
		return f.claimErr
	}
	for _, id := range ids {
		if f.missing[id] {
			return mediaclaims.ErrNotFound
		}
	}
	k := key{kind, ref}
	if f.held[k] == nil {
		f.held[k] = map[uuid.UUID]bool{}
	}
	for _, id := range ids {
		f.held[k][id] = true
	}
	return nil
}

func (f *fakeClaims) Release(_ context.Context, kind string, ref uuid.UUID, ids []uuid.UUID) error {
	*f.log = append(*f.log, "release:"+kind)
	if f.releaseErr != nil {
		return f.releaseErr
	}
	if len(ids) == 0 {
		delete(f.held, key{kind, ref})
		return nil
	}
	for _, id := range ids {
		delete(f.held[key{kind, ref}], id)
	}
	return nil
}

func (f *fakeClaims) ReleaseExcept(_ context.Context, kind string, ref uuid.UUID, keep []uuid.UUID) error {
	*f.log = append(*f.log, "release-except:"+kind)
	if len(keep) == 0 {
		return errors.New("empty keep")
	}
	k := map[uuid.UUID]bool{}
	for _, id := range keep {
		k[id] = true
	}
	for id := range f.held[key{kind, ref}] {
		if !k[id] {
			delete(f.held[key{kind, ref}], id)
		}
	}
	return nil
}

func (f *fakeClaims) ids(kind string, ref uuid.UUID) string {
	var out []string
	for id := range f.held[key{kind, ref}] {
		out = append(out, id.String())
	}
	sort.Strings(out)
	return strings.Join(out, ",")
}

// fakeSource: the rows as the page read them, the rows as they are NOW (read
// under the lock), and media_claims' refs.
type fakeSource struct {
	page     map[string][]postgres.MediaHolding
	now      map[key]postgres.MediaHolding
	refs     []postgres.ClaimedRef
	noTable  bool
	log      *[]string
	lockFail bool
}

func (s *fakeSource) LockMediaHolder(_ context.Context, kind string, ref uuid.UUID) (func(), error) {
	if s.lockFail {
		return nil, errors.New("no connection")
	}
	*s.log = append(*s.log, "lock")
	return func() { *s.log = append(*s.log, "unlock") }, nil
}

func (s *fakeSource) MediaHeldBy(_ context.Context, kind string, ref uuid.UUID) (postgres.MediaHolding, error) {
	*s.log = append(*s.log, "read")
	if h, ok := s.now[key{kind, ref}]; ok {
		return h, nil
	}
	return postgres.MediaHolding{Kind: kind, Ref: ref}, nil
}

func (s *fakeSource) MediaHoldingPage(_ context.Context, kind string, after uuid.UUID, limit int) ([]postgres.MediaHolding, error) {
	if after != uuid.Nil {
		return nil, nil
	}
	return s.page[kind], nil
}

func (s *fakeSource) ClaimedRefsPage(_ context.Context, _ string, after postgres.ClaimCursor, limit int) ([]postgres.ClaimedRef, postgres.ClaimCursor, bool, error) {
	if s.noTable {
		return nil, after, false, postgres.ErrClaimsTableAbsent
	}
	// Two pages of one, to prove the cursor is followed.
	start := 0
	if after.Ref != "" {
		for i, r := range s.refs {
			if r.Ref.String() == after.Ref {
				start = i + 1
			}
		}
	}
	if start >= len(s.refs) {
		return nil, after, false, nil
	}
	r := s.refs[start]
	return []postgres.ClaimedRef{r}, postgres.ClaimCursor{Kind: r.Kind, Ref: r.Ref.String()}, start+1 < len(s.refs), nil
}

func rig() (*fakeSource, *fakeClaims) {
	var log []string
	src := &fakeSource{page: map[string][]postgres.MediaHolding{}, now: map[key]postgres.MediaHolding{}, log: &log}
	c := &fakeClaims{held: map[key]map[uuid.UUID]bool{}, missing: map[uuid.UUID]bool{}, log: &log}
	return src, c
}

func (s *fakeSource) row(kind string, ref uuid.UUID, live bool, ids ...uuid.UUID) postgres.MediaHolding {
	h := postgres.MediaHolding{Kind: kind, Ref: ref, MediaIDs: ids, Live: live}
	s.page[kind] = append(s.page[kind], h)
	s.now[key{kind, ref}] = h
	return h
}

func TestDryRunSendsNothing(t *testing.T) {
	src, c := rig()
	src.row(mediaclaims.KindPostCover, uuid.New(), true, uuid.New())
	src.row(mediaclaims.KindReelDraft, uuid.New(), false, uuid.New())
	orphan := uuid.New()
	src.refs = []postgres.ClaimedRef{{Kind: mediaclaims.KindEventCover, Ref: orphan}}
	rep, err := Run(context.Background(), src, c, false)
	if err != nil {
		t.Fatal(err)
	}
	for _, l := range *c.log {
		if strings.Contains(l, "claim") || strings.Contains(l, "release") || l == "lock" {
			t.Fatalf("a dry run sent %q", l)
		}
	}
	if rep.Claimed != 1 || rep.Released != 2 || rep.Orphans != 1 || rep.OrphanScan != "done" {
		t.Fatalf("report = %s", rep)
	}
}

// A live row: claim what it holds, THEN release anything else.
func TestApplyClaimsALiveRowThenReleasesTheRest(t *testing.T) {
	src, c := rig()
	ref, cover, stale := uuid.New(), uuid.New(), uuid.New()
	src.row(mediaclaims.KindPostCover, ref, true, cover)
	c.held[key{mediaclaims.KindPostCover, ref}] = map[uuid.UUID]bool{stale: true}
	rep, err := Run(context.Background(), src, c, true)
	if err != nil {
		t.Fatal(err)
	}
	if got := c.ids(mediaclaims.KindPostCover, ref); got != cover.String() {
		t.Fatalf("claims = %s, want only the current cover", got)
	}
	want := "lock read claim:post_cover release-except:post_cover unlock"
	if got := strings.Join(*c.log, " "); !strings.HasPrefix(got, want) {
		t.Fatalf("order = %s, want %s", got, want)
	}
	if rep.Claimed != 1 || rep.Failed != 0 {
		t.Fatalf("report = %s", rep)
	}
}

// Applying acts on the row as it is under the lock, not as the page read it.
func TestApplyRereadsTheRowUnderTheLock(t *testing.T) {
	src, c := rig()
	ref, old, edited := uuid.New(), uuid.New(), uuid.New()
	src.row(mediaclaims.KindReelDraft, ref, true, old)
	src.now[key{mediaclaims.KindReelDraft, ref}] = postgres.MediaHolding{Kind: mediaclaims.KindReelDraft, Ref: ref, MediaIDs: []uuid.UUID{edited}, Live: true}
	if _, err := Run(context.Background(), src, c, true); err != nil {
		t.Fatal(err)
	}
	if got := c.ids(mediaclaims.KindReelDraft, ref); got != edited.String() {
		t.Fatalf("claims = %s; the stale page was acted on", got)
	}
}

func TestApplyReleasesADeadRow(t *testing.T) {
	src, c := rig()
	ref := uuid.New()
	src.row(mediaclaims.KindReelDraft, ref, false, uuid.New())
	c.held[key{mediaclaims.KindReelDraft, ref}] = map[uuid.UUID]bool{uuid.New(): true}
	rep, _ := Run(context.Background(), src, c, true)
	if c.ids(mediaclaims.KindReelDraft, ref) != "" || rep.Released != 1 {
		t.Fatalf("a deleted draft kept its claims: %s (%s)", c.ids(mediaclaims.KindReelDraft, ref), rep)
	}
}

// One missing asset does not leave the row's other media unprotected.
func TestApplyReportsDanglingAndClaimsTheRest(t *testing.T) {
	src, c := rig()
	ref, gone, there := uuid.New(), uuid.New(), uuid.New()
	src.row(mediaclaims.KindReelDraft, ref, true, gone, there)
	c.missing[gone] = true
	rep, _ := Run(context.Background(), src, c, true)
	if got := c.ids(mediaclaims.KindReelDraft, ref); got != there.String() {
		t.Fatalf("claims = %s", got)
	}
	if len(rep.Dangling) != 1 || rep.Dangling[0].MediaID != gone {
		t.Fatalf("dangling = %+v", rep.Dangling)
	}
}

// A claim that fails releases nothing: releasing first would leave the row
// naming an unclaimed asset.
func TestApplyClaimFailureReleasesNothing(t *testing.T) {
	src, c := rig()
	ref, cover, other := uuid.New(), uuid.New(), uuid.New()
	src.row(mediaclaims.KindEventCover, ref, true, cover)
	c.held[key{mediaclaims.KindEventCover, ref}] = map[uuid.UUID]bool{other: true}
	c.claimErr = mediaclaims.ErrUnavailable
	rep, _ := Run(context.Background(), src, c, true)
	if rep.Failed != 1 {
		t.Fatalf("report = %s", rep)
	}
	for _, l := range *c.log {
		if strings.HasPrefix(l, "release") {
			t.Fatalf("released after a failed claim: %v", *c.log)
		}
	}
}

func TestApplyLockFailureIsCountedNotSkippedSilently(t *testing.T) {
	src, c := rig()
	src.row(mediaclaims.KindPostCover, uuid.New(), true, uuid.New())
	src.lockFail = true
	rep, _ := Run(context.Background(), src, c, true)
	if rep.Failed != 1 || len(*c.log) != 0 {
		t.Fatalf("report = %s log = %v", rep, *c.log)
	}
}

// Orphans: a claim whose row is gone is released; one whose row is live is
// the table pass's and is left alone. Pages are followed.
func TestOrphanPassReleasesOnlyGoneRows(t *testing.T) {
	src, c := rig()
	live, gone := uuid.New(), uuid.New()
	cover := uuid.New()
	src.row(mediaclaims.KindPostCover, live, true, cover)
	src.refs = []postgres.ClaimedRef{{Kind: mediaclaims.KindPostCover, Ref: gone}, {Kind: mediaclaims.KindPostCover, Ref: live}}
	c.held[key{mediaclaims.KindPostCover, gone}] = map[uuid.UUID]bool{uuid.New(): true}
	rep, err := Run(context.Background(), src, c, true)
	if err != nil {
		t.Fatal(err)
	}
	if c.ids(mediaclaims.KindPostCover, gone) != "" {
		t.Fatal("a purged post's claim survived the orphan pass")
	}
	if c.ids(mediaclaims.KindPostCover, live) != cover.String() {
		t.Fatal("the orphan pass touched a live row")
	}
	if rep.Orphans != 1 || rep.OrphanScan != "done" {
		t.Fatalf("report = %s", rep)
	}
}

func TestOrphanPassSkipsWhenTheClaimsTableIsNotVisible(t *testing.T) {
	src, c := rig()
	src.noTable = true
	rep, err := Run(context.Background(), src, c, true)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(rep.OrphanScan, "skipped") {
		t.Fatalf("orphan scan = %q", rep.OrphanScan)
	}
}

func TestOrphanPassCountsAnUnknownKind(t *testing.T) {
	src, c := rig()
	src.refs = []postgres.ClaimedRef{{Kind: "something_else", Ref: uuid.New()}}
	rep, _ := Run(context.Background(), src, c, true)
	if rep.Failed != 1 || len(*c.log) != 0 {
		t.Fatalf("an unknown kind must be reported, not released: %s %v", rep, *c.log)
	}
}

func TestReportString(t *testing.T) {
	r := Report{Rows: 3, Claimed: 1, Released: 2, OrphanScan: "done"}
	if got := r.String(); got != "rows=3 claimed=1 released=2 orphans=0 dangling=0 failed=0 orphan_scan=done" {
		t.Fatal(got)
	}
}

// Composer draft covers are backfilled like every other kind: a live draft's
// payload cover is claimed, a published or deleted draft's claim released,
// and an erased account's draft (row gone) found by the orphan pass.
func TestRunReconcilesPostDraftCovers(t *testing.T) {
	src, c := rig()
	live, published, erased := uuid.New(), uuid.New(), uuid.New()
	cover := uuid.New()
	src.row(mediaclaims.KindPostDraftCover, live, true, cover)
	src.row(mediaclaims.KindPostDraftCover, published, false, cover)
	c.held[key{mediaclaims.KindPostDraftCover, published}] = map[uuid.UUID]bool{cover: true}
	c.held[key{mediaclaims.KindPostDraftCover, erased}] = map[uuid.UUID]bool{cover: true}
	src.refs = []postgres.ClaimedRef{{Kind: mediaclaims.KindPostDraftCover, Ref: erased}}
	rep, err := Run(context.Background(), src, c, true)
	if err != nil {
		t.Fatal(err)
	}
	if got := c.ids(mediaclaims.KindPostDraftCover, live); got != cover.String() {
		t.Fatalf("live draft claims = %q", got)
	}
	for name, ref := range map[string]uuid.UUID{"published": published, "erased": erased} {
		if got := c.ids(mediaclaims.KindPostDraftCover, ref); got != "" {
			t.Fatalf("%s draft kept %q", name, got)
		}
	}
	if rep.Claimed != 1 || rep.Released != 2 || rep.Orphans != 1 || rep.Failed != 0 {
		t.Fatalf("report = %s", rep)
	}
}
