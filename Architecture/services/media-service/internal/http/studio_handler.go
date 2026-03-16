package http

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/atpost/media-service/internal/store/postgres"
	"github.com/atpost/shared/api"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// RegisterStudioRoutes registers Creative Studio endpoints under /v1/media/studio
// and the media cover-frame helpers.
func (h *Handler) RegisterStudioRoutes(r *gin.Engine, authMW gin.HandlerFunc) {
	studio := r.Group("/v1/media/studio")
	{
		studio.POST("/sessions", authMW, h.CreateEditorSession)
		studio.GET("/sessions", authMW, h.ListEditorSessions)
		studio.PUT("/sessions/:id", authMW, h.UpdateEditorSession)
		studio.DELETE("/sessions/:id", authMW, h.DeleteEditorSession)

		studio.GET("/stickers", h.ListStickers)
		studio.GET("/sticker-packs", h.ListStickerPacks)
		studio.POST("/stickers/:id/use", h.RecordStickerUse)

		studio.GET("/templates", h.ListTemplates)
	}

	// Cover-frame helpers live alongside the existing media routes.
	r.GET("/v1/media/:mediaId/suggested-frames", authMW, h.GetSuggestedFrames)
	r.POST("/v1/media/:mediaId/cover-frame", authMW, h.SetCoverFrame)
}

// -----------------------------------------------------------------------
// Editor sessions
// -----------------------------------------------------------------------

type createSessionReq struct {
	Mode         string          `json:"mode" binding:"required"`
	StateJSON    json.RawMessage `json:"state_json"`
	ThumbnailB64 string          `json:"thumbnail_base64"`
}

func (h *Handler) CreateEditorSession(c *gin.Context) {
	userID, err := uuid.Parse(c.GetHeader("X-User-Id"))
	if err != nil {
		api.Error(c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Invalid user ID", nil, nil)
		return
	}

	var req createSessionReq
	if err := c.ShouldBindJSON(&req); err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "BAD_REQUEST", err.Error(), nil, nil)
		return
	}

	sess, err := h.svc.CreateEditorSession(c.Request.Context(), userID, req.Mode, req.StateJSON)
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "BAD_REQUEST", err.Error(), nil, nil)
		return
	}

	api.JSON(c.Writer, http.StatusCreated, sess, nil)
}

func (h *Handler) ListEditorSessions(c *gin.Context) {
	userID, err := uuid.Parse(c.GetHeader("X-User-Id"))
	if err != nil {
		api.Error(c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Invalid user ID", nil, nil)
		return
	}

	sessions, err := h.svc.ListEditorSessions(c.Request.Context(), userID)
	if err != nil {
		api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil, nil)
		return
	}
	if sessions == nil {
		sessions = []postgres.EditorSessionSummary{}
	}

	api.JSON(c.Writer, http.StatusOK, sessions, nil)
}

func (h *Handler) UpdateEditorSession(c *gin.Context) {
	userID, err := uuid.Parse(c.GetHeader("X-User-Id"))
	if err != nil {
		api.Error(c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Invalid user ID", nil, nil)
		return
	}

	sessionID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "BAD_REQUEST", "Invalid session ID", nil, nil)
		return
	}

	var body struct {
		StateJSON json.RawMessage `json:"state_json"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "BAD_REQUEST", err.Error(), nil, nil)
		return
	}

	if err := h.svc.UpdateEditorSession(c.Request.Context(), sessionID, userID, body.StateJSON); err != nil {
		if err == pgx.ErrNoRows {
			api.Error(c.Writer, http.StatusNotFound, "NOT_FOUND", "Session not found", nil, nil)
			return
		}
		api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil, nil)
		return
	}

	api.JSON(c.Writer, http.StatusOK, map[string]string{"status": "updated"}, nil)
}

func (h *Handler) DeleteEditorSession(c *gin.Context) {
	userID, err := uuid.Parse(c.GetHeader("X-User-Id"))
	if err != nil {
		api.Error(c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Invalid user ID", nil, nil)
		return
	}

	sessionID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "BAD_REQUEST", "Invalid session ID", nil, nil)
		return
	}

	if err := h.svc.DeleteEditorSession(c.Request.Context(), sessionID, userID); err != nil {
		if err == pgx.ErrNoRows {
			api.Error(c.Writer, http.StatusNotFound, "NOT_FOUND", "Session not found", nil, nil)
			return
		}
		api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil, nil)
		return
	}

	api.JSON(c.Writer, http.StatusOK, map[string]string{"status": "deleted"}, nil)
}

// -----------------------------------------------------------------------
// Stickers
// -----------------------------------------------------------------------

func (h *Handler) ListStickers(c *gin.Context) {
	category := c.Query("category")
	limit := 20
	if lStr := c.Query("limit"); lStr != "" {
		if l, err := strconv.Atoi(lStr); err == nil && l > 0 {
			limit = l
		}
	}

	stickers, err := h.svc.ListStickers(c.Request.Context(), category, limit)
	if err != nil {
		api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil, nil)
		return
	}
	if stickers == nil {
		stickers = []postgres.Sticker{}
	}

	api.JSON(c.Writer, http.StatusOK, stickers, nil)
}

func (h *Handler) ListStickerPacks(c *gin.Context) {
	packs, err := h.svc.ListStickerPacks(c.Request.Context())
	if err != nil {
		api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil, nil)
		return
	}
	if packs == nil {
		packs = []postgres.StickerPack{}
	}

	api.JSON(c.Writer, http.StatusOK, packs, nil)
}

func (h *Handler) RecordStickerUse(c *gin.Context) {
	stickerID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "BAD_REQUEST", "Invalid sticker ID", nil, nil)
		return
	}

	// Fire-and-forget: use background context so the goroutine is not
	// cancelled when the HTTP request completes.
	go h.svc.RecordStickerUse(context.Background(), stickerID)

	api.JSON(c.Writer, http.StatusOK, map[string]string{"status": "recorded"}, nil)
}

// -----------------------------------------------------------------------
// Templates
// -----------------------------------------------------------------------

func (h *Handler) ListTemplates(c *gin.Context) {
	category := c.Query("category")
	limit := 20
	if lStr := c.Query("limit"); lStr != "" {
		if l, err := strconv.Atoi(lStr); err == nil && l > 0 {
			limit = l
		}
	}

	templates, err := h.svc.ListTemplates(c.Request.Context(), category, limit)
	if err != nil {
		api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil, nil)
		return
	}
	if templates == nil {
		templates = []postgres.FlickTemplate{}
	}

	api.JSON(c.Writer, http.StatusOK, templates, nil)
}

// -----------------------------------------------------------------------
// Cover-frame helpers
// -----------------------------------------------------------------------

// SuggestedFrame is a candidate cover frame at a specific video offset.
type SuggestedFrame struct {
	OffsetMs     int     `json:"offset_ms"`
	ThumbnailURL string  `json:"thumbnail_url"`
	QualityScore float64 `json:"quality_score"`
}

// GetSuggestedFrames returns 3 candidate cover frames at 10 %, 50 %, 90 % of video duration.
// In production this would query real processing results; this version returns a stub.
func (h *Handler) GetSuggestedFrames(c *gin.Context) {
	mediaID, err := uuid.Parse(c.Param("mediaId"))
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "BAD_REQUEST", "Invalid media ID", nil, nil)
		return
	}

	media, err := h.svc.GetMedia(c.Request.Context(), mediaID)
	if err != nil {
		api.Error(c.Writer, http.StatusNotFound, "NOT_FOUND", "Media not found", nil, nil)
		return
	}

	// Derive a rough duration: use DurationSeconds if available, else assume 60 s.
	durationMs := 60_000
	if media.DurationSeconds != nil && *media.DurationSeconds > 0 {
		durationMs = *media.DurationSeconds * 1000
	}

	frames := []SuggestedFrame{
		{OffsetMs: durationMs / 10, ThumbnailURL: "", QualityScore: 0.72},
		{OffsetMs: durationMs / 2, ThumbnailURL: "", QualityScore: 0.85},
		{OffsetMs: durationMs * 9 / 10, ThumbnailURL: "", QualityScore: 0.78},
	}

	api.JSON(c.Writer, http.StatusOK, gin.H{"media_id": mediaID, "frames": frames}, nil)
}

type setCoverFrameReq struct {
	OffsetMs int `json:"offset_ms"`
}

// SetCoverFrame records the user's chosen cover-frame offset on the media asset.
// The column cover_frame_offset_ms does not yet exist in the schema, so this
// returns 200 OK as a stub until a migration adds the column.
func (h *Handler) SetCoverFrame(c *gin.Context) {
	_, err := uuid.Parse(c.GetHeader("X-User-Id"))
	if err != nil {
		api.Error(c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Invalid user ID", nil, nil)
		return
	}

	mediaID, err := uuid.Parse(c.Param("mediaId"))
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "BAD_REQUEST", "Invalid media ID", nil, nil)
		return
	}

	var req setCoverFrameReq
	if err := c.ShouldBindJSON(&req); err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "BAD_REQUEST", err.Error(), nil, nil)
		return
	}

	// Stub: cover_frame_offset_ms column doesn't exist yet.
	// Return 200 so callers can integrate against the real schema later.
	api.JSON(c.Writer, http.StatusOK, gin.H{
		"media_id":  mediaID,
		"offset_ms": req.OffsetMs,
		"status":    "accepted",
	}, nil)
}
