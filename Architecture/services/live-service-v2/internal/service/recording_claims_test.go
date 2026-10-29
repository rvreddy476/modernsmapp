package service

import (
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/atpost/live-service-v2/internal/mediaclient"
	"github.com/atpost/live-service-v2/internal/store/postgres"
)

// importHeld reports whether the stream's recording import holds id.
func (f *fakeCoverMedia) importHeld(stream, id uuid.UUID) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.held[claimKindImport+"|"+stream.String()][id]
}

func (f *fakeCoverMedia) importHeldCount(stream uuid.UUID) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.held[claimKindImport+"|"+stream.String()])
}

// The id media-service answers is claimed before the job records it, held
// while the asset processes and after vod_ready, and released once the video
// post is found (post-service holds the asset then).
func TestRecordingImportClaimsTheAssetBeforeRecordingIt(t *testing.T) {
	r, st := importRig(t)
	posts := newFakePosts()
	r.svc.posts = posts
	mediaID := uuid.New()
	status := "processing"
	r.svc.media = &fakeImporter{fn: func() (ImportResult, error) { return ImportResult{MediaID: mediaID, ProcessingStatus: status}, nil }}
	r.media.onClaim = func(ref string) {
		if ref == st.ID.String() && r.store.Imports[st.ID].MediaID != nil {
			t.Error("the job recorded the media id before it was claimed")
		}
	}
	r.svc.RunImports(ctx)
	r.media.onClaim = nil
	if j := r.store.Imports[st.ID]; j.MediaID == nil || *j.MediaID != mediaID || !r.media.importHeld(st.ID, mediaID) {
		t.Fatalf("processing: job=%+v held=%v", j, r.media.held)
	}

	status = "ready"
	r.clock.Advance(importPollInterval + time.Second)
	r.svc.RunImports(ctx)
	if r.store.Imports[st.ID].State != postgres.ImportDone || len(r.store.Outbox) != 1 || !r.media.importHeld(st.ID, mediaID) {
		t.Fatalf("ready: job=%+v outbox=%v held=%v", r.store.Imports[st.ID], outboxTypes(r), r.media.held)
	}

	// No post yet: the row still relies on the asset.
	r.svc.ResolveRecordingPosts(ctx)
	if !r.media.importHeld(st.ID, mediaID) {
		t.Fatal("the claim was released before the video post exists")
	}
	posts.posts[st.ID] = RecordingPost{PostID: uuid.New()}
	r.clock.Advance(time.Hour)
	r.svc.ResolveRecordingPosts(ctx)
	if lookupState(t, r, st.ID) != postgres.PostLookupFound || r.media.importHeldCount(st.ID) != 0 {
		t.Fatalf("found: state=%s held=%v", lookupState(t, r, st.ID), r.media.held)
	}
}

// Fail closed: an id that cannot be claimed is not recorded and announces
// nothing; the next attempt claims it.
func TestRecordingImportWithoutAClaimRecordsNoMediaID(t *testing.T) {
	r, st := importRig(t)
	mediaID := uuid.New()
	r.svc.media = &fakeImporter{fn: func() (ImportResult, error) { return ImportResult{MediaID: mediaID, ProcessingStatus: "ready"}, nil }}
	r.media.claimErr = mediaclient.ErrUnavailable
	r.svc.RunImports(ctx)
	j := r.store.Imports[st.ID]
	if j.State != postgres.ImportPending || j.MediaID != nil || len(r.store.Outbox) != 0 {
		t.Fatalf("unclaimed: job=%+v outbox=%v", j, outboxTypes(r))
	}

	r.media.claimErr = nil
	r.clock.Advance(importBackoff(2) + time.Second)
	r.svc.RunImports(ctx)
	if r.store.Imports[st.ID].State != postgres.ImportDone || len(r.store.Outbox) != 1 || !r.media.importHeld(st.ID, mediaID) {
		t.Fatalf("after recovery: job=%+v outbox=%v held=%v", r.store.Imports[st.ID], outboxTypes(r), r.media.held)
	}

	// No claim client at all: the same refusal.
	r2, st2 := importRig(t)
	r2.svc.claims = nil
	r2.svc.media = &fakeImporter{fn: func() (ImportResult, error) { return ImportResult{MediaID: uuid.New(), ProcessingStatus: "ready"}, nil }}
	r2.svc.RunImports(ctx)
	if j := r2.store.Imports[st2.ID]; j.MediaID != nil || j.State != postgres.ImportPending || len(r2.store.Outbox) != 0 {
		t.Fatalf("no claim client: job=%+v outbox=%v", j, outboxTypes(r2))
	}
}

// A failed completion releases its claim; the retry that records the id
// claims it again, so the job names the id exactly when it is held.
func TestRecordingImportFailedCompletionKeepsClaimAndRowInStep(t *testing.T) {
	r, st := importRig(t)
	mediaID := uuid.New()
	r.svc.media = &fakeImporter{fn: func() (ImportResult, error) { return ImportResult{MediaID: mediaID, ProcessingStatus: "ready"}, nil }}
	r.store.FailOutbox = true
	r.svc.RunImports(ctx)
	j := r.store.Imports[st.ID]
	if j.State != postgres.ImportPending || j.MediaID == nil || *j.MediaID != mediaID || !r.media.importHeld(st.ID, mediaID) {
		t.Fatalf("failed completion: job=%+v held=%v", j, r.media.held)
	}
	if r.media.releases == 0 {
		t.Fatal("the failed write's claim was not compensated")
	}
}

// A job that ends without vod_ready holds nothing.
func TestRecordingImportTerminalReleases(t *testing.T) {
	r, st := importRig(t)
	mediaID := uuid.New()
	status := "processing"
	r.svc.media = &fakeImporter{fn: func() (ImportResult, error) { return ImportResult{MediaID: mediaID, ProcessingStatus: status}, nil }}
	r.svc.RunImports(ctx)
	if !r.media.importHeld(st.ID, mediaID) {
		t.Fatal("setup: not claimed while processing")
	}
	status = "rejected"
	r.clock.Advance(importPollInterval + time.Second)
	r.svc.RunImports(ctx)
	if r.store.Imports[st.ID].State != postgres.ImportFailed || r.media.importHeldCount(st.ID) != 0 {
		t.Fatalf("terminal: job=%+v held=%v", r.store.Imports[st.ID], r.media.held)
	}
}

// A lookup that stops without a post (deleted, gave up) releases the claim.
func TestRecordingImportLookupStopReleases(t *testing.T) {
	r, posts, host := postRig(t)
	deleted := recordedStream(t, r, host)
	mediaID := *r.store.Imports[deleted.ID].MediaID
	_ = r.media.Claim(ctx, claimKindImport, deleted.ID.String(), []uuid.UUID{mediaID})
	posts.posts[deleted.ID] = RecordingPost{PostID: uuid.New(), Deleted: true}
	r.svc.ResolveRecordingPosts(ctx)
	if lookupState(t, r, deleted.ID) != postgres.PostLookupDeleted || r.media.importHeldCount(deleted.ID) != 0 {
		t.Fatalf("deleted: state=%s held=%v", lookupState(t, r, deleted.ID), r.media.held)
	}

	never := recordedStream(t, r, host)
	neverID := *r.store.Imports[never.ID].MediaID
	_ = r.media.Claim(ctx, claimKindImport, never.ID.String(), []uuid.UUID{neverID})
	r.clock.Advance(postLookupGiveUp + time.Hour)
	r.svc.ResolveRecordingPosts(ctx)
	if lookupState(t, r, never.ID) != postgres.PostLookupGaveUp || r.media.importHeldCount(never.ID) != 0 {
		t.Fatalf("gave up: state=%s held=%v", lookupState(t, r, never.ID), r.media.held)
	}
}

func TestPurgeReleasesTheHostsRecordingImportClaims(t *testing.T) {
	r, posts, host := postRig(t)
	_ = posts
	other := uuid.New()
	mine := recordedStream(t, r, host)
	theirs := recordedStream(t, r, other)
	for _, st := range []*postgres.LiveStream{mine, theirs} {
		_ = r.media.Claim(ctx, claimKindImport, st.ID.String(), []uuid.UUID{*r.store.Imports[st.ID].MediaID})
	}
	next := &recEraser{}
	er := r.svc.ReleasingEraser(next)

	r.media.releaseErr = mediaclient.ErrUnavailable
	if err := er.PurgeUser(ctx, host); err == nil || next.calls != 0 {
		t.Fatalf("media-service down: err=%v erases=%d; want an error and no erase", err, next.calls)
	}
	r.media.releaseErr = nil
	if err := er.PurgeUser(ctx, host); err != nil || next.calls != 1 {
		t.Fatalf("purge: err=%v erases=%d", err, next.calls)
	}
	if r.media.importHeldCount(mine.ID) != 0 {
		t.Fatal("a purged host's recording import still holds its asset")
	}
	if r.media.importHeldCount(theirs.ID) != 1 {
		t.Fatal("the purge released another host's recording import")
	}
}
