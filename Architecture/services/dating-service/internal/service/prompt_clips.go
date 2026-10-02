// Prompt clips (mechanic M15, DATING_MEDIA_PROMPTS_ENABLED).
//
//	PUT    /v1/dating/prompts/:promptId/clip {media_id}   attach a voice/video clip
//	DELETE /v1/dating/prompts/:promptId/clip
//	GET    /v1/dating/people/:userId/prompts/:promptId/clip  307 to a short-lived URL
//	GET    /v1/dating/admin/clips/pending, POST /v1/dating/admin/clips/moderation
//
// The client uploads through media-service as for photos, then attaches the
// media id. media-service checks it is the caller's, at most 30 s, and moves
// it into the private dating_clip scope; its moderation verdict decides the
// clip's state (store/prompt_clips.go). Nobody but the owner ever sees a
// clip that is not approved. A video clip shows a face, so it follows the
// owner's "blur until match" choice the way a full photo does; a voice clip
// follows the profile's visibility only.
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

const (
	// clipRecheckAfter is how long a pending clip waits between asks.
	clipRecheckAfter = time.Minute
	clipKindAudio    = "audio"
	clipKindVideo    = "video"
)

// ErrInvalidClipDecision maps to 400 INVALID_CLIP_DECISION.
var ErrInvalidClipDecision = errors.New("invalid: decision must be approved or rejected")

// SetMediaClipClient wires media-service for the mechanic.
func (s *Service) SetMediaClipClient(c MediaClipClient) { s.mediaClips = c }

// PromptClip is the clip on a card's prompt answer: only approved clips,
// and only a route, never a media id.
type PromptClip struct {
	Kind       string `json:"kind"`
	DurationMs int    `json:"duration_ms"`
	URL        string `json:"url"`
}

// PromptClipPath is the clip's route for viewers.
func PromptClipPath(ownerID uuid.UUID, promptID int) string {
	return fmt.Sprintf("/v1/dating/people/%s/prompts/%d/clip", ownerID, promptID)
}

// PromptClipView is the owner's view of their clip.
type PromptClipView struct {
	PromptID   int     `json:"prompt_id"`
	Kind       string  `json:"kind"`
	DurationMs int     `json:"duration_ms"`
	Status     string  `json:"status"`
	Reason     *string `json:"reason,omitempty"`
}

// clipState maps media-service's verdict to the clip's state.
func clipState(moderation string) (string, *string) {
	switch moderation {
	case "passed":
		return store.ClipStatusApproved, nil
	case "review":
		return store.ClipStatusPendingReview, nil
	case "rejected":
		r := "This clip can't be shown on your profile."
		return store.ClipStatusRejected, &r
	}
	return store.ClipStatusPending, nil
}

func (s *Service) clipsReady() error {
	if !s.mechanics.MediaPrompts {
		return ErrMechanicDisabled
	}
	if s.mediaClips == nil {
		return fmt.Errorf("%w: no media clip client configured", ErrClipMediaUnavailable)
	}
	return nil
}

// PutPromptClip attaches the caller's uploaded clip to one of their prompts.
func (s *Service) PutPromptClip(ctx context.Context, userID uuid.UUID, promptID int, mediaID uuid.UUID) (*PromptClipView, error) {
	if err := s.clipsReady(); err != nil {
		return nil, err
	}
	if !validPromptID(promptID) {
		return nil, ErrUnknownPrompt
	}
	if mediaID == uuid.Nil {
		return nil, ErrClipMediaNotFound
	}
	st, err := s.mediaClips.ClipOwnerStatus(ctx, mediaID, userID)
	if err != nil {
		return nil, err
	}
	if st.OwnerUserID != userID {
		return nil, ErrClipMediaNotFound
	}
	if st.Kind != clipKindAudio && st.Kind != clipKindVideo {
		return nil, ErrClipUnsupported
	}
	if st.Processing == "processing" {
		return nil, ErrClipNotReady
	}
	kind, durationMs, err := s.mediaClips.PrepareClip(ctx, mediaID, userID)
	if err != nil {
		return nil, err
	}
	status, reason := clipState(st.Moderation)
	previous, err := s.store.SetPromptClip(ctx, userID, promptID, mediaID, kind, durationMs, status, reason)
	if err != nil {
		return nil, err
	}
	if previous != nil {
		if err := s.mediaClips.DeleteClip(ctx, *previous, userID); err != nil {
			slog.Warn("prompt clip: replaced clip not deleted", "user_id", userID, "error", err)
		}
	}
	s.InvalidateDecksForCandidate(ctx, userID)
	return &PromptClipView{PromptID: promptID, Kind: kind, DurationMs: durationMs, Status: status, Reason: reason}, nil
}

// DeletePromptClip removes the clip from one of the caller's prompts.
func (s *Service) DeletePromptClip(ctx context.Context, userID uuid.UUID, promptID int) error {
	if err := s.clipsReady(); err != nil {
		return err
	}
	media, err := s.store.ClearPromptClip(ctx, userID, promptID)
	if err != nil {
		return err
	}
	if media != nil {
		if err := s.mediaClips.DeleteClip(ctx, *media, userID); err != nil {
			slog.Warn("prompt clip: removed clip not deleted", "user_id", userID, "error", err)
		}
	}
	s.InvalidateDecksForCandidate(ctx, userID)
	return nil
}

// PromptClipURL authorizes viewerID for ownerID's clip on promptID and
// returns media-service's short-lived URL. Every refusal is
// store.ErrPromptNotFound, so a viewer learns nothing about why.
func (s *Service) PromptClipURL(ctx context.Context, viewerID, ownerID uuid.UUID, promptID int) (string, error) {
	if err := s.clipsReady(); err != nil {
		return "", err
	}
	p, err := s.store.GetPrompt(ctx, ownerID, promptID)
	if err != nil {
		return "", store.ErrPromptNotFound
	}
	if p.ClipMediaID == nil || p.ClipStatus == nil || *p.ClipStatus != store.ClipStatusApproved {
		return "", store.ErrPromptNotFound
	}
	if viewerID != ownerID {
		facts, err := s.store.PhotoAudience(ctx, viewerID, ownerID)
		if err != nil {
			return "", err
		}
		if !facts.OwnerVisible {
			return "", store.ErrPromptNotFound
		}
		if p.ClipKind != nil && *p.ClipKind == clipKindVideo && facts.BlurUntilMatch && !facts.Matched {
			return "", store.ErrPromptNotFound
		}
	}
	d, err := s.mediaClips.ClipDeliveryURL(ctx, *p.ClipMediaID, ownerID)
	if errors.Is(err, ErrClipMediaNotFound) {
		return "", store.ErrPromptNotFound
	}
	if err != nil {
		return "", err
	}
	return d.URL, nil
}

// RecheckPromptClips asks media-service again about clips it had not
// decided yet (the sweeper).
func (s *Service) RecheckPromptClips(ctx context.Context, limit int) (int, error) {
	if !s.mechanics.MediaPrompts || s.mediaClips == nil {
		return 0, nil
	}
	pending, err := s.store.ListPromptClipsByStatus(ctx, store.ClipStatusPending, clipRecheckAfter, limit)
	if err != nil {
		return 0, err
	}
	changed := 0
	for _, p := range pending {
		st, err := s.mediaClips.ClipOwnerStatus(ctx, *p.ClipMediaID, p.UserID)
		if err != nil {
			if errors.Is(err, ErrClipMediaNotFound) {
				r := "This clip is no longer available."
				_ = s.store.SetPromptClipStatus(ctx, p.UserID, p.PromptID, *p.ClipMediaID, store.ClipStatusRejected, &r, "auto")
				changed++
			} else {
				_ = s.store.TouchPromptClip(ctx, p.UserID, p.PromptID)
			}
			continue
		}
		status, reason := clipState(st.Moderation)
		if status == store.ClipStatusPending {
			_ = s.store.TouchPromptClip(ctx, p.UserID, p.PromptID)
			continue
		}
		if err := s.store.SetPromptClipStatus(ctx, p.UserID, p.PromptID, *p.ClipMediaID, status, reason, "auto"); err == nil {
			changed++
			s.InvalidateDecksForCandidate(ctx, p.UserID)
		}
	}
	return changed, nil
}

// PendingClipReview is one clip waiting for a moderator.
type PendingClipReview struct {
	UserID      uuid.UUID `json:"user_id"`
	PromptID    int       `json:"prompt_id"`
	Question    string    `json:"question"`
	Kind        string    `json:"kind"`
	DurationMs  int       `json:"duration_ms"`
	SubmittedAt time.Time `json:"submitted_at"`
}

// ListPendingClipReviews is the moderators' queue.
func (s *Service) ListPendingClipReviews(ctx context.Context, limit int) ([]PendingClipReview, error) {
	if !s.mechanics.MediaPrompts {
		return nil, ErrMechanicDisabled
	}
	rows, err := s.store.ListPromptClipsByStatus(ctx, store.ClipStatusPendingReview, 0, limit)
	if err != nil {
		return nil, err
	}
	out := make([]PendingClipReview, 0, len(rows))
	for _, p := range rows {
		q, _ := promptQuestion(p.PromptID)
		item := PendingClipReview{UserID: p.UserID, PromptID: p.PromptID, Question: q, SubmittedAt: p.UpdatedAt.UTC()}
		if p.ClipKind != nil {
			item.Kind = *p.ClipKind
		}
		if p.ClipDurationMs != nil {
			item.DurationMs = *p.ClipDurationMs
		}
		out = append(out, item)
	}
	return out, nil
}

// ReviewPromptClip records a moderator's decision, audited.
func (s *Service) ReviewPromptClip(ctx context.Context, adminID, ownerID uuid.UUID, promptID int, decision string, reason *string) error {
	if !s.mechanics.MediaPrompts {
		return ErrMechanicDisabled
	}
	if adminID == uuid.Nil {
		return errAdminActorRequired
	}
	if decision != store.ClipStatusApproved && decision != store.ClipStatusRejected {
		return ErrInvalidClipDecision
	}
	p, err := s.store.GetPrompt(ctx, ownerID, promptID)
	if err != nil {
		return err
	}
	if p.ClipMediaID == nil {
		return store.ErrPromptNotFound
	}
	if decision == store.ClipStatusApproved {
		reason = nil
	}
	if err := s.store.SetPromptClipStatus(ctx, ownerID, promptID, *p.ClipMediaID, decision, reason, "admin"); err != nil {
		return err
	}
	s.InvalidateDecksForCandidate(ctx, ownerID)
	if err := s.store.InsertAdminAudit(ctx, &store.AdminAuditEntry{
		ActorAdminID: adminID, Action: "prompt_clip_" + decision, TargetUserID: ownerID,
		TargetResource: fmt.Sprintf("prompt_clip:%d", promptID),
	}); err != nil {
		slog.Error("admin audit: insert failed for ReviewPromptClip", "owner", ownerID, "prompt_id", promptID, "error", err)
	}
	return nil
}
