package http

import (
	"encoding/base64"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"

	"github.com/atpost/feed-service/internal/service"
	"github.com/atpost/shared/api"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// GET /v1/feed/videos/:postId/related — what should play after this video.
//
// Shaped like the other Tube surfaces on purpose: the same `limit` bounds,
// the same opaque base64 `cursor` query parameter, the same
// `{data: [...], meta: {next_cursor}}` envelope of hydrated posts, the
// same X-Feed-Surface header, and the same refusal to fall back to raw
// identifiers when hydration fails. A client that can render /v1/feed/videos
// can render this with no new code.
//
// WHY THE CURSOR IS NOT THE SAME KIND OF CURSOR
//
// The ranked feed surfaces carry a Scylla timeuuid: their pages are
// windows over a timeline, and the token names the exact row to resume
// after. A related list has no timeline. It is a bounded pool assembled
// per request from post-service and ordered by relevance to the video
// being watched, so the only meaningful place to resume is a position in
// that ordering.
//
// The token is therefore an offset, and it is still opaque and versioned
// so it cannot be confused with a feed cursor and can be changed later
// without breaking clients. The honest consequence: because the pool is
// re-assembled and re-ranked per request, a video published between two
// page requests can shift the ordering slightly, so deep paging is
// approximate. That is an acceptable trade for a surface people read one
// or two pages of, and it is the same trade every relevance-ordered list
// makes; it would not be acceptable on a feed, which is why the feeds
// keep their timeline cursors.

const relatedCursorPrefix = "v1r:"

// relatedPageParams parses limit and the offset cursor. Limit bounds match
// rankedPageParams exactly (default 20, cap 50) so the two surfaces cannot
// disagree about what a page is.
func relatedPageParams(c *gin.Context) (limit, offset int, err error) {
	limit, err = strconv.Atoi(c.DefaultQuery("limit", "20"))
	if err != nil || limit <= 0 {
		limit = 20
	}
	if limit > 50 {
		limit = 50
	}
	raw := c.Query("cursor")
	if raw == "" {
		return limit, 0, nil
	}
	decoded, decodeErr := base64.RawURLEncoding.DecodeString(raw)
	if decodeErr != nil {
		return 0, 0, fmt.Errorf("invalid related feed cursor")
	}
	body, ok := strings.CutPrefix(string(decoded), relatedCursorPrefix)
	if !ok {
		return 0, 0, fmt.Errorf("invalid related feed cursor")
	}
	// A negative or unparseable offset is a malformed cursor, not a
	// request for the first page: silently serving page one would hide a
	// client bug behind a plausible-looking response.
	parsed, parseErr := strconv.Atoi(body)
	if parseErr != nil || parsed < 0 {
		return 0, 0, fmt.Errorf("invalid related feed cursor")
	}
	return limit, parsed, nil
}

func relatedPageMeta(next int) *api.Meta {
	if next <= 0 {
		return nil
	}
	token := base64.RawURLEncoding.EncodeToString([]byte(relatedCursorPrefix + strconv.Itoa(next)))
	return &api.Meta{NextCursor: token}
}

// GetRelatedVideos handles GET /v1/feed/videos/:postId/related.
func (h *Handler) GetRelatedVideos(c *gin.Context) {
	userID, err := uuid.Parse(c.GetHeader("X-User-Id"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Invalid user ID", nil)
		return
	}
	postID, err := uuid.Parse(c.Param("postId"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid post ID", nil)
		return
	}
	limit, offset, err := relatedPageParams(c)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_CURSOR", err.Error(), nil)
		return
	}

	posts, next, err := h.svc.GetRelatedVideos(c.Request.Context(), userID, postID, limit, offset)
	switch {
	case errors.Is(err, service.ErrFeedbackPostNotFound):
		// The seed does not exist, or this viewer cannot see it. One
		// answer for both, so the endpoint cannot be used to probe for
		// the existence of posts the caller has no right to.
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusNotFound, "NOT_FOUND", "Post not found", nil)
		return
	case errors.Is(err, service.ErrRelatedUnsupported):
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "UNSUPPORTED_CONTENT_TYPE",
			"Related videos are only available for video posts", nil)
		return
	case err != nil:
		// Same policy as every other surface that returns other people's
		// content: no degraded response made of raw identifiers.
		log.Printf("related videos failed for post %s: %v", postID, err)
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusServiceUnavailable,
			"FEED_UNAVAILABLE", "Related videos are temporarily unavailable", nil)
		return
	}

	if posts == nil {
		posts = []service.HydratedPost{}
	}
	c.Writer.Header().Set("X-Feed-Surface", "related")
	api.JSON(c.Writer, http.StatusOK, posts, relatedPageMeta(next))
}
