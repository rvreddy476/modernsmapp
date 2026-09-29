package http

import (
	"errors"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/atpost/post-service/internal/service"
	"github.com/atpost/shared/api"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// Original sounds on reels (2026-09-29, service/sounds.go).
//
// Both routes live under /v1/posts: the sound catalogue itself (/v1/audio)
// is media-service's, and this service registers nothing there
// (audio_catalogue_guard_test.go).

// requestedSound is the sound a create request asks for: its id, and where
// in it playback starts (audio_start_ms, 0 when absent). ok is false when
// the request names no sound, or something that is not a sound id. The
// start is held inside the sound when the link is made.
func requestedSound(req *CreatePostRequest) (id uuid.UUID, startMs int, ok bool) {
	if req == nil || req.AudioTrackID == nil || *req.AudioTrackID == "" {
		return uuid.Nil, 0, false
	}
	id, err := uuid.Parse(*req.AudioTrackID)
	if err != nil {
		return uuid.Nil, 0, false
	}
	if req.AudioStartMs != nil {
		startMs = *req.AudioStartMs
	}
	return id, startMs, true
}

// useSoundResponse is the `data` of POST /v1/posts/:postId/sound.
type useSoundResponse struct {
	Sound *service.PostSound `json:"sound"`
}

// writeSoundError answers the refusals and faults of the sound routes;
// false when err is none of them. A fault's own text stays in the log.
func writeSoundError(c *gin.Context, err error) bool {
	ctx := c.Request.Context()
	var refusal *service.SoundRefusal
	switch {
	case errors.Is(err, service.ErrSoundNotFound):
		api.ErrorWithContext(ctx, c.Writer, http.StatusNotFound, "NOT_FOUND", "Sound not found", nil)
	case errors.As(err, &refusal):
		api.ErrorWithContext(ctx, c.Writer, http.StatusUnprocessableEntity, refusal.Code, refusal.Message(), nil)
	case errors.Is(err, service.ErrSoundReuseNotAllowed):
		api.ErrorWithContext(ctx, c.Writer, http.StatusForbidden, "SOUND_REUSE_NOT_ALLOWED", "The creator has turned off reuse of this sound", nil)
	case errors.Is(err, service.ErrSoundRateLimited):
		api.ErrorWithContext(ctx, c.Writer, http.StatusTooManyRequests, "RATE_LIMITED", "Too many sounds requested, please slow down", nil)
	case errors.Is(err, service.ErrInvalidSoundCursor):
		api.ErrorWithContext(ctx, c.Writer, http.StatusBadRequest, "INVALID_CURSOR", "cursor is not a value this endpoint issued", nil)
	case errors.Is(err, service.ErrSoundUnavailable):
		slog.WarnContext(ctx, "sound unavailable", "path", c.FullPath(), "err", err)
		api.ErrorWithContext(ctx, c.Writer, http.StatusServiceUnavailable, "SOUND_UNAVAILABLE", "The sound is unavailable right now, try again", nil)
	default:
		return false
	}
	return true
}

// UseSound handles POST /v1/posts/:postId/sound ("use this sound").
//
// 200 {data: {sound}}: the sound the reel plays, made on first use.
// 401 UNAUTHORIZED signed out; 400 INVALID_ID; 404 NOT_FOUND for a post the
// viewer may not open (and the age gate's answers, exactly as
// GET /v1/posts/:postId gives them); 422 NOT_A_REEL | NOT_READY | TOO_LONG;
// 403 SOUND_REUSE_NOT_ALLOWED when the creator turned reuse off; 429
// RATE_LIMITED; 503 SOUND_UNAVAILABLE when media-service or a decision
// could not answer.
func (h *Handler) UseSound(c *gin.Context) {
	ctx := c.Request.Context()
	viewerID, err := uuid.Parse(c.GetHeader("X-User-Id"))
	if err != nil {
		api.ErrorWithContext(ctx, c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Sign in to use a sound", nil)
		return
	}
	postID, err := uuid.Parse(c.Param("postId"))
	if err != nil {
		api.ErrorWithContext(ctx, c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid post ID", nil)
		return
	}
	sound, err := h.svc.UseSound(ctx, viewerID, postID)
	if err != nil {
		if writeReadGateError(c, err) || writeSoundError(c, err) {
			return
		}
		slog.ErrorContext(ctx, "use sound failed", "post_id", postID, "err", err)
		api.ErrorWithContext(ctx, c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", "Could not use this sound", nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, useSoundResponse{Sound: sound}, nil)
}

// GetPostsBySound handles GET /v1/posts/by-sound/:soundId?limit=&cursor=.
//
// The reels that play a sound, newest first; the viewer is optional.
// 200 {data: {sound, origin, items}, meta: {next_cursor}}; 400 INVALID_ID,
// 400 INVALID_CURSOR; 404 NOT_FOUND for a sound that does not exist and,
// with the same body, for one this viewer may not hear; 503
// SOUND_UNAVAILABLE when that could not be decided. limit defaults to 24
// and is capped at 50.
func (h *Handler) GetPostsBySound(c *gin.Context) {
	ctx := c.Request.Context()
	soundID, err := uuid.Parse(c.Param("soundId"))
	if err != nil {
		api.ErrorWithContext(ctx, c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid sound ID", nil)
		return
	}
	// The service refuses a cursor it did not issue and resolves the limit.
	cursor := c.Query("cursor")
	limit, _ := strconv.Atoi(c.Query("limit"))

	var viewerID *uuid.UUID
	if v := c.GetHeader("X-User-Id"); v != "" {
		if id, err := uuid.Parse(v); err == nil {
			viewerID = &id
		}
	}

	page, nextCursor, err := h.svc.PostsBySound(ctx, viewerID, soundID, limit, cursor)
	if err != nil {
		if writeSoundError(c, err) {
			return
		}
		slog.ErrorContext(ctx, "posts by sound failed", "sound_id", soundID, "err", err)
		api.ErrorWithContext(ctx, c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", "Could not list the posts that play this sound", nil)
		return
	}
	if page.Items == nil {
		page.Items = []service.PostDetail{}
	}
	var meta *api.Meta
	if nextCursor != "" {
		meta = &api.Meta{NextCursor: nextCursor}
	}
	api.JSON(c.Writer, http.StatusOK, page, meta)
}
