package personalization

import (
	"context"
	"fmt"

	"github.com/google/uuid"
)

// ViewerSignalKeys are the Redis keys the warmer writes per viewer. Every
// other key this service writes is per post (post:cqs:*,
// post:rt_engagement:*) or per consumed event (consumed:*) and holds no
// user id.
func ViewerSignalKeys(userID uuid.UUID) []string {
	id := userID.String()
	return []string{
		fmt.Sprintf(AffinityKeyFmt, id),
		fmt.Sprintf(MediaPrefsKeyFmt, id),
		fmt.Sprintf(TopicAffinityKeyFmt, id),
		fmt.Sprintf(CompletionsKeyFmt, id),
	}
}

// DeleteViewerSignals erases the viewer's signal keys (reels-8). It takes
// the run lock, so a warmer pass that read the viewer's sessions before
// PostgreSQL erased them cannot write the keys back after this delete:
// the account eraser runs this after the PostgreSQL purge, and the next
// pass finds nothing to write. Deleting absent keys is a no-op.
//
// The viewer's id can also sit as a field inside other viewers'
// user:affinities hashes when they were a creator; those hashes are
// rewritten whole on every pass and expire after signalTTL (26h).
func (w *Warmer) DeleteViewerSignals(ctx context.Context, userID uuid.UUID) error {
	if w == nil || w.rdb == nil {
		return nil
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if err := w.rdb.Del(ctx, ViewerSignalKeys(userID)...).Err(); err != nil {
		return fmt.Errorf("purge: delete viewer signals: %w", err)
	}
	return nil
}
