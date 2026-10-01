//go:build integration

package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	sharedevents "github.com/atpost/shared/events"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Live recording import against a real schema (migration 025).
//
//	POSTGRES_DSN=postgres://…/media_rec_it_test go test -tags integration ./internal/store/postgres -run RecordingImport -v
//
// The database name must end in _test: this suite writes media rows.

const recImportKeyPrefix = "user/rec-it/"

func recImportPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	pool := subtitleEventsPool(t) // DSN, _test name guard, real bootstrap
	requireScratchDatabase(t, pool)
	t.Cleanup(func() {
		c := context.Background()
		_, _ = pool.Exec(c, `DELETE FROM media_event_outbox WHERE media_asset_id IN (SELECT id FROM media_assets WHERE import_source_ref LIKE 'rec-it-%')`)
		_, _ = pool.Exec(c, `DELETE FROM media_assets WHERE import_source_ref LIKE 'rec-it-%'`)
	})
	return pool
}

func recImportVideo(owner uuid.UUID, ref string) ImportedVideo {
	id := uuid.New()
	return ImportedVideo{
		ID:            id,
		OwnerID:       owner,
		MimeType:      "video/mp4",
		SizeBytes:     4096,
		StorageBucket: "media",
		StorageKey:    recImportKeyPrefix + owner.String() + "/" + id.String() + "/original",
		OriginalETag:  "etag-it",
		UploadPurpose: UploadPurposeLiveRecording,
		Source:        ImportSourceLiveRecording,
		SourceRef:     ref,
	}
}

func TestRecordingImportIT_RowOutboxAndIdempotency(t *testing.T) {
	pool := recImportPool(t)
	s := New(pool)
	ctx := context.Background()
	owner := uuid.New()
	ref := "rec-it-" + uuid.NewString()

	first := recImportVideo(owner, ref)
	got, created, err := s.CreateImportedVideo(ctx, first)
	if err != nil || !created || got.ID != first.ID {
		t.Fatalf("first import: %+v created=%v err=%v", got, created, err)
	}

	// The row: owner, video, processing, moderation pending, the purpose and
	// the upload-time identity.
	var (
		uploader                                       uuid.UUID
		fileType, status, moderation, purpose, source  string
		sourceRef, timeSource, etag, key, bucket, mime string
		confirmed                                      bool
		size                                           int64
	)
	if err := pool.QueryRow(ctx, `
		SELECT uploader_id, file_type, processing_status, moderation_status, upload_purpose,
		       import_source, import_source_ref, upload_time_source, original_etag,
		       storage_key, storage_bucket, mime_type, upload_confirmed_at IS NOT NULL, file_size_bytes
		  FROM media_assets WHERE id = $1`, first.ID).Scan(
		&uploader, &fileType, &status, &moderation, &purpose, &source, &sourceRef,
		&timeSource, &etag, &key, &bucket, &mime, &confirmed, &size); err != nil {
		t.Fatal(err)
	}
	if uploader != owner || fileType != "video" || status != "processing" || moderation != "pending" ||
		purpose != UploadPurposeLiveRecording || source != ImportSourceLiveRecording || sourceRef != ref ||
		timeSource != UploadTimeConfirmed || etag != "etag-it" || key != first.StorageKey || bucket != "media" ||
		mime != "video/mp4" || !confirmed || size != 4096 {
		t.Fatalf("row = %s %s %s %s %s %s %s %s %s %s %s %s %v %d", uploader, fileType, status, moderation, purpose,
			source, sourceRef, timeSource, etag, key, bucket, mime, confirmed, size)
	}

	// The transcode request, in the same commit, the payload the worker reads.
	var raw []byte
	var actor string
	if err := pool.QueryRow(ctx, `
		SELECT payload, actor_user_id::text FROM media_event_outbox
		 WHERE media_asset_id = $1 AND event_type = $2`, first.ID, sharedevents.MediaTranscodeRequested).Scan(&raw, &actor); err != nil {
		t.Fatalf("no transcode request: %v", err)
	}
	var p TranscodeRequestPayload
	if err := json.Unmarshal(raw, &p); err != nil {
		t.Fatal(err)
	}
	if p.MediaAssetID != first.ID.String() || p.UploaderID != owner.String() || p.StorageKey != first.StorageKey ||
		p.MimeType != "video/mp4" || p.MediaGeneration != 1 || actor != owner.String() {
		t.Fatalf("transcode request = %+v actor=%s", p, actor)
	}

	// A second import of the same pair writes nothing and names the first.
	second := recImportVideo(owner, ref)
	got, created, err = s.CreateImportedVideo(ctx, second)
	if err != nil || created || got.ID != first.ID {
		t.Fatalf("second import: %+v created=%v err=%v", got, created, err)
	}
	var n int
	if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM media_assets WHERE import_source = $1 AND import_source_ref = $2`,
		ImportSourceLiveRecording, ref).Scan(&n); err != nil || n != 1 {
		t.Fatalf("rows for the pair = %d (%v)", n, err)
	}
	if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM media_event_outbox WHERE media_asset_id = $1`, second.ID).Scan(&n); err != nil || n != 0 {
		t.Fatalf("the losing import queued %d events (%v)", n, err)
	}
	found, err := s.FindImportedMedia(ctx, ImportSourceLiveRecording, ref)
	if err != nil || found == nil || found.ID != first.ID || found.UploaderID != owner {
		t.Fatalf("find = %+v %v", found, err)
	}
	if none, err := s.FindImportedMedia(ctx, ImportSourceLiveRecording, "rec-it-absent"); err != nil || none != nil {
		t.Fatalf("absent find = %+v %v", none, err)
	}
}

func TestRecordingImportIT_ConcurrentImportsMakeOneAsset(t *testing.T) {
	pool := recImportPool(t)
	s := New(pool)
	ctx := context.Background()
	owner := uuid.New()
	ref := "rec-it-" + uuid.NewString()

	const n = 8
	var wg sync.WaitGroup
	ids := make([]uuid.UUID, n)
	createdCount := make([]bool, n)
	errs := make([]error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			got, created, err := s.CreateImportedVideo(ctx, recImportVideo(owner, ref))
			errs[i], createdCount[i] = err, created
			if got != nil {
				ids[i] = got.ID
			}
		}(i)
	}
	wg.Wait()
	winners := 0
	for i := 0; i < n; i++ {
		if errs[i] != nil {
			t.Fatalf("import %d: %v", i, errs[i])
		}
		if createdCount[i] {
			winners++
		}
		if ids[i] != ids[0] {
			t.Fatalf("imports disagree on the asset: %v", ids)
		}
	}
	if winners != 1 {
		t.Fatalf("%d imports created an asset, want exactly 1", winners)
	}
}

func TestRecordingImportIT_NeverReclaimed(t *testing.T) {
	pool := recImportPool(t)
	s := New(pool)
	ctx := context.Background()
	v := recImportVideo(uuid.New(), "rec-it-"+uuid.NewString())
	if _, _, err := s.CreateImportedVideo(ctx, v); err != nil {
		t.Fatal(err)
	}
	// Age it far past every sweep's threshold.
	if _, err := pool.Exec(ctx, `UPDATE media_assets SET created_at = NOW() - INTERVAL '400 days' WHERE id = $1`, v.ID); err != nil {
		t.Fatal(err)
	}
	cutoff := time.Now()

	orphans, err := s.ListOrphanedPendingUploads(ctx, cutoff, 100000)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range orphans {
		if id == v.ID {
			t.Fatal("an imported recording is an orphaned-pending-upload candidate")
		}
	}
	reclaimable, err := s.ListReclaimableMedia(ctx, cutoff, 100000)
	var unclassified ErrUnclassifiedMediaReference
	switch {
	case errors.As(err, &unclassified):
		// This scratch schema carries a column the policy does not classify;
		// the sweep refuses wholesale, which reclaims nothing either.
		t.Logf("reclaim sweep refused on this schema: %v", err)
	case err != nil:
		t.Fatal(err)
	}
	for _, id := range reclaimable {
		if id == v.ID {
			t.Fatal("an imported recording is a reclaim candidate")
		}
	}
	// And the one function that deletes refuses it: it was never pending.
	if _, err := s.DeleteOrphanMediaAtomic(ctx, v.ID, time.Hour); !errors.Is(err, ErrMediaConfirmed) {
		t.Fatalf("orphan delete of an imported recording: err = %v, want ErrMediaConfirmed", err)
	}
}
