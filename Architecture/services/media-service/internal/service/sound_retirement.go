package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/atpost/media-service/internal/store/postgres"
	"github.com/google/uuid"
)

// Retired sounds (2026-11-16, migration 032). When its author deletes a
// video, the sound taken from it is retired, not destroyed (the store's
// delete paths, store/postgres/asset_purge.go): the reels that already play
// it keep playing it. Its objects go only on an account erasure, on a
// takedown (TakeDownSound), or once no post references it any more
// (reclaimUnusedRetiredSounds, run by the blob reclaim worker).

const (
	// retiredSoundGrace is how long a retired sound is kept even with no
	// post referencing it: an attach that read the sound as ready just
	// before it was retired commits within it.
	retiredSoundGrace = time.Hour
	// retiredSoundBatch bounds one sweep.
	retiredSoundBatch = 200
)

// ErrLibrarySoundTakedown: a library sound is withdrawn through the admin
// library (withdraw everywhere), not taken down by id (409).
var ErrLibrarySoundTakedown = errors.New("a library sound is withdrawn through the sound library")

// soundTakedownStore is the store slice the takedown needs.
type soundTakedownStore interface {
	TakeDownSound(ctx context.Context, soundID uuid.UUID) ([]string, error)
}

// TakeDownSound destroys one extracted sound for copyright or moderation,
// whatever its status (a retired sound included): every reel that plays it
// goes silent. Its objects are recorded for reclaim in the same transaction
// and deleted now; a failure is retried by the reclaim worker. It returns
// how many objects were queued.
func (s *Service) TakeDownSound(ctx context.Context, soundID uuid.UUID) (int, error) {
	if s == nil || s.pgStore == nil {
		return 0, fmt.Errorf("sound takedown: store not configured")
	}
	return takeDownSound(ctx, s.pgStore, s.reclaimBlobs, soundID)
}

// takeDownSound is TakeDownSound over explicit dependencies (tests).
func takeDownSound(ctx context.Context, store soundTakedownStore, reclaim func(context.Context, []string), soundID uuid.UUID) (int, error) {
	keys, err := store.TakeDownSound(ctx, soundID)
	switch {
	case errors.Is(err, postgres.ErrSoundNotFound):
		return 0, ErrSoundNotFound
	case errors.Is(err, postgres.ErrLibrarySoundTakedown):
		return 0, ErrLibrarySoundTakedown
	case err != nil:
		return 0, fmt.Errorf("sound takedown: %w", err)
	}
	reclaim(ctx, keys)
	slog.InfoContext(ctx, "sound taken down", "audio_track_id", soundID, "objects", len(keys))
	return len(keys), nil
}

// retiredSoundSweepStore is the store slice the sweep needs.
type retiredSoundSweepStore interface {
	ReclaimUnusedRetiredSounds(ctx context.Context, grace time.Duration, limit int) ([]string, error)
}

// reclaimUnusedRetiredSounds destroys the retired sounds no post references
// any more (past the grace). Their keys are already in the reclaim ledger;
// the reclaim worker's drain, which runs right after it, deletes them.
func reclaimUnusedRetiredSounds(ctx context.Context, store retiredSoundSweepStore) int {
	keys, err := store.ReclaimUnusedRetiredSounds(ctx, retiredSoundGrace, retiredSoundBatch)
	if err != nil {
		slog.ErrorContext(ctx, "retired sound sweep failed", "error", err)
		return 0
	}
	if len(keys) > 0 {
		slog.InfoContext(ctx, "retired sound sweep", "objects_queued", len(keys))
	}
	return len(keys)
}
