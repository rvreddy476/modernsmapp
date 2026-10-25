package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/atpost/identity-profile-service/internal/mediaclient"
	"github.com/google/uuid"
)

// Media claims — keeping media-service's owner delete from leaving a profile
// pointing at nothing.
//
// # The hole
//
// profile.profiles.avatar_media_id and cover_media_id live in this service's
// database. PUT /me/avatar and /me/cover ask media-service whether the asset
// is the user's own ready, approved image before it is attached
// (internal/http/media_authority.go), but nothing stopped the user deleting
// it afterwards (DELETE /v1/media/{id}): media-service could not see the
// profile row, and the profile was left naming an asset that no longer
// exists.
//
// # The protocol (media-service internal/store/postgres/media_claims.go)
//
// Under the profile's media lock (store.LockMediaHolder), for one kind:
//
//  1. READ the id the profile holds now.
//  2. CLAIM the new id, before the write. media-service locks the asset row
//     while it records the claim, so an owner delete racing the attach either
//     lands first (the claim is refused, ErrMediaNotFound, and nothing is
//     written) or waits and is refused.
//  3. WRITE the row. If the write fails, or there is no profile row, RELEASE
//     what step 2 claimed (compensation).
//  4. RELEASE the id the profile held before.
//
// A failure in steps 3–4 leaves an EXTRA claim, never a missing one: the
// owner is refused a delete they should be allowed, which cmd/media-claims
// (the reconcile tool) repairs. The opposite error — a reference with no
// claim — is the defect this exists to close: nothing is written before its
// claim succeeds, and nothing still referenced is ever released.
//
// Fail closed: with no claim client, or media-service unreachable, a write
// that sets an avatar or cover is refused with ErrMediaUnavailable (503).
// The ref media-service records is the user id; the claimant and kinds are
// mediaclient.Claimant / KindProfileAvatar / KindProfileCover.

var (
	// ErrProfileNotFound: there is no profile row to attach the image to.
	ErrProfileNotFound = errors.New("profile not found")
	// ErrMediaNotFound: media-service refused the claim because the asset no
	// longer exists (deleted after it was checked). Nothing was written.
	ErrMediaNotFound = errors.New("media: the image no longer exists")
	// ErrMediaUnavailable: the claim could not be recorded (no client, or
	// media-service did not answer). Nothing was written.
	ErrMediaUnavailable = errors.New("media: media-service could not record the profile's use of the image")
	// ErrMediaRouteRequired: the general profile update was asked to write an
	// avatar or cover. Only the dedicated routes check and claim media.
	ErrMediaRouteRequired = errors.New("avatar and cover are set through their own routes")
)

// MediaClaims is media-service's claim API (mediaclient.Client).
type MediaClaims interface {
	Claim(ctx context.Context, kind string, ref uuid.UUID, mediaIDs []uuid.UUID) error
	Release(ctx context.Context, kind string, ref uuid.UUID, mediaIDs []uuid.UUID) error
}

// ProfileMediaStore is what an avatar or cover write needs from storage
// (*store.Store).
type ProfileMediaStore interface {
	LockMediaHolder(ctx context.Context, userID uuid.UUID) (func(), error)
	GetProfileMediaID(ctx context.Context, userID uuid.UUID, kind string) (*uuid.UUID, bool, error)
	SetProfileMediaID(ctx context.Context, userID uuid.UUID, kind string, mediaID uuid.UUID) (bool, error)
}

// WithMediaClaims installs the claim client. Without one every avatar and
// cover write is refused (fail closed).
func (s *Service) WithMediaClaims(c MediaClaims) *Service {
	s.claims = c
	return s
}

// WithProfileMediaStore replaces the storage behind UpdateAvatar and
// UpdateCover. For tests.
func (s *Service) WithProfileMediaStore(m ProfileMediaStore) *Service {
	s.media = m
	return s
}

// releaseTimeout bounds one best-effort release. Releases run on a context
// detached from the request: a client hanging up after its write committed
// must not leave the compensation undone.
const releaseTimeout = 5 * time.Second

// UpdateAvatar sets the profile picture under the claim protocol.
func (s *Service) UpdateAvatar(ctx context.Context, userID uuid.UUID, mediaID uuid.UUID) error {
	return s.setProfileMedia(ctx, userID, mediaclient.KindProfileAvatar, mediaID)
}

// UpdateCover sets the cover image under the claim protocol.
func (s *Service) UpdateCover(ctx context.Context, userID uuid.UUID, mediaID uuid.UUID) error {
	return s.setProfileMedia(ctx, userID, mediaclient.KindProfileCover, mediaID)
}

func (s *Service) setProfileMedia(ctx context.Context, userID uuid.UUID, kind string, mediaID uuid.UUID) error {
	if mediaID == uuid.Nil {
		return ErrMediaNotFound
	}
	if s.media == nil {
		return errors.New("profile media store not configured")
	}
	unlock, err := s.media.LockMediaHolder(ctx, userID)
	if err != nil {
		return err
	}
	defer unlock()

	// 1. What the profile holds now.
	prev, found, err := s.media.GetProfileMediaID(ctx, userID, kind)
	if err != nil {
		return fmt.Errorf("read profile media: %w", err)
	}
	if !found {
		return ErrProfileNotFound
	}
	if prev != nil && *prev == mediaID {
		return nil // already holds it: no new reference, nothing to claim or release
	}

	// 2. Claim before the row names it.
	if s.claims == nil {
		return fmt.Errorf("%w: no media-service claim client configured", ErrMediaUnavailable)
	}
	if err := s.claims.Claim(ctx, kind, userID, []uuid.UUID{mediaID}); err != nil {
		if errors.Is(err, mediaclient.ErrNotFound) {
			return ErrMediaNotFound
		}
		return fmt.Errorf("%w: claim: %v", ErrMediaUnavailable, err)
	}

	// 3. Write; compensate when nothing was written.
	written, err := s.media.SetProfileMediaID(ctx, userID, kind, mediaID)
	if err != nil || !written {
		s.release(ctx, kind, userID, []uuid.UUID{mediaID})
		if err != nil {
			return fmt.Errorf("write profile media: %w", err)
		}
		return ErrProfileNotFound
	}
	s.invalidateProfileCard(ctx, userID)

	// 4. The image it replaced is no longer held here.
	if prev != nil {
		s.release(ctx, kind, userID, []uuid.UUID{*prev})
	}
	return nil
}

func (s *Service) invalidateProfileCard(ctx context.Context, userID uuid.UUID) {
	if s.rdb != nil {
		s.rdb.Del(ctx, fmt.Sprintf("profile:card:%s", userID))
	}
}

// release is best effort: a failure leaves a stale claim (the owner's delete
// is refused until the reconcile tool clears it), never a dangling reference.
// ids nil releases everything ref holds of kind.
func (s *Service) release(ctx context.Context, kind string, ref uuid.UUID, ids []uuid.UUID) bool {
	if s.claims == nil {
		s.logger().Warn("profile-service: media claim not released (no claim client); run the reconcile tool",
			"kind", kind, "ref", ref)
		return false
	}
	rctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), releaseTimeout)
	defer cancel()
	if err := s.claims.Release(rctx, kind, ref, ids); err != nil {
		s.logger().Warn("profile-service: media claim release failed; the owner cannot delete it until the reconcile tool runs",
			"kind", kind, "ref", ref, "media", len(ids), "error", err)
		return false
	}
	return true
}

func (s *Service) logger() *slog.Logger {
	if s.log != nil {
		return s.log
	}
	return slog.Default()
}

// Eraser is the account purge (store.Store.PurgeUser; purge.Eraser).
type Eraser interface {
	PurgeUser(ctx context.Context, userID uuid.UUID) error
}

// ReleasingMediaOnPurge wraps the account purge so that, once the profile is
// erased, its avatar and cover claims are released. Best effort: a release
// that fails does not fail the purge (it is acked and never redelivered for
// this reason), and media-service's own account erasure clears every claim
// on the user's assets anyway.
func (s *Service) ReleasingMediaOnPurge(e Eraser) Eraser {
	return purgeReleasingMedia{eraser: e, svc: s}
}

type purgeReleasingMedia struct {
	eraser Eraser
	svc    *Service
}

func (p purgeReleasingMedia) PurgeUser(ctx context.Context, userID uuid.UUID) error {
	if err := p.eraser.PurgeUser(ctx, userID); err != nil {
		return err
	}
	if p.svc.release(ctx, mediaclient.KindProfileAvatar, userID, nil) {
		p.svc.release(ctx, mediaclient.KindProfileCover, userID, nil)
	}
	return nil
}
