package service

// Stream covers and media claims (25 Oct 2026).
//
// # The hole
//
// live_streams.cover_media_id is a plain UUID in this service's database.
// Nothing checked it named the host's own finished upload (any uuid was
// stored), and nothing stopped the uploader deleting the asset afterwards
// (DELETE /v1/media/{id}): media-service could not see this row, and the
// stream and its recording were left showing a cover that no longer exists.
//
// # The check
//
// A cover being set is read from media-service first (verifyCover): it must
// exist, be the host's own upload, have finished uploading and not have been
// rejected by moderation — channel-service's rule for channel media.
//
// # The protocol (media-service internal/store/postgres/media_claims.go)
//
//  1. CLAIM the cover a write is about to set, before the write. media-service
//     locks the asset row while it records the claim, so an owner delete
//     racing the attach either lands first (the claim is refused and the row
//     is never written) or waits and is refused.
//  2. WRITE the row. If the write fails, RELEASE exactly what step 1 claimed.
//  3. After a successful write, RELEASE the cover the row no longer holds.
//
// Every claim-write-release of one stream's cover runs under the store's
// per-stream lock (postgres.Store.LockMediaHolder), so two edits cannot
// interleave and have one release a cover the other just set again.
//
// A failure in steps 2–3 leaves an EXTRA claim, never a missing one: the
// owner is refused a delete they should be allowed, which cmd/media-claims
// (the reconcile tool) repairs. Fail closed: with no media client, or
// media-service unreachable, a write that SETS a cover is refused with
// ErrMediaUnavailable (503). Clearing a cover is never refused for that
// reason; its release is best effort.

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"

	"github.com/atpost/live-service-v2/internal/mediaclient"
)

// Cover sentinels; the HTTP layer maps them (writeServiceErr).
var (
	ErrCoverNotFound    = errors.New("invalid: cover_media_id names no media")
	ErrCoverNotOwned    = errors.New("forbidden: the cover must be your own upload")
	ErrCoverNotReady    = errors.New("invalid: the cover has not finished uploading or was rejected")
	ErrMediaUnavailable = errors.New("media-service is unavailable; try again")
)

// claimKindCover is the kind media-service registers for a stream cover.
const claimKindCover = mediaclient.KindLiveCover

// MediaLookup reads an asset's record (mediaclient.Client).
type MediaLookup interface {
	Get(ctx context.Context, mediaID uuid.UUID) (*mediaclient.Asset, error)
}

// MediaClaims is media-service's claim API (mediaclient.Client).
type MediaClaims interface {
	Claim(ctx context.Context, kind, ref string, mediaIDs []uuid.UUID) error
	Release(ctx context.Context, kind, ref string, mediaIDs []uuid.UUID) error
}

// MediaHolders is the store side of the protocol (*postgres.Store). A store
// without it (the in-memory test store) has no concurrent writers to
// serialise and no rows to list.
type MediaHolders interface {
	// LockMediaHolder serialises the claim-write-release of one stream.
	LockMediaHolder(ctx context.Context, streamID uuid.UUID) (unlock func(), err error)
	// StreamIDsWithCover lists the creator's streams that name a cover.
	StreamIDsWithCover(ctx context.Context, creatorID uuid.UUID) ([]uuid.UUID, error)
}

// WithMedia installs the cover lookup, and the claim client when m also
// claims (mediaclient.Client does). Nil refuses every cover: fail closed.
func (s *Service) WithMedia(m MediaLookup) *Service {
	s.coverMedia = m
	if c, ok := m.(MediaClaims); ok {
		s.claims = c
	}
	return s
}

// WithMediaClaims installs the claim client on its own (tests).
func (s *Service) WithMediaClaims(c MediaClaims) *Service {
	s.claims = c
	return s
}

// confirmedCoverStatuses are the processing states of an upload that
// finished; the variants may still be rendering, the original is there.
var confirmedCoverStatuses = map[string]bool{"uploaded": true, "processing": true, "ready": true}

// verifyCover checks id may become hostID's stream cover. nil is no cover.
func (s *Service) verifyCover(ctx context.Context, hostID uuid.UUID, id *uuid.UUID) error {
	if id == nil {
		return nil
	}
	if *id == uuid.Nil {
		return ErrCoverNotFound
	}
	if s.coverMedia == nil {
		return fmt.Errorf("%w: no media-service client configured", ErrMediaUnavailable)
	}
	a, err := s.coverMedia.Get(ctx, *id)
	switch {
	case errors.Is(err, mediaclient.ErrNotFound):
		return ErrCoverNotFound
	case err != nil:
		return fmt.Errorf("%w: %v", ErrMediaUnavailable, err)
	case a == nil:
		return fmt.Errorf("%w: empty answer", ErrMediaUnavailable)
	}
	if a.UploaderID != hostID {
		return ErrCoverNotOwned
	}
	if !confirmedCoverStatuses[a.ProcessingStatus] || a.ModerationStatus == "" || a.ModerationStatus == "rejected" {
		return fmt.Errorf("%w: processing %q, moderation %q", ErrCoverNotReady, a.ProcessingStatus, a.ModerationStatus)
	}
	return nil
}

// coverDiff is what a write does to one stream's cover: the id it sets (if
// new) and the id it stops holding (if any).
func coverDiff(prev, next *uuid.UUID) (add, drop []uuid.UUID) {
	if prev != nil && next != nil && *prev == *next {
		return nil, nil
	}
	if next != nil && *next != uuid.Nil {
		add = []uuid.UUID{*next}
	}
	if prev != nil && *prev != uuid.Nil {
		drop = []uuid.UUID{*prev}
	}
	return add, drop
}

// releaseTimeout bounds one best-effort release. Releases run on a context
// detached from the request: a client hanging up after its write committed
// must not leave the compensation undone.
const releaseTimeout = 5 * time.Second

// writeWithClaims runs write under the claim protocol for one stream.
func (s *Service) writeWithClaims(ctx context.Context, streamID uuid.UUID, add, drop []uuid.UUID, write func() error) error {
	ref := streamID.String()
	if len(add) > 0 {
		if s.claims == nil {
			return fmt.Errorf("%w: no media-service claim client configured", ErrMediaUnavailable)
		}
		if err := s.claims.Claim(ctx, claimKindCover, ref, add); err != nil {
			if errors.Is(err, mediaclient.ErrNotFound) {
				return ErrCoverNotFound // the owner deleted it first; nothing is written
			}
			return fmt.Errorf("%w: claim: %v", ErrMediaUnavailable, err)
		}
	}
	if err := write(); err != nil {
		if len(add) > 0 {
			s.releaseCover(ctx, ref, add) // compensate the failed write
		}
		return err
	}
	if len(drop) > 0 {
		s.releaseCover(ctx, ref, drop)
	}
	return nil
}

// releaseCover is best effort: a failure leaves a stale claim (the owner's
// delete is refused until the reconcile tool clears it), never a dangling
// reference. ids nil releases everything the stream holds.
func (s *Service) releaseCover(ctx context.Context, ref string, ids []uuid.UUID) bool {
	if s.claims == nil {
		slog.Warn("live-v2: cover claim not released (no claim client); run /media-claims", "stream_id", ref)
		return false
	}
	rctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), releaseTimeout)
	defer cancel()
	if err := s.claims.Release(rctx, claimKindCover, ref, ids); err != nil {
		slog.Warn("live-v2: cover claim release failed; the owner cannot delete it until /media-claims runs",
			"stream_id", ref, "error", err)
		return false
	}
	return true
}

// lockMediaHolder takes the store's per-stream cover lock when it has one.
func (s *Service) lockMediaHolder(ctx context.Context, streamID uuid.UUID) (func(), error) {
	h, ok := s.store.(MediaHolders)
	if !ok {
		return func() {}, nil
	}
	unlock, err := h.LockMediaHolder(ctx, streamID)
	if err != nil {
		return nil, fmt.Errorf("lock stream cover: %w", err)
	}
	return unlock, nil
}

// UserEraser is the account erase the purge consumer runs (*postgres.Store).
type UserEraser interface {
	PurgeUser(ctx context.Context, userID uuid.UUID) error
}

// ReleasingEraser releases a purged host's cover claims, then erases.
type ReleasingEraser struct {
	svc  *Service
	next UserEraser
}

// ReleasingEraser wraps next (the store's erase) so a purged host's stream
// covers stop being held in media-service.
func (s *Service) ReleasingEraser(next UserEraser) *ReleasingEraser {
	return &ReleasingEraser{svc: s, next: next}
}

// PurgeUser releases first and erases second: once the rows are gone nothing
// names which streams held claims, so a release that failed after the erase
// could never be retried. A release failure returns the error and the purge
// event is redelivered (the erase is idempotent). The rows name an unclaimed
// cover only for the moment between the two, while their account is erased.
func (e *ReleasingEraser) PurgeUser(ctx context.Context, userID uuid.UUID) error {
	if err := e.svc.releaseCreatorCovers(ctx, userID); err != nil {
		return err
	}
	return e.next.PurgeUser(ctx, userID)
}

func (s *Service) releaseCreatorCovers(ctx context.Context, creatorID uuid.UUID) error {
	h, ok := s.store.(MediaHolders)
	if !ok {
		return nil
	}
	ids, err := h.StreamIDsWithCover(ctx, creatorID)
	if err != nil {
		return fmt.Errorf("purge: list covers: %w", err)
	}
	if len(ids) == 0 {
		return nil
	}
	if s.claims == nil {
		// Never configured means never claimed by this process; media-service's
		// own erasure clears claims on the host's assets.
		slog.Warn("live-v2: purge did not release cover claims (no claim client); run /media-claims",
			"user_id", creatorID, "streams", len(ids))
		return nil
	}
	for _, id := range ids {
		if !s.releaseCover(ctx, id.String(), nil) {
			return fmt.Errorf("purge: release cover claim of stream %s: %w", id, ErrMediaUnavailable)
		}
	}
	return nil
}
