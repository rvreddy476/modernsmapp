package service

import (
	"context"
	"fmt"
	"log/slog"

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

// CreatePhoto validates input and inserts the photo.
func (s *Service) CreatePhoto(ctx context.Context, userID uuid.UUID, p store.CreatePhotoParams) (*store.Photo, error) {
	if p.MediaID == uuid.Nil {
		return nil, fmt.Errorf("invalid: media_id is required")
	}
	if p.Visibility != "" && !validVisibility(p.Visibility) {
		return nil, fmt.Errorf("invalid: visibility must be one of public|match_only|sparked_only")
	}
	return s.store.CreatePhoto(ctx, userID, p)
}

// UpdatePhoto applies the partial update.
func (s *Service) UpdatePhoto(ctx context.Context, userID, photoID uuid.UUID, p store.UpdatePhotoParams) (*store.Photo, error) {
	if p.Visibility != nil && !validVisibility(*p.Visibility) {
		return nil, fmt.Errorf("invalid: visibility must be one of public|match_only|sparked_only")
	}
	return s.store.UpdatePhoto(ctx, userID, photoID, p)
}

// DeletePhoto removes the photo.
func (s *Service) DeletePhoto(ctx context.Context, userID, photoID uuid.UUID) error {
	return s.store.DeletePhoto(ctx, userID, photoID)
}

// SetPhotoModerationStatus is the admin / content-scanner entry point
// for flipping a photo's moderation_status. Wires three side-effects:
//
//  1. Updates the dating_photos row.
//  2. Drops every viewer's cached deck that contains the owner (via
//     InvalidateDecksForCandidate) so the candidate query's
//     approved-only filter takes effect on the next pulse refresh.
//  3. Graduates the owner's profile_status from pending_photo to
//     pending_selfie if this is their first approved primary photo.
//
// On rejection, publishes dating.photo.moderation_rejected so the
// notification consumer can push a user-facing notice.
//
// adminID is the X-Admin-Id header value the gateway injects on
// admin-scope traffic. uuid.Nil is accepted with a slog.Warn so the
// action never bounces because of a missing header. Every call
// writes one row to dating_admin_audit after the photo flip lands;
// an audit insert failure is logged but does NOT roll back the
// moderation action. PHASE_0_TEST_PLANS.md §P0-8 acceptance test D.
//
// P0-6 (approved-only discovery — wire the invalidation) + §P1-1
// (profile lifecycle — wire pending_photo → pending_selfie) +
// §P0-8 (audit trail) in dating/PRODUCTION_GAP_ANALYSIS.md.
func (s *Service) SetPhotoModerationStatus(ctx context.Context, adminID, photoID uuid.UUID, status, rejectReason string) (*store.Photo, error) {
	photo, err := s.store.SetPhotoModerationStatus(ctx, photoID, status)
	if err != nil {
		return nil, err
	}

	// Fan out deck invalidation regardless of approve/reject — both
	// flip whether this user is discoverable.
	s.InvalidateDecksForCandidate(ctx, photo.UserID)

	switch status {
	case "approved":
		// Profile state transition: pending_photo → pending_selfie when
		// the user has at least one approved primary photo and is
		// still in pending_photo. Idempotent — re-running on a profile
		// already past pending_photo is a no-op.
		prof, perr := s.store.GetProfile(ctx, photo.UserID)
		if perr == nil && prof != nil && prof.ProfileStatus == store.ProfileStatusPendingPhoto {
			if photo.IsPrimary && photo.ModerationStatus == "approved" {
				if _, err := s.store.SetProfileStatus(ctx, photo.UserID, store.ProfileStatusPendingSelfie); err != nil {
					slog.Warn("photo moderation: graduate to pending_selfie failed",
						"user_id", photo.UserID, "photo_id", photoID, "error", err)
				}
			}
		}
	case "rejected":
		if s.producer != nil {
			if err := s.producer.PublishPhotoModerationRejected(ctx, photo.UserID, photoID.String(), rejectReason); err != nil {
				slog.Warn("photo moderation: publish rejected event failed",
					"user_id", photo.UserID, "photo_id", photoID, "error", err)
			}
		}
	}

	if adminID == uuid.Nil {
		slog.Warn("admin audit: actor id missing on SetPhotoModerationStatus",
			"photo_id", photoID, "status", status, "user_id", photo.UserID)
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
