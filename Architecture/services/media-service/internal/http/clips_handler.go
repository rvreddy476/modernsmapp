package http

import (
	"net/http"

	"github.com/atpost/media-service/internal/store/postgres"
	"github.com/atpost/shared/api"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// RegisterClipsRoutes registers multi-clip editor and subtitle endpoints.
func (h *Handler) RegisterClipsRoutes(r *gin.Engine, authMW gin.HandlerFunc) {
	clips := r.Group("/v1/clips")
	{
		clips.POST("/:postId", authMW, h.SaveClips)
		clips.GET("/:postId", h.GetClips)
	}

	subtitles := r.Group("/v1/subtitles")
	{
		subtitles.GET("/:mediaId", h.GetSubtitles)
		subtitles.POST("/:mediaId", authMW, h.CreateSubtitle)
	}
}

func (h *Handler) SaveClips(c *gin.Context) {
	_, err := uuid.Parse(c.GetHeader("X-User-Id"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Invalid user ID", nil)
		return
	}

	postID, err := uuid.Parse(c.Param("postId"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "BAD_REQUEST", "Invalid post ID", nil)
		return
	}

	var req struct {
		Clips []struct {
			MediaAssetID uuid.UUID `json:"media_asset_id" binding:"required"`
			ClipOrder    int       `json:"clip_order"`
			TrimStartMs  int       `json:"trim_start_ms"`
			TrimEndMs    *int      `json:"trim_end_ms"`
			DurationMs   int       `json:"duration_ms" binding:"required"`
		} `json:"clips" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "BAD_REQUEST", err.Error(), nil)
		return
	}

	clips := make([]postgres.MediaClip, len(req.Clips))
	for i, clip := range req.Clips {
		clips[i] = postgres.MediaClip{
			PostID:       postID,
			MediaAssetID: clip.MediaAssetID,
			ClipOrder:    clip.ClipOrder,
			TrimStartMs:  clip.TrimStartMs,
			TrimEndMs:    clip.TrimEndMs,
			DurationMs:   clip.DurationMs,
		}
	}

	if err := h.svc.SaveMediaClips(c.Request.Context(), postID, clips); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}

	api.JSON(c.Writer, http.StatusOK, gin.H{"ok": true}, nil)
}

func (h *Handler) GetClips(c *gin.Context) {
	postID, err := uuid.Parse(c.Param("postId"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "BAD_REQUEST", "Invalid post ID", nil)
		return
	}

	clips, err := h.svc.GetMediaClips(c.Request.Context(), postID)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	if clips == nil {
		clips = []postgres.MediaClip{}
	}

	api.JSON(c.Writer, http.StatusOK, gin.H{"clips": clips}, nil)
}

func (h *Handler) GetSubtitles(c *gin.Context) {
	mediaID, err := uuid.Parse(c.Param("mediaId"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "BAD_REQUEST", "Invalid media ID", nil)
		return
	}

	subs, err := h.svc.GetSubtitles(c.Request.Context(), mediaID)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	if subs == nil {
		subs = []postgres.MediaSubtitle{}
	}

	api.JSON(c.Writer, http.StatusOK, gin.H{"subtitles": subs}, nil)
}

func (h *Handler) CreateSubtitle(c *gin.Context) {
	mediaID, err := uuid.Parse(c.Param("mediaId"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "BAD_REQUEST", "Invalid media ID", nil)
		return
	}

	var req struct {
		Language   string   `json:"language" binding:"required"`
		Source     string   `json:"source" binding:"required"`
		Format     string   `json:"format"`
		ContentURL string   `json:"content_url" binding:"required"`
		Confidence *float32 `json:"confidence"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "BAD_REQUEST", err.Error(), nil)
		return
	}
	if req.Format == "" {
		req.Format = "vtt"
	}

	sub, err := h.svc.CreateSubtitle(c.Request.Context(), &postgres.MediaSubtitle{
		MediaAssetID: mediaID,
		Language:     req.Language,
		Source:       req.Source,
		Format:       req.Format,
		ContentURL:   req.ContentURL,
		Confidence:   req.Confidence,
	})
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}

	api.JSON(c.Writer, http.StatusCreated, sub, nil)
}
