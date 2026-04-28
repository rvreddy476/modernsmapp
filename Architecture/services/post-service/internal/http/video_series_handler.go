package http

import (
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/atpost/post-service/internal/store/postgres"
	"github.com/atpost/shared/api"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// ─── Video Series ─────────────────────────────────────────────────────────────

type createVideoSeriesRequest struct {
	Title         string  `json:"title" binding:"required"`
	Description   string  `json:"description"`
	ChannelID     *string `json:"channel_id"`
	CoverMediaID  *string `json:"cover_media_id"`
	TrailerPostID *string `json:"trailer_post_id"`
	IsComplete    bool    `json:"is_complete"`
	IsPublic      *bool   `json:"is_public"`
}

func (h *Handler) CreateVideoSeries(c *gin.Context) {
	userID, err := uuid.Parse(c.GetHeader("X-User-Id"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Missing or invalid X-User-Id header", nil)
		return
	}
	var req createVideoSeriesRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_REQUEST", err.Error(), nil)
		return
	}

	vs := &postgres.VideoSeries{
		CreatorID:   userID,
		Title:       req.Title,
		Description: req.Description,
		IsComplete:  req.IsComplete,
		IsPublic:    true,
	}
	if req.IsPublic != nil {
		vs.IsPublic = *req.IsPublic
	}
	if req.ChannelID != nil {
		if id, err := uuid.Parse(*req.ChannelID); err == nil {
			vs.ChannelID = &id
		}
	}
	if req.CoverMediaID != nil {
		if id, err := uuid.Parse(*req.CoverMediaID); err == nil {
			vs.CoverMediaID = &id
		}
	}
	if req.TrailerPostID != nil {
		if id, err := uuid.Parse(*req.TrailerPostID); err == nil {
			vs.TrailerPostID = &id
		}
	}

	if err := h.svc.CreateVideoSeries(c.Request.Context(), vs); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	api.JSON(c.Writer, http.StatusCreated, vs, nil)
}

func (h *Handler) GetVideoSeries(c *gin.Context) {
	seriesID, err := uuid.Parse(c.Param("seriesId"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid series ID", nil)
		return
	}
	vs, err := h.svc.GetVideoSeries(c.Request.Context(), seriesID)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusNotFound, "NOT_FOUND", err.Error(), nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, vs, nil)
}

func (h *Handler) GetVideoSeriesEpisodes(c *gin.Context) {
	seriesID, err := uuid.Parse(c.Param("seriesId"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid series ID", nil)
		return
	}
	eps, err := h.svc.GetVideoSeriesEpisodes(c.Request.Context(), seriesID)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	if eps == nil {
		eps = []postgres.VideoSeriesEpisode{}
	}
	api.JSON(c.Writer, http.StatusOK, eps, nil)
}

type addVideoSeriesEpisodeRequest struct {
	PostID     string  `json:"post_id" binding:"required"`
	EpisodeNum int     `json:"episode_num" binding:"required"`
	Title      *string `json:"title"`
}

func (h *Handler) AddVideoSeriesEpisode(c *gin.Context) {
	userID, err := uuid.Parse(c.GetHeader("X-User-Id"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Missing or invalid X-User-Id header", nil)
		return
	}
	seriesID, err := uuid.Parse(c.Param("seriesId"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid series ID", nil)
		return
	}
	var req addVideoSeriesEpisodeRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_REQUEST", err.Error(), nil)
		return
	}
	postID, err := uuid.Parse(req.PostID)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid post ID", nil)
		return
	}

	ep, err := h.svc.AddEpisodeToVideoSeries(c.Request.Context(), userID, seriesID, postID, req.EpisodeNum, req.Title)
	if err != nil {
		status := http.StatusInternalServerError
		code := "INTERNAL_ERROR"
		if err.Error() == "video series not found" {
			status = http.StatusNotFound
			code = "NOT_FOUND"
		} else if err.Error() == "forbidden: you do not own this video series" {
			status = http.StatusForbidden
			code = "FORBIDDEN"
		}
		api.ErrorWithContext(c.Request.Context(), c.Writer, status, code, err.Error(), nil)
		return
	}
	api.JSON(c.Writer, http.StatusCreated, ep, nil)
}

func (h *Handler) ListCreatorVideoSeries(c *gin.Context) {
	creatorID, err := uuid.Parse(c.Param("creatorId"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid creator ID", nil)
		return
	}
	limit, offset := parseLimitOffset(c)
	series, err := h.svc.ListVideoSeriesByCreator(c.Request.Context(), creatorID, limit, offset)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	if series == nil {
		series = []postgres.VideoSeries{}
	}
	api.JSON(c.Writer, http.StatusOK, series, nil)
}

// ─── Playlists ────────────────────────────────────────────────────────────────

type createPlaylistRequest struct {
	Title       string  `json:"title" binding:"required"`
	Description string  `json:"description"`
	ChannelID   *string `json:"channel_id"`
	CoverURL    *string `json:"cover_url"`
	Visibility  string  `json:"visibility"`
}

func (h *Handler) CreatePlaylist(c *gin.Context) {
	userID, err := uuid.Parse(c.GetHeader("X-User-Id"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Missing or invalid X-User-Id header", nil)
		return
	}
	var req createPlaylistRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_REQUEST", err.Error(), nil)
		return
	}

	vis := req.Visibility
	if vis == "" {
		vis = "public"
	}

	p := &postgres.Playlist{
		CreatorID:   userID,
		Title:       req.Title,
		Description: req.Description,
		Visibility:  vis,
		CoverURL:    req.CoverURL,
	}
	if req.ChannelID != nil {
		if id, err := uuid.Parse(*req.ChannelID); err == nil {
			p.ChannelID = &id
		}
	}

	if err := h.svc.CreatePlaylist(c.Request.Context(), p); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	api.JSON(c.Writer, http.StatusCreated, p, nil)
}

func (h *Handler) GetPlaylist(c *gin.Context) {
	playlistID, err := uuid.Parse(c.Param("playlistId"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid playlist ID", nil)
		return
	}
	var callerID *uuid.UUID
	if raw := c.GetHeader("X-User-Id"); raw != "" {
		if id, err := uuid.Parse(raw); err == nil {
			callerID = &id
		}
	}

	p, err := h.svc.GetPlaylist(c.Request.Context(), playlistID, callerID)
	if err != nil {
		status := http.StatusInternalServerError
		code := "INTERNAL_ERROR"
		msg := err.Error()
		if msg == "playlist not found" {
			status = http.StatusNotFound
			code = "NOT_FOUND"
		} else if msg == "forbidden: playlist is private" {
			status = http.StatusForbidden
			code = "FORBIDDEN"
		}
		api.ErrorWithContext(c.Request.Context(), c.Writer, status, code, msg, nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, p, nil)
}

func (h *Handler) DeletePlaylist(c *gin.Context) {
	userID, err := uuid.Parse(c.GetHeader("X-User-Id"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Missing or invalid X-User-Id header", nil)
		return
	}
	playlistID, err := uuid.Parse(c.Param("playlistId"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid playlist ID", nil)
		return
	}
	if err := h.svc.DeletePlaylist(c.Request.Context(), userID, playlistID); err != nil {
		status := http.StatusInternalServerError
		code := "INTERNAL_ERROR"
		msg := err.Error()
		if msg == "playlist not found" {
			status = http.StatusNotFound
			code = "NOT_FOUND"
		} else if msg == "forbidden: you do not own this playlist" {
			status = http.StatusForbidden
			code = "FORBIDDEN"
		}
		api.ErrorWithContext(c.Request.Context(), c.Writer, status, code, msg, nil)
		return
	}
	api.JSON(c.Writer, http.StatusNoContent, nil, nil)
}

type addPlaylistItemRequest struct {
	PostID   string `json:"post_id" binding:"required"`
	Position int    `json:"position"`
}

func (h *Handler) AddPlaylistItem(c *gin.Context) {
	_, err := uuid.Parse(c.GetHeader("X-User-Id"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Missing or invalid X-User-Id header", nil)
		return
	}
	playlistID, err := uuid.Parse(c.Param("playlistId"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid playlist ID", nil)
		return
	}
	var req addPlaylistItemRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_REQUEST", err.Error(), nil)
		return
	}
	postID, err := uuid.Parse(req.PostID)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid post ID", nil)
		return
	}
	if err := h.svc.AddPlaylistItem(c.Request.Context(), playlistID, postID, req.Position); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	api.JSON(c.Writer, http.StatusCreated, gin.H{"playlist_id": playlistID, "post_id": postID, "position": req.Position}, nil)
}

func (h *Handler) RemovePlaylistItem(c *gin.Context) {
	_, err := uuid.Parse(c.GetHeader("X-User-Id"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Missing or invalid X-User-Id header", nil)
		return
	}
	playlistID, err := uuid.Parse(c.Param("playlistId"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid playlist ID", nil)
		return
	}
	postID, err := uuid.Parse(c.Param("postId"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid post ID", nil)
		return
	}
	if err := h.svc.RemovePlaylistItem(c.Request.Context(), playlistID, postID); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	api.JSON(c.Writer, http.StatusNoContent, nil, nil)
}

func (h *Handler) GetPlaylistItems(c *gin.Context) {
	playlistID, err := uuid.Parse(c.Param("playlistId"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid playlist ID", nil)
		return
	}
	items, err := h.svc.GetPlaylistItems(c.Request.Context(), playlistID)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	if items == nil {
		items = []postgres.PlaylistItem{}
	}
	api.JSON(c.Writer, http.StatusOK, items, nil)
}

func (h *Handler) ListCreatorPlaylists(c *gin.Context) {
	creatorID, err := uuid.Parse(c.Param("creatorId"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid creator ID", nil)
		return
	}
	limit, offset := parseLimitOffset(c)
	playlists, err := h.svc.ListPlaylistsByCreator(c.Request.Context(), creatorID, limit, offset)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	if playlists == nil {
		playlists = []postgres.Playlist{}
	}
	api.JSON(c.Writer, http.StatusOK, playlists, nil)
}

// ─── Chapters ─────────────────────────────────────────────────────────────────

type chapterInput struct {
	ChapterIndex int     `json:"chapter_index"`
	Title        string  `json:"title" binding:"required"`
	StartMs      int     `json:"start_ms"`
	ThumbnailURL *string `json:"thumbnail_url"`
	Source       string  `json:"source"`
}

type saveChaptersRequest struct {
	Chapters []chapterInput `json:"chapters" binding:"required"`
}

func (h *Handler) SaveChapters(c *gin.Context) {
	_, err := uuid.Parse(c.GetHeader("X-User-Id"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Missing or invalid X-User-Id header", nil)
		return
	}
	postID, err := uuid.Parse(c.Param("postId"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid post ID", nil)
		return
	}
	var req saveChaptersRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_REQUEST", err.Error(), nil)
		return
	}

	chapters := make([]postgres.MediaChapter, len(req.Chapters))
	for i, ch := range req.Chapters {
		chapters[i] = postgres.MediaChapter{
			PostID:       postID,
			ChapterIndex: ch.ChapterIndex,
			Title:        ch.Title,
			StartMs:      ch.StartMs,
			ThumbnailURL: ch.ThumbnailURL,
			Source:       ch.Source,
		}
	}

	if err := h.svc.SaveChapters(c.Request.Context(), postID, chapters); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, gin.H{"saved": len(chapters)}, nil)
}

func (h *Handler) GetChapters(c *gin.Context) {
	postID, err := uuid.Parse(c.Param("postId"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid post ID", nil)
		return
	}
	chapters, err := h.svc.GetChapters(c.Request.Context(), postID)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	if chapters == nil {
		chapters = []postgres.MediaChapter{}
	}
	api.JSON(c.Writer, http.StatusOK, chapters, nil)
}

// ─── End Screens ──────────────────────────────────────────────────────────────

type endScreenInput struct {
	Type      string          `json:"type" binding:"required"`
	TargetID  *string         `json:"target_id"`
	TargetURL *string         `json:"target_url"`
	Title     *string         `json:"title"`
	Position  json.RawMessage `json:"position" binding:"required"`
	StartMs   int             `json:"start_ms"`
	EndMs     int             `json:"end_ms"`
}

type saveEndScreensRequest struct {
	Screens []endScreenInput `json:"screens" binding:"required"`
}

func (h *Handler) SaveEndScreens(c *gin.Context) {
	_, err := uuid.Parse(c.GetHeader("X-User-Id"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Missing or invalid X-User-Id header", nil)
		return
	}
	postID, err := uuid.Parse(c.Param("postId"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid post ID", nil)
		return
	}
	var req saveEndScreensRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_REQUEST", err.Error(), nil)
		return
	}

	screens := make([]postgres.EndScreen, len(req.Screens))
	for i, sc := range req.Screens {
		screens[i] = postgres.EndScreen{
			PostID:    postID,
			Type:      sc.Type,
			TargetURL: sc.TargetURL,
			Title:     sc.Title,
			Position:  sc.Position,
			StartMs:   sc.StartMs,
			EndMs:     sc.EndMs,
		}
		if sc.TargetID != nil {
			if id, err := uuid.Parse(*sc.TargetID); err == nil {
				screens[i].TargetID = &id
			}
		}
	}

	if err := h.svc.SaveEndScreens(c.Request.Context(), postID, screens); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, gin.H{"saved": len(screens)}, nil)
}

func (h *Handler) GetEndScreens(c *gin.Context) {
	postID, err := uuid.Parse(c.Param("postId"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid post ID", nil)
		return
	}
	screens, err := h.svc.GetEndScreens(c.Request.Context(), postID)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	if screens == nil {
		screens = []postgres.EndScreen{}
	}
	api.JSON(c.Writer, http.StatusOK, screens, nil)
}

// ─── Video Cards ──────────────────────────────────────────────────────────────

type videoCardInput struct {
	Type       string  `json:"type" binding:"required"`
	TargetID   *string `json:"target_id"`
	TargetURL  *string `json:"target_url"`
	Title      string  `json:"title" binding:"required"`
	TeaserText *string `json:"teaser_text"`
	AppearAtMs int     `json:"appear_at_ms"`
}

type saveVideoCardsRequest struct {
	Cards []videoCardInput `json:"cards" binding:"required"`
}

func (h *Handler) SaveVideoCards(c *gin.Context) {
	_, err := uuid.Parse(c.GetHeader("X-User-Id"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Missing or invalid X-User-Id header", nil)
		return
	}
	postID, err := uuid.Parse(c.Param("postId"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid post ID", nil)
		return
	}
	var req saveVideoCardsRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_REQUEST", err.Error(), nil)
		return
	}

	cards := make([]postgres.VideoCard, len(req.Cards))
	for i, card := range req.Cards {
		cards[i] = postgres.VideoCard{
			PostID:     postID,
			Type:       card.Type,
			TargetURL:  card.TargetURL,
			Title:      card.Title,
			TeaserText: card.TeaserText,
			AppearAtMs: card.AppearAtMs,
		}
		if card.TargetID != nil {
			if id, err := uuid.Parse(*card.TargetID); err == nil {
				cards[i].TargetID = &id
			}
		}
	}

	if err := h.svc.SaveVideoCards(c.Request.Context(), postID, cards); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, gin.H{"saved": len(cards)}, nil)
}

func (h *Handler) GetVideoCards(c *gin.Context) {
	postID, err := uuid.Parse(c.Param("postId"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid post ID", nil)
		return
	}
	cards, err := h.svc.GetVideoCards(c.Request.Context(), postID)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	if cards == nil {
		cards = []postgres.VideoCard{}
	}
	api.JSON(c.Writer, http.StatusOK, cards, nil)
}

// ─── Watch Progress ───────────────────────────────────────────────────────────

type saveWatchProgressRequest struct {
	PositionMs int `json:"position_ms"`
	DurationMs int `json:"duration_ms" binding:"required"`
}

func (h *Handler) SaveWatchProgress(c *gin.Context) {
	userID, err := uuid.Parse(c.GetHeader("X-User-Id"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Missing or invalid X-User-Id header", nil)
		return
	}
	postID, err := uuid.Parse(c.Param("videoId"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid post ID", nil)
		return
	}
	var req saveWatchProgressRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_REQUEST", err.Error(), nil)
		return
	}

	var pct float32
	if req.DurationMs > 0 {
		pct = float32(req.PositionMs) / float32(req.DurationMs) * 100
	}
	completed := pct >= 90.0

	wp := &postgres.WatchProgress{
		UserID:         userID,
		PostID:         postID,
		PositionMs:     req.PositionMs,
		DurationMs:     req.DurationMs,
		PercentWatched: pct,
		Completed:      completed,
	}

	if err := h.svc.SaveWatchProgress(c.Request.Context(), wp); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, wp, nil)
}

func (h *Handler) GetContinueWatching(c *gin.Context) {
	userID, err := uuid.Parse(c.GetHeader("X-User-Id"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Missing or invalid X-User-Id header", nil)
		return
	}
	limit := 20
	if l, err := strconv.Atoi(c.DefaultQuery("limit", "20")); err == nil && l > 0 && l <= 100 {
		limit = l
	}
	items, err := h.svc.GetContinueWatching(c.Request.Context(), userID, limit)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	if items == nil {
		items = []postgres.WatchProgress{}
	}
	api.JSON(c.Writer, http.StatusOK, items, nil)
}

func (h *Handler) DeleteWatchProgress(c *gin.Context) {
	userID, err := uuid.Parse(c.GetHeader("X-User-Id"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Missing or invalid X-User-Id header", nil)
		return
	}
	postID, err := uuid.Parse(c.Param("videoId"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid post ID", nil)
		return
	}
	if err := h.svc.DeleteWatchProgress(c.Request.Context(), userID, postID); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	api.JSON(c.Writer, http.StatusNoContent, nil, nil)
}

// ─── helpers ──────────────────────────────────────────────────────────────────

func parseLimitOffset(c *gin.Context) (int, int) {
	limit := 20
	if l, err := strconv.Atoi(c.DefaultQuery("limit", "20")); err == nil && l > 0 && l <= 100 {
		limit = l
	}
	offset := 0
	if o, err := strconv.Atoi(c.DefaultQuery("offset", "0")); err == nil && o >= 0 {
		offset = o
	}
	return limit, offset
}
