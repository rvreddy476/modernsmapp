package http

import (
	"net/http"

	"github.com/facebook-like/media-service/internal/service"
	"github.com/facebook-like/shared/api"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

type Handler struct {
	svc *service.Service
}

func New(svc *service.Service) *Handler {
	return &Handler{svc: svc}
}

func (h *Handler) RegisterRoutes(r *gin.Engine, authMW, optionalAuthMW gin.HandlerFunc) {
	r.GET("/healthz", h.HealthCheck)

	v1 := r.Group("/v1/media")
	{
		// Write endpoints — require authentication
		v1.POST("/init", authMW, h.InitUpload)
		v1.POST("/confirm", authMW, h.ConfirmUpload)
		v1.DELETE("/:mediaId", authMW, h.DeleteMedia)
		v1.PATCH("/:mediaId/alt-text", authMW, h.UpdateAltText)
		v1.POST("/upload/presigned", authMW, h.GetPresignedUploadURL)

		// Read endpoints — public (media URLs need to be accessible for rendering)
		v1.POST("/batch", h.BatchMediaURLs)
		v1.GET("/:mediaId", h.GetMedia)
		v1.GET("/:mediaId/status", h.GetMediaStatus)
		v1.GET("/:mediaId/url", h.GetMediaURL)
		v1.GET("/:mediaId/url/:variant", h.GetMediaVariantURL)
		v1.GET("/:mediaId/serve", h.ServeMedia)
		v1.GET("/:mediaId/serve/:variant", h.ServeMediaVariant)
	}
}

func (h *Handler) HealthCheck(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}

type InitUploadRequest struct {
	FileType      string `json:"file_type" binding:"required,oneof=image video"`
	MediaSubtype  string `json:"media_subtype"`
	MimeType      string `json:"mime_type" binding:"required"`
	FileSizeBytes int64  `json:"file_size_bytes" binding:"required,min=1"`
	AltText       string `json:"alt_text"`
}

func (h *Handler) InitUpload(c *gin.Context) {
	userID, err := uuid.Parse(c.GetHeader("X-User-Id"))
	if err != nil {
		api.Error(c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Invalid user ID", nil, nil)
		return
	}

	var req InitUploadRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "BAD_REQUEST", err.Error(), nil, nil)
		return
	}

	// Default media_subtype to "general" if not provided
	subtype := req.MediaSubtype
	if subtype == "" {
		subtype = "general"
	}

	res, err := h.svc.InitUpload(c.Request.Context(), userID, req.FileType, subtype, req.MimeType, req.FileSizeBytes, req.AltText)
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "BAD_REQUEST", err.Error(), nil, nil)
		return
	}

	api.JSON(c.Writer, http.StatusOK, res, nil)
}

type ConfirmUploadRequest struct {
	MediaID string `json:"media_id" binding:"required"`
}

func (h *Handler) ConfirmUpload(c *gin.Context) {
	userID, err := uuid.Parse(c.GetHeader("X-User-Id"))
	if err != nil {
		api.Error(c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Invalid user ID", nil, nil)
		return
	}

	var req ConfirmUploadRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "BAD_REQUEST", err.Error(), nil, nil)
		return
	}

	mediaID, err := uuid.Parse(req.MediaID)
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "BAD_REQUEST", "Invalid media ID", nil, nil)
		return
	}

	res, err := h.svc.ConfirmUpload(c.Request.Context(), mediaID, userID)
	if err != nil {
		if err.Error() == "forbidden: you do not own this media" {
			api.Error(c.Writer, http.StatusForbidden, "FORBIDDEN", err.Error(), nil, nil)
			return
		}
		api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil, nil)
		return
	}

	api.JSON(c.Writer, http.StatusOK, res, nil)
}

func (h *Handler) GetMedia(c *gin.Context) {
	mediaIDStr := c.Param("mediaId")
	mediaID, err := uuid.Parse(mediaIDStr)
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "BAD_REQUEST", "Invalid media ID", nil, nil)
		return
	}

	res, err := h.svc.GetMedia(c.Request.Context(), mediaID)
	if err != nil {
		api.Error(c.Writer, http.StatusNotFound, "NOT_FOUND", "Media not found", nil, nil)
		return
	}

	api.JSON(c.Writer, http.StatusOK, res, nil)
}

func (h *Handler) GetMediaURL(c *gin.Context) {
	mediaIDStr := c.Param("mediaId")
	mediaID, err := uuid.Parse(mediaIDStr)
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "BAD_REQUEST", "Invalid media ID", nil, nil)
		return
	}

	res, err := h.svc.GetMediaURL(c.Request.Context(), mediaID)
	if err != nil {
		api.Error(c.Writer, http.StatusNotFound, "NOT_FOUND", "Media not found", nil, nil)
		return
	}

	api.JSON(c.Writer, http.StatusOK, res, nil)
}

func (h *Handler) GetMediaVariantURL(c *gin.Context) {
	mediaIDStr := c.Param("mediaId")
	mediaID, err := uuid.Parse(mediaIDStr)
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "BAD_REQUEST", "Invalid media ID", nil, nil)
		return
	}

	variant := c.Param("variant")
	url, err := h.svc.GetMediaVariantURL(c.Request.Context(), mediaID, variant)
	if err != nil {
		api.Error(c.Writer, http.StatusNotFound, "NOT_FOUND", err.Error(), nil, nil)
		return
	}

	api.JSON(c.Writer, http.StatusOK, gin.H{"url": url}, nil)
}

type BatchMediaURLsRequest struct {
	IDs []string `json:"ids" binding:"required,min=1,max=50"`
}

func (h *Handler) BatchMediaURLs(c *gin.Context) {
	var req BatchMediaURLsRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "BAD_REQUEST", err.Error(), nil, nil)
		return
	}

	var ids []uuid.UUID
	for _, idStr := range req.IDs {
		id, err := uuid.Parse(idStr)
		if err != nil {
			api.Error(c.Writer, http.StatusBadRequest, "BAD_REQUEST", "Invalid media ID: "+idStr, nil, nil)
			return
		}
		ids = append(ids, id)
	}

	res, err := h.svc.BatchMediaURLs(c.Request.Context(), ids)
	if err != nil {
		api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil, nil)
		return
	}

	api.JSON(c.Writer, http.StatusOK, res, nil)
}

func (h *Handler) DeleteMedia(c *gin.Context) {
	userID, err := uuid.Parse(c.GetHeader("X-User-Id"))
	if err != nil {
		api.Error(c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Invalid user ID", nil, nil)
		return
	}

	mediaID, err := uuid.Parse(c.Param("mediaId"))
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "BAD_REQUEST", "Invalid media ID", nil, nil)
		return
	}

	err = h.svc.DeleteMedia(c.Request.Context(), mediaID, userID)
	if err != nil {
		if err.Error() == "forbidden: you do not own this media" {
			api.Error(c.Writer, http.StatusForbidden, "FORBIDDEN", err.Error(), nil, nil)
			return
		}
		if err.Error() == "media not found" {
			api.Error(c.Writer, http.StatusNotFound, "NOT_FOUND", err.Error(), nil, nil)
			return
		}
		api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil, nil)
		return
	}

	api.JSON(c.Writer, http.StatusOK, map[string]string{"status": "deleted"}, nil)
}

func (h *Handler) GetMediaStatus(c *gin.Context) {
	mediaID, err := uuid.Parse(c.Param("mediaId"))
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "BAD_REQUEST", "Invalid media ID", nil, nil)
		return
	}

	res, err := h.svc.GetMediaStatus(c.Request.Context(), mediaID)
	if err != nil {
		api.Error(c.Writer, http.StatusNotFound, "NOT_FOUND", "Media not found", nil, nil)
		return
	}

	api.JSON(c.Writer, http.StatusOK, res, nil)
}

// ServeMedia redirects to the presigned URL of the original file.
// Use this as <img src="/v1/media/:id/serve"> for direct image rendering.
func (h *Handler) ServeMedia(c *gin.Context) {
	mediaID, err := uuid.Parse(c.Param("mediaId"))
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "BAD_REQUEST", "Invalid media ID", nil, nil)
		return
	}

	imgURL, err := h.svc.GetMediaVariantURL(c.Request.Context(), mediaID, "original")
	if err != nil {
		api.Error(c.Writer, http.StatusNotFound, "NOT_FOUND", "Media not found", nil, nil)
		return
	}

	c.Redirect(http.StatusTemporaryRedirect, imgURL)
}

// ServeMediaVariant redirects to the presigned URL of a specific variant.
func (h *Handler) ServeMediaVariant(c *gin.Context) {
	mediaID, err := uuid.Parse(c.Param("mediaId"))
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "BAD_REQUEST", "Invalid media ID", nil, nil)
		return
	}

	variant := c.Param("variant")
	imgURL, err := h.svc.GetMediaVariantURL(c.Request.Context(), mediaID, variant)
	if err != nil {
		api.Error(c.Writer, http.StatusNotFound, "NOT_FOUND", err.Error(), nil, nil)
		return
	}

	c.Redirect(http.StatusTemporaryRedirect, imgURL)
}

type UpdateAltTextRequest struct {
	AltText string `json:"alt_text" binding:"required"`
}

func (h *Handler) UpdateAltText(c *gin.Context) {
	userID, err := uuid.Parse(c.GetHeader("X-User-Id"))
	if err != nil {
		api.Error(c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Invalid user ID", nil, nil)
		return
	}

	mediaID, err := uuid.Parse(c.Param("mediaId"))
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "BAD_REQUEST", "Invalid media ID", nil, nil)
		return
	}

	var req UpdateAltTextRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "BAD_REQUEST", err.Error(), nil, nil)
		return
	}

	err = h.svc.UpdateAltText(c.Request.Context(), mediaID, userID, req.AltText)
	if err != nil {
		if err.Error() == "no rows in result set" {
			api.Error(c.Writer, http.StatusNotFound, "NOT_FOUND", "Media not found or not owned by user", nil, nil)
			return
		}
		api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil, nil)
		return
	}

	api.JSON(c.Writer, http.StatusOK, map[string]string{"status": "updated"}, nil)
}

type GetPresignedUploadURLRequest struct {
	Filename    string `json:"filename" binding:"required"`
	ContentType string `json:"content_type" binding:"required"`
}

func (h *Handler) GetPresignedUploadURL(c *gin.Context) {
	userID, err := uuid.Parse(c.GetHeader("X-User-Id"))
	if err != nil {
		api.Error(c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Invalid user ID", nil, nil)
		return
	}

	var req GetPresignedUploadURLRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "BAD_REQUEST", err.Error(), nil, nil)
		return
	}

	res, err := h.svc.GetPresignedUploadURL(c.Request.Context(), userID, req.Filename, req.ContentType)
	if err != nil {
		api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil, nil)
		return
	}

	api.JSON(c.Writer, http.StatusOK, map[string]interface{}{
		"upload_url": res.UploadURL,
		"media_id":   res.MediaID,
		"object_key": res.ObjectKey,
		"expires_at": res.ExpiresAt,
	}, nil)
}
