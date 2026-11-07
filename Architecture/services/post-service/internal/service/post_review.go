// post_review.go is the moderation side of a post: auto-resolving flagged
// posts, the moderation subject and verdict, resubmission, promoting staged
// posts, queueing for review, and the review status a new voice or video
// post starts in. Moved out of post.go unchanged.

package service

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/atpost/post-service/internal/store/postgres"
	"github.com/google/uuid"
)

// AutoResolveFlagged sets a FLAGGED post to a terminal review_status
// (approved|rejected). Used by reviewer-service's ML pre-filter to clear
// content without a human. Scoped to flagged rows so it can't override a
// human/pending verdict. Busts the cached post body on success.
// The actor is written to post_review_audit with the change.
func (s *Service) AutoResolveFlagged(ctx context.Context, postID uuid.UUID, status string, actor postgres.ReviewAuditActor) (bool, error) {
	ok, err := s.pgStore.SetReviewStatusFromFlagged(ctx, postID, status, actor)
	if err == nil && ok && s.rdb != nil {
		_ = s.rdb.Del(ctx, "post:body:"+postID.String()).Err()
	}
	return ok, err
}

func (s *Service) GetModerationSubject(ctx context.Context, postID uuid.UUID) (*postgres.ModerationSubject, error) {
	return s.pgStore.GetModerationSubject(ctx, postID)
}

func (s *Service) ModeratePost(ctx context.Context, in postgres.ModeratePostInput) (*postgres.ModerationDecision, error) {
	decision, err := s.pgStore.ModeratePost(ctx, in)
	if err == nil && decision.Changed && s.rdb != nil {
		_ = s.rdb.Del(ctx, "post:body:"+in.PostID.String()).Err()
	}
	return decision, err
}

// Resubmit lets the creator send an edited post (in 'needs_changes' after a
// super-admin requested edits) back into human review. Owner-gated; re-enqueues
// to reviewer-service so the loop continues.
func (s *Service) Resubmit(ctx context.Context, postID, actorID uuid.UUID) (bool, error) {
	owner, err := s.IsPostAuthor(ctx, postID, actorID)
	if err != nil {
		return false, err
	}
	if !owner {
		return false, fmt.Errorf("forbidden: not the author")
	}
	changed, err := s.pgStore.ResubmitFromNeedsChanges(ctx, postID)
	if err != nil || !changed {
		return false, err
	}
	if s.rdb != nil {
		_ = s.rdb.Del(ctx, "post:body:"+postID.String()).Err()
	}
	// Re-enqueue for human review (fresh content → let the pre-filter/human decide).
	if posts, err := s.pgStore.GetPostsByIDs(ctx, []uuid.UUID{postID}); err == nil && len(posts) > 0 {
		s.enqueueForReview(&posts[0], 0)
	}
	return true, nil
}

// PromoteStaged finalizes a test-audience rollout by moving a STAGED post to a
// new visibility (typically 'public'). Used by the reviewer promotion worker.
// The actor is written to post_review_audit with the change.
func (s *Service) PromoteStaged(ctx context.Context, postID uuid.UUID, visibility string, actor postgres.ReviewAuditActor) (bool, error) {
	ok, err := s.pgStore.SetVisibilityFromStaged(ctx, postID, visibility, actor)
	if err == nil && ok && s.rdb != nil {
		_ = s.rdb.Del(ctx, "post:body:"+postID.String()).Err()
	}
	return ok, err
}

// enqueueForReview best-effort notifies reviewer-service that a piece of video
// content needs human review (review_status='flagged'). Fire-and-forget: never
// blocks or fails the post-create/transcode path. No-op when the URL is unset
// or the content isn't video.
func (s *Service) enqueueForReview(p *postgres.Post, spamScore float64) {
	if s.reviewerServiceURL == "" || !isVideoContentType(p.ContentType) {
		return
	}
	langs := []string{}
	if p.Language != "" {
		langs = []string{p.Language}
	}
	body, _ := json.Marshal(map[string]any{
		"content_id":      p.ID.String(),
		"creator_id":      p.AuthorID.String(),
		"content_type":    p.ContentType,
		"languages":       langs,
		"content_seconds": 0, // duration may not be known yet; reviewer caps anyway
		"spam_score":      spamScore,
	})
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
		defer cancel()
		req, err := http.NewRequestWithContext(ctx, http.MethodPost,
			s.reviewerServiceURL+"/v1/reviewer/internal/enqueue", bytes.NewReader(body))
		if err != nil {
			return
		}
		req.Header.Set("Content-Type", "application/json")
		if s.internalServiceKey != "" {
			req.Header.Set("X-Internal-Service-Key", s.internalServiceKey)
		}
		resp, err := s.httpClient.Do(req)
		if err != nil {
			slog.Warn("reviewer enqueue failed (best-effort)", "post", p.ID, "err", err)
			return
		}
		_ = resp.Body.Close()
	}()
}

// gateVoiceReviewStatus holds a voice post out of public surfaces until
// media-service reports the baseline audio-safety pass complete
// (Codex P0-6). A failed/unknown check holds 'pending' rather than
// auto-approving; a rejected asset rejects the post.
func (s *Service) gateVoiceReviewStatus(ctx context.Context, mediaID uuid.UUID) string {
	procStatus, modStatus, err := s.getMediaModeration(ctx, mediaID)
	if err != nil {
		log.Printf("Warning: voice media safety check failed for %s, holding pending: %v", mediaID, err)
		return "pending"
	}
	if procStatus == "rejected" || modStatus == "rejected" {
		return "rejected"
	}
	if procStatus != "ready" {
		return "pending"
	}
	switch modStatus {
	case "approved":
		return "approved"
	case "failed":
		// Safety pipeline failed → human review, never auto-approve.
		return "flagged"
	default:
		return "pending"
	}
}

// gateVideoReviewStatus decides a fresh video post's review_status from
// its media's processing + moderation state:
//   - media still processing  → "pending" (transcode consumer flips it)
//   - ready + scan rejected    → "rejected"
//   - ready + scan clean       → "approved"
//
// On a media-service error it fails safe to "pending" — the post stays
// hidden rather than risking an unprocessed or unscanned video going live.
func (s *Service) gateVideoReviewStatus(ctx context.Context, mediaID uuid.UUID) string {
	procStatus, modStatus, err := s.getMediaModeration(ctx, mediaID)
	if err != nil {
		log.Printf("Warning: media moderation check failed for %s, holding pending: %v", mediaID, err)
		return "pending"
	}
	if procStatus != "ready" {
		return "pending"
	}
	if modStatus == "rejected" {
		return "rejected"
	}
	return "approved"
}

// getMediaModeration fetches a media asset's processing_status and
// moderation_status from media-service (GET /v1/media/:id).
func (s *Service) getMediaModeration(ctx context.Context, mediaID uuid.UUID) (processingStatus, moderationStatus string, err error) {
	base := os.Getenv("MEDIA_SERVICE_URL")
	if base == "" {
		base = "http://media-service:8087"
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		strings.TrimRight(base, "/")+"/v1/media/"+mediaID.String(), nil)
	if err != nil {
		return "", "", err
	}
	if s.internalServiceKey != "" {
		req.Header.Set("X-Internal-Service-Key", s.internalServiceKey)
	}
	resp, err := s.httpClient.Do(req)
	if err != nil {
		return "", "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", "", fmt.Errorf("media-service returned %d", resp.StatusCode)
	}
	var env struct {
		Data struct {
			ProcessingStatus string `json:"processing_status"`
			ModerationStatus string `json:"moderation_status"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&env); err != nil {
		return "", "", err
	}
	return env.Data.ProcessingStatus, env.Data.ModerationStatus, nil
}
