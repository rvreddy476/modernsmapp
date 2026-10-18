package main

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/atpost/media-service/internal/processing"
	"github.com/google/uuid"
)

// moderationScanWriter is the one store call recordVideoScanLabels needs.
type moderationScanWriter interface {
	UpdateMediaModerationScan(ctx context.Context, id uuid.UUID, labels []byte, scanner string) error
}

// recordVideoScanLabels keeps a passing video scan's labels next to the
// verdict, as processImage does for images (chat parity pass 2: media-service
// derives "sensitive" from them; dating-service already reads them). A
// rejected video needs none — the verdict says everything. A failed write is
// returned, so the transcode retries like every other store uncertainty in the
// scan step rather than leaving a ready video whose labels are a previous
// run's.
func recordVideoScanLabels(ctx context.Context, w moderationScanWriter, mediaAssetID uuid.UUID,
	scanner processing.Scanner, res processing.ScanResult) error {
	if !res.IsSafe {
		return nil
	}
	labels := res.Labels
	if labels == nil {
		labels = []processing.ModerationLabel{}
	}
	raw, err := json.Marshal(labels)
	if err != nil {
		return fmt.Errorf("encode video moderation labels: %w", err)
	}
	if err := w.UpdateMediaModerationScan(ctx, mediaAssetID, raw, processing.ScannerName(scanner)); err != nil {
		return fmt.Errorf("persist video moderation labels: %w", err)
	}
	return nil
}
