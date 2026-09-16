// HTTP handlers for /v1/dating/verification.
//
// DPDP Act compliant — see PULSE_DATING_SPEC.md §15.8
// The Aadhaar number is NEVER accepted by these endpoints. The Aadhaar
// flow uses DigiLocker (Setu/Signzy partner): the mobile app opens the
// authorize URL, the user authenticates with UIDAI/DigiLocker, and the
// partner returns an *opaque* assertion id. We never see the number.
//
// Selfie verification (lane D5) is decided server-side from a short blink
// video: the client asks for a challenge, records the video, uploads it to
// media-service and submits {challenge_id, video_media_id}. Client-computed
// embeddings are refused with 410.
package http

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/atpost/dating-service/internal/service"
	"github.com/atpost/dating-service/internal/store"
	"github.com/atpost/shared/api"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// Stable verification error codes.
const (
	CodeSelfieEmbeddingRemoved      = "SELFIE_EMBEDDING_REMOVED"
	CodeSelfieFrameBurstUnsupported = "SELFIE_FRAME_BURST_UNSUPPORTED"
	CodeSelfieChallengeRequired     = "SELFIE_CHALLENGE_REQUIRED"
	CodeSelfieChallengeInvalid      = "SELFIE_CHALLENGE_INVALID"
	CodePrimaryPhotoNotApproved     = "PRIMARY_PHOTO_NOT_APPROVED"
	CodeSelfieAttemptsExceeded      = "SELFIE_ATTEMPTS_EXCEEDED"
	CodeSelfieMediaNotFound         = "SELFIE_MEDIA_NOT_FOUND"
	// CodeSelfieMediaNotReady (409) — the video exists but media-service
	// has not finished processing it; the same upload can be retried.
	CodeSelfieMediaNotReady      = "MEDIA_NOT_READY"
	CodeSelfieSameAsPrimaryPhoto = "SELFIE_SAME_AS_PRIMARY_PHOTO"
	CodeSelfieVideoTooLong       = "SELFIE_VIDEO_TOO_LONG"
	CodeSelfieVideoUnsupported   = "SELFIE_VIDEO_UNSUPPORTED"
	CodeFaceCompareUnavailable   = "FACE_COMPARE_UNAVAILABLE"
	CodeSelfieAlreadyPassed      = "SELFIE_ALREADY_PASSED"
	CodeSelfieReviewPending      = "SELFIE_REVIEW_PENDING"
	CodeSelfieNotPendingReview   = "SELFIE_NOT_PENDING_REVIEW"
	CodeAadhaarDisabled          = "AADHAAR_DISABLED"
)

// respondVerificationError maps verification errors to stable codes, then
// falls back to respondServiceError.
func (h *Handler) respondVerificationError(c *gin.Context, err error) {
	ctx := c.Request.Context()
	w := c.Writer
	switch {
	case errors.Is(err, store.ErrSelfieChallengeInvalid):
		api.ErrorWithContext(ctx, w, http.StatusBadRequest, CodeSelfieChallengeInvalid,
			"the selfie challenge is unknown, expired, already used or not yours; request a new one", nil)
	case errors.Is(err, service.ErrPrimaryPhotoNotApproved):
		api.ErrorWithContext(ctx, w, http.StatusConflict, CodePrimaryPhotoNotApproved,
			"your primary photo must be approved before selfie verification", nil)
	case errors.Is(err, store.ErrSelfieAttemptsExceeded):
		cfg := h.svc.SelfieSettings()
		c.Header("Retry-After", "86400")
		api.ErrorWithContext(ctx, w, http.StatusTooManyRequests, CodeSelfieAttemptsExceeded,
			"selfie verification attempt limit reached; try again later",
			map[string]any{"limit": cfg.MaxAttemptsPerDay, "window_hours": int(store.SelfieAttemptWindow.Hours())})
	case errors.Is(err, service.ErrSelfieMediaNotReady):
		api.ErrorWithContext(ctx, w, http.StatusConflict, CodeSelfieMediaNotReady,
			"the selfie video is still being processed; try again in a moment", nil)
	case errors.Is(err, service.ErrSelfieMediaNotFound):
		api.ErrorWithContext(ctx, w, http.StatusNotFound, CodeSelfieMediaNotFound,
			"selfie video not found", nil)
	case errors.Is(err, service.ErrSelfieSameAsPrimaryPhoto):
		api.ErrorWithContext(ctx, w, http.StatusBadRequest, CodeSelfieSameAsPrimaryPhoto,
			"record a new selfie video; the primary profile photo cannot verify itself", nil)
	case errors.Is(err, service.ErrSelfieVideoTooLong):
		api.ErrorWithContext(ctx, w, http.StatusUnprocessableEntity, CodeSelfieVideoTooLong,
			"the selfie video is too long; record again within the time limit",
			map[string]any{"max_duration_ms": h.svc.SelfieSettings().MaxVideoDurationMs})
	case errors.Is(err, service.ErrSelfieVideoUnsupported):
		api.ErrorWithContext(ctx, w, http.StatusUnprocessableEntity, CodeSelfieVideoUnsupported,
			"the selfie video could not be read; record again", nil)
	case errors.Is(err, service.ErrFaceCompareUnavailable):
		api.ErrorWithContext(ctx, w, http.StatusServiceUnavailable, CodeFaceCompareUnavailable,
			"selfie verification is temporarily unavailable; try again shortly", nil)
	case errors.Is(err, store.ErrSelfieAlreadyPassed):
		api.ErrorWithContext(ctx, w, http.StatusConflict, CodeSelfieAlreadyPassed, "selfie verification already passed", nil)
	case errors.Is(err, store.ErrSelfieReviewPending):
		api.ErrorWithContext(ctx, w, http.StatusConflict, CodeSelfieReviewPending,
			"your selfie is being reviewed by a moderator", nil)
	case errors.Is(err, store.ErrSelfieNotPendingReview):
		api.ErrorWithContext(ctx, w, http.StatusConflict, CodeSelfieNotPendingReview, "this selfie is not awaiting review", nil)
	case errors.Is(err, service.ErrAadhaarDisabled):
		api.ErrorWithContext(ctx, w, http.StatusServiceUnavailable, CodeAadhaarDisabled,
			"Aadhaar verification is not available", nil)
	default:
		respondServiceError(c, err, http.StatusInternalServerError, "VERIFICATION_FAILED")
	}
}

// StartAadhaar — POST /v1/dating/verification/aadhaar/start.
// Returns the DigiLocker authorize URL the mobile app must open.
func (h *Handler) StartAadhaar(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}
	out, err := h.svc.StartAadhaarFlow(c.Request.Context(), userID)
	if err != nil {
		h.respondVerificationError(c, err)
		return
	}
	api.JSON(c.Writer, http.StatusOK, out, nil)
}

// AadhaarCallbackRequest — body for the callback. Only the OAuth-style
// code + the PKCE state nonce. NO Aadhaar number is accepted here.
//
// DPDP Act compliant — see PULSE_DATING_SPEC.md §15.8
type aadhaarCallbackRequest struct {
	Code  string `json:"code"`
	State string `json:"state"`
}

// AadhaarCallback — POST /v1/dating/verification/aadhaar/callback.
func (h *Handler) AadhaarCallback(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}
	var body aadhaarCallbackRequest
	if err := c.ShouldBindJSON(&body); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_BODY", err.Error(), nil)
		return
	}
	if body.Code == "" || body.State == "" {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_REQUEST", "code and state required", nil)
		return
	}
	out, err := h.svc.CompleteAadhaarFlow(c.Request.Context(), userID, body.Code, body.State)
	if err != nil {
		h.respondVerificationError(c, err)
		return
	}
	api.JSON(c.Writer, http.StatusOK, out, nil)
}

// CreateSelfieChallenge — POST /v1/dating/verification/selfie/challenge.
// 200 {challenge_id, instruction: "blink_twice", max_duration_ms, expires_at}:
// valid 10 minutes, usable once, owned by the caller.
func (h *Handler) CreateSelfieChallenge(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}
	out, err := h.svc.CreateSelfieChallenge(c.Request.Context(), userID)
	if err != nil {
		h.respondVerificationError(c, err)
		return
	}
	api.JSON(c.Writer, http.StatusOK, out, nil)
}

// selfieRequestMaxBytes bounds the selfie body (it carries two UUIDs).
const selfieRequestMaxBytes = 64 << 10

// SubmitSelfie — POST /v1/dating/verification/selfie {challenge_id, video_media_id}.
//
// video_media_id is the caller's blink video, uploaded to media-service. A
// body still carrying the removed client `embedding` field is refused with
// 410 SELFIE_EMBEDDING_REMOVED whatever else it holds; a still-frame burst
// (`frame_media_ids`) with 400 SELFIE_FRAME_BURST_UNSUPPORTED.
func (h *Handler) SubmitSelfie(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}
	ctx := c.Request.Context()
	raw, err := io.ReadAll(io.LimitReader(c.Request.Body, selfieRequestMaxBytes))
	var fields map[string]json.RawMessage
	if err != nil || json.Unmarshal(raw, &fields) != nil {
		api.ErrorWithContext(ctx, c.Writer, http.StatusBadRequest, "INVALID_BODY", "body must be a JSON object", nil)
		return
	}
	if _, legacy := fields["embedding"]; legacy {
		api.ErrorWithContext(ctx, c.Writer, http.StatusGone, CodeSelfieEmbeddingRemoved,
			"client-computed face embeddings are no longer accepted: request POST /v1/dating/verification/selfie/challenge, "+
				"record the blink video, upload it to media-service, then send {challenge_id, video_media_id}", nil)
		return
	}
	if _, burst := fields["frame_media_ids"]; burst {
		api.ErrorWithContext(ctx, c.Writer, http.StatusBadRequest, CodeSelfieFrameBurstUnsupported,
			"still-frame bursts are not accepted; upload the blink recording as one short video (video_media_id)", nil)
		return
	}
	var body struct {
		VideoMediaID string `json:"video_media_id"`
		ChallengeID  string `json:"challenge_id"`
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		api.ErrorWithContext(ctx, c.Writer, http.StatusBadRequest, "INVALID_BODY", "video_media_id and challenge_id must be strings", nil)
		return
	}
	videoID, err := uuid.Parse(strings.TrimSpace(body.VideoMediaID))
	if err != nil || videoID == uuid.Nil {
		api.ErrorWithContext(ctx, c.Writer, http.StatusBadRequest, "INVALID_REQUEST", "video_media_id must be a UUID", nil)
		return
	}
	if strings.TrimSpace(body.ChallengeID) == "" {
		api.ErrorWithContext(ctx, c.Writer, http.StatusBadRequest, CodeSelfieChallengeRequired,
			"challenge_id required: request POST /v1/dating/verification/selfie/challenge first", nil)
		return
	}
	challengeID, err := uuid.Parse(strings.TrimSpace(body.ChallengeID))
	if err != nil || challengeID == uuid.Nil {
		h.respondVerificationError(c, store.ErrSelfieChallengeInvalid)
		return
	}
	out, err := h.svc.SubmitSelfie(ctx, userID, videoID, challengeID)
	if err != nil {
		h.respondVerificationError(c, err)
		return
	}
	api.JSON(c.Writer, http.StatusOK, out, nil)
}

// ListSelfieReviews — GET /v1/dating/admin/verification/selfie/pending?limit=
// (requireAdmin). Selfies awaiting a moderator with the video and primary
// photo media ids, the blink count, the similarity, the provider and why
// they were routed.
func (h *Handler) ListSelfieReviews(c *gin.Context) {
	limit := parseIntQuery(c, "limit", 50, 200)
	items, err := h.svc.ListSelfieReviews(c.Request.Context(), limit)
	if err != nil {
		respondServiceError(c, err, http.StatusInternalServerError, "QUERY_FAILED")
		return
	}
	api.JSON(c.Writer, http.StatusOK, gin.H{"items": items, "limit": limit}, nil)
}

type reviewSelfieRequest struct {
	Decision string `json:"decision"`
	Reason   string `json:"reason"`
}

// ReviewSelfie — POST /v1/dating/admin/verification/selfie/:userId/review
// {decision: approve|reject, reason?} (requireAdmin). Approve is a pass
// (pending_selfie → active through the status writer); reject is a fail.
// Audited with the admin's gateway-derived id.
func (h *Handler) ReviewSelfie(c *gin.Context) {
	adminID, ok := adminActor(c)
	if !ok {
		return
	}
	userID, ok := parseUUID(c, "userId")
	if !ok {
		return
	}
	var body reviewSelfieRequest
	if err := c.ShouldBindJSON(&body); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_BODY", "decision required", nil)
		return
	}
	out, err := h.svc.ReviewSelfie(c.Request.Context(), adminID, userID, strings.TrimSpace(body.Decision), body.Reason)
	if err != nil {
		h.respondVerificationError(c, err)
		return
	}
	api.JSON(c.Writer, http.StatusOK, out, nil)
}

// GetVerificationStatus — GET /v1/dating/verification/status.
//
// The selfie state (none | pending | review | passed | failed), the attempts
// left in the rolling window, the trust tier and the next step. A read only:
// it never starts an attempt and never consumes a challenge.
func (h *Handler) GetVerificationStatus(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}
	out, err := h.svc.GetVerificationStatus(c.Request.Context(), userID)
	if err != nil {
		h.respondVerificationError(c, err)
		return
	}
	api.JSON(c.Writer, http.StatusOK, out, nil)
}
