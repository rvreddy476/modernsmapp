package service

import (
	"context"
	"errors"
	"fmt"

	"github.com/atpost/channel-service/internal/mediaclient"
	"github.com/google/uuid"
)

// Channel-update media authority.
//
// # The hole
//
// CreateUpdate and EditUpdate wrote channel_updates.media_ids with whatever
// UUIDs the request carried. Nothing asked whether the asset existed, whose
// it was, or whether its bytes had arrived. So an admin could attach another
// user's photo by lifting its id from any public payload, and could attach a
// `pending_upload` id that media-service's orphan reclaim then deletes:
// reclaim (C-CLB-1) only protects CONFIRMED assets and takes no lock that a
// plain UUID[] column in another database could join, so the update is left
// pointing at nothing.
//
// # The rule (the post-service create rule, create_guards.go)
//
//   - the asset exists (media-service admits it to a keyed service caller);
//   - its uploader is the caller;
//   - its upload is CONFIRMED: `uploaded`, `processing` or `ready`. An
//     allowlist: `pending_upload`, `failed`, `rejected`, `deleted`, empty and
//     anything unknown are refused;
//   - moderation has not refused it (`rejected`, or empty, is refused).
//
// Refusing `pending_upload` is what closes the reclaim race without a lock:
// processing_status only moves forward from `pending_upload`, and a confirmed
// asset is never reclaimed, so an id that passed this check cannot be swept.
//
// What this check does NOT stop is the uploader deleting the asset after it is
// attached; the claim protocol (media_claims.go) closes that.
//
// # Fail closed
//
// No client, an unreachable media-service or an unreadable answer is
// ErrMediaUnavailable (503). An update without media never calls out.

var (
	// ErrMediaNotFound: media-service has no such asset (or will not admit it).
	ErrMediaNotFound = errors.New("media does not exist")
	// ErrMediaNotOwned is worded to say nothing about the foreign asset.
	ErrMediaNotOwned = errors.New("media cannot be attached by this user")
	// ErrMediaNotReady: the upload has not been confirmed, failed, or was
	// refused by moderation.
	ErrMediaNotReady = errors.New("media has not finished uploading")
	// ErrMediaUnavailable: the check could not be made. Retryable.
	ErrMediaUnavailable = errors.New("media-service is unavailable")
)

// maxUpdateMedia bounds the media on one update. Each new id is one call to
// media-service, made while the admin waits; an unbounded list is an
// unbounded fan-out of lookups from a single request.
const maxUpdateMedia = 20

// MediaLookup is what the gate needs from media-service.
type MediaLookup interface {
	Get(ctx context.Context, mediaID uuid.UUID) (*mediaclient.Asset, error)
}

// WithMedia installs the media-service lookup. Without one, any update that
// carries media is refused with ErrMediaUnavailable.
//
// A lookup that also implements MediaClaims (mediaclient.Client does) is
// installed as the claim client too: the gate and the claims are the same
// media-service, and wiring one without the other would refuse every attach.
func (s *Service) WithMedia(m MediaLookup) *Service {
	s.media = m
	if c, ok := m.(MediaClaims); ok {
		s.claims = c
	}
	return s
}

var confirmedMediaStatuses = map[string]bool{
	"uploaded":   true,
	"processing": true,
	"ready":      true,
}

// checkUpdateMediaAsset is the per-asset decision, as a pure function.
func checkUpdateMediaAsset(a *mediaclient.Asset, actorID uuid.UUID) error {
	if a.UploaderID != actorID {
		return ErrMediaNotOwned
	}
	if !confirmedMediaStatuses[a.ProcessingStatus] {
		return fmt.Errorf("%w: %s is %q", ErrMediaNotReady, a.ID, a.ProcessingStatus)
	}
	if a.ModerationStatus == "" || a.ModerationStatus == "rejected" {
		return fmt.Errorf("%w: %s moderation is %q", ErrMediaNotReady, a.ID, a.ModerationStatus)
	}
	return nil
}

// verifyUpdateMedia checks every id in ids that is not already in attached.
//
// attached is the update's current media on an edit (nil on a create). Those
// ids were already on the update, possibly uploaded by the admin who wrote it
// rather than the one editing it; re-sending them attaches nothing new, so
// they are not re-verified. Every id the request ADDS must be the actor's
// own, confirmed upload.
//
// It stops at the first refusal: reporting which ids of a batch belong to
// someone else would be a bulk ownership oracle.
func (s *Service) verifyUpdateMedia(ctx context.Context, actorID uuid.UUID, ids, attached []uuid.UUID) error {
	if len(ids) > maxUpdateMedia {
		return fmt.Errorf("invalid: an update can carry at most %d media items", maxUpdateMedia)
	}
	seen := make(map[uuid.UUID]bool, len(ids)+len(attached))
	for _, id := range attached {
		seen[id] = true
	}
	for _, id := range ids {
		if seen[id] {
			continue
		}
		seen[id] = true
		if id == uuid.Nil {
			return ErrMediaNotFound
		}
		if s.media == nil {
			return fmt.Errorf("%w: no media-service client configured", ErrMediaUnavailable)
		}
		a, err := s.media.Get(ctx, id)
		switch {
		case errors.Is(err, mediaclient.ErrNotFound):
			return ErrMediaNotFound
		case err != nil:
			return fmt.Errorf("%w: %v", ErrMediaUnavailable, err)
		case a == nil:
			return fmt.Errorf("%w: empty answer", ErrMediaUnavailable)
		}
		if err := checkUpdateMediaAsset(a, actorID); err != nil {
			return err
		}
	}
	return nil
}
