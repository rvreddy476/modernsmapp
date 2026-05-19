package http

import (
	"net/http"
	"strconv"

	"github.com/atpost/shared/api"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// RegisterResumableRoutes registers resumable upload endpoints.
func (h *Handler) RegisterResumableRoutes(r *gin.Engine, authMW gin.HandlerFunc) {
	v1 := r.Group("/v1/media/upload")
	{
		v1.POST("/resumable/init", authMW, h.InitResumableUpload)
		v1.GET("/resumable/:uploadId", authMW, h.GetResumableUploadStatus)
		v1.POST("/resumable/:uploadId/chunk", authMW, h.UploadChunk)
		v1.POST("/resumable/:uploadId/complete", authMW, h.CompleteResumableUpload)
	}
}

type InitResumableUploadRequest struct {
	FileType   string `json:"file_type" binding:"required,oneof=image video"`
	MimeType   string `json:"mime_type" binding:"required"`
	TotalBytes int64  `json:"total_bytes" binding:"required,min=1"`
}

func (h *Handler) InitResumableUpload(c *gin.Context) {
	userID, err := uuid.Parse(c.GetHeader("X-User-Id"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Invalid user ID", nil)
		return
	}

	var req InitResumableUploadRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "BAD_REQUEST", err.Error(), nil)
		return
	}

	resp, err := h.svc.InitResumableUpload(c.Request.Context(), userID, req.FileType, req.MimeType, req.TotalBytes)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "BAD_REQUEST", err.Error(), nil)
		return
	}

	api.JSON(c.Writer, http.StatusOK, resp, nil)
}

func (h *Handler) GetResumableUploadStatus(c *gin.Context) {
	userID, err := uuid.Parse(c.GetHeader("X-User-Id"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Invalid user ID", nil)
		return
	}

	uploadID, err := uuid.Parse(c.Param("uploadId"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "BAD_REQUEST", "Invalid upload ID", nil)
		return
	}

	upload, err := h.svc.GetResumableUploadStatus(c.Request.Context(), uploadID, userID)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusNotFound, "NOT_FOUND", err.Error(), nil)
		return
	}

	api.JSON(c.Writer, http.StatusOK, upload, nil)
}

// UploadChunk receives one part's raw bytes as the request body. The part
// index comes from the `part_number` query parameter (1-based) and the
// size from Content-Length. media-service streams the body straight into
// the object store's multipart upload.
func (h *Handler) UploadChunk(c *gin.Context) {
	userID, err := uuid.Parse(c.GetHeader("X-User-Id"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Invalid user ID", nil)
		return
	}

	uploadID, err := uuid.Parse(c.Param("uploadId"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "BAD_REQUEST", "Invalid upload ID", nil)
		return
	}

	partNumber, err := strconv.Atoi(c.Query("part_number"))
	if err != nil || partNumber < 1 {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "BAD_REQUEST", "part_number query parameter required", nil)
		return
	}

	size := c.Request.ContentLength
	if size <= 0 {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "BAD_REQUEST", "Content-Length header required", nil)
		return
	}

	resp, err := h.svc.UploadPart(c.Request.Context(), uploadID, userID, partNumber, c.Request.Body, size)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "BAD_REQUEST", err.Error(), nil)
		return
	}

	api.JSON(c.Writer, http.StatusOK, resp, nil)
}

func (h *Handler) CompleteResumableUpload(c *gin.Context) {
	userID, err := uuid.Parse(c.GetHeader("X-User-Id"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Invalid user ID", nil)
		return
	}

	uploadID, err := uuid.Parse(c.Param("uploadId"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "BAD_REQUEST", "Invalid upload ID", nil)
		return
	}

	media, err := h.svc.CompleteResumableUpload(c.Request.Context(), uploadID, userID)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "BAD_REQUEST", err.Error(), nil)
		return
	}

	api.JSON(c.Writer, http.StatusOK, media, nil)
}
