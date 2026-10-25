package service

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/atpost/live-service-v2/internal/mediaclient"
	"github.com/atpost/live-service-v2/internal/store/postgres"
	"github.com/atpost/live-service-v2/internal/storetest"
)

// fakeCoverMedia is media-service for covers: the asset records (Get) and
// the claims it keeps (Claim / Release), with failures to inject.
type fakeCoverMedia struct {
	mu     sync.Mutex
	owner  uuid.UUID // an unknown id is this user's finished upload
	assets map[uuid.UUID]*mediaclient.Asset
	gone   map[uuid.UUID]bool            // Get and Claim answer not-found
	held   map[string]map[uuid.UUID]bool // kind|ref -> ids
	// onClaim runs inside Claim (to look at the world at claim time).
	onClaim                      func(ref string)
	getErr, claimErr, releaseErr error
	claims, releases             int
}

func newFakeCoverMedia() *fakeCoverMedia {
	return &fakeCoverMedia{assets: map[uuid.UUID]*mediaclient.Asset{}, gone: map[uuid.UUID]bool{},
		held: map[string]map[uuid.UUID]bool{}}
}

func (f *fakeCoverMedia) Get(_ context.Context, id uuid.UUID) (*mediaclient.Asset, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.getErr != nil {
		return nil, f.getErr
	}
	if f.gone[id] {
		return nil, mediaclient.ErrNotFound
	}
	if a, ok := f.assets[id]; ok {
		cp := *a
		return &cp, nil
	}
	return &mediaclient.Asset{ID: id, UploaderID: f.owner, FileType: "image", ProcessingStatus: "ready", ModerationStatus: "passed"}, nil
}

func (f *fakeCoverMedia) Claim(_ context.Context, kind, ref string, ids []uuid.UUID) error {
	if f.onClaim != nil {
		f.onClaim(ref)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.claims++
	if f.claimErr != nil {
		return f.claimErr
	}
	for _, id := range ids {
		if f.gone[id] {
			return mediaclient.ErrNotFound // all or nothing
		}
	}
	k := kind + "|" + ref
	if f.held[k] == nil {
		f.held[k] = map[uuid.UUID]bool{}
	}
	for _, id := range ids {
		f.held[k][id] = true
	}
	return nil
}

func (f *fakeCoverMedia) Release(_ context.Context, kind, ref string, ids []uuid.UUID) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.releases++
	if f.releaseErr != nil {
		return f.releaseErr
	}
	k := kind + "|" + ref
	if len(ids) == 0 {
		delete(f.held, k)
		return nil
	}
	for _, id := range ids {
		delete(f.held[k], id)
	}
	return nil
}

// holds reports whether the stream holds a claim on id.
func (f *fakeCoverMedia) holds(stream uuid.UUID, id uuid.UUID) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.held[claimKindCover+"|"+stream.String()][id]
}

func (f *fakeCoverMedia) heldBy(stream uuid.UUID) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.held[claimKindCover+"|"+stream.String()])
}

func (f *fakeCoverMedia) totalHeld() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, ids := range f.held {
		n += len(ids)
	}
	return n
}

func (f *fakeCoverMedia) asset(id, owner uuid.UUID, processing, moderation string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.assets[id] = &mediaclient.Asset{ID: id, UploaderID: owner, FileType: "image", ProcessingStatus: processing, ModerationStatus: moderation}
}

func coverParams(cover *uuid.UUID) CreateStreamParams {
	return CreateStreamParams{Title: "Show", CoverMediaID: cover}
}

func TestCreateStreamClaimsTheCoverForTheRowBeforeWritingIt(t *testing.T) {
	host := uuid.New()
	r := newRig(host)
	cover := uuid.New()
	rowsAtClaim := -1
	r.media.onClaim = func(string) { rowsAtClaim = len(r.store.Streams) }

	st, err := r.svc.CreateStream(ctx, host, coverParams(&cover))
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if !r.media.holds(st.ID, cover) {
		t.Fatal("a created stream does not hold a claim on its cover (ref must be the row id)")
	}
	if rowsAtClaim != 0 {
		t.Fatalf("the claim was made with %d rows already written; it must come before the write", rowsAtClaim)
	}
}

func TestCreateStreamWithoutCoverNeedsNoMediaService(t *testing.T) {
	host := uuid.New()
	r := newRig(host)
	r.svc.coverMedia, r.svc.claims = nil, nil
	if _, err := r.svc.CreateStream(ctx, host, coverParams(nil)); err != nil {
		t.Fatalf("a stream without a cover was refused: %v", err)
	}
}

func TestCreateStreamRefusesACoverThatIsNotTheHostsFinishedUpload(t *testing.T) {
	host := uuid.New()
	cases := []struct {
		name string
		set  func(r *rig, id uuid.UUID)
		want error
	}{
		{"someone else's", func(r *rig, id uuid.UUID) { r.media.asset(id, uuid.New(), "ready", "passed") }, ErrCoverNotOwned},
		{"still uploading", func(r *rig, id uuid.UUID) { r.media.asset(id, host, "pending_upload", "pending") }, ErrCoverNotReady},
		{"failed processing", func(r *rig, id uuid.UUID) { r.media.asset(id, host, "failed", "passed") }, ErrCoverNotReady},
		{"rejected", func(r *rig, id uuid.UUID) { r.media.asset(id, host, "ready", "rejected") }, ErrCoverNotReady},
		{"not moderated", func(r *rig, id uuid.UUID) { r.media.asset(id, host, "ready", "") }, ErrCoverNotReady},
		{"missing", func(r *rig, id uuid.UUID) { r.media.gone[id] = true }, ErrCoverNotFound},
		{"lookup down", func(r *rig, id uuid.UUID) { r.media.getErr = mediaclient.ErrUnavailable }, ErrMediaUnavailable},
		{"no client", func(r *rig, id uuid.UUID) { r.svc.coverMedia = nil }, ErrMediaUnavailable},
		{"no claim client", func(r *rig, id uuid.UUID) { r.svc.claims = nil }, ErrMediaUnavailable},
		{"claim down", func(r *rig, id uuid.UUID) { r.media.claimErr = mediaclient.ErrUnavailable }, ErrMediaUnavailable},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := newRig(host)
			id := uuid.New()
			tc.set(r, id)
			if _, err := r.svc.CreateStream(ctx, host, coverParams(&id)); !errors.Is(err, tc.want) {
				t.Fatalf("got %v, want %v", err, tc.want)
			}
			if len(r.store.Streams) != 0 {
				t.Fatal("a refused cover still wrote the stream")
			}
		})
	}
	nilID := uuid.Nil
	if _, err := newRig(host).svc.CreateStream(ctx, host, coverParams(&nilID)); !errors.Is(err, ErrCoverNotFound) {
		t.Fatalf("nil cover id = %v", err)
	}
}

// The owner deleted the asset between the check and the claim: the claim is
// refused and the stream is never written, so nothing can dangle.
func TestCreateStreamWritesNothingWhenTheClaimFindsNoAsset(t *testing.T) {
	host := uuid.New()
	r := newRig(host)
	cover := uuid.New()
	r.media.onClaim = func(string) { r.media.gone[cover] = true }
	if _, err := r.svc.CreateStream(ctx, host, coverParams(&cover)); !errors.Is(err, ErrCoverNotFound) {
		t.Fatalf("got %v, want ErrCoverNotFound", err)
	}
	if len(r.store.Streams) != 0 {
		t.Fatal("the stream was written although its cover could not be claimed")
	}
}

// failingStore fails the writes the claim protocol wraps.
type failingStore struct {
	*storetest.MemStore
	err error
}

func (f failingStore) CreateStream(context.Context, postgres.CreateStreamParams) (*postgres.LiveStream, error) {
	return nil, f.err
}

func (f failingStore) UpdateScheduled(context.Context, uuid.UUID, postgres.StreamPatch) (*postgres.LiveStream, error) {
	return nil, f.err
}

func TestAFailedWriteReleasesWhatItClaimed(t *testing.T) {
	host := uuid.New()
	r := newRig(host)
	boom := errors.New("database is down")
	r.svc.store = failingStore{MemStore: r.store, err: boom}
	cover := uuid.New()
	if _, err := r.svc.CreateStream(ctx, host, coverParams(&cover)); !errors.Is(err, boom) {
		t.Fatalf("create = %v", err)
	}
	if r.media.claims != 1 || r.media.totalHeld() != 0 {
		t.Fatalf("claims=%d held=%v: the failed create's claim was not released", r.media.claims, r.media.held)
	}

	// The same for an edit, and the old cover stays held.
	r.svc.store = r.store
	old := uuid.New()
	st, err := r.svc.CreateStream(ctx, host, coverParams(&old))
	if err != nil {
		t.Fatal(err)
	}
	r.svc.store = failingStore{MemStore: r.store, err: boom}
	next := uuid.New()
	if _, err := r.svc.UpdateStream(ctx, st.ID, host, UpdateStreamParams{SetCoverMediaID: true, CoverMediaID: &next}); !errors.Is(err, boom) {
		t.Fatalf("edit = %v", err)
	}
	if r.media.holds(st.ID, next) || !r.media.holds(st.ID, old) {
		t.Fatalf("after a failed edit: holds next=%v old=%v; want only the old cover", r.media.holds(st.ID, next), r.media.holds(st.ID, old))
	}
}

func TestUpdateStreamReplacesAndClearsTheCoverClaim(t *testing.T) {
	host := uuid.New()
	r := newRig(host)
	first, second := uuid.New(), uuid.New()
	st, err := r.svc.CreateStream(ctx, host, coverParams(&first))
	if err != nil {
		t.Fatal(err)
	}

	releasedBeforeWrite := false
	r.media.onClaim = func(string) { releasedBeforeWrite = !r.media.holds(st.ID, first) }
	if _, err := r.svc.UpdateStream(ctx, st.ID, host, UpdateStreamParams{SetCoverMediaID: true, CoverMediaID: &second}); err != nil {
		t.Fatalf("replace: %v", err)
	}
	r.media.onClaim = nil
	if releasedBeforeWrite {
		t.Fatal("the old cover was released before the new one was claimed")
	}
	if !r.media.holds(st.ID, second) || r.media.holds(st.ID, first) {
		t.Fatalf("after replacing: second=%v first=%v", r.media.holds(st.ID, second), r.media.holds(st.ID, first))
	}

	// Saving the same cover again claims and releases nothing.
	claims, releases := r.media.claims, r.media.releases
	if _, err := r.svc.UpdateStream(ctx, st.ID, host, UpdateStreamParams{SetCoverMediaID: true, CoverMediaID: &second}); err != nil {
		t.Fatal(err)
	}
	if r.media.claims != claims || r.media.releases != releases || !r.media.holds(st.ID, second) {
		t.Fatal("re-saving the same cover changed its claim")
	}

	// Clearing is never refused for media-service's sake, and releases.
	r.media.claimErr, r.media.getErr = mediaclient.ErrUnavailable, mediaclient.ErrUnavailable
	got, err := r.svc.UpdateStream(ctx, st.ID, host, UpdateStreamParams{SetCoverMediaID: true})
	if err != nil || got.CoverMediaID != nil {
		t.Fatalf("clear = %+v, %v", got, err)
	}
	if r.media.heldBy(st.ID) != 0 {
		t.Fatal("a cleared cover is still claimed")
	}
}

func TestUpdateStreamChecksANewCover(t *testing.T) {
	host := uuid.New()
	r := newRig(host)
	st, err := r.svc.CreateStream(ctx, host, coverParams(nil))
	if err != nil {
		t.Fatal(err)
	}
	theirs := uuid.New()
	r.media.asset(theirs, uuid.New(), "ready", "passed")
	if _, err := r.svc.UpdateStream(ctx, st.ID, host, UpdateStreamParams{SetCoverMediaID: true, CoverMediaID: &theirs}); !errors.Is(err, ErrCoverNotOwned) {
		t.Fatalf("someone else's cover = %v, want ErrCoverNotOwned", err)
	}
	if r.store.Stream(st.ID).CoverMediaID != nil || r.media.claims != 0 {
		t.Fatal("a refused cover was written or claimed")
	}
}

// A cover edit holds the stream's lock from before its read until after its
// release, so two edits cannot interleave.
func TestUpdateStreamCoverWaitsForTheStreamLock(t *testing.T) {
	host := uuid.New()
	r := newRig(host)
	st, err := r.svc.CreateStream(ctx, host, coverParams(nil))
	if err != nil {
		t.Fatal(err)
	}
	unlock, _ := r.store.LockMediaHolder(ctx, st.ID)
	done := make(chan error, 1)
	cover := uuid.New()
	go func() {
		_, err := r.svc.UpdateStream(ctx, st.ID, host, UpdateStreamParams{SetCoverMediaID: true, CoverMediaID: &cover})
		done <- err
	}()
	select {
	case err := <-done:
		unlock()
		t.Fatalf("the cover edit did not wait for the stream lock (err %v)", err)
	case <-time.After(50 * time.Millisecond):
	}
	unlock()
	if err := <-done; err != nil {
		t.Fatalf("edit after unlock: %v", err)
	}
}

type recEraser struct{ calls int }

func (e *recEraser) PurgeUser(context.Context, uuid.UUID) error { e.calls++; return nil }

func TestPurgeReleasesTheHostsCoverClaimsBeforeErasing(t *testing.T) {
	host, other := uuid.New(), uuid.New()
	r := newRig(host, other)
	a, b := uuid.New(), uuid.New()
	s1, _ := r.svc.CreateStream(ctx, host, coverParams(&a))
	s2, _ := r.svc.CreateStream(ctx, host, coverParams(&b))
	r.media.asset(b, other, "ready", "passed") // irrelevant after the claim
	theirCover := uuid.New()
	r.media.asset(theirCover, other, "ready", "passed")
	s3, err := r.svc.CreateStream(ctx, other, coverParams(&theirCover))
	if err != nil {
		t.Fatal(err)
	}

	next := &recEraser{}
	er := r.svc.ReleasingEraser(next)

	// media-service down: nothing is erased, and the error asks for a retry.
	r.media.releaseErr = mediaclient.ErrUnavailable
	if err := er.PurgeUser(ctx, host); err == nil || next.calls != 0 {
		t.Fatalf("purge with media-service down: err=%v erases=%d; want an error and no erase", err, next.calls)
	}
	r.media.releaseErr = nil

	if err := er.PurgeUser(ctx, host); err != nil || next.calls != 1 {
		t.Fatalf("purge: err=%v erases=%d", err, next.calls)
	}
	if r.media.heldBy(s1.ID) != 0 || r.media.heldBy(s2.ID) != 0 {
		t.Fatal("a purged host's streams still hold their covers")
	}
	if !r.media.holds(s3.ID, theirCover) {
		t.Fatal("the purge released another host's cover")
	}
}
