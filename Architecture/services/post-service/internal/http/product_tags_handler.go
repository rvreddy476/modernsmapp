package http

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/atpost/post-service/internal/service"
	"github.com/atpost/post-service/internal/store/postgres"
	"github.com/atpost/shared/api"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// In-video product-tag handlers. Five routes:
//
//   POST   /v1/posts/:postId/product-tags                 author tags a product
//   GET    /v1/posts/:postId/product-tags                 viewer fetches overlay set
//   DELETE /v1/posts/:postId/product-tags/:tagId          author removes a tag
//   POST   /v1/posts/:postId/product-tags/:tagId/impression   player view fired
//   POST   /v1/posts/:postId/product-tags/:tagId/click        player tap fired
//
// + a creator-side list:
//   GET    /v1/creators/:creatorId/product-tags           creator analytics
//
// Affiliate-link existence + ownership is validated cross-service here:
// the handler calls monetization-service via the internal-key channel
// before persisting. The service layer doesn't make that call so unit
// tests stay free of HTTP plumbing.

type createProductTagRequest struct {
	AffiliateLinkID uuid.UUID `json:"affiliate_link_id" binding:"required"`
	TimeStartMS     *int32    `json:"time_start_ms,omitempty"`
	TimeEndMS       *int32    `json:"time_end_ms,omitempty"`
	PositionX       *float32  `json:"position_x,omitempty"`
	PositionY       *float32  `json:"position_y,omitempty"`
	Label           string    `json:"label"`
	ImageURL        string    `json:"image_url"`
}

// CreateProductTag — POST /v1/posts/:postId/product-tags
func (h *Handler) CreateProductTag(c *gin.Context) {
	postID, ok := parsePathUUID(c, "postId")
	if !ok {
		return
	}
	callerID, ok := mustCallerID(c)
	if !ok {
		return
	}

	var req createProductTagRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer,
			http.StatusBadRequest, "INVALID_REQUEST", err.Error(), nil)
		return
	}
	// Per-field validation that exceeds binding tag expressiveness.
	if req.PositionX != nil && (*req.PositionX < 0 || *req.PositionX > 100) {
		api.ErrorWithContext(c.Request.Context(), c.Writer,
			http.StatusBadRequest, "INVALID_REQUEST", "position_x must be 0..100", nil)
		return
	}
	if req.PositionY != nil && (*req.PositionY < 0 || *req.PositionY > 100) {
		api.ErrorWithContext(c.Request.Context(), c.Writer,
			http.StatusBadRequest, "INVALID_REQUEST", "position_y must be 0..100", nil)
		return
	}
	if req.TimeStartMS != nil && req.TimeEndMS != nil && *req.TimeEndMS < *req.TimeStartMS {
		api.ErrorWithContext(c.Request.Context(), c.Writer,
			http.StatusBadRequest, "INVALID_REQUEST", "time_end_ms must be >= time_start_ms", nil)
		return
	}

	// Cross-service validation: the affiliate link must exist + be
	// active + be owned by the caller. Fail-closed on monetization-
	// service unreachability — a creator tagging someone else's
	// affiliate link is a real commission-fraud vector, so a transient
	// outage should NOT let a bad tag through.
	if _, _, err := h.svc.ValidateAffiliateLink(
		c.Request.Context(), req.AffiliateLinkID, callerID,
	); err != nil {
		writeProductTagError(c, err)
		return
	}

	tag, err := h.svc.CreateProductTag(c.Request.Context(), service.CreateProductTagInput{
		PostID:          postID,
		AffiliateLinkID: req.AffiliateLinkID,
		CallerID:        callerID,
		TimeStartMS:     req.TimeStartMS,
		TimeEndMS:       req.TimeEndMS,
		PositionX:       req.PositionX,
		PositionY:       req.PositionY,
		Label:           req.Label,
		ImageURL:        req.ImageURL,
	})
	if err != nil {
		writeProductTagError(c, err)
		return
	}
	api.JSON(c.Writer, http.StatusCreated, tag, nil)
}

// ListProductTagsByPost — GET /v1/posts/:postId/product-tags
//
// Public read — the player calls this on every open. Visibility gating
// (private post → only follower etc.) is done by the same upstream
// middleware that gates the post itself, not here.
func (h *Handler) ListProductTagsByPost(c *gin.Context) {
	postID, ok := parsePathUUID(c, "postId")
	if !ok {
		return
	}
	tags, err := h.svc.ListProductTagsByPost(c.Request.Context(), postID)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer,
			http.StatusInternalServerError, "INTERNAL", err.Error(), nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, tags, nil)
}

// ListProductTagsByCreator — GET /v1/creators/:creatorId/product-tags
//
// Creator-analytics view. Auth required + caller must be the creator
// (or a moderator — moderator override is TODO).
func (h *Handler) ListProductTagsByCreator(c *gin.Context) {
	creatorID, ok := parsePathUUID(c, "creatorId")
	if !ok {
		return
	}
	callerID, ok := mustCallerID(c)
	if !ok {
		return
	}
	if callerID != creatorID {
		api.ErrorWithContext(c.Request.Context(), c.Writer,
			http.StatusForbidden, "FORBIDDEN", "can only list own tags", nil)
		return
	}

	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "50"))
	offset, _ := strconv.Atoi(c.DefaultQuery("offset", "0"))
	tags, err := h.svc.ListProductTagsByCreator(c.Request.Context(), creatorID, limit, offset)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer,
			http.StatusInternalServerError, "INTERNAL", err.Error(), nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, tags, nil)
}

// DeleteProductTag — DELETE /v1/posts/:postId/product-tags/:tagId
func (h *Handler) DeleteProductTag(c *gin.Context) {
	tagID, ok := parsePathUUID(c, "tagId")
	if !ok {
		return
	}
	callerID, ok := mustCallerID(c)
	if !ok {
		return
	}
	if err := h.svc.DeleteProductTag(c.Request.Context(), tagID, callerID); err != nil {
		writeProductTagError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

// RecordProductTagImpression — POST /v1/posts/:postId/product-tags/:tagId/impression
//
// Player view event. Unauthenticated path — gateway rate-limit (H5)
// throttles aggregate traffic, and per-(tag, IP) dedup in the service
// layer prevents a single viewer's repeat watches from inflating
// counts. We don't validate that postId matches the tag's post; the
// tag ID alone is sufficient.
func (h *Handler) RecordProductTagImpression(c *gin.Context) {
	tagID, ok := parsePathUUID(c, "tagId")
	if !ok {
		return
	}
	ipHash := hashClientIP(c)
	if err := h.svc.RecordProductTagImpression(c.Request.Context(), tagID, ipHash); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer,
			http.StatusInternalServerError, "INTERNAL", err.Error(), nil)
		return
	}
	c.Status(http.StatusNoContent)
}

// RecordProductTagClick — POST /v1/posts/:postId/product-tags/:tagId/click
func (h *Handler) RecordProductTagClick(c *gin.Context) {
	tagID, ok := parsePathUUID(c, "tagId")
	if !ok {
		return
	}
	ipHash := hashClientIP(c)
	if err := h.svc.RecordProductTagClick(c.Request.Context(), tagID, ipHash); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer,
			http.StatusInternalServerError, "INTERNAL", err.Error(), nil)
		return
	}
	c.Status(http.StatusNoContent)
}

// hashClientIP returns a stable hex digest of the request's client IP.
// Used as the dedup key so a long-lived Redis record doesn't store the
// raw IP (PII). Falls back to "" if no IP can be determined — the
// service's dedup layer treats that as "skip dedup" and accepts the
// event (the gateway flood gate is the upstream guard).
//
// Precedence:
//   X-Real-IP (the gateway already sets this for the proxied request)
//   X-Forwarded-For first hop
//   c.ClientIP() (gin's helper, ultimately RemoteAddr)
func hashClientIP(c *gin.Context) string {
	ip := strings.TrimSpace(c.GetHeader("X-Real-IP"))
	if ip == "" {
		if xff := c.GetHeader("X-Forwarded-For"); xff != "" {
			if idx := strings.Index(xff, ","); idx != -1 {
				ip = strings.TrimSpace(xff[:idx])
			} else {
				ip = strings.TrimSpace(xff)
			}
		}
	}
	if ip == "" {
		ip = c.ClientIP()
	}
	if ip == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(ip))
	return hex.EncodeToString(sum[:16]) // first 128 bits is plenty
}

// ─── helpers ────────────────────────────────────────────────────────

func parsePathUUID(c *gin.Context, name string) (uuid.UUID, bool) {
	id, err := uuid.Parse(c.Param(name))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer,
			http.StatusBadRequest, "INVALID_REQUEST", name+" must be a UUID", nil)
		return uuid.Nil, false
	}
	return id, true
}

func mustCallerID(c *gin.Context) (uuid.UUID, bool) {
	id, err := uuid.Parse(c.GetHeader("X-User-Id"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer,
			http.StatusUnauthorized, "UNAUTHORIZED", "missing or invalid X-User-Id", nil)
		return uuid.Nil, false
	}
	return id, true
}

func writeProductTagError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, service.ErrPostNotFound),
		errors.Is(err, postgres.ErrTagNotFound),
		errors.Is(err, service.ErrAffiliateLinkNotFound):
		api.ErrorWithContext(c.Request.Context(), c.Writer,
			http.StatusNotFound, "NOT_FOUND", err.Error(), nil)
	case errors.Is(err, service.ErrProductTagNotAuthorized),
		errors.Is(err, service.ErrAffiliateLinkNotOwned):
		api.ErrorWithContext(c.Request.Context(), c.Writer,
			http.StatusForbidden, "FORBIDDEN", err.Error(), nil)
	case errors.Is(err, service.ErrAffiliateLinkInactive):
		api.ErrorWithContext(c.Request.Context(), c.Writer,
			http.StatusGone, "GONE", err.Error(), nil)
	case errors.Is(err, postgres.ErrTagAlreadyExists):
		api.ErrorWithContext(c.Request.Context(), c.Writer,
			http.StatusConflict, "CONFLICT", err.Error(), nil)
	default:
		api.ErrorWithContext(c.Request.Context(), c.Writer,
			http.StatusInternalServerError, "INTERNAL", err.Error(), nil)
	}
}
