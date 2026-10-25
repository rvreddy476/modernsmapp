package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/atpost/post-service/internal/mediaclaims"
	"github.com/atpost/post-service/internal/store/postgres"
	"github.com/google/uuid"
)

// Media claims — keeping media-service's owner delete from leaving a post
// cover, a reel draft or an event cover pointing at nothing.
//
// # Which references need one (internal/mediaclaims has the detail)
//
//	posts.cover_media_id                        no FK  → claim  post_cover
//	reel_drafts.media_id, reel_drafts.cover_... no FK  → claim  reel_draft
//	events.cover_media_id                       no FK  → claim  event_cover
//	video_series.cover_media_id                 FK (migration 012) → none
//	post_media / post_draft_media / stories     FK RESTRICT (030, 032) → none
//
// # The protocol (media-service internal/store/postgres/media_claims.go)
//
//  0. LOCK the holder row (store.LockMediaHolder) and re-read what it holds.
//  1. CLAIM every id the write adds, before the write. media-service locks
//     each asset while it records the claim, so an owner delete racing the
//     attach either lands first (the claim answers not found and the write
//     never happens) or waits and is refused.
//  2. WRITE. If the write fails, RELEASE what step 1 claimed and the row
//     does not hold now (a draft publish reuses the draft id as the post id,
//     so a failed retry must not release the claim the first attempt's post
//     relies on).
//  3. RELEASE what the row no longer holds.
//
// A failure in steps 2–3 leaves an EXTRA claim, never a missing one: the
// owner is refused a delete they should be allowed, which cmd/media-claims
// repairs. Nothing is written before its claim succeeds, and nothing still
// referenced is released.
//
// Fail closed: with no claim client, or media-service unreachable, a write
// that ADDS media is refused (503 MEDIA_CLAIMS_UNAVAILABLE). A write that
// only removes media is never refused for that reason.
//
// # Who may be claimed
//
// A claim pins an asset against its OWNER's delete, so only the writer's own
// upload may be claimed: a stranger's id would let anyone stop anyone else
// deleting their media. Post covers already pass verifyCoverOnWrite /
// verifyCoverMedia; reel draft media and event covers pass verifyOwnMedia.

// MediaClaims is media-service's claim API (mediaclaims.Client).
type MediaClaims interface {
	Claim(ctx context.Context, kind string, ref uuid.UUID, mediaIDs []uuid.UUID) error
	Release(ctx context.Context, kind string, ref uuid.UUID, mediaIDs []uuid.UUID) error
}

// mediaHolderStore is the store half (store/postgres/media_holders.go).
type mediaHolderStore interface {
	LockMediaHolder(ctx context.Context, kind string, ref uuid.UUID) (func(), error)
	MediaHeldBy(ctx context.Context, kind string, ref uuid.UUID) (postgres.MediaHolding, error)
}

// ErrMediaClaimsUnavailable: media-service could not record the reference
// (unreachable, not configured, or does not know this claimant yet). Nothing
// was written. 503, never a verdict about the request.
var ErrMediaClaimsUnavailable = errors.New("media claims are unavailable")

// ClaimMediaGoneError: an asset the write adds was deleted between the
// authority check and the claim. Nothing was written. It unwraps to
// ErrMediaNotFound, so every route answers its usual MEDIA_NOT_FOUND.
type ClaimMediaGoneError struct{ Kind string }

func (e *ClaimMediaGoneError) Error() string {
	return ErrMediaNotFound.Error() + ": removed before it could be attached"
}
func (e *ClaimMediaGoneError) Unwrap() error { return ErrMediaNotFound }

// SetMediaClaims installs the claim client.
func (s *Service) SetMediaClaims(c MediaClaims) { s.claims = c }

// claimReleaseTimeout bounds one best-effort release. Releases run on a
// context detached from the request: a client hanging up after its write
// committed must not leave the compensation undone.
const claimReleaseTimeout = 5 * time.Second

// holderWrite runs write under the claim protocol for one holder row.
//
// next is given what the row holds now (read under the lock) and returns the
// set it will hold once write succeeds; an error from it refuses the write.
func (s *Service) holderWrite(
	ctx context.Context, kind string, ref uuid.UUID,
	next func(prev postgres.MediaHolding) ([]uuid.UUID, error),
	write func() error,
) error {
	unlock, err := s.lockMediaHolder(ctx, kind, ref)
	if err != nil {
		return err
	}
	defer unlock()

	prev, err := s.mediaHeldBy(ctx, kind, ref)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrMediaClaimsUnavailable, err)
	}
	want, err := next(prev)
	if err != nil {
		return err
	}
	var held []uuid.UUID
	if prev.Live {
		held = prev.MediaIDs
	}
	add, drop := mediaDiff(held, want)

	// 1. Claim.
	if len(add) > 0 {
		if s.claims == nil {
			return fmt.Errorf("%w: no media-service claim client configured", ErrMediaClaimsUnavailable)
		}
		if err := s.claims.Claim(ctx, kind, ref, add); err != nil {
			if errors.Is(err, mediaclaims.ErrNotFound) {
				// Deleted between the authority check and the claim.
				return &ClaimMediaGoneError{Kind: kind}
			}
			return fmt.Errorf("%w: %v", ErrMediaClaimsUnavailable, err)
		}
	}

	// 2. Write; compensate on failure.
	if err := write(); err != nil {
		s.compensateClaim(ctx, kind, ref, add)
		return err
	}

	// 3. Release what the row stopped holding.
	if len(drop) > 0 {
		s.releaseClaims(ctx, kind, ref, drop)
	}
	return nil
}

// compensateClaim releases the ids a failed write claimed, except any the row
// holds now (the caller still holds the row's lock).
func (s *Service) compensateClaim(ctx context.Context, kind string, ref uuid.UUID, claimed []uuid.UUID) {
	if len(claimed) == 0 {
		return
	}
	now, err := s.mediaHeldBy(context.WithoutCancel(ctx), kind, ref)
	if err != nil {
		// Unknown is not "nothing": keeping the claim costs a refused
		// delete; releasing a live one costs the asset.
		slog.Warn("post-service: failed write's media claim kept (holder unreadable); run media-claims",
			"kind", kind, "ref", ref, "error", err)
		return
	}
	var keep []uuid.UUID
	if now.Live {
		keep = now.MediaIDs
	}
	if release := subtractIDs(claimed, keep); len(release) > 0 {
		s.releaseClaims(ctx, kind, ref, release)
	}
}

// settleMediaHolder releases every claim of a holder row that is gone or will
// never be shown again (deleted/published reel draft, purged post). A live
// row is left alone. Returns an error only when a release that was due did
// not happen.
func (s *Service) settleMediaHolder(ctx context.Context, kind string, ref uuid.UUID) error {
	unlock, err := s.lockMediaHolder(ctx, kind, ref)
	if err != nil {
		return err
	}
	defer unlock()
	h, err := s.mediaHeldBy(ctx, kind, ref)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrMediaClaimsUnavailable, err)
	}
	if h.Live && len(h.MediaIDs) > 0 {
		return nil
	}
	if !s.releaseClaims(ctx, kind, ref, nil) {
		return fmt.Errorf("%w: release of %s %s did not happen", ErrMediaClaimsUnavailable, kind, ref)
	}
	return nil
}

// settleMediaHolderBestEffort is settleMediaHolder for paths that must not
// fail over it (a delete, a publish that already happened).
func (s *Service) settleMediaHolderBestEffort(ctx context.Context, kind string, ref uuid.UUID) {
	if err := s.settleMediaHolder(ctx, kind, ref); err != nil {
		slog.Warn("post-service: media claims not settled; run media-claims",
			"kind", kind, "ref", ref, "error", err)
	}
}

// ReleasePurgedPostClaims releases the cover claim of a post the purge just
// removed (postpurge.Worker, after PurgePost). Best effort there; the media
// queue drain repeats it durably for the assets it deletes.
func (s *Service) ReleasePurgedPostClaims(ctx context.Context, postID uuid.UUID) error {
	return s.settleMediaHolder(ctx, postgres.HolderPostCover, postID)
}

// releaseClaims is best effort: a failure leaves a stale claim, never a
// dangling reference. ids nil releases everything ref holds of kind.
func (s *Service) releaseClaims(ctx context.Context, kind string, ref uuid.UUID, ids []uuid.UUID) bool {
	if s.claims == nil {
		slog.Warn("post-service: media claim not released (no claim client); run media-claims",
			"kind", kind, "ref", ref)
		return false
	}
	rctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), claimReleaseTimeout)
	defer cancel()
	if err := s.claims.Release(rctx, kind, ref, ids); err != nil {
		slog.Warn("post-service: media claim release failed; the owner cannot delete it until media-claims runs",
			"kind", kind, "ref", ref, "media", len(ids), "error", err)
		return false
	}
	return true
}

func (s *Service) lockMediaHolder(ctx context.Context, kind string, ref uuid.UUID) (func(), error) {
	if s.mediaHolders == nil {
		return func() {}, nil
	}
	unlock, err := s.mediaHolders.LockMediaHolder(ctx, kind, ref)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrMediaClaimsUnavailable, err)
	}
	return unlock, nil
}

// mediaHeldBy re-reads the holder. With no store (unit rigs of the pure
// protocol) the row holds nothing and does not exist yet.
func (s *Service) mediaHeldBy(ctx context.Context, kind string, ref uuid.UUID) (postgres.MediaHolding, error) {
	if s.mediaHolders == nil {
		return postgres.MediaHolding{Kind: kind, Ref: ref}, nil
	}
	return s.mediaHolders.MediaHeldBy(ctx, kind, ref)
}

// verifyOwnMedia is the claim's authority for reel draft media and event
// covers: the asset exists and the writer uploaded it. Fail closed on an
// unreadable store. Kind and processing state are deliberately not asked
// here: a draft is made while its upload is still processing, and neither
// route ever checked them; what a claim adds is the pin, so the pin is what
// is gated.
func (s *Service) verifyOwnMedia(ctx context.Context, ownerID uuid.UUID, ids ...uuid.UUID) error {
	var want []uuid.UUID
	for _, id := range ids {
		if id != uuid.Nil {
			want = append(want, id)
		}
	}
	if len(want) == 0 {
		return nil
	}
	if s.ownMedia == nil {
		return fmt.Errorf("verify media ownership: %w", ErrAuthoringStoreUnavailable)
	}
	ownership, err := s.ownMedia.BatchGetMediaOwnership(ctx, want)
	if err != nil {
		return fmt.Errorf("verify media ownership: %w", err)
	}
	for _, id := range want {
		if err := checkOwnMedia(id, ownerID, ownership[id], hasOwnership(ownership, id)); err != nil {
			return err
		}
	}
	return nil
}

// checkOwnMedia is verifyOwnMedia's decision, pure.
func checkOwnMedia(mediaID, ownerID uuid.UUID, m postgres.MediaOwnership, found bool) error {
	if !found {
		return fmt.Errorf("%w: %s", ErrMediaNotFound, mediaID)
	}
	if m.UploaderID != ownerID {
		return ErrMediaNotOwned
	}
	return nil
}

func hasOwnership(m map[uuid.UUID]postgres.MediaOwnership, id uuid.UUID) bool {
	_, ok := m[id]
	return ok
}

// mediaOwnershipReader is the read verifyOwnMedia makes (*postgres.Store).
type mediaOwnershipReader interface {
	BatchGetMediaOwnership(ctx context.Context, ids []uuid.UUID) (map[uuid.UUID]postgres.MediaOwnership, error)
}

// mediaDiff returns the ids in next that are not in prev (added) and the ids
// in prev that are not in next (removed). uuid.Nil is not media and is
// ignored; duplicates collapse; order is kept.
func mediaDiff(prev, next []uuid.UUID) (added, removed []uuid.UUID) {
	inPrev := map[uuid.UUID]bool{}
	for _, id := range prev {
		if id != uuid.Nil {
			inPrev[id] = true
		}
	}
	inNext := map[uuid.UUID]bool{}
	for _, id := range next {
		if id == uuid.Nil || inNext[id] {
			continue
		}
		inNext[id] = true
		if !inPrev[id] {
			added = append(added, id)
		}
	}
	for _, id := range prev {
		if id != uuid.Nil && !inNext[id] {
			removed = append(removed, id)
			inNext[id] = true // collapse duplicates in prev
		}
	}
	return added, removed
}

// subtractIDs is ids minus drop (uuid.Nil dropped, duplicates collapsed).
func subtractIDs(ids, drop []uuid.UUID) []uuid.UUID {
	out, _ := mediaDiff(drop, ids)
	return out
}

// optionalID is the one-element set of an optional media id.
func optionalID(id *uuid.UUID) []uuid.UUID {
	if id == nil || *id == uuid.Nil {
		return nil
	}
	return []uuid.UUID{*id}
}
