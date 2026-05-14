package service

import (
	"context"
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
	ids, err := w.svc.pgStore.ListOrphanedPendingUploads(ctx, cutoff, w.batch)
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

// purgeOne deletes the row + blob objects for a single orphan. Best-
// effort on the blob side: if MinIO rejects an object we still return
// the DB delete success — the row is gone and the row owns no
// further references, so leaving a stray blob is preferable to
// retrying the DB delete forever.
func (w *OrphanGCWorker) purgeOne(ctx context.Context, id uuid.UUID) error {
	keys, err := w.svc.pgStore.DeleteMedia(ctx, id)
	if err != nil {
		return err
	}
	for _, key := range keys {
		if err := w.svc.blobStore.DeleteObject(ctx, key); err != nil {
			w.log.Warn("orphan blob delete failed", "media_id", id, "key", key, "err", err)
		}
	}
	return nil
}
