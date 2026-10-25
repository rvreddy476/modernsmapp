package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/atpost/channel-service/internal/mediaclient"
	"github.com/google/uuid"
)

// Media claims — keeping media-service's owner delete from leaving a channel
// row pointing at nothing.
//
// # The hole
//
// channel_updates.media_ids and broadcast_channels.avatar_media_id /
// banner_media_id live in this service's database. update_media.go checks an
// asset is the admin's own finished upload before it is attached, but nothing
// stopped the uploader deleting it afterwards (DELETE /v1/media/{id}):
// media-service could not see these rows, and the update or channel was left
// naming an asset that no longer exists.
//
// # The protocol (media-service internal/store/postgres/media_claims.go)
//
//  1. CLAIM every id a write is about to add, before the write. media-service
//     locks each asset row while it records the claim, so an owner delete
//     racing the attach either lands first (the claim is refused with
//     ErrMediaNotFound and the write never happens) or waits and is refused.
//  2. WRITE the row. If the write fails, RELEASE exactly what step 1 claimed
//     (compensation), so a refused write does not pin the owner's media.
//  3. After a successful write, RELEASE every id the row no longer holds.
//
// A failure in steps 2–3 leaves an EXTRA claim, never a missing one: the
// owner is refused a delete they should be allowed, which cmd/media-claims
// (the reconcile tool) repairs. The opposite error — a reference with no
// claim — is the defect this exists to close, so every step is ordered to
// make that impossible: nothing is written before its claim succeeds, and
// nothing still referenced is ever released.
//
// Fail closed: with no claim client, or media-service unreachable, a write
// that ADDS media is refused with ErrMediaUnavailable (503). A write that only
// removes media is never refused for that reason; its release is best effort.

// Claim kinds, as media-service registers them for "channel-service".
const (
	claimKindUpdate = mediaclient.KindChannelUpdate
	claimKindAvatar = mediaclient.KindChannelAvatar
	claimKindBanner = mediaclient.KindChannelBanner
)

// MediaClaims is media-service's claim API (mediaclient.Client).
type MediaClaims interface {
	Claim(ctx context.Context, kind string, ref uuid.UUID, mediaIDs []uuid.UUID) error
	Release(ctx context.Context, kind string, ref uuid.UUID, mediaIDs []uuid.UUID) error
}

// WithMediaClaims installs the claim client. WithMedia installs it too when
// the lookup it is given also claims (mediaclient.Client does).
func (s *Service) WithMediaClaims(c MediaClaims) *Service {
	s.claims = c
	return s
}

// releaseTimeout bounds one best-effort release. Releases run on a context
// detached from the request: a client hanging up after its write committed
// must not leave the compensation undone.
const releaseTimeout = 5 * time.Second

// claimChange is what one write does to one holder's media: ids it adds and
// ids it stops holding.
type claimChange struct {
	kind      string
	ref       uuid.UUID
	add, drop []uuid.UUID
}

// mediaDiff returns the ids in next that are not in prev (added) and the ids
// in prev that are not in next (removed). uuid.Nil is not media and is
// ignored; duplicates collapse.
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

// optionalMediaDiff is mediaDiff for a single optional column.
func optionalMediaDiff(prev, next *uuid.UUID) (added, removed []uuid.UUID) {
	var p, n []uuid.UUID
	if prev != nil {
		p = []uuid.UUID{*prev}
	}
	if next != nil {
		n = []uuid.UUID{*next}
	}
	return mediaDiff(p, n)
}

// writeWithClaims runs write under the claim protocol for changes.
func (s *Service) writeWithClaims(ctx context.Context, changes []claimChange, write func() error) error {
	// 1. Claim, in order. A refusal part way releases what earlier changes
	// claimed: an all-or-nothing call per change, compensated across them.
	var claimed []claimChange
	for _, ch := range changes {
		if len(ch.add) == 0 {
			continue
		}
		if s.claims == nil {
			s.releaseClaimed(ctx, claimed)
			return fmt.Errorf("%w: no media-service claim client configured", ErrMediaUnavailable)
		}
		if err := s.claims.Claim(ctx, ch.kind, ch.ref, ch.add); err != nil {
			s.releaseClaimed(ctx, claimed) // compensate a partial claim
			if errors.Is(err, mediaclient.ErrNotFound) {
				return ErrMediaNotFound
			}
			return fmt.Errorf("%w: claim: %v", ErrMediaUnavailable, err)
		}
		claimed = append(claimed, ch)
	}

	// 2. Write; compensate on failure.
	if err := write(); err != nil {
		s.releaseClaimed(ctx, claimed) // compensate the failed write
		return err
	}

	// 3. Release what the row no longer holds.
	for _, ch := range changes {
		if len(ch.drop) > 0 {
			s.release(ctx, ch.kind, ch.ref, ch.drop)
		}
	}
	return nil
}

func (s *Service) releaseClaimed(ctx context.Context, claimed []claimChange) {
	for _, ch := range claimed {
		s.release(ctx, ch.kind, ch.ref, ch.add)
	}
}

// release is best effort: a failure leaves a stale claim (the owner's delete
// is refused until the reconcile tool clears it), never a dangling reference.
// ids nil releases everything ref holds of kind.
func (s *Service) release(ctx context.Context, kind string, ref uuid.UUID, ids []uuid.UUID) bool {
	if s.claims == nil {
		slog.Warn("channel-service: media claim not released (no claim client); run the reconcile tool",
			"kind", kind, "ref", ref)
		return false
	}
	rctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), releaseTimeout)
	defer cancel()
	if err := s.claims.Release(rctx, kind, ref, ids); err != nil {
		slog.Warn("channel-service: media claim release failed; the owner cannot delete it until the reconcile tool runs",
			"kind", kind, "ref", ref, "media", len(ids), "error", err)
		return false
	}
	return true
}

// uniqueMedia drops uuid.Nil and duplicates, keeping order.
func uniqueMedia(ids []uuid.UUID) []uuid.UUID {
	added, _ := mediaDiff(nil, ids)
	return added
}

// releaseChannelClaims releases everything a deleted channel held: its
// picture and banner, and the media of every update it ever had. Best effort
// and bounded; whatever is left is the reconcile tool's.
func (s *Service) releaseChannelClaims(ctx context.Context, channelID uuid.UUID) {
	if s.claims == nil {
		slog.Warn("channel-service: deleted channel's media claims not released (no claim client); run the reconcile tool",
			"channel_id", channelID)
		return
	}
	if !s.release(ctx, claimKindAvatar, channelID, nil) || !s.release(ctx, claimKindBanner, channelID, nil) {
		return // media-service is not answering; one timeout, not one per update
	}
	ids, err := s.store.ListUpdateIDsWithMedia(context.WithoutCancel(ctx), channelID)
	if err != nil {
		slog.Warn("channel-service: deleted channel's update media claims not released; run the reconcile tool",
			"channel_id", channelID, "error", err)
		return
	}
	for i, id := range ids {
		if !s.release(ctx, claimKindUpdate, id, nil) {
			slog.Warn("channel-service: stopped releasing a deleted channel's update media; run the reconcile tool",
				"channel_id", channelID, "left", len(ids)-i)
			return
		}
	}
}

// lockMediaHolder takes the store's per-row media lock (store.LockMediaHolder).
// A Service without a store (unit tests of the pure protocol) has no rows to
// race over.
func (s *Service) lockMediaHolder(ctx context.Context, ref uuid.UUID) (func(), error) {
	if s.store == nil {
		return func() {}, nil
	}
	unlock, err := s.store.LockMediaHolder(ctx, ref)
	if err != nil {
		return nil, fmt.Errorf("failed to lock media holder: %w", err)
	}
	return unlock, nil
}
