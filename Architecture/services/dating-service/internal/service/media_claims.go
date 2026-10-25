package service

// Media claims — keeping media-service's owner delete from removing an asset a
// dating row still names (25 Oct 2026).
//
// # The hole
//
// dating_photos.media_id, dating_prompts.clip_media_id,
// dating_selfie_attempts.video_media_id and dating_verifications.selfie_media_id
// live in this service's database with no foreign key. media-service's
// DELETE /v1/media/{id} checks only the uploader, so a user could delete the
// blink video their verification verdict rests on, or a photo a moderator is
// reviewing, and leave the row pointing at nothing.
//
// # The protocol (media-service internal/store/postgres/media_claims.go)
//
//  1. CLAIM every id a write is about to add, before the write; media-service
//     locks the asset while it records the claim, so an owner delete racing
//     the attach either lands first (the claim is refused and nothing is
//     written) or waits and is refused.
//  2. WRITE the row. If the write fails, RELEASE exactly what step 1 claimed.
//  3. After a successful write, RELEASE what the row no longer names.
//
// Each claim-write-release of one holder runs under the store's per-holder
// lock (store.LockMediaHolder) when another writer of the same holder could
// interleave: the prompt clip slot and the verification video.
//
// # Deleting dating media
//
// dating-service deletes its own media through media-service's referrer
// purge (DeletePhotoMedia, DeleteClip), and that purge refuses while ANY
// claim on the asset exists. So the claim is released BEFORE the purge is
// asked, and taken again if the purge could not be reached; the row names an
// unclaimed asset only while its own owner is deleting it.
//
// # Account purges
//
// PurgeProfile and PurgeUserAuxiliary release first and erase second: once
// the rows are gone nothing names which holders had claims, so a release that
// failed after the erase could never be retried. A release failure fails the
// purge, which is retried (the platform purge is redelivered; the DPDP cron
// sees the profile again).
//
// A failure anywhere else leaves an EXTRA claim, never a missing one; the
// reconcile tool (cmd/media-claims) repairs both directions. Fail closed: with
// no claim client, a write that ADDS media is refused as media-service
// unavailable. A write that only removes media is never refused for it.

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/atpost/dating-service/internal/mediaclient"
	"github.com/atpost/dating-service/internal/store"
	"github.com/google/uuid"
)

// MediaClaims is media-service's claim API (mediaclient.Client).
type MediaClaims interface {
	Claim(ctx context.Context, kind, ref string, mediaIDs []uuid.UUID) error
	Release(ctx context.Context, kind, ref string, mediaIDs []uuid.UUID) error
}

// SetMediaClaims installs the claim client. Nil refuses every write that adds
// media (fail closed).
func (s *Service) SetMediaClaims(c MediaClaims) { s.claims = c }

// errNoClaimClient is wrapped into each path's own "unavailable" error.
var errNoClaimClient = errors.New("no media-service claim client configured")

// claim records that (kind, ref) holds ids. notFound and unavailable are the
// calling path's own errors, so each surface keeps its codes.
func (s *Service) claim(ctx context.Context, kind, ref string, ids []uuid.UUID, notFound, unavailable error) error {
	if len(ids) == 0 {
		return nil
	}
	if s.claims == nil {
		return fmt.Errorf("%w: %v", unavailable, errNoClaimClient)
	}
	switch err := s.claims.Claim(ctx, kind, ref, ids); {
	case errors.Is(err, mediaclient.ErrNotFound):
		return notFound
	case err != nil:
		return fmt.Errorf("%w: media claim: %v", unavailable, err)
	}
	return nil
}

// releaseTimeout bounds one release. Releases run on a context detached from
// the request: a client hanging up after its write committed must not leave
// the compensation undone.
const releaseTimeout = 5 * time.Second

// release removes (kind, ref)'s claims on ids (all of them when ids is
// empty). It reports whether media-service confirmed it; a caller that does
// not check leaves a stale claim for the reconcile tool, never a dangling
// reference.
func (s *Service) release(ctx context.Context, kind, ref string, ids []uuid.UUID) bool {
	if s.claims == nil {
		slog.Warn("dating: media claim not released (no claim client); run /media-claims", "kind", kind, "ref", ref)
		return false
	}
	rctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), releaseTimeout)
	defer cancel()
	if err := s.claims.Release(rctx, kind, ref, ids); err != nil {
		slog.Warn("dating: media claim release failed; the owner cannot delete it until /media-claims runs",
			"kind", kind, "ref", ref, "media", len(ids), "error", err)
		return false
	}
	return true
}

// mediaChange is what a write does to one single-asset holder: the id it
// sets (when new) and the id it stops naming (when any).
func mediaChange(prev, next *uuid.UUID) (add, drop []uuid.UUID) {
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

// lockMediaHolder takes the store's per-holder lock. A Service without a
// store (unit tests of the pure protocol) has nothing to race over.
func (s *Service) lockMediaHolder(ctx context.Context, kind, ref string) (func(), error) {
	if s.store == nil {
		return func() {}, nil
	}
	unlock, err := s.store.LockMediaHolder(ctx, kind, ref)
	if err != nil {
		return nil, fmt.Errorf("lock media holder: %w", err)
	}
	return unlock, nil
}

// releaseHolders releases every claim of each holder, stopping at the first
// failure. With no claim client nothing was claimed by this process; the
// account erasure in media-service clears claims on the user's own assets.
func (s *Service) releaseHolders(ctx context.Context, userID uuid.UUID, holders []store.MediaHolder) error {
	if len(holders) == 0 {
		return nil
	}
	if s.claims == nil {
		slog.Warn("dating: purge did not release media claims (no claim client); run /media-claims",
			"user_id", userID, "holders", len(holders))
		return nil
	}
	for _, h := range holders {
		if !s.release(ctx, h.Kind, h.Ref, nil) {
			return fmt.Errorf("purge: release %s claim %s: media-service unavailable; retry", h.Kind, h.Ref)
		}
	}
	return nil
}

// releaseProfileClaims releases what PurgeUserData is about to erase.
func (s *Service) releaseProfileClaims(ctx context.Context, userID uuid.UUID) error {
	holders, err := s.store.ProfileMediaHolders(ctx, userID)
	if err != nil {
		return err
	}
	return s.releaseHolders(ctx, userID, holders)
}

// PurgeUserAuxiliary releases the user's selfie attempt claims, then runs the
// store's auxiliary erase (purge.AuxStore).
func (s *Service) PurgeUserAuxiliary(ctx context.Context, userID uuid.UUID) error {
	holders, err := s.store.SelfieAttemptHolders(ctx, userID)
	if err != nil {
		return err
	}
	if err := s.releaseHolders(ctx, userID, holders); err != nil {
		return err
	}
	return s.store.PurgeUserAuxiliary(ctx, userID)
}

// releaseBeforeDetach releases every claim (kind, ref) holds before the
// holder stops naming its media: media-service's referrer purge refuses
// while a claim exists, and once the row is gone nothing would name a claim
// left behind. A release media-service did not confirm refuses the removal
// (the caller retries). With no claim client nothing was claimed here.
func (s *Service) releaseBeforeDetach(ctx context.Context, kind, ref string, unavailable error) error {
	if s.claims == nil {
		return nil
	}
	if !s.release(ctx, kind, ref, nil) {
		return fmt.Errorf("%w: media claim release failed", unavailable)
	}
	return nil
}

// reclaim takes a released claim back when the removal it was released for
// did not happen. Best effort: a failure leaves the row naming an unclaimed
// asset of the user who was deleting it, until the reconcile tool runs.
func (s *Service) reclaim(ctx context.Context, kind, ref string, id uuid.UUID) {
	if s.claims == nil || id == uuid.Nil {
		return
	}
	rctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), releaseTimeout)
	defer cancel()
	if err := s.claims.Claim(rctx, kind, ref, []uuid.UUID{id}); err != nil {
		slog.Warn("dating: media claim not restored after a failed removal; run /media-claims",
			"kind", kind, "ref", ref, "error", err)
	}
}
