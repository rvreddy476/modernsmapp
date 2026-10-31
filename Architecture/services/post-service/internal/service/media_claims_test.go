package service

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"
	"testing"

	"github.com/atpost/post-service/internal/mediaclaims"
	"github.com/atpost/post-service/internal/store/postgres"
	"github.com/google/uuid"
)

// ── fakes ────────────────────────────────────────────────────────────────

type claimKey struct {
	kind string
	ref  uuid.UUID
}

// fakeClaims is media-service's claim table, in memory. log records every
// call in order, shared with fakeHolders so the protocol's order is visible.
type fakeClaims struct {
	mu         sync.Mutex
	claims     map[claimKey]map[uuid.UUID]bool
	log        *[]string
	claimErr   error
	releaseErr error
	missing    map[uuid.UUID]bool // ids media-service does not have
}

func newFakeClaims() *fakeClaims {
	var log []string
	return &fakeClaims{claims: map[claimKey]map[uuid.UUID]bool{}, log: &log, missing: map[uuid.UUID]bool{}}
}

func (f *fakeClaims) Claim(_ context.Context, kind string, ref uuid.UUID, ids []uuid.UUID) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	*f.log = append(*f.log, "claim")
	if f.claimErr != nil {
		return f.claimErr
	}
	for _, id := range ids {
		if f.missing[id] {
			return mediaclaims.ErrNotFound
		}
	}
	k := claimKey{kind, ref}
	if f.claims[k] == nil {
		f.claims[k] = map[uuid.UUID]bool{}
	}
	for _, id := range ids {
		f.claims[k][id] = true
	}
	return nil
}

func (f *fakeClaims) Release(_ context.Context, kind string, ref uuid.UUID, ids []uuid.UUID) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	*f.log = append(*f.log, "release")
	if f.releaseErr != nil {
		return f.releaseErr
	}
	k := claimKey{kind, ref}
	if len(ids) == 0 {
		delete(f.claims, k)
		return nil
	}
	for _, id := range ids {
		delete(f.claims[k], id)
	}
	return nil
}

func (f *fakeClaims) held(kind string, ref uuid.UUID) []uuid.UUID {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []uuid.UUID
	for id := range f.claims[claimKey{kind, ref}] {
		out = append(out, id)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].String() < out[j].String() })
	return out
}

// fakeHolders is the holder rows plus the per-row lock.
type fakeHolders struct {
	rows    map[claimKey]postgres.MediaHolding
	log     *[]string
	readErr error
	lockErr error
	reads   int
}

func newFakeHolders(log *[]string) *fakeHolders {
	return &fakeHolders{rows: map[claimKey]postgres.MediaHolding{}, log: log}
}

func (h *fakeHolders) LockMediaHolder(_ context.Context, kind string, ref uuid.UUID) (func(), error) {
	if h.lockErr != nil {
		return nil, h.lockErr
	}
	*h.log = append(*h.log, "lock")
	return func() { *h.log = append(*h.log, "unlock") }, nil
}

func (h *fakeHolders) MediaHeldBy(_ context.Context, kind string, ref uuid.UUID) (postgres.MediaHolding, error) {
	h.reads++
	if h.readErr != nil {
		return postgres.MediaHolding{}, h.readErr
	}
	return h.rows[claimKey{kind, ref}], nil
}

func (h *fakeHolders) set(kind string, ref uuid.UUID, live bool, ids ...uuid.UUID) {
	h.rows[claimKey{kind, ref}] = postgres.MediaHolding{Kind: kind, Ref: ref, MediaIDs: ids, Live: live}
}

func claimRig() (*Service, *fakeClaims, *fakeHolders) {
	claims := newFakeClaims()
	holders := newFakeHolders(claims.log)
	s := &Service{mediaHolders: holders}
	s.SetMediaClaims(claims)
	return s, claims, holders
}

func wants(ids ...uuid.UUID) func(postgres.MediaHolding) ([]uuid.UUID, error) {
	return func(postgres.MediaHolding) ([]uuid.UUID, error) { return ids, nil }
}

func sameIDs(t *testing.T, what string, got []uuid.UUID, want ...uuid.UUID) {
	t.Helper()
	sort.Slice(want, func(i, j int) bool { return want[i].String() < want[j].String() })
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("%s = %v, want %v", what, got, want)
	}
}

// ── the protocol ─────────────────────────────────────────────────────────

func TestHolderKindsMatchClaimKinds(t *testing.T) {
	if postgres.HolderPostCover != mediaclaims.KindPostCover ||
		postgres.HolderReelDraft != mediaclaims.KindReelDraft ||
		postgres.HolderEventCover != mediaclaims.KindEventCover ||
		postgres.HolderPostDraftCover != mediaclaims.KindPostDraftCover ||
		postgres.HolderVideoSeriesCover != mediaclaims.KindVideoSeriesCover ||
		postgres.HolderPlaylistCover != mediaclaims.KindPlaylistCover {
		t.Fatal("the store's holder kinds and the claim kinds media-service registers have drifted")
	}
	if mediaclaims.Claimant != "post-service" {
		t.Fatalf("claimant = %q; media-service registers post-service", mediaclaims.Claimant)
	}
}

// Claim, then write, under the row's lock: nothing names an unclaimed asset.
func TestHolderWriteClaimsBeforeTheWriteUnderTheLock(t *testing.T) {
	s, claims, _ := claimRig()
	ref, cover := uuid.New(), uuid.New()
	err := s.holderWrite(context.Background(), postgres.HolderPostCover, ref, wants(cover), func() error {
		*claims.log = append(*claims.log, "write")
		if got := claims.held(postgres.HolderPostCover, ref); len(got) != 1 {
			t.Fatalf("the write ran before its claim: %v", got)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := fmt.Sprint(*claims.log); got != "[lock claim write unlock]" {
		t.Fatalf("order = %s, want lock, claim, write, unlock", got)
	}
}

// Fail closed: no client, or media-service refusing, writes nothing.
func TestHolderWriteFailsClosed(t *testing.T) {
	ref, cover := uuid.New(), uuid.New()
	wrote := false
	write := func() error { wrote = true; return nil }

	noClient := &Service{mediaHolders: newFakeHolders(new([]string))}
	if err := noClient.holderWrite(context.Background(), postgres.HolderPostCover, ref, wants(cover), write); !errors.Is(err, ErrMediaClaimsUnavailable) {
		t.Fatalf("no client: err = %v, want ErrMediaClaimsUnavailable", err)
	}
	if wrote {
		t.Fatal("no client: the row was written with an unclaimed cover")
	}

	s, claims, _ := claimRig()
	claims.claimErr = fmt.Errorf("%w: status 503", mediaclaims.ErrUnavailable)
	if err := s.holderWrite(context.Background(), postgres.HolderPostCover, ref, wants(cover), write); !errors.Is(err, ErrMediaClaimsUnavailable) {
		t.Fatalf("media-service down: err = %v, want ErrMediaClaimsUnavailable", err)
	}
	if wrote {
		t.Fatal("media-service down: the row was written with an unclaimed cover")
	}
}

// An asset deleted between the authority check and the claim refuses the
// write as MEDIA_NOT_FOUND.
func TestHolderWriteRefusesMediaGoneAtClaim(t *testing.T) {
	s, claims, _ := claimRig()
	ref, cover := uuid.New(), uuid.New()
	claims.missing[cover] = true
	wrote := false
	err := s.holderWrite(context.Background(), postgres.HolderPostCover, ref, wants(cover), func() error { wrote = true; return nil })
	var gone *ClaimMediaGoneError
	if !errors.As(err, &gone) || !errors.Is(err, ErrMediaNotFound) {
		t.Fatalf("err = %v, want ClaimMediaGoneError unwrapping to ErrMediaNotFound", err)
	}
	if wrote {
		t.Fatal("the row was written naming a deleted asset")
	}
}

// A failed write releases what it claimed...
func TestHolderWriteCompensatesAFailedWrite(t *testing.T) {
	s, claims, _ := claimRig()
	ref, cover := uuid.New(), uuid.New()
	boom := errors.New("insert failed")
	if err := s.holderWrite(context.Background(), postgres.HolderPostCover, ref, wants(cover), func() error { return boom }); !errors.Is(err, boom) {
		t.Fatalf("err = %v, want the write's", err)
	}
	if got := claims.held(postgres.HolderPostCover, ref); len(got) != 0 {
		t.Fatalf("a failed write left its claim: %v", got)
	}
}

// ...except what the row holds NOW: a draft publish reuses the draft id as
// the post id, so a failed retry must not release the claim the first
// attempt's post relies on.
func TestHolderWriteCompensationKeepsWhatTheRowHoldsNow(t *testing.T) {
	s, claims, holders := claimRig()
	ref, cover := uuid.New(), uuid.New()
	// The read before the write sees no row (prev), the read after sees
	// the post a concurrent first attempt committed with the same cover.
	reads := 0
	next := func(postgres.MediaHolding) ([]uuid.UUID, error) { return []uuid.UUID{cover}, nil }
	write := func() error {
		reads = holders.reads
		holders.set(postgres.HolderPostCover, ref, true, cover)
		return errors.New("duplicate key value violates unique constraint \"posts_pkey\"")
	}
	_ = s.holderWrite(context.Background(), postgres.HolderPostCover, ref, next, write)
	if holders.reads <= reads {
		t.Fatal("compensation did not re-read the row")
	}
	sameIDs(t, "claims after the failed retry", claims.held(postgres.HolderPostCover, ref), cover)
}

// An unreadable row keeps the claim: unknown is not "nothing".
func TestHolderWriteCompensationKeepsTheClaimWhenTheRowIsUnreadable(t *testing.T) {
	s, claims, holders := claimRig()
	ref, cover := uuid.New(), uuid.New()
	_ = s.holderWrite(context.Background(), postgres.HolderPostCover, ref, wants(cover), func() error {
		holders.readErr = errors.New("db down")
		return errors.New("insert failed")
	})
	sameIDs(t, "claims", claims.held(postgres.HolderPostCover, ref), cover)
}

// A successful edit releases what the row stopped holding, never what it
// holds.
func TestHolderWriteReleasesOnlyWhatTheRowDropped(t *testing.T) {
	s, claims, holders := claimRig()
	ref, oldCover, newCover, source := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	holders.set(postgres.HolderReelDraft, ref, true, source, oldCover)
	_ = claims.Claim(context.Background(), postgres.HolderReelDraft, ref, []uuid.UUID{source, oldCover})

	if err := s.holderWrite(context.Background(), postgres.HolderReelDraft, ref, wants(source, newCover), func() error { return nil }); err != nil {
		t.Fatal(err)
	}
	sameIDs(t, "claims after the edit", claims.held(postgres.HolderReelDraft, ref), source, newCover)
}

// The set the row will hold is decided from the row read UNDER the lock.
func TestHolderWriteReadsTheRowUnderTheLock(t *testing.T) {
	s, claims, holders := claimRig()
	ref := uuid.New()
	holders.set(postgres.HolderPostCover, ref, true, uuid.New())
	var seenAt int
	_ = s.holderWrite(context.Background(), postgres.HolderPostCover, ref, func(postgres.MediaHolding) ([]uuid.UUID, error) {
		seenAt = len(*claims.log)
		return nil, nil
	}, func() error { return nil })
	if seenAt == 0 || (*claims.log)[0] != "lock" {
		t.Fatalf("the row was read before the lock: %v", *claims.log)
	}
}

func TestHolderWriteRefusesWhenTheLockIsUnavailable(t *testing.T) {
	s, _, holders := claimRig()
	holders.lockErr = errors.New("pool exhausted")
	wrote := false
	err := s.holderWrite(context.Background(), postgres.HolderPostCover, uuid.New(), wants(uuid.New()), func() error { wrote = true; return nil })
	if !errors.Is(err, ErrMediaClaimsUnavailable) || wrote {
		t.Fatalf("err = %v wrote = %v; an unlockable row must not be written", err, wrote)
	}
}

// ── settle: release a row that is gone or never shown again ───────────────

func TestSettleReleasesOnlyADeadRow(t *testing.T) {
	ctx := context.Background()
	s, claims, holders := claimRig()
	live, dead, gone, empty := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	asset := uuid.New()
	for _, ref := range []uuid.UUID{live, dead, gone, empty} {
		_ = claims.Claim(ctx, postgres.HolderReelDraft, ref, []uuid.UUID{asset})
	}
	holders.set(postgres.HolderReelDraft, live, true, asset)
	holders.set(postgres.HolderReelDraft, dead, false, asset) // deleted / published
	holders.set(postgres.HolderReelDraft, empty, true)        // live, holds nothing
	// gone: no row at all (purged, erased)

	for _, ref := range []uuid.UUID{live, dead, gone, empty} {
		if err := s.settleMediaHolder(ctx, postgres.HolderReelDraft, ref); err != nil {
			t.Fatal(err)
		}
	}
	sameIDs(t, "live draft's claims", claims.held(postgres.HolderReelDraft, live), asset)
	for name, ref := range map[string]uuid.UUID{"dead": dead, "gone": gone, "empty": empty} {
		if got := claims.held(postgres.HolderReelDraft, ref); len(got) != 0 {
			t.Fatalf("%s row kept its claims: %v", name, got)
		}
	}
}

func TestSettleReportsAReleaseThatDidNotHappen(t *testing.T) {
	s, claims, _ := claimRig()
	claims.releaseErr = errors.New("media-service down")
	if err := s.settleMediaHolder(context.Background(), postgres.HolderPostCover, uuid.New()); !errors.Is(err, ErrMediaClaimsUnavailable) {
		t.Fatalf("err = %v, want ErrMediaClaimsUnavailable", err)
	}
	noClient := &Service{mediaHolders: newFakeHolders(new([]string))}
	if err := noClient.settleMediaHolder(context.Background(), postgres.HolderPostCover, uuid.New()); err == nil {
		t.Fatal("with no client a due release must be reported, not assumed")
	}
}

func TestSettleKeepsTheClaimWhenTheRowIsUnreadable(t *testing.T) {
	ctx := context.Background()
	s, claims, holders := claimRig()
	ref, asset := uuid.New(), uuid.New()
	_ = claims.Claim(ctx, postgres.HolderPostCover, ref, []uuid.UUID{asset})
	holders.readErr = errors.New("db down")
	if err := s.settleMediaHolder(ctx, postgres.HolderPostCover, ref); err == nil {
		t.Fatal("an unreadable row settled as if it were gone")
	}
	sameIDs(t, "claims", claims.held(postgres.HolderPostCover, ref), asset)
}

// The purge's media drain releases the purged post's own cover claim before
// it asks media-service to delete, and defers when it cannot.
func TestDeleteMediaForPurgedPostReleasesTheCoverClaimFirst(t *testing.T) {
	s, claims, _ := claimRig()
	claims.releaseErr = errors.New("media-service down")
	err := s.DeleteMediaForPurgedPost(context.Background(), uuid.New(), uuid.New())
	if !errors.Is(err, ErrMediaClaimsUnavailable) {
		t.Fatalf("err = %v; a drain whose release failed must defer, not ask media-service to delete", err)
	}
	if got := fmt.Sprint(*claims.log); got != "[lock release unlock]" {
		t.Fatalf("calls = %s", got)
	}
}

// ── who may be claimed ───────────────────────────────────────────────────

func TestCheckOwnMedia(t *testing.T) {
	owner, id := uuid.New(), uuid.New()
	if err := checkOwnMedia(id, owner, postgres.MediaOwnership{}, false); !errors.Is(err, ErrMediaNotFound) {
		t.Fatalf("missing: %v", err)
	}
	if err := checkOwnMedia(id, owner, postgres.MediaOwnership{UploaderID: uuid.New()}, true); !errors.Is(err, ErrMediaNotOwned) {
		t.Fatalf("a stranger's asset: %v", err)
	}
	if err := checkOwnMedia(id, owner, postgres.MediaOwnership{UploaderID: owner, ProcessingStatus: "processing"}, true); err != nil {
		t.Fatalf("own asset, still processing: %v", err)
	}
}

type fakeOwnership map[uuid.UUID]postgres.MediaOwnership

func (f fakeOwnership) BatchGetMediaOwnership(_ context.Context, ids []uuid.UUID) (map[uuid.UUID]postgres.MediaOwnership, error) {
	out := map[uuid.UUID]postgres.MediaOwnership{}
	for _, id := range ids {
		if m, ok := f[id]; ok {
			out[id] = m
		}
	}
	return out, nil
}

func TestVerifyOwnMedia(t *testing.T) {
	ctx := context.Background()
	owner, mine, theirs := uuid.New(), uuid.New(), uuid.New()
	s := &Service{ownMedia: fakeOwnership{mine: {UploaderID: owner}, theirs: {UploaderID: uuid.New()}}}
	if err := s.verifyOwnMedia(ctx, owner, mine); err != nil {
		t.Fatalf("own: %v", err)
	}
	if err := s.verifyOwnMedia(ctx, owner, mine, theirs); !errors.Is(err, ErrMediaNotOwned) {
		t.Fatalf("one of them a stranger's: %v", err)
	}
	if err := s.verifyOwnMedia(ctx, owner, uuid.New()); !errors.Is(err, ErrMediaNotFound) {
		t.Fatalf("missing: %v", err)
	}
	if err := (&Service{}).verifyOwnMedia(ctx, owner, mine); err == nil {
		t.Fatal("no ownership store: must fail closed")
	}
	if err := (&Service{}).verifyOwnMedia(ctx, owner); err != nil {
		t.Fatalf("nothing to verify: %v", err)
	}
}

// An event cover that is not the creator's is never claimed, and never
// written.
func TestCreateEventRefusesAStrangersCoverBeforeClaiming(t *testing.T) {
	s, claims, _ := claimRig()
	creator, theirs := uuid.New(), uuid.New()
	s.ownMedia = fakeOwnership{theirs: {UploaderID: uuid.New()}}
	_, err := s.createEventClaimed(context.Background(), &postgres.Event{CreatorID: creator, CoverMediaID: &theirs})
	if !errors.Is(err, ErrMediaNotOwned) {
		t.Fatalf("err = %v, want ErrMediaNotOwned", err)
	}
	if len(*claims.log) != 0 {
		t.Fatalf("a stranger's asset reached media-service: %v", *claims.log)
	}
}

func TestCreateReelDraftRefusesAStrangersAssetBeforeClaiming(t *testing.T) {
	s, claims, _ := claimRig()
	author, theirs := uuid.New(), uuid.New()
	s.ownMedia = fakeOwnership{theirs: {UploaderID: uuid.New()}}
	err := s.createReelDraftClaimed(context.Background(), &postgres.ReelDraft{ID: uuid.New(), AuthorID: author, MediaID: &theirs})
	if !errors.Is(err, ErrMediaNotOwned) {
		t.Fatalf("err = %v, want ErrMediaNotOwned", err)
	}
	if len(*claims.log) != 0 {
		t.Fatalf("a stranger's asset reached media-service: %v", *claims.log)
	}
}

func TestUpdateReelDraftCoverRefusesAStrangersCoverBeforeClaiming(t *testing.T) {
	s, claims, _ := claimRig()
	author, theirs := uuid.New(), uuid.New()
	s.ownMedia = fakeOwnership{theirs: {UploaderID: uuid.New()}}
	wrote := false
	err := s.updateReelDraftCoverClaimed(context.Background(), uuid.New(), author, theirs, func() error { wrote = true; return nil })
	if !errors.Is(err, ErrMediaNotOwned) || wrote {
		t.Fatalf("err = %v wrote = %v, want ErrMediaNotOwned and no write", err, wrote)
	}
	if len(*claims.log) != 0 {
		t.Fatalf("a stranger's asset reached media-service: %v", *claims.log)
	}
}

// A dead draft (deleted, published) holds nothing: a cover set on it takes
// no claim.
func TestUpdateReelDraftCoverOnADeadDraftClaimsNothing(t *testing.T) {
	s, claims, holders := claimRig()
	author, draft, cover := uuid.New(), uuid.New(), uuid.New()
	s.ownMedia = fakeOwnership{cover: {UploaderID: author}}
	holders.set(postgres.HolderReelDraft, draft, false)
	if err := s.updateReelDraftCoverClaimed(context.Background(), draft, author, cover, func() error { return nil }); err != nil {
		t.Fatal(err)
	}
	if got := claims.held(postgres.HolderReelDraft, draft); len(got) != 0 {
		t.Fatalf("a dead draft was claimed for: %v", got)
	}
}

// A cover whose claim finds the asset gone is a refusal of the COVER.
func TestSetPostCoverClaimedRefusesAGoneCoverAsACoverError(t *testing.T) {
	s, claims, _ := claimRig()
	cover := uuid.New()
	claims.missing[cover] = true
	err := s.setPostCoverClaimed(context.Background(), uuid.New(), cover, func() error { return nil })
	var coverErr *CoverMediaError
	if !errors.As(err, &coverErr) || !errors.Is(err, ErrMediaNotFound) {
		t.Fatalf("err = %v, want a CoverMediaError wrapping ErrMediaNotFound", err)
	}
}

// ── mediaDiff ────────────────────────────────────────────────────────────

func TestMediaDiff(t *testing.T) {
	a, b, c := uuid.New(), uuid.New(), uuid.New()
	added, removed := mediaDiff([]uuid.UUID{a, b, b, uuid.Nil}, []uuid.UUID{b, c, c, uuid.Nil})
	if fmt.Sprint(added) != fmt.Sprint([]uuid.UUID{c}) || fmt.Sprint(removed) != fmt.Sprint([]uuid.UUID{a}) {
		t.Fatalf("added=%v removed=%v", added, removed)
	}
	if got := subtractIDs([]uuid.UUID{a, b, c}, []uuid.UUID{b}); fmt.Sprint(got) != fmt.Sprint([]uuid.UUID{a, c}) {
		t.Fatalf("subtract = %v", got)
	}
	if got := holdingOf(&a, nil, &a, &b); fmt.Sprint(got) != fmt.Sprint([]uuid.UUID{a, b}) {
		t.Fatalf("holdingOf = %v", got)
	}
}

// ── CreatePost's write ───────────────────────────────────────────────────

// The new post's cover is claimed under the post's own id before the row is
// written, and released when the write is refused (an idempotent replay
// writes nothing).
func TestWriteCreatedPostClaimsTheCoverUnderThePostID(t *testing.T) {
	s, claims, _ := claimRig()
	cover := uuid.New()
	p := &postgres.Post{ID: uuid.New(), CoverMediaID: &cover}
	var claimedAtWrite []uuid.UUID
	s.createPostWriteSeam = func(context.Context, *postgres.Post, string, interface{}, *postgres.CreateIdempotency) error {
		claimedAtWrite = claims.held(postgres.HolderPostCover, p.ID)
		return nil
	}
	if err := s.writeCreatedPostClaimed(context.Background(), p, "", nil, nil); err != nil {
		t.Fatal(err)
	}
	sameIDs(t, "claimed when the row was written", claimedAtWrite, cover)

	replayed := &postgres.Post{ID: uuid.New(), CoverMediaID: &cover}
	s.createPostWriteSeam = func(context.Context, *postgres.Post, string, interface{}, *postgres.CreateIdempotency) error {
		return postgres.ErrCreateKeyReplay{PostID: p.ID}
	}
	if err := s.writeCreatedPostClaimed(context.Background(), replayed, "", nil, nil); err == nil {
		t.Fatal("want the replay error back")
	}
	if got := claims.held(postgres.HolderPostCover, replayed.ID); len(got) != 0 {
		t.Fatalf("a replayed create kept a claim for a post that was never written: %v", got)
	}
}

func TestWriteCreatedPostWithoutACoverClaimsNothing(t *testing.T) {
	noClient := &Service{}
	wrote := false
	noClient.createPostWriteSeam = func(context.Context, *postgres.Post, string, interface{}, *postgres.CreateIdempotency) error {
		wrote = true
		return nil
	}
	if err := noClient.writeCreatedPostClaimed(context.Background(), &postgres.Post{ID: uuid.New()}, "", nil, nil); err != nil || !wrote {
		t.Fatalf("err=%v wrote=%v: a post with no cover needs no claim client", err, wrote)
	}
}
