package http

import (
	"errors"
	"net/http"
	"strings"

	"github.com/atpost/media-service/internal/service"
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
		subtitles.POST("/:mediaId/auto", authMW, h.GenerateAutoCaptions)
		// Module 1 P0-9: explicit caption/transcript status + request.
		// Status is honest about a missing provider ("unavailable"),
		// never a fabricated success.
		subtitles.GET("/:mediaId/status", h.GetCaptionStatus)
		subtitles.POST("/:mediaId/request", authMW, h.RequestCaptions)
		// fixes-v2 / Codex P1-3: owner transcript correction.
		subtitles.PATCH("/:mediaId", authMW, h.CorrectCaption)
	}
}

// CorrectCaption — PATCH /v1/subtitles/:mediaId
// Body: {"language":"en","content":"corrected transcript"}. Owner-only.
// Writes canonical content with edited_by_owner=true, after which
// auto-generation will not overwrite it.
func (h *Handler) CorrectCaption(c *gin.Context) {
	userID, err := uuid.Parse(c.GetHeader("X-User-Id"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Invalid user ID", nil)
		return
	}
	mediaID, err := uuid.Parse(c.Param("mediaId"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "BAD_REQUEST", "Invalid media ID", nil)
		return
	}
	var body struct {
		Language string `json:"language"`
		Content  string `json:"content" binding:"required"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "BAD_REQUEST", err.Error(), nil)
		return
	}

	status, err := h.svc.CorrectCaption(c.Request.Context(), mediaID, userID, body.Language, body.Content)
	if err != nil {
		switch {
		case errors.Is(err, service.ErrNotMediaOwner):
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusForbidden, "FORBIDDEN", err.Error(), nil)
		case errors.Is(err, service.ErrInvalidCaption):
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_CAPTION", err.Error(), nil)
		default:
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		}
		return
	}
	api.JSON(c.Writer, http.StatusOK, status, nil)
}

// GetCaptionStatus — GET /v1/subtitles/:mediaId/status
// Returns {status: unavailable|pending|completed|failed, ...}.
func (h *Handler) GetCaptionStatus(c *gin.Context) {
	mediaID, err := uuid.Parse(c.Param("mediaId"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "BAD_REQUEST", "Invalid media ID", nil)
		return
	}
	status, err := h.svc.GetCaptionStatus(c.Request.Context(), mediaID)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, status, nil)
}

// RequestCaptions — POST /v1/subtitles/:mediaId/request
// Body: {"language": "hi"} (optional; "" = auto-detect). Owner-only.
func (h *Handler) RequestCaptions(c *gin.Context) {
	userID, err := uuid.Parse(c.GetHeader("X-User-Id"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Invalid user ID", nil)
		return
	}
	mediaID, err := uuid.Parse(c.Param("mediaId"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "BAD_REQUEST", "Invalid media ID", nil)
		return
	}
	var body struct {
		Language string `json:"language"`
	}
	_ = c.ShouldBindJSON(&body)

	status, err := h.svc.RequestCaptions(c.Request.Context(), mediaID, userID, body.Language)
	if err != nil {
		if errors.Is(err, service.ErrNotMediaOwner) || strings.Contains(err.Error(), "forbidden") {
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusForbidden, "FORBIDDEN", err.Error(), nil)
			return
		}
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, status, nil)
}

// GenerateAutoCaptions — POST /v1/subtitles/:mediaId/auto
// Body: {"language": "en"}  (optional; "" or omitted = auto-detect)
//
// Runs the configured speech-to-text backend against the media's
// audio and persists a media_subtitles row with source="auto". When
// OPENAI_API_KEY isn't set, the StubBackend returns a placeholder
// row so the studio renders a "captions pending" state instead of
// failing.
func (h *Handler) GenerateAutoCaptions(c *gin.Context) {
	actorID, err := uuid.Parse(c.GetHeader("X-User-Id"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Invalid user ID", nil)
		return
	}
	mediaID, err := uuid.Parse(c.Param("mediaId"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "BAD_REQUEST", "Invalid media ID", nil)
		return
	}
	var body struct {
		Language string `json:"language"`
	}
	_ = c.ShouldBindJSON(&body)

	// Ownership gate — this legacy route authenticated but never
	// authorized (Codex P0-3).
	if err := h.svc.AssertMediaOwner(c.Request.Context(), mediaID, actorID); err != nil {
		if errors.Is(err, service.ErrNotMediaOwner) {
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusForbidden, "FORBIDDEN", err.Error(), nil)
			return
		}
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}

	sub, err := h.svc.GenerateAutoCaptions(c.Request.Context(), mediaID, body.Language)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "AUTO_CAPTIONS_FAILED", err.Error(), nil)
		return
	}
	if sub == nil {
		// P0-9: no real backend wired — say so rather than returning a
		// placeholder that looks like a finished caption.
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusServiceUnavailable,
			"CAPTIONS_UNAVAILABLE", "no transcription backend is configured", nil)
		return
	}
	api.JSON(c.Writer, http.StatusCreated, sub, nil)
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
	// Module 1 fixes-v1 / Codex P0-3: authentication alone let any user
	// overwrite another creator's caption track. Ownership is enforced in
	// the service, and the actor must be supplied here.
	actorID, err := uuid.Parse(c.GetHeader("X-User-Id"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Invalid user ID", nil)
		return
	}
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

	sub, err := h.svc.CreateSubtitle(c.Request.Context(), actorID, &postgres.MediaSubtitle{
		MediaAssetID: mediaID,
		Language:     req.Language,
		Source:       req.Source,
		Format:       req.Format,
		ContentURL:   req.ContentURL,
		Confidence:   req.Confidence,
	})
	if err != nil {
		if errors.Is(err, service.ErrNotMediaOwner) {
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusForbidden, "FORBIDDEN", err.Error(), nil)
			return
		}
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}

	api.JSON(c.Writer, http.StatusCreated, sub, nil)
}
