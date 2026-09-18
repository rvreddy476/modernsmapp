package http

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/atpost/media-service/internal/service"
	"github.com/atpost/media-service/internal/store/postgres"
	"github.com/atpost/shared/api"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// clipsService is the slice of the service these endpoints use.
//
// The authorization these endpoints were missing is the thing most worth
// testing, so it is reachable through an interface: the handler tests drive
// every allow/deny path without a PostgreSQL instance, and the decisions
// themselves are unit-tested in the service (captionReadLocalVerdict,
// assertClipsOwned) and in the delivery package (Gate.AuthorizeAsset).
type clipsService interface {
	AuthorizeMediaRead(ctx context.Context, viewerID, mediaID uuid.UUID) error
	GetSubtitles(ctx context.Context, mediaAssetID uuid.UUID) ([]postgres.MediaSubtitle, error)
	GetCaptionStatus(ctx context.Context, mediaID uuid.UUID) (*service.CaptionStatus, error)
	CaptionTrackVTT(ctx context.Context, mediaID uuid.UUID, language string) (string, error)
	SaveMediaClips(ctx context.Context, actorID, postID uuid.UUID, clips []postgres.MediaClip) error
}

// SubtitleTrackRoute is the caption track a <track src> points at.
//
// The language sits in its own path segment because gin's router will not
// mix a parameter with a literal suffix in one segment; the handler accepts
// the segment with or without the ".vtt" extension, so
// `/v1/subtitles/<id>/track/en.vtt` is a stable, cacheable, extension-
// bearing URL — which is what players and CDNs want — and `…/track/en`
// resolves to the same track.
const SubtitleTrackRoute = "/:mediaId/track/:language"

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
		// The browser caption track. Same group, same gate; no authMW,
		// for the same reason the media reads carry none — a public
		// asset's captions are public, and the gate is what decides.
		subtitles.GET(SubtitleTrackRoute, h.ServeSubtitleTrack)
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
//
// This response carries the transcript in `text`, so it is a content read,
// not a progress ping: it goes through the same gate as the asset itself.
func (h *Handler) GetCaptionStatus(c *gin.Context) {
	mediaID, err := uuid.Parse(c.Param("mediaId"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "BAD_REQUEST", "Invalid media ID", nil)
		return
	}
	if err := h.clipsSvc().AuthorizeMediaRead(c.Request.Context(), deliveryViewer(c), mediaID); err != nil {
		writeDeliveryError(c, err)
		return
	}
	status, err := h.clipsSvc().GetCaptionStatus(c.Request.Context(), mediaID)
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

// SaveClips — POST /v1/clips/:postId
//
// Authenticated AND authorized: the actor must own every media asset in
// the sequence it writes and every asset in the sequence it replaces. It
// used to discard the caller's identity entirely (`_, err := uuid.Parse`)
// and hand the post id straight to the store.
func (h *Handler) SaveClips(c *gin.Context) {
	actorID, err := uuid.Parse(c.GetHeader("X-User-Id"))
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

	if err := h.clipsSvc().SaveMediaClips(c.Request.Context(), actorID, postID, clips); err != nil {
		if errors.Is(err, service.ErrNotMediaOwner) {
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusForbidden, "FORBIDDEN", err.Error(), nil)
			return
		}
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

// subtitleReads returns the caption read slice. Always the service in
// production; overridden in tests.
func (h *Handler) clipsSvc() clipsService {
	if h.subtitles != nil {
		return h.subtitles
	}
	return h.svc
}

// GetSubtitles — GET /v1/subtitles/:mediaId
//
// The rows carry the transcript inline, so this is a content read and is
// gated exactly like GET /v1/media/:mediaId/url. It was anonymous and
// ungated: the transcript of any private, unlisted or not-yet-moderated
// asset was readable by anyone holding its UUID.
func (h *Handler) GetSubtitles(c *gin.Context) {
	mediaID, err := uuid.Parse(c.Param("mediaId"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "BAD_REQUEST", "Invalid media ID", nil)
		return
	}
	if err := h.clipsSvc().AuthorizeMediaRead(c.Request.Context(), deliveryViewer(c), mediaID); err != nil {
		writeDeliveryError(c, err)
		return
	}

	subs, err := h.clipsSvc().GetSubtitles(c.Request.Context(), mediaID)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	if subs == nil {
		subs = []postgres.MediaSubtitle{}
	}

	api.JSON(c.Writer, http.StatusOK, gin.H{"subtitles": subs}, nil)
}

// ServeSubtitleTrack — GET /v1/subtitles/:mediaId/track/:language(.vtt)
//
// Renders the stored cues as a WebVTT file so a `<track src>` has
// something to point at. Nothing served text/vtt before: the transcript
// lived inline in JSON and content_url was usually empty, so captions
// could not be turned on in a browser at all.
//
// Same gate as every other caption read. The body is the media's spoken
// content, so it is cached as private: a shared cache must not hand one
// viewer's authorized track to the next viewer, who may be a stranger.
func (h *Handler) ServeSubtitleTrack(c *gin.Context) {
	mediaID, err := uuid.Parse(c.Param("mediaId"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "BAD_REQUEST", "Invalid media ID", nil)
		return
	}
	language := strings.TrimSuffix(c.Param("language"), ".vtt")

	if err := h.clipsSvc().AuthorizeMediaRead(c.Request.Context(), deliveryViewer(c), mediaID); err != nil {
		writeDeliveryError(c, err)
		return
	}

	body, err := h.clipsSvc().CaptionTrackVTT(c.Request.Context(), mediaID, language)
	if err != nil {
		if errors.Is(err, service.ErrCaptionTrackNotFound) {
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusNotFound,
				"NOT_FOUND", "No caption track for that language", nil)
			return
		}
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}

	c.Header("Cache-Control", "private, max-age=300")
	c.Header("X-Content-Type-Options", "nosniff")
	c.Data(http.StatusOK, "text/vtt; charset=utf-8", []byte(body))
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
		Language string `json:"language" binding:"required"`
		Source   string `json:"source" binding:"required"`
		Format   string `json:"format"`
		// Content is the transcript itself. media_subtitles is the one
		// canonical caption store and the track is rendered from these
		// rows, so the text is what a caller supplies.
		Content string `json:"content"`
		// ContentURL is accepted only so it can be REFUSED with a clear
		// message. It used to be required and was stored verbatim, which
		// made this endpoint an arbitrary-URL sink for a value whose only
		// use is a `<track src>`: `javascript:…` or a URL on a host this
		// service does not serve would have been handed to every viewer's
		// browser. The one legitimate value is derived by the service.
		ContentURL string   `json:"content_url"`
		Confidence *float32 `json:"confidence"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "BAD_REQUEST", err.Error(), nil)
		return
	}
	if strings.TrimSpace(req.ContentURL) != "" {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest,
			"CONTENT_URL_NOT_ACCEPTED",
			"content_url is derived by media-service and must not be sent; send the transcript in content", nil)
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
		Content:      req.Content,
		Confidence:   req.Confidence,
	})
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

	api.JSON(c.Writer, http.StatusCreated, sub, nil)
}
