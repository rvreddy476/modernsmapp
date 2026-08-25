package service

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/google/uuid"
)

// OrphanGCWorker is a background sweeper that reclaims media rows
// abandoned by the 3-step upload flow:
//
//	POST /v1/media/init       → row inserted, status=pending_upload
//	PUT  <presigned URL>      → bytes land in MinIO
//	POST /v1/media/confirm    → status flips to processing/ready
//
// If the client crashes (or the user drops mid-upload), the row
// stays at `pending_upload` forever. Per audit H9, that was the
// unbounded-storage-growth gap — no GC pass deleted the row OR
// the half-written blob.
//
// Policy:
//   - Sweep on a 15-minute cadence (configurable).
//   - Pick rows older than 24 h still at status=pending_upload.
//   - For each: call DeleteMedia (clears DB rows + transcoding jobs)
//     then RemoveObject on every returned blob key.
//   - Cap each sweep at 500 rows so a backlog doesn't pin the DB.
type OrphanGCWorker struct {
	svc      *Service
	interval time.Duration
	batch    int
	maxAge   time.Duration
	log      *slog.Logger
}

func NewOrphanGCWorker(svc *Service) *OrphanGCWorker {
	return &OrphanGCWorker{
		svc:      svc,
		interval: 15 * time.Minute,
		batch:    500,
		maxAge:   24 * time.Hour,
		log:      slog.Default().With("component", "media-orphan-gc"),
	}
}

func (w *OrphanGCWorker) Start(ctx context.Context) {
	go w.run(ctx)
}

func (w *OrphanGCWorker) run(ctx context.Context) {
	// One initial sweep on boot — catches anything carried over from
	// a prior process; the periodic ticker handles steady state.
	w.sweep(ctx)

	t := time.NewTicker(w.interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			w.sweep(ctx)
		}
	}
}

func (w *OrphanGCWorker) sweep(ctx context.Context) {
	cutoff := time.Now().Add(-w.maxAge)
	// Slice C / C-CLB-1: candidates are `pending_upload` ONLY.
	//
	// An earlier version also swept CONFIRMED composer-leased assets, so an
	// uploaded-but-never-posted photo was collected. That had to be withdrawn:
	// a confirmed asset can be claimed by writers that never lock the media row
	// (`users.avatar_media_id` and every other plain UUID column), so the
	// reclaim transaction cannot see the claim coming. See ErrMediaConfirmed.
	//
	// The consequence is honest and bounded: an abandoned composer photo is
	// retained rather than swept. The alternative was deleting live avatars.
	ids, err := w.svc.pgStore.ListReclaimableMedia(ctx, cutoff, w.batch)
	if err != nil {
		w.log.Warn("orphan media list failed", "err", err)
		return
	}
	if len(ids) == 0 {
		return
	}

	w.log.Info("orphan media sweep starting", "candidates", len(ids))
	var purged, failed int
	for _, id := range ids {
		if err := w.purgeOne(ctx, id); err != nil {
			failed++
			w.log.Warn("orphan media purge failed", "media_id", id, "err", err)
			continue
		}
		purged++
	}
	w.log.Info("orphan media sweep done", "purged", purged, "failed", failed)
}

// purgeOne reclaims a single orphan through the DURABLE protocol.
//
// Slice C / C-LB-5.7. This used to call DeleteMedia and then delete blobs
// best-effort, which had two defects the newer protocol already solved for the
// on-demand path:
//
//  1. THE ROW WENT FIRST. Once the database row was gone, a transient
//     object-store failure left an untracked blob — nothing recorded that the
//     object still existed, so no later sweep could ever find it. "Storage grows
//     without bound" was the exact problem the GC was written to fix, and this
//     reintroduced it one failed DeleteObject at a time.
//  2. NO ROW LOCK. DeleteMedia does not serialize against a concurrent attach,
//     so a post being created at that moment could reference an asset already
//     being deleted.
//
// DeleteOrphanMedia does both correctly: it re-checks eligibility under
// SELECT … FOR UPDATE (so an in-flight attach blocks and then wins or loses
// cleanly), records every object key in `media_blob_reclaim` BEFORE removing
// rows, and leaves the reclaim row behind for the blob worker when an object
// deletion fails.
//
// It re-checks age and references itself, so a candidate that gained a
// reference between the scan and now is refused rather than deleted — which is
// why ErrMediaNotOrphaned is a normal, non-alarming outcome here.
func (w *OrphanGCWorker) purgeOne(ctx context.Context, id uuid.UUID) error {
	err := w.svc.DeleteOrphanMedia(ctx, id)
	if errors.Is(err, ErrMediaNotOrphaned) {
		// Raced with an attach, or is younger than the reclaim window under
		// the service's own clock. Correct outcome; not a failure.
		w.log.Info("orphan media skipped: no longer eligible", "media_id", id)
		return nil
	}
	return err
}
