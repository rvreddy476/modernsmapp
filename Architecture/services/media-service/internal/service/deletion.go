// deletion.go removes media: the owner delete (DeleteMedia), the internal
// orphan delete with its eligibility rules (DeleteOrphanMedia), and blob
// reclaim, where every object key is recorded before the rows go so the
// background worker (StartBlobReclaimWorker, which also expires resumable
// sessions) retries a failed removal instead of leaking the object.
// TestOwnerDeleteRoutesTheStoreAnswer reads DeleteMedia from this file.

package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/atpost/media-service/internal/store/postgres"
	"github.com/google/uuid"
)

// ─── Delete ────────────────────────────────────────────────────────

// DeleteMedia verifies ownership, removes blobs from storage, then deletes the DB record.
// ErrMediaNotOrphaned is returned when an internal orphan-delete request
// targets media that is still in use or too recent to reclaim.
var ErrMediaNotOrphaned = errors.New("media is not orphaned")

// orphanMinAge is the minimum age before an unreferenced asset may be
// reclaimed. Protects an upload that is mid-flight between /confirm and
// the post create that will attach it.
const orphanMinAge = 24 * time.Hour

// DeleteOrphanMedia deletes an asset on behalf of an internal caller
// (fixes-v2 / Codex P1-5), re-verifying eligibility here rather than
// trusting the caller:
//
//   - the asset must exist,
//   - it must be older than orphanMinAge,
//   - it must not be attached to any post.
//
// media-service does not own the posts table, so the post-attachment
// check is delegated to the shared `post_media` table via the same
// database. When that table is unreachable the delete is refused —
// fail-closed, because deleting live media is unrecoverable.
func (s *Service) DeleteOrphanMedia(ctx context.Context, mediaID uuid.UUID) error {
	// Module 1 fixes-v3 / LB-1: the whole eligibility decision (age,
	// published references, surviving-draft references) and the row
	// deletion now happen in ONE transaction that locks the asset row,
	// so a concurrent attach cannot slip between check and delete. See
	// DeleteOrphanMediaAtomic for the locking rationale.
	objectKeys, err := s.pgStore.DeleteOrphanMediaAtomic(ctx, mediaID, orphanMinAge)
	switch {
	case errors.Is(err, postgres.ErrMediaNotFound):
		// Already reclaimed by an earlier attempt. Idempotent success:
		// the caller's goal (asset gone) is satisfied.
		slog.Info("orphan delete: media already absent", "media_id", mediaID)
		return nil
	case errors.Is(err, postgres.ErrMediaTooYoung):
		return fmt.Errorf("%w: asset is younger than the reclaim window", ErrMediaNotOrphaned)
	case errors.Is(err, postgres.ErrMediaConfirmed):
		// Slice C / C-CLB-1. Confirmed assets are not reclaimable while
		// non-FK live references exist, so this is a policy refusal, not a
		// fault: the sweeper logs it and moves on, exactly as it does for an
		// asset that gained a reference mid-scan.
		return fmt.Errorf("%w: confirmed assets are not reclaimable", ErrMediaNotOrphaned)
	case errors.Is(err, postgres.ErrMediaStillReferenced):
		return fmt.Errorf("%w: still attached to a post or surviving draft", ErrMediaNotOrphaned)
	case err != nil:
		return err
	}

	// Rows are gone and the object keys are durably recorded in
	// media_blob_reclaim, so a failure below is retried by the sweeper
	// rather than leaking the object forever.
	s.reclaimBlobs(ctx, objectKeys)
	slog.Info("orphan media reclaimed", "media_id", mediaID, "objects", len(objectKeys))
	return nil
}

// reclaimBlobs deletes objects and clears their reclaim rows. Any failure
// leaves the row in place for the sweeper to retry.
func (s *Service) reclaimBlobs(ctx context.Context, objectKeys []string) {
	reclaimKeys(ctx, s.blobStore, s.pgStore, objectKeys)
}

// reclaimBlobStore / reclaimRecorder are what reclaimKeys needs.
type reclaimBlobStore interface {
	DeleteObject(ctx context.Context, objectKey string) error
	ListObjectKeys(ctx context.Context, prefix string) ([]string, error)
}

type reclaimRecorder interface {
	RecordBlobReclaimFailure(ctx context.Context, objectKey, reason string) error
	ClearBlobReclaim(ctx context.Context, objectKey string) error
}

// reclaimKeys deletes each object, or, for an asset prefix
// ("user/<uuid>/<uuid>/", postgres.IsAssetPrefix), everything under it:
// the HLS ladder, storyboard, audio tracks and anything else the asset
// produced that has no row (finding 8). A key ending in "/" that is not
// exactly one asset's prefix is never expanded: it is recorded as a
// failure and left for an operator. Any failure leaves the reclaim row for
// the sweeper to retry.
func reclaimKeys(ctx context.Context, blobs reclaimBlobStore, rec reclaimRecorder, objectKeys []string) {
	for _, key := range objectKeys {
		if key == "" {
			continue
		}
		var err error
		switch {
		case postgres.IsAssetPrefix(key):
			err = deletePrefix(ctx, blobs, key)
		case strings.HasSuffix(key, "/"):
			err = fmt.Errorf("refusing to reclaim %q: not a single asset's prefix", key)
		default:
			err = blobs.DeleteObject(ctx, key)
		}
		if err != nil {
			slog.Warn("blob reclaim: removal failed (will retry)", "key", key, "error", err)
			_ = rec.RecordBlobReclaimFailure(ctx, key, err.Error())
			continue
		}
		if err := rec.ClearBlobReclaim(ctx, key); err != nil {
			slog.Warn("blob reclaim: reclaim row cleanup failed", "key", key, "error", err)
		}
	}
}

// deletePrefix removes every object under one asset prefix.
func deletePrefix(ctx context.Context, blobs reclaimBlobStore, prefix string) error {
	keys, err := blobs.ListObjectKeys(ctx, prefix)
	if err != nil {
		return err
	}
	var failed int
	var last error
	for _, k := range keys {
		if !strings.HasPrefix(k, prefix) {
			continue
		}
		if err := blobs.DeleteObject(ctx, k); err != nil {
			failed++
			last = err
		}
	}
	if failed > 0 {
		return fmt.Errorf("%d of %d objects under %s not deleted: %w", failed, len(keys), prefix, last)
	}
	return nil
}

// StartBlobReclaimWorker drains media_blob_reclaim. Without it a transient
// S3/MinIO failure during orphan deletion would leak the object with no
// record left to retry from (LB-1 requirement 7).
func (s *Service) StartBlobReclaimWorker(ctx context.Context) {
	go func() {
		ticker := time.NewTicker(10 * time.Minute)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				s.expireResumableUploads(ctx)
				keys, err := s.pgStore.PendingBlobReclaims(ctx, 200)
				if err != nil {
					slog.Error("blob reclaim: list failed", "error", err)
					continue
				}
				if len(keys) == 0 {
					continue
				}
				s.reclaimBlobs(ctx, keys)
				slog.Info("blob reclaim sweep", "attempted", len(keys))
			}
		}
	}()
}

func (s *Service) DeleteMedia(ctx context.Context, mediaID uuid.UUID, userID uuid.UUID) error {
	// 1. Fetch and verify ownership
	media, err := s.pgStore.GetMedia(ctx, mediaID)
	if err != nil {
		return fmt.Errorf("media not found")
	}
	if media.UploaderID != userID {
		return fmt.Errorf("forbidden: you do not own this media")
	}

	// 2. Delete from DB (returns all object keys). Refused while another
	// service holds a claim on the asset (media_claims.go): the owner is told
	// where it is used instead of leaving that reference pointing at nothing.
	objectKeys, err := s.pgStore.DeleteMedia(ctx, mediaID)
	if err := ownerDeleteStoreError(err); err != nil {
		return err
	}

	// 3. Delete blobs from storage. Every key, and the asset's whole
	// prefix (the HLS ladder has no rows), was recorded in
	// media_blob_reclaim by the same transaction that removed the rows, so
	// a failure here is retried by the reclaim worker instead of leaking
	// (finding 8). The request does not wait on a retry.
	s.reclaimBlobs(ctx, objectKeys)

	return nil
}

// expireResumableUploads closes resumable sessions past their expiry and
// aborts their multipart uploads on the object store (finding 14:
// CleanupExpiredUploads had no caller). Best-effort: an abort that fails
// is logged; S3's lifecycle rule is the backstop in production.
func (s *Service) expireResumableUploads(ctx context.Context) {
	expired, err := s.pgStore.ExpireResumableUploads(ctx, 200)
	if err != nil {
		slog.Error("resumable expiry: sweep failed", "error", err)
		return
	}
	for _, e := range expired {
		if e.StorageUploadID == "" {
			continue
		}
		if err := s.blobStore.AbortMultipartUpload(ctx, e.ObjectKey, e.StorageUploadID); err != nil {
			slog.Warn("resumable expiry: abort failed", "upload_id", e.UploadID, "error", err)
		}
	}
	if len(expired) > 0 {
		slog.Info("resumable expiry sweep", "expired", len(expired))
	}
}
