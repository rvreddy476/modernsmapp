package http

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/atpost/post-service/internal/service"
	"github.com/atpost/post-service/internal/store/postgres"
	"github.com/atpost/shared/api"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// ─── Video authoring: shared error mapping and enum validation ────────────────

// writeVideoAuthoringError is the ONE mapping for every ownership /
// visibility refusal on the authoring endpoints (cards, end screens,
// chapters, playlists and playlist items).
//
// 404 for "no such row", 403 for "not yours". A non-owner therefore learns
// that the post or playlist exists — a deliberate choice, matching what this
// service already answers for the identical shape (AddVideoSeriesEpisode,
// DeletePlaylist, RemoveCrosspost, the product-tag routes) and what the
// sibling GET routes for cards / chapters / end screens already reveal.
// Reason it out in one place rather than have each endpoint pick its own.
//
// Anything unrecognised is a 500: an unexpected error must never be reported
// as a successful or merely-forbidden outcome.
func writeVideoAuthoringError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, service.ErrPostNotFound),
		errors.Is(err, service.ErrPlaylistNotFound):
		api.ErrorWithContext(c.Request.Context(), c.Writer,
			http.StatusNotFound, "NOT_FOUND", err.Error(), nil)
	case errors.Is(err, service.ErrNotPostAuthor),
		errors.Is(err, service.ErrNotPlaylistOwner),
		errors.Is(err, service.ErrPlaylistPrivate):
		api.ErrorWithContext(c.Request.Context(), c.Writer,
			http.StatusForbidden, "FORBIDDEN", err.Error(), nil)
	default:
		api.ErrorWithContext(c.Request.Context(), c.Writer,
			http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
	}
}

// The two type enums. They are NOT the same list and must not be unified:
// cards can point at a poll, end screens can offer a channel subscribe.
// Both mirror the CHECK constraints in migrations/012_posttube_features.sql;
// validating here turns a constraint violation (500) into a 400 that names
// the bad value.
var (
	videoCardTypeList = []string{"video", "playlist", "poll", "external_link"}
	endScreenTypeList = []string{"video", "playlist", "channel_subscribe", "external_link"}

	validVideoCardTypes = sliceToSet(videoCardTypeList)
	validEndScreenTypes = sliceToSet(endScreenTypeList)
)

func sliceToSet(values []string) map[string]bool {
	set := make(map[string]bool, len(values))
	for _, v := range values {
		set[v] = true
	}
	return set
}

func invalidEnumMessage(field string, index int, key, got string, allowed []string) string {
	return fmt.Sprintf("%s[%d].%s %q is not valid; allowed values are %s",
		field, index, key, got, strings.Join(allowed, ", "))
}

// media_chapters.source carries the same kind of CHECK the card and
// end-screen `type` columns do (migrations/012_posttube_features.sql) and had
// the same hole: an unknown value reached Postgres and came back as a 500
// with a constraint name in it. Empty is NOT an error — the store defaults it
// to 'manual' — so it is accepted here too.
var (
	chapterSourceList  = []string{"manual", "ai_generated"}
	validChapterSource = sliceToSet(chapterSourceList)
)

// ─── Field validation for the authoring rows ─────────────────────────────────
//
// Everything below turns a value the watch page cannot render into a 400 that
// names the field. All of it used to answer 200 {"saved":1}:
//
//   - a card with no title, which draws an empty box on the player;
//   - target_id: "not-a-uuid", which uuid.Parse quietly DROPPED, leaving a
//     card whose link goes nowhere (the row is not, as it appeared, stored
//     verbatim — it is silently discarded, which is worse: the creator sees
//     a save succeed and a broken card);
//   - a negative appear_at_ms / start_ms, a timestamp that can never arrive;
//   - an end screen whose window closes before it opens.
//
// These are handler-level checks on purpose: they need no database, so a bad
// request is refused before the store's full REPLACE deletes the creator's
// existing rows.

// parseOptionalTargetID validates an optional target id. A nil pointer and an
// empty string both mean "no target" — that is how the composer sends a card
// that points at a URL instead of an id. Anything else must be a real uuid.
func parseOptionalTargetID(raw *string) (*uuid.UUID, bool) {
	if raw == nil || strings.TrimSpace(*raw) == "" {
		return nil, true
	}
	id, err := uuid.Parse(strings.TrimSpace(*raw))
	if err != nil {
		return nil, false
	}
	return &id, true
}

func invalidFieldMessage(field string, index int, key, why string) string {
	return fmt.Sprintf("%s[%d].%s %s", field, index, key, why)
}

func badRequest(c *gin.Context, code, message string) {
	api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, code, message, nil)
}

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

// writeVideoSeriesError is the ONE mapping for the series refusals, and it
// follows the same rule writeVideoAuthoringError documents: 404 for "no such
// row", 403 for "not yours", 409 for a conflict with a row that is already
// there. Anything unrecognised is a 500 — an unexpected error must never be
// reported as a merely-forbidden outcome.
func writeVideoSeriesError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, service.ErrVideoSeriesNotFound),
		errors.Is(err, service.ErrVideoSeriesEpisodeNotFound),
		// ErrPostNotInSeries covers "in a private series too": from a post
		// id, a 403 would confirm the private series exists. See
		// service.GetPostSeries.
		errors.Is(err, service.ErrPostNotInSeries):
		api.ErrorWithContext(c.Request.Context(), c.Writer,
			http.StatusNotFound, "NOT_FOUND", err.Error(), nil)
	case errors.Is(err, service.ErrNotVideoSeriesOwner),
		errors.Is(err, service.ErrVideoSeriesPrivate):
		api.ErrorWithContext(c.Request.Context(), c.Writer,
			http.StatusForbidden, "FORBIDDEN", err.Error(), nil)
	case errors.Is(err, service.ErrEpisodePostDuplicate):
		api.ErrorWithContext(c.Request.Context(), c.Writer,
			http.StatusConflict, "EPISODE_EXISTS", err.Error(), nil)
	case errors.Is(err, service.ErrVideoSeriesFull):
		api.ErrorWithContext(c.Request.Context(), c.Writer,
			http.StatusConflict, "SERIES_FULL", err.Error(), nil)
	case errors.Is(err, service.ErrVideoSeriesTitleRequired):
		api.ErrorWithContext(c.Request.Context(), c.Writer,
			http.StatusBadRequest, "INVALID_REQUEST", err.Error(), nil)
	default:
		api.ErrorWithContext(c.Request.Context(), c.Writer,
			http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
	}
}

// MaxEpisodeNum is the largest episode number a creator can assign. The
// number is a label rendered as "Episode N", and three digits is as wide as
// the product's layouts allow; anything larger is a typo (a year, a
// timestamp) rather than intent. It is a shape check on the input, so it
// belongs here with the other 400s, unlike postgres.MaxSeriesEpisodes, which
// is a count and has to be read inside the store's transaction.
const MaxEpisodeNum = 999

// GetPostSeries is GET /v1/posts/:postId/series: the watch page's "which
// series is this, and what comes before and after" in one read. Open to
// anonymous callers like the other series reads; the caller identity only
// decides whether private series and unpublished episodes are visible.
func (h *Handler) GetPostSeries(c *gin.Context) {
	postID, err := uuid.Parse(c.Param("postId"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid post ID", nil)
		return
	}
	view, err := h.svc.GetPostSeries(c.Request.Context(), postID, optionalCallerID(c))
	if err != nil {
		writeVideoSeriesError(c, err)
		return
	}
	api.JSON(c.Writer, http.StatusOK, view, nil)
}

type updateVideoSeriesRequest struct {
	Title         *string `json:"title"`
	Description   *string `json:"description"`
	IsComplete    *bool   `json:"is_complete"`
	IsPublic      *bool   `json:"is_public"`
	CoverMediaID  *string `json:"cover_media_id"`
	TrailerPostID *string `json:"trailer_post_id"`
	ChannelID     *string `json:"channel_id"`
}

// UpdateVideoSeries is PATCH /v1/video-series/:seriesId. Any subset of the
// fields; a field left out is left alone.
//
// Unlike CreateVideoSeries, which drops an unparseable id on the floor and
// creates the series without it, an unparseable id here is a 400. On create
// the id is an optional extra; on a patch it is the whole request, and a
// silent drop would answer 200 to an edit that did not happen.
func (h *Handler) UpdateVideoSeries(c *gin.Context) {
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
	var req updateVideoSeriesRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_REQUEST", err.Error(), nil)
		return
	}

	patch := postgres.VideoSeriesPatch{
		Title:       req.Title,
		Description: req.Description,
		IsComplete:  req.IsComplete,
		IsPublic:    req.IsPublic,
	}
	for _, f := range []struct {
		name string
		raw  *string
		dst  **uuid.UUID
	}{
		{"cover_media_id", req.CoverMediaID, &patch.CoverMediaID},
		{"trailer_post_id", req.TrailerPostID, &patch.TrailerPostID},
		{"channel_id", req.ChannelID, &patch.ChannelID},
	} {
		if f.raw == nil {
			continue
		}
		id, err := uuid.Parse(*f.raw)
		if err != nil {
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID",
				f.name+" must be a UUID", nil)
			return
		}
		*f.dst = &id
	}

	vs, err := h.svc.UpdateVideoSeries(c.Request.Context(), userID, seriesID, patch)
	if err != nil {
		writeVideoSeriesError(c, err)
		return
	}
	api.JSON(c.Writer, http.StatusOK, vs, nil)
}

// optionalCallerID reads X-User-Id when it is present and parseable. The
// series reads are open to anonymous callers, so a missing header is not an
// error — it is simply "not the owner". Same shape GetPlaylist uses.
func optionalCallerID(c *gin.Context) *uuid.UUID {
	if raw := c.GetHeader("X-User-Id"); raw != "" {
		if id, err := uuid.Parse(raw); err == nil {
			return &id
		}
	}
	return nil
}

func (h *Handler) GetVideoSeries(c *gin.Context) {
	seriesID, err := uuid.Parse(c.Param("seriesId"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid series ID", nil)
		return
	}
	// is_public was written at create time and never read here: a private
	// series answered its full body to any caller, token or not.
	vs, err := h.svc.GetVideoSeries(c.Request.Context(), seriesID, optionalCallerID(c))
	if err != nil {
		writeVideoSeriesError(c, err)
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
	// Behind the same gate as the series itself.
	eps, err := h.svc.GetVideoSeriesEpisodes(c.Request.Context(), seriesID, optionalCallerID(c))
	if err != nil {
		writeVideoSeriesError(c, err)
		return
	}
	if eps == nil {
		eps = []postgres.VideoSeriesEpisode{}
	}
	api.JSON(c.Writer, http.StatusOK, eps, nil)
}

// DeleteVideoSeries — DELETE /v1/video-series/:seriesId. The creator's own
// series only; 403 for anyone else, 404 for a series that is not there.
func (h *Handler) DeleteVideoSeries(c *gin.Context) {
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
	if err := h.svc.DeleteVideoSeries(c.Request.Context(), userID, seriesID); err != nil {
		writeVideoSeriesError(c, err)
		return
	}
	api.JSON(c.Writer, http.StatusNoContent, nil, nil)
}

// DeleteVideoSeriesEpisode — DELETE /v1/video-series/:seriesId/episodes/:episodeRef.
//
// One route, two spellings of the reference: an episode NUMBER ("3") or the
// POST ID of the episode. Gin cannot route on the shape of a path segment, so
// the choice is made here rather than by registering two conflicting routes —
// and both are needed, because the creator tools hold post ids while the
// watch page and the API contract talk in episode numbers.
//
// Removing an episode leaves a GAP in the numbering; it does not renumber the
// episodes after it. That decision is argued in
// internal/service/video_series.go.
func (h *Handler) DeleteVideoSeriesEpisode(c *gin.Context) {
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

	ref := c.Param("episodeRef")
	ctx := c.Request.Context()
	switch {
	case isEpisodeNumber(ref):
		num, _ := strconv.Atoi(ref)
		err = h.svc.DeleteVideoSeriesEpisodeByNum(ctx, userID, seriesID, num)
	default:
		postID, perr := uuid.Parse(ref)
		if perr != nil {
			api.ErrorWithContext(ctx, c.Writer, http.StatusBadRequest, "INVALID_ID",
				"episode reference must be an episode number or a post id", nil)
			return
		}
		err = h.svc.DeleteVideoSeriesEpisodeByPost(ctx, userID, seriesID, postID)
	}
	if err != nil {
		writeVideoSeriesError(c, err)
		return
	}
	api.JSON(c.Writer, http.StatusNoContent, nil, nil)
}

// isEpisodeNumber reports whether the path segment is an episode number
// rather than a post id. Digits only — a uuid never is.
func isEpisodeNumber(ref string) bool {
	if ref == "" {
		return false
	}
	for _, r := range ref {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
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

	if req.EpisodeNum < 1 || req.EpisodeNum > MaxEpisodeNum {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_REQUEST",
			fmt.Sprintf("episode_num must be between 1 and %d", MaxEpisodeNum), nil)
		return
	}

	ep, err := h.svc.AddEpisodeToVideoSeries(c.Request.Context(), userID, seriesID, postID, req.EpisodeNum, req.Title)
	if err != nil {
		// Was a string comparison on err.Error(); the sentinels carry the
		// same messages, so the wire contract is unchanged and the duplicate
		// case (409) now has somewhere to go.
		writeVideoSeriesError(c, err)
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
	// A stranger's copy of this list omits the creator's private series; the
	// creator's own copy is complete. See DECISION 2 in
	// internal/service/video_series.go.
	series, err := h.svc.ListVideoSeriesByCreator(c.Request.Context(), creatorID, optionalCallerID(c), limit, offset)
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
		writeVideoAuthoringError(c, err)
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
		writeVideoAuthoringError(c, err)
		return
	}
	api.JSON(c.Writer, http.StatusNoContent, nil, nil)
}

type addPlaylistItemRequest struct {
	PostID   string `json:"post_id" binding:"required"`
	Position int    `json:"position"`
}

func (h *Handler) AddPlaylistItem(c *gin.Context) {
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
	if err := h.svc.AddPlaylistItem(c.Request.Context(), userID, playlistID, postID, req.Position); err != nil {
		writeVideoAuthoringError(c, err)
		return
	}
	api.JSON(c.Writer, http.StatusCreated, gin.H{"playlist_id": playlistID, "post_id": postID, "position": req.Position}, nil)
}

func (h *Handler) RemovePlaylistItem(c *gin.Context) {
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
	postID, err := uuid.Parse(c.Param("postId"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid post ID", nil)
		return
	}
	if err := h.svc.RemovePlaylistItem(c.Request.Context(), userID, playlistID, postID); err != nil {
		writeVideoAuthoringError(c, err)
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
	// Same visibility rule as GET /v1/playlists/:playlistId — a private
	// playlist's contents are its creator's alone.
	var callerID *uuid.UUID
	if raw := c.GetHeader("X-User-Id"); raw != "" {
		if id, err := uuid.Parse(raw); err == nil {
			callerID = &id
		}
	}
	items, err := h.svc.GetPlaylistItems(c.Request.Context(), playlistID, callerID)
	if err != nil {
		writeVideoAuthoringError(c, err)
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
	userID, err := uuid.Parse(c.GetHeader("X-User-Id"))
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
		// The source enum. Empty means "unset" and the store writes
		// 'manual'; anything else outside the CHECK is a 400 here rather
		// than a constraint violation reported as a 500.
		if ch.Source != "" && !validChapterSource[ch.Source] {
			badRequest(c, "INVALID_SOURCE",
				invalidEnumMessage("chapters", i, "source", ch.Source, chapterSourceList))
			return
		}
		chapters[i] = postgres.MediaChapter{
			PostID:       postID,
			ChapterIndex: ch.ChapterIndex,
			Title:        ch.Title,
			StartMs:      ch.StartMs,
			ThumbnailURL: ch.ThumbnailURL,
			Source:       ch.Source,
		}
	}

	if err := h.svc.SaveChapters(c.Request.Context(), userID, postID, chapters); err != nil {
		writeVideoAuthoringError(c, err)
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
	userID, err := uuid.Parse(c.GetHeader("X-User-Id"))
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
		// Validated here, not left to the video_end_screens CHECK: a bad
		// value used to reach Postgres and come back as a 500 with a
		// constraint name in it. The list differs from the cards one on
		// purpose (channel_subscribe here, poll there) — see
		// migrations/012_posttube_features.sql.
		if !validEndScreenTypes[sc.Type] {
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_TYPE",
				invalidEnumMessage("screens", i, "type", sc.Type, endScreenTypeList), nil)
			return
		}
		targetID, ok := parseOptionalTargetID(sc.TargetID)
		if !ok {
			badRequest(c, "INVALID_TARGET_ID",
				invalidFieldMessage("screens", i, "target_id", "must be a UUID"))
			return
		}
		if sc.StartMs < 0 || sc.EndMs < 0 {
			badRequest(c, "INVALID_TIMING",
				invalidFieldMessage("screens", i, "start_ms/end_ms", "must not be negative"))
			return
		}
		// An end screen is a window. One that closes before — or exactly
		// when — it opens can never be shown, so it is a mistake, not a
		// preference.
		if sc.EndMs <= sc.StartMs {
			badRequest(c, "INVALID_TIMING",
				invalidFieldMessage("screens", i, "end_ms", "must be greater than start_ms"))
			return
		}
		screens[i] = postgres.EndScreen{
			PostID:    postID,
			Type:      sc.Type,
			TargetID:  targetID,
			TargetURL: sc.TargetURL,
			Title:     sc.Title,
			Position:  sc.Position,
			StartMs:   sc.StartMs,
			EndMs:     sc.EndMs,
		}
	}

	if err := h.svc.SaveEndScreens(c.Request.Context(), userID, postID, screens); err != nil {
		writeVideoAuthoringError(c, err)
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
	userID, err := uuid.Parse(c.GetHeader("X-User-Id"))
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
		// See SaveEndScreens: same reason, deliberately different list
		// (cards have poll, end screens have channel_subscribe).
		if !validVideoCardTypes[card.Type] {
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_TYPE",
				invalidEnumMessage("cards", i, "type", card.Type, videoCardTypeList), nil)
			return
		}
		// title is NOT NULL in the table but "" satisfies that, so a card
		// with no title stored an empty string and the player drew an empty
		// box. A card is a label on a link; without one there is nothing to
		// render.
		if strings.TrimSpace(card.Title) == "" {
			badRequest(c, "INVALID_TITLE",
				invalidFieldMessage("cards", i, "title", "is required and must not be blank"))
			return
		}
		targetID, ok := parseOptionalTargetID(card.TargetID)
		if !ok {
			badRequest(c, "INVALID_TARGET_ID",
				invalidFieldMessage("cards", i, "target_id", "must be a UUID"))
			return
		}
		if card.AppearAtMs < 0 {
			badRequest(c, "INVALID_TIMING",
				invalidFieldMessage("cards", i, "appear_at_ms", "must not be negative"))
			return
		}
		cards[i] = postgres.VideoCard{
			PostID:     postID,
			Type:       card.Type,
			TargetID:   targetID,
			TargetURL:  card.TargetURL,
			Title:      card.Title,
			TeaserText: card.TeaserText,
			AppearAtMs: card.AppearAtMs,
		}
	}

	if err := h.svc.SaveVideoCards(c.Request.Context(), userID, postID, cards); err != nil {
		writeVideoAuthoringError(c, err)
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

// saveWatchProgressRequest is the Tube contract (2026-09-05):
// {"position_ms": int, "duration_ms": int, "completed": bool}. duration_ms
// may be 0 when the player has not learned it yet; the server then falls
// back to the duration it already knows (video_metadata, else the viewer's
// previous progress row) so percent_watched is real rather than 0%, and the
// store keeps the last known value. completed=true is honored as sent;
// otherwise it is derived from the 90% rule.
type saveWatchProgressRequest struct {
	PositionMs int   `json:"position_ms"`
	DurationMs int   `json:"duration_ms"`
	Completed  *bool `json:"completed"`
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
	if req.PositionMs < 0 || req.DurationMs < 0 {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_REQUEST", "position_ms and duration_ms must not be negative", nil)
		return
	}

	known := 0
	if req.DurationMs == 0 {
		known = h.knownWatchDurationMs(c.Request.Context(), userID, postID)
	}
	durationMs := effectiveWatchDurationMs(req.DurationMs, known)
	pct, completed := watchProgressState(req.PositionMs, durationMs, req.Completed)

	wp := &postgres.WatchProgress{
		UserID:         userID,
		PostID:         postID,
		PositionMs:     req.PositionMs,
		DurationMs:     durationMs,
		PercentWatched: pct,
		Completed:      completed,
	}

	if err := h.svc.SaveWatchProgress(c.Request.Context(), wp); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, wp, nil)
}

// knownWatchDurationMs is the duration the server already holds for a post
// when the player sent duration_ms: 0 — the transcode measurement on
// video_metadata first, else what this viewer's last progress report
// carried. 0 when neither knows (transcode pending, first report).
func (h *Handler) knownWatchDurationMs(ctx context.Context, userID, postID uuid.UUID) int {
	if vm, err := h.svc.GetVideoDetail(ctx, postID); err == nil && vm != nil && vm.DurationSeconds > 0 {
		return int(vm.DurationSeconds * 1000)
	}
	if prev, err := h.svc.GetWatchProgress(ctx, userID, postID); err == nil && prev != nil && prev.DurationMs > 0 {
		return prev.DurationMs
	}
	return 0
}

// effectiveWatchDurationMs picks the duration percent_watched is computed
// against: what the player sent when it knows, else what the server knows.
func effectiveWatchDurationMs(sentMs, knownMs int) int {
	if sentMs > 0 {
		return sentMs
	}
	return knownMs
}

// watchProgressState derives percent_watched and completed from the
// position and the duration in effect (the client's, or the server's known
// one when the client sent 0). An explicit completed=true from the player
// wins (it saw the end); otherwise 90% of a known duration counts as
// finished, and an unknown duration never does.
func watchProgressState(positionMs, durationMs int, explicit *bool) (pct float32, completed bool) {
	if durationMs > 0 {
		pct = float32(positionMs) / float32(durationMs) * 100
		if pct > 100 {
			pct = 100
		}
	}
	if explicit != nil && *explicit {
		return pct, true
	}
	return pct, pct >= 90.0
}

// GetWatchProgress — GET /v1/videos/:videoId/progress. 404 when the viewer
// has never reported progress on the post.
func (h *Handler) GetWatchProgress(c *gin.Context) {
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
	wp, err := h.svc.GetWatchProgress(c.Request.Context(), userID, postID)
	if err != nil {
		if errors.Is(err, service.ErrWatchProgressNotFound) {
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusNotFound, "NOT_FOUND", "No watch progress for this video", nil)
			return
		}
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
		items = []service.ContinueWatchingItem{}
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
