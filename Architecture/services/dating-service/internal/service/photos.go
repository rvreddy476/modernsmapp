package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/atpost/dating-service/internal/store"
	"github.com/google/uuid"
)

// validVisibility gates the spec §10 enum.
func validVisibility(v string) bool {
	switch v {
	case "public", "match_only", "sparked_only":
		return true
	}
	return false
}

// ListPhotos returns the user's photos.
func (s *Service) ListPhotos(ctx context.Context, userID uuid.UUID) ([]store.Photo, error) {
	return s.store.ListPhotos(ctx, userID)
}

// ListMyPhotos returns the caller's photos filtered by moderation_status.
// Used by `GET /v1/dating/photos/me?status=rejected` so the owner can
// see the moderation reason for each photo — the §P1-2 "Why was my
// photo rejected?" transparency control.
//
// Empty status returns every photo (same as ListPhotos but ordered
// newest-first so the most recent moderation actions surface first).
// Other status values are passed through verbatim; an unknown value
// yields an empty list rather than an error so the UI can call the
// endpoint with any client-side filter without breaking.
func (s *Service) ListMyPhotos(ctx context.Context, userID uuid.UUID, status string) ([]store.Photo, error) {
	switch status {
	case "", store.PhotoStatusPending, store.PhotoStatusPendingReview, store.PhotoStatusApproved, store.PhotoStatusRejected:
	default:
		// Unknown status → empty list (defensive — clients can
		// evolve filter chips without coordinating).
		return []store.Photo{}, nil
	}
	return s.store.ListPhotosByStatus(ctx, userID, status)
}

// CreatePhoto attaches a photo (lane D6, photo_safety.go): media-service
// confirms the media is the caller's ready, moderation-passed image, the
// per-profile limit holds, media-service strips its metadata and renders the
// blurred variant, and the automated moderation decision is stored with the
// row. Every media-service failure refuses the attach.
func (s *Service) CreatePhoto(ctx context.Context, userID uuid.UUID, p store.CreatePhotoParams) (*store.Photo, error) {
	if p.MediaID == uuid.Nil {
		return nil, fmt.Errorf("invalid: media_id is required")
	}
	if p.Visibility != "" && !validVisibility(p.Visibility) {
		return nil, fmt.Errorf("invalid: visibility must be one of public|match_only|sparked_only")
	}
	if s.mediaPhotos == nil {
		return nil, fmt.Errorf("%w: no media photo client configured", ErrPhotoMediaUnavailable)
	}
	cfg := s.PhotoSafety()

	owned, err := s.mediaPhotos.PhotoOwnerStatus(ctx, p.MediaID, userID)
	if err != nil {
		return nil, err
	}
	if !owned.OwnerMatches {
		return nil, ErrPhotoMediaNotFound
	}
	if !owned.Usable() {
		return nil, ErrPhotoMediaNotReady
	}

	active, attached, err := s.store.PhotoSlotUsage(ctx, userID, p.MediaID)
	if err != nil {
		return nil, err
	}
	if attached {
		return nil, store.ErrPhotoAlreadyAttached
	}
	if active >= cfg.MaxPhotos {
		return nil, store.ErrPhotoLimitReached
	}

	prepared, err := s.mediaPhotos.PreparePhoto(ctx, p.MediaID, userID, p.IsPrimary && cfg.RequireFaceOnPrimary)
	if err != nil {
		return nil, err
	}
	decision := ClassifyPhoto(cfg, prepared, p.IsPrimary)
	photo, err := s.store.CreateModeratedPhoto(ctx, userID, p, cfg.MaxPhotos, decision)
	if err != nil {
		return nil, err
	}
	slog.Info("dating photo attached", "user_id", userID, "photo_id", photo.ID,
		"status", decision.Status, "reason", decision.Reason, "primary", photo.IsPrimary)

	s.syncPhotoState(ctx, userID)
	if decision.Status == store.PhotoStatusRejected {
		s.publishPhotoRejected(ctx, userID, photo.ID, decision.Reason)
	}
	return photo, nil
}

// UpdatePhoto applies the partial update. A primary or visibility change
// refreshes decks and the profile's photo step.
func (s *Service) UpdatePhoto(ctx context.Context, userID, photoID uuid.UUID, p store.UpdatePhotoParams) (*store.Photo, error) {
	if p.Visibility != nil && !validVisibility(*p.Visibility) {
		return nil, fmt.Errorf("invalid: visibility must be one of public|match_only|sparked_only")
	}
	photo, err := s.store.UpdatePhoto(ctx, userID, photoID, p)
	if err != nil {
		return nil, err
	}
	if p.IsPrimary != nil || p.Visibility != nil {
		s.syncPhotoState(ctx, userID)
	}
	return photo, nil
}

// DeletePhoto removes the photo and, unless another of the user's photos
// uses the same media, the media asset (rows, renditions, the blurred
// variant, objects) through media-service. media-service unavailable → the
// photo stays and the caller retries.
func (s *Service) DeletePhoto(ctx context.Context, userID, photoID uuid.UUID) error {
	photo, err := s.store.GetPhotoForUser(ctx, userID, photoID)
	if err != nil {
		return err
	}
	others, err := s.store.CountPhotosUsingMedia(ctx, userID, photo.MediaID, photo.ID)
	if err != nil {
		return err
	}
	if others == 0 {
		if s.mediaPhotos == nil {
			return fmt.Errorf("%w: no media photo client configured", ErrPhotoMediaUnavailable)
		}
		if err := s.mediaPhotos.DeletePhotoMedia(ctx, photo.MediaID, userID); err != nil {
			return err
		}
	}
	if err := s.store.DeletePhoto(ctx, userID, photoID); err != nil {
		return err
	}
	s.syncPhotoState(ctx, userID)
	return nil
}

// SetPhotoModerationStatus is the moderator entry point (admin-only route).
// It writes the decision with source admin, refreshes decks, moves the
// profile's photo step through the status writer (an approved primary
// advances pending_photo → pending_selfie; rejecting the approved primary of
// a later step revokes it to pending_photo), publishes
// dating.photo.moderation_rejected on rejection, and writes one
// dating_admin_audit row.
//
// adminID is the admin's gateway-derived user id (the HTTP layer's
// requireAdmin). uuid.Nil is refused before the photo changes. An audit
// insert failure is logged but does NOT roll back the moderation action.
// PHASE_0_TEST_PLANS.md §P0-8 acceptance test D.
func (s *Service) SetPhotoModerationStatus(ctx context.Context, adminID, photoID uuid.UUID, status, rejectReason string) (*store.Photo, error) {
	if adminID == uuid.Nil {
		return nil, errAdminActorRequired
	}
	photo, changed, err := s.store.SetPhotoModerationDecision(ctx, photoID, store.PhotoDecision{
		Status: status, Reason: rejectReason, Source: store.PhotoSourceAdmin,
	})
	if err != nil {
		return nil, err
	}

	s.syncPhotoState(ctx, photo.UserID)
	if status == store.PhotoStatusRejected && changed {
		s.publishPhotoRejected(ctx, photo.UserID, photoID, rejectReason)
	}

	entry := &store.AdminAuditEntry{
		ActorAdminID:   adminID,
		Action:         "photo_" + status,
		TargetUserID:   photo.UserID,
		TargetResource: "photo:" + photoID.String(),
		Reason:         rejectReason,
	}
	if err := s.store.InsertAdminAudit(ctx, entry); err != nil {
		// Audit failure must not roll back the moderation flip — see godoc.
		slog.Error("admin audit: insert failed for SetPhotoModerationStatus",
			"photo_id", photoID, "status", status,
			"user_id", photo.UserID, "actor_admin_id", adminID,
			"error", err)
	}

	return photo, nil
}

// RecheckPhotoMedia re-reads media-service for up to one batch of photos
// whose media was last confirmed before now - RecheckInterval. A photo whose
// media is gone, no longer passed, or not the owner's is rejected
// (MEDIA_UNAVAILABLE) — which revokes an active profile's photo step. A photo
// attached before lane D6 is prepared (stripped, blurred) and, unless a
// moderator decided it, classified. A moderator's decision otherwise stands.
// Idempotent: a result equal to the stored one writes only the cursor.
// Returns how many photos were checked; stops early when media-service is
// unavailable.
func (s *Service) RecheckPhotoMedia(ctx context.Context, now time.Time) (int, error) {
	if s.mediaPhotos == nil {
		return 0, fmt.Errorf("%w: no media photo client configured", ErrPhotoMediaUnavailable)
	}
	cfg := s.PhotoSafety()
	batch, err := s.store.ListPhotosForMediaRecheck(ctx, now.Add(-cfg.RecheckInterval), cfg.RecheckBatch)
	if err != nil {
		return 0, err
	}
	n := 0
	for i := range batch {
		if err := s.recheckPhoto(ctx, cfg, batch[i]); err != nil {
			if errors.Is(err, ErrPhotoMediaUnavailable) {
				return n, err
			}
			slog.Warn("dating photo recheck failed", "photo_id", batch[i].ID, "error", err)
		}
		n++
	}
	return n, nil
}

func (s *Service) recheckPhoto(ctx context.Context, cfg PhotoSafetyConfig, ph store.PhotoRecheck) error {
	st, err := s.mediaPhotos.PhotoOwnerStatus(ctx, ph.MediaID, ph.UserID)
	switch {
	case errors.Is(err, ErrPhotoMediaNotFound):
		st = &MediaPhotoStatus{}
	case err != nil:
		return err
	}
	if st.Usable() && !st.Prepared {
		prepared, perr := s.mediaPhotos.PreparePhoto(ctx, ph.MediaID, ph.UserID, false)
		switch {
		case perr == nil:
			st = prepared
		case errors.Is(perr, ErrPhotoMediaUnavailable):
			return perr
		default:
			st = &MediaPhotoStatus{}
		}
	}

	decision := ClassifyPhoto(cfg, st, ph.IsPrimary)
	// Rows from before lane D6 were approved by a moderator (D1 made
	// approval admin-only), so an approved row with no source is theirs too.
	moderatorDecided := ph.ModerationSource == store.PhotoSourceAdmin ||
		(ph.ModerationSource == "" && ph.ModerationStatus == store.PhotoStatusApproved)
	if moderatorDecided && decision.Reason != PhotoReasonMediaUnavailable {
		return s.store.TouchPhotoMediaCheck(ctx, ph.ID)
	}
	photo, changed, err := s.store.SetPhotoModerationDecision(ctx, ph.ID, decision)
	if err != nil {
		return err
	}
	if !changed {
		return s.store.TouchPhotoMediaCheck(ctx, ph.ID)
	}
	slog.Info("dating photo recheck changed moderation", "photo_id", ph.ID, "user_id", ph.UserID,
		"from", ph.ModerationStatus, "to", decision.Status, "reason", decision.Reason)
	s.syncPhotoState(ctx, photo.UserID)
	if decision.Status == store.PhotoStatusRejected {
		s.publishPhotoRejected(ctx, photo.UserID, photo.ID, decision.Reason)
	}
	return nil
}

// PhotoImageURL authorizes viewer for one variant of a photo and returns
// media-service's short-lived signed URL for it. The owner may fetch any of
// their photos. Anyone else: the photo must be approved, its owner visible to
// them (not deleted, suspended, blocked either way, or incognito toward
// them), and the variant allowed by PhotoVariantFor — asking for "full" when
// only "blurred" is allowed is a not-found, exactly like a missing photo.
func (s *Service) PhotoImageURL(ctx context.Context, viewerID, photoID uuid.UUID, variant string) (string, error) {
	if variant != PhotoVariantFull && variant != PhotoVariantBlurred {
		return "", store.ErrPhotoNotFound
	}
	photo, err := s.store.GetPhotoByID(ctx, photoID)
	if err != nil {
		return "", err
	}
	if viewerID != photo.UserID {
		if photo.ModerationStatus != store.PhotoStatusApproved {
			return "", store.ErrPhotoNotFound
		}
		facts, err := s.store.PhotoAudience(ctx, viewerID, photo.UserID)
		if err != nil {
			return "", err
		}
		if !facts.OwnerVisible {
			return "", store.ErrPhotoNotFound
		}
		allowed := PhotoVariantFor(photo.Visibility, PhotoViewer{
			Matched: facts.Matched, OwnerSparkedViewer: facts.OwnerSparkedViewer, OwnerBlursUntilMatch: facts.BlurUntilMatch,
		})
		if variant == PhotoVariantFull && allowed != PhotoVariantFull {
			return "", store.ErrPhotoNotFound
		}
	}
	if s.mediaPhotos == nil {
		return "", fmt.Errorf("%w: no media photo client configured", ErrPhotoMediaUnavailable)
	}
	u, err := s.mediaPhotos.PhotoDeliveryURL(ctx, photo.MediaID, photo.UserID, variant)
	if errors.Is(err, ErrPhotoMediaNotFound) || errors.Is(err, ErrPhotoMediaNotReady) {
		return "", store.ErrPhotoNotFound
	}
	return u, err
}

// syncPhotoState drops every cached deck that shows the user and moves the
// profile's photo step to what the photos now support: photo_revoked when no
// approved primary remains (a no-op otherwise), then the onboarding advance.
func (s *Service) syncPhotoState(ctx context.Context, userID uuid.UUID) {
	s.InvalidateDecksForCandidate(ctx, userID)
	_, err := s.store.TransitionProfileStatus(ctx, userID, store.ProfileEventPhotoRevoked, store.ProfileActorSystem)
	switch {
	case err == nil, errors.Is(err, store.ErrPhotoStillApproved), errors.Is(err, store.ErrProfileNotFound),
		errors.Is(err, store.ErrProfileTransitionNotAllowed):
	default:
		slog.Warn("dating photo: revoke photo step failed", "user_id", userID, "error", err)
	}
	if _, err := s.advanceOnboarding(ctx, userID); err != nil && !errors.Is(err, store.ErrProfileNotFound) {
		slog.Warn("dating photo: advance onboarding failed", "user_id", userID, "error", err)
	}
}

func (s *Service) publishPhotoRejected(ctx context.Context, userID, photoID uuid.UUID, reason string) {
	if s.producer == nil {
		return
	}
	if err := s.producer.PublishPhotoModerationRejected(ctx, userID, photoID.String(), reason); err != nil {
		slog.Warn("photo moderation: publish rejected event failed",
			"user_id", userID, "photo_id", photoID, "error", err)
	}
}
