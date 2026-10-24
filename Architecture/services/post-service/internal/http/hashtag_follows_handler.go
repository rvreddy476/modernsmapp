package http

import (
	"errors"
	"net/http"

	"github.com/atpost/post-service/internal/service"
	"github.com/atpost/shared/api"
	"github.com/gin-gonic/gin"
)

/*
	Followed hashtags (2026-10-24, service/hashtag_follows.go). Signed-in
	only (X-User-Id).

	POST   /v1/hashtags/:tag/follow     200 data: {tag, display_name, following:true}   (idempotent)
	DELETE /v1/hashtags/:tag/follow     200 data: {tag, display_name, following:false}  (idempotent)
	GET    /v1/hashtags/:tag/follow     200 data: {tag, display_name, following}
	GET    /v1/hashtags/followed?limit=&cursor=
	       200 data: [{tag, display_name, followed_at}], meta.next_cursor
	GET    /v1/hashtags/followed/posts?limit=&cursor=
	       200 data: [post detail, as GET /v1/hashtags/:tag/posts], meta.next_cursor

	:tag may carry a leading # (sent as %23); it is normalised exactly as a
	post's hashtags are. Errors: 400 INVALID_HASHTAG, 404 HASHTAG_UNAVAILABLE
	(blocked by moderation; follow only), 409 HASHTAG_FOLLOW_LIMIT (500),
	401 UNAUTHORIZED.
*/

func writeHashtagFollowError(c *gin.Context, err error) {
	ctx := c.Request.Context()
	switch {
	case errors.Is(err, service.ErrInvalidHashtag), errors.Is(err, service.ErrTooManyHashtags):
		api.ErrorWithContext(ctx, c.Writer, http.StatusBadRequest, "INVALID_HASHTAG",
			"A hashtag is 1-50 letters, digits or underscores", nil)
	case errors.Is(err, service.ErrHashtagUnavailable):
		api.ErrorWithContext(ctx, c.Writer, http.StatusNotFound, "HASHTAG_UNAVAILABLE", "This hashtag is unavailable", nil)
	case errors.Is(err, service.ErrHashtagFollowLimit):
		api.ErrorWithContext(ctx, c.Writer, http.StatusConflict, "HASHTAG_FOLLOW_LIMIT", "You can follow at most 500 hashtags", nil)
	case errors.Is(err, service.ErrAuthoringStoreUnavailable):
		api.ErrorWithContext(ctx, c.Writer, http.StatusServiceUnavailable, "STORE_UNAVAILABLE", "Hashtags are unavailable right now", nil)
	default:
		api.ErrorWithContext(ctx, c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", "Internal server error", nil)
	}
}

// FollowHashtag is POST /v1/hashtags/:tag/follow.
func (h *Handler) FollowHashtag(c *gin.Context) {
	userID, ok := callerID(c)
	if !ok {
		return
	}
	out, err := h.svc.FollowHashtag(c.Request.Context(), userID, c.Param("tag"))
	if err != nil {
		writeHashtagFollowError(c, err)
		return
	}
	api.JSON(c.Writer, http.StatusOK, out, nil)
}

// UnfollowHashtag is DELETE /v1/hashtags/:tag/follow.
func (h *Handler) UnfollowHashtag(c *gin.Context) {
	userID, ok := callerID(c)
	if !ok {
		return
	}
	out, err := h.svc.UnfollowHashtag(c.Request.Context(), userID, c.Param("tag"))
	if err != nil {
		writeHashtagFollowError(c, err)
		return
	}
	api.JSON(c.Writer, http.StatusOK, out, nil)
}

// GetHashtagFollow is GET /v1/hashtags/:tag/follow.
func (h *Handler) GetHashtagFollow(c *gin.Context) {
	userID, ok := callerID(c)
	if !ok {
		return
	}
	out, err := h.svc.HashtagFollowStateFor(c.Request.Context(), userID, c.Param("tag"))
	if err != nil {
		writeHashtagFollowError(c, err)
		return
	}
	api.JSON(c.Writer, http.StatusOK, out, nil)
}

// ListFollowedHashtags is GET /v1/hashtags/followed.
func (h *Handler) ListFollowedHashtags(c *gin.Context) {
	userID, ok := callerID(c)
	if !ok {
		return
	}
	out, next, err := h.svc.ListFollowedHashtags(c.Request.Context(), userID, pageLimit(c.Query("limit"), 50, 100), c.Query("cursor"))
	if err != nil {
		writeHashtagFollowError(c, err)
		return
	}
	var meta *api.Meta
	if next != "" {
		meta = &api.Meta{NextCursor: next}
	}
	api.JSON(c.Writer, http.StatusOK, out, meta)
}

// GetFollowedHashtagPosts is GET /v1/hashtags/followed/posts.
func (h *Handler) GetFollowedHashtagPosts(c *gin.Context) {
	userID, ok := callerID(c)
	if !ok {
		return
	}
	posts, next, err := h.svc.FollowedHashtagPosts(c.Request.Context(), userID, pageLimit(c.Query("limit"), 20, 50), c.Query("cursor"))
	if err != nil {
		writeHashtagFollowError(c, err)
		return
	}
	if posts == nil {
		posts = []service.PostDetail{}
	}
	var meta *api.Meta
	if next != "" {
		meta = &api.Meta{NextCursor: next}
	}
	api.JSON(c.Writer, http.StatusOK, posts, meta)
}
