//go:build integration

package postgres

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Reels audit 2026-10-30 against live PostgreSQL (CI only, on _test
// databases):
//
//	MEDIA_DURABILITY_POSTGRES_DSN=postgres://…/media_it_test POSTGRES_DSN=… \
//	  go test -tags integration ./internal/store/postgres -run Reels -v

func seedReelAsset(t *testing.T, store *MediaAssetStore, status string) *MediaAsset {
	t.Helper()
	uploader, id := uuid.New(), uuid.New()
	m := &MediaAsset{
		ID: id, UploaderID: uploader, FileType: "video", MediaSubtype: "general",
		MimeType: "video/mp4", FileSizeBytes: 1024, StorageBucket: "test",
		StorageKey:       fmt.Sprintf("user/%s/%s/original", uploader, id),
		ProcessingStatus: status, CreatedAt: time.Now(),
	}
	if err := store.CreateMedia(context.Background(), m); err != nil {
		t.Fatalf("seed media: %v", err)
	}
	return m
}

func inboxOutcome(t *testing.T, store *MediaAssetStore, eventID string) string {
	t.Helper()
	var outcome string
	err := store.db.QueryRow(context.Background(),
		`SELECT outcome FROM media_transcode_inbox WHERE event_id = $1`, eventID).Scan(&outcome)
	if errors.Is(err, pgx.ErrNoRows) {
		return ""
	}
	if err != nil {
		t.Fatalf("read inbox: %v", err)
	}
	return outcome
}

// Finding 1: a completion for a deleted asset is a recorded skip, not an
// error the worker retries forever.
func TestLiveReelsCompletionForDeletedAssetIsRecordedSkip(t *testing.T) {
	store := New(durabilityPool(t))
	ctx := context.Background()
	m := seedReelAsset(t, store, "processing")
	if _, err := store.DeleteMedia(ctx, m.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	for _, outcome := range []string{"ready", "failed"} {
		ev := "reels-gone-" + outcome + "-" + uuid.NewString()
		err := store.CompleteTranscode(ctx, ev, m.ID, outcome, "", "manual_review",
			TranscodeCompletion{ProcessingStatus: outcome, ModerationStatus: "manual_review"})
		if !errors.Is(err, ErrTranscodeAssetGone) {
			t.Fatalf("%s: err = %v, want ErrTranscodeAssetGone", outcome, err)
		}
		if got := inboxOutcome(t, store, ev); got != TranscodeOutcomeSkippedGone {
			t.Fatalf("%s: inbox outcome %q, want %q (committed, so the delivery is applied)", outcome, got, TranscodeOutcomeSkippedGone)
		}
		if done, err := store.AlreadyApplied(ctx, ev); err != nil || !done {
			t.Fatalf("%s: AlreadyApplied = %v, %v", outcome, done, err)
		}
	}
}

func TestLiveReelsRecordTranscodeSkippedIsIdempotent(t *testing.T) {
	store := New(durabilityPool(t))
	ctx := context.Background()
	ev, id := "reels-skip-"+uuid.NewString(), uuid.New()
	for i := 0; i < 2; i++ {
		if err := store.RecordTranscodeSkipped(ctx, ev, id); err != nil {
			t.Fatalf("record %d: %v", i, err)
		}
	}
	if got := inboxOutcome(t, store, ev); got != TranscodeOutcomeSkippedGone {
		t.Fatalf("outcome %q", got)
	}
}

// Finding 12: a late confirm cannot move a finished asset back.
func TestLiveReelsLateConfirmDoesNotResetAFinishedAsset(t *testing.T) {
	store := New(durabilityPool(t))
	ctx := context.Background()
	for _, status := range []string{"ready", "processing", "failed", "rejected"} {
		m := seedReelAsset(t, store, status)
		if err := store.MarkUploadConfirmed(ctx, m.ID, "etag"); !errors.Is(err, ErrUploadAlreadyConfirmed) {
			t.Fatalf("%s: err = %v, want ErrUploadAlreadyConfirmed", status, err)
		}
		got, err := store.GetMedia(ctx, m.ID)
		if err != nil || got.ProcessingStatus != status {
			t.Fatalf("%s: status after a late confirm = %v (%v)", status, got.ProcessingStatus, err)
		}
	}
	for _, status := range []string{"pending_upload", "uploaded"} {
		m := seedReelAsset(t, store, status)
		if err := store.MarkUploadConfirmed(ctx, m.ID, "etag-1"); err != nil {
			t.Fatalf("%s: %v", status, err)
		}
		got, _ := store.GetMedia(ctx, m.ID)
		if got.ProcessingStatus != "uploaded" || got.OriginalETag != "etag-1" {
			t.Fatalf("%s: status %q etag %q", status, got.ProcessingStatus, got.OriginalETag)
		}
	}
	if err := store.MarkUploadConfirmed(ctx, uuid.New(), "x"); !errors.Is(err, ErrUploadAlreadyConfirmed) {
		t.Fatalf("no row: %v", err)
	}
}

// Finding 8: the owner delete records every key AND the asset prefix for
// the reclaim worker in the delete's own transaction.
func TestLiveReelsOwnerDeleteRecordsThePrefixForReclaim(t *testing.T) {
	store := New(durabilityPool(t))
	ctx := context.Background()
	m := seedReelAsset(t, store, "ready")
	prefix := m.StorageKey[:len(m.StorageKey)-len("original")]
	w, h, size := 720, 1280, int64(10)
	if err := store.InsertVariants(ctx, []MediaVariant{{MediaAssetID: m.ID, Name: "720p", Width: &w, Height: &h,
		SizeBytes: &size, Mime: "video/mp4", ObjectKey: prefix + "720p"}}); err != nil {
		t.Fatalf("variants: %v", err)
	}
	keys, err := store.DeleteMedia(ctx, m.ID)
	if err != nil {
		t.Fatalf("delete: %v", err)
	}
	if keys[len(keys)-1] != prefix {
		t.Fatalf("returned keys %v do not end with the prefix %s", keys, prefix)
	}
	rows, err := store.db.Query(ctx, `SELECT object_key FROM media_blob_reclaim WHERE media_id = $1`, m.ID)
	if err != nil {
		t.Fatal(err)
	}
	var recorded []string
	for rows.Next() {
		var k string
		_ = rows.Scan(&k)
		recorded = append(recorded, k)
	}
	rows.Close()
	sort.Strings(recorded)
	want := []string{m.StorageKey, prefix, prefix + "720p"}
	sort.Strings(want)
	if fmt.Sprint(recorded) != fmt.Sprint(want) {
		t.Fatalf("reclaim rows %v, want %v", recorded, want)
	}
}

// A refused delete records nothing: the reclaim rows commit with the rows.
func TestLiveReelsRefusedDeleteRecordsNoReclaim(t *testing.T) {
	store := New(durabilityPool(t))
	ctx := context.Background()
	m := seedReelAsset(t, store, "ready")
	if _, err := store.db.Exec(ctx, `
		INSERT INTO media_claims (media_id, claimant, claim_kind, claim_ref) VALUES ($1, 'post-service', 'post', 'p1')`, m.ID); err != nil {
		t.Fatalf("seed claim: %v", err)
	}
	if _, err := store.DeleteMedia(ctx, m.ID); err == nil {
		t.Fatal("a claimed asset was deleted")
	}
	var n int
	_ = store.db.QueryRow(ctx, `SELECT count(*) FROM media_blob_reclaim WHERE media_id = $1`, m.ID).Scan(&n)
	if n != 0 {
		t.Fatalf("%d reclaim rows for an asset that was not deleted", n)
	}
}

// Finding 14: expired resumable sessions are closed once and returned for
// the multipart abort.
func TestLiveReelsExpiredResumableUploadsAreClosedOnce(t *testing.T) {
	store := New(durabilityPool(t))
	ctx := context.Background()
	m := seedReelAsset(t, store, "pending_upload")
	expired, live := uuid.New(), uuid.New()
	token := func() *string { s := uuid.NewString(); return &s }
	for _, u := range []struct {
		id  uuid.UUID
		exp time.Time
	}{{expired, time.Now().Add(-time.Hour)}, {live, time.Now().Add(time.Hour)}} {
		if err := store.CreateResumableUpload(ctx, &ResumableUpload{
			UploadID: u.id, MediaID: m.ID, UploaderID: m.UploaderID, TotalBytes: 10, ChunkSize: 5, TotalParts: 2,
			Status: "initiated", MimeType: "video/mp4", ObjectKey: m.StorageKey, StorageUploadID: "mp-" + u.id.String(),
			UploadToken: token(), ExpiresAt: u.exp,
		}); err != nil {
			t.Fatalf("seed upload: %v", err)
		}
	}
	first, err := store.ExpireResumableUploads(ctx, 1000)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, e := range first {
		if e.UploadID == live {
			t.Fatal("a live session was expired")
		}
		if e.UploadID == expired {
			found = e.StorageUploadID == "mp-"+expired.String() && e.ObjectKey == m.StorageKey
		}
	}
	if !found {
		t.Fatalf("expired session not returned with its abort coordinates: %+v", first)
	}
	again, err := store.ExpireResumableUploads(ctx, 1000)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range again {
		if e.UploadID == expired {
			t.Fatal("an expired session was returned twice")
		}
	}
}

// Finding 13 (sounds): a library sound that is withdrawn, switched off or
// past its licence reads as unavailable for new posts while its status
// stays 'ready' (existing posts keep playing it), and an expired licence is
// never materialised.
func TestLiveReelsLibrarySoundReuseBlock(t *testing.T) {
	for name, shape := range soundShapes() {
		t.Run(name, func(t *testing.T) {
			store, exec := librarySchema(t, shape)
			ctx := context.Background()
			active, withdrawn, inactive, expired, future := uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New()
			for _, id := range []uuid.UUID{active, withdrawn, inactive, expired, future} {
				exec(`INSERT INTO audio_library (id, title, duration_ms, audio_url, source) VALUES ($1, 'S', 1000, 'library/s.m4a', 'licensed')`, id)
			}
			exec(`UPDATE audio_library SET license_expires_at = NOW() + INTERVAL '1 day' WHERE id = $1`, future)
			// Materialise every row while it is usable, then change the
			// library rows: the sounds stay, as they do in production.
			tracks := map[uuid.UUID]uuid.UUID{}
			for _, id := range []uuid.UUID{active, withdrawn, inactive, expired, future} {
				tr, err := store.EnsureLibrarySound(ctx, id, "library/s.m4a")
				if err != nil {
					t.Fatalf("materialise: %v", err)
				}
				tracks[id] = tr.ID
			}
			exec(`UPDATE audio_library SET is_active = FALSE, withdrawn_at = NOW(), withdraw_scope = 'new_uses' WHERE id = $1`, withdrawn)
			exec(`UPDATE audio_library SET is_active = FALSE WHERE id = $1`, inactive)
			exec(`UPDATE audio_library SET license_expires_at = NOW() - INTERVAL '1 minute' WHERE id = $1`, expired)

			want := map[uuid.UUID]string{active: "", future: "", withdrawn: SoundReuseWithdrawn,
				inactive: SoundReuseInactive, expired: SoundReuseLicenseExpired}
			for lib, block := range want {
				tr, err := store.GetAudioTrack(ctx, tracks[lib])
				if err != nil {
					t.Fatal(err)
				}
				if tr.ReuseBlock != block || tr.Status != "ready" || tr.AvailableForNewPosts() != (block == "") {
					t.Fatalf("library %s: block %q status %q available %v, want block %q, status ready",
						lib, tr.ReuseBlock, tr.Status, tr.AvailableForNewPosts(), block)
				}
			}

			fresh := uuid.New()
			exec(`INSERT INTO audio_library (id, title, duration_ms, audio_url, source, license_expires_at)
				VALUES ($1, 'Lapsed', 1000, 'library/l.m4a', 'licensed', NOW() - INTERVAL '1 day')`, fresh)
			if _, err := store.EnsureLibrarySound(ctx, fresh, "library/l.m4a"); !errors.Is(err, pgx.ErrNoRows) {
				t.Fatalf("an expired licence was materialised: %v", err)
			}
			if lib, err := store.GetAudioLibraryTrack(ctx, fresh); err != nil || lib != nil {
				t.Fatalf("an expired licence is offered: %v %v", lib, err)
			}
		})
	}
}
