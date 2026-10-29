package service

// Recording import claims (29 Oct 2026).
//
// # What the row holds
//
// live_recording_imports.media_id is the asset media-service made from the
// stream's recording (recording.go). This service creates it on the host's
// behalf: the asset's uploader is the stream's host (owner_user_id), and the
// id comes from media-service's own answer, never from a client, so there is
// no owner or readiness check to make. What was missing is the claim: the
// host could delete the asset (DELETE /v1/media/{id}) while this row still
// named it, and the vod_ready event, or the post lookup after it, would then
// point at nothing.
//
// The claim names THIS ROW, not a person: claimant live-service-v2, kind
// live_recording_import, ref = the stream id. The person it binds is the
// host, the asset's uploader; the refusal they read is "used in 1 live
// recording import".
//
// The row holds its media id while this service still relies on it:
//
//   - the job is pending (the asset is processing, or vod_ready is not out);
//   - the job is done and the post lookup is pending (the video post is not
//     known yet; post-service resolves vod_ready against the asset).
//
// It stops holding it when the lookup settles (found: the post now holds the
// asset; deleted; gave up), when the job fails, and when the host's account
// is purged. postgres.ImportHoldsSQL is the same rule for the reconcile tool.
//
// # The protocol
//
// The same three steps as a cover (media_claims.go), under the stream's
// holder lock: claim a media id before the write that first records it,
// release it if that write fails, release what the row stops holding after
// the write. Fail closed: if the id cannot be claimed (no claim client, or
// media-service down) the attempt is recorded WITHOUT the id and retried;
// media-service's import is idempotent on the stream, so the next attempt
// learns the same id and claims it then.

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/google/uuid"

	"github.com/atpost/live-service-v2/internal/mediaclient"
)

// claimKindImport is the kind media-service registers for a recording import.
const claimKindImport = mediaclient.KindLiveRecordingImport

// ImportHolders lists a host's streams whose import row names media, for the
// purge (*postgres.Store, storetest.MemStore).
type ImportHolders interface {
	StreamIDsWithImportMedia(ctx context.Context, creatorID uuid.UUID) ([]uuid.UUID, error)
}

// writeImportClaim runs write under the claim protocol for one stream's
// import row. before is the media id the row holds now (nil: none); after is
// the one it holds once write succeeds (nil: none).
func (s *Service) writeImportClaim(ctx context.Context, streamID uuid.UUID, before, after *uuid.UUID, write func() error) error {
	unlock, err := s.lockMediaHolder(ctx, streamID)
	if err != nil {
		return err
	}
	defer unlock()
	ref := streamID.String()
	var add []uuid.UUID
	if after != nil && *after != uuid.Nil && (before == nil || *before != *after) {
		add = []uuid.UUID{*after}
	}
	if len(add) > 0 {
		if s.claims == nil {
			return fmt.Errorf("%w: no media-service claim client configured", ErrMediaUnavailable)
		}
		if err := s.claims.Claim(ctx, claimKindImport, ref, add); err != nil {
			return fmt.Errorf("%w: claim recording: %v", ErrMediaUnavailable, err)
		}
	}
	if err := write(); err != nil {
		if len(add) > 0 {
			s.releaseImport(ctx, ref, add) // compensate the failed write
		}
		return err
	}
	switch {
	case before == nil:
	case after == nil:
		s.releaseImport(ctx, ref, nil) // the row holds nothing now
	case *before != *after:
		s.releaseImport(ctx, ref, []uuid.UUID{*before})
	}
	return nil
}

// settleImport runs a write after which the stream's import row holds
// nothing (the post lookup settled), then releases what it held. A failed
// write releases nothing: the row still holds its media.
func (s *Service) settleImport(ctx context.Context, streamID uuid.UUID, write func() error) error {
	unlock, err := s.lockMediaHolder(ctx, streamID)
	if err != nil {
		return err
	}
	defer unlock()
	if err := write(); err != nil {
		return err
	}
	s.releaseImport(ctx, streamID.String(), nil)
	return nil
}

// releaseImport is best effort: a failure leaves a stale claim (the host's
// delete is refused until the reconcile tool clears it), never a dangling
// reference. ids nil releases everything the stream's import holds.
func (s *Service) releaseImport(ctx context.Context, ref string, ids []uuid.UUID) bool {
	if s.claims == nil {
		slog.Warn("live-v2: recording import claim not released (no claim client); run /media-claims", "stream_id", ref)
		return false
	}
	rctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), releaseTimeout)
	defer cancel()
	if err := s.claims.Release(rctx, claimKindImport, ref, ids); err != nil {
		slog.Warn("live-v2: recording import claim release failed; the host cannot delete it until /media-claims runs",
			"stream_id", ref, "error", err)
		return false
	}
	return true
}

// releaseCreatorImports releases the purged host's recording import claims.
// Like the covers, before the erase: afterwards nothing names the streams.
func (s *Service) releaseCreatorImports(ctx context.Context, creatorID uuid.UUID) error {
	h, ok := s.store.(ImportHolders)
	if !ok {
		return nil
	}
	ids, err := h.StreamIDsWithImportMedia(ctx, creatorID)
	if err != nil {
		return fmt.Errorf("purge: list recording imports: %w", err)
	}
	if len(ids) == 0 {
		return nil
	}
	if s.claims == nil {
		slog.Warn("live-v2: purge did not release recording import claims (no claim client); run /media-claims",
			"user_id", creatorID, "streams", len(ids))
		return nil
	}
	for _, id := range ids {
		if !s.releaseImport(ctx, id.String(), nil) {
			return fmt.Errorf("purge: release recording import claim of stream %s: %w", id, ErrMediaUnavailable)
		}
	}
	return nil
}
