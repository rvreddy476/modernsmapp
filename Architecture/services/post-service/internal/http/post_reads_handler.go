// post_reads_handler.go holds the post read routes: categories, a single
// post, recent posts, live recordings, an author's posts and counts, the
// batch read, pinning, and the content_type filter parser they share.
// Moved out of handler.go unchanged.

package http

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/atpost/post-service/internal/service"
	"github.com/atpost/post-service/internal/store/postgres"
	"github.com/atpost/shared/api"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// ListCategories serves the ONE video taxonomy so no composer hardcodes it.
// GET /v1/posts/categories — static, no viewer state, safe to cache.
//
// Shape (2026-09-27): a plain list of {id, slug, label, kind}. The list was
// {id, label} for flicks alone; `slug` (= id) and `kind` (all | short |
// long) are additive, and the 18 original entries keep their ids and
// order with kind "all", followed by the long-video entries. A reel
// composer that predates `kind` reads the same objects it always did; the
// Tube studio filters on kind in ("all", "long"). ?kind=short|long narrows
// the list server-side for clients that prefer that.
func (h *Handler) ListCategories(c *gin.Context) {
	switch strings.ToLower(strings.TrimSpace(c.Query("kind"))) {
	case "short":
		api.JSON(c.Writer, http.StatusOK, service.FlickCategories(), nil)
	case "long":
		api.JSON(c.Writer, http.StatusOK, service.LongVideoCategories(), nil)
	default:
		api.JSON(c.Writer, http.StatusOK, service.VideoCategories(), nil)
	}
}

func (h *Handler) GetPost(c *gin.Context) {
	postIDStr := c.Param("postId")
	postID, err := uuid.Parse(postIDStr)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid post ID", nil)
		return
	}

	viewerID := c.GetHeader("X-User-Id")
	var viewerUUID *uuid.UUID
	if id, err := uuid.Parse(viewerID); err == nil {
		viewerUUID = &id
	}

	p, err := h.svc.GetPost(c.Request.Context(), postID, viewerUUID)
	if err != nil {
		// Age-restricted posts (2026-09-28): 401 / 403 with the age codes.
		if writeReadGateError(c, err) {
			return
		}
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	if p == nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusNotFound, "NOT_FOUND", "Post not found", nil)
		return
	}

	// Tier 3c: members-only gating. If the post is gated and the
	// viewer isn't entitled (or there is no viewer), redact the body
	// fields so the response can render as a "subscribe to view"
	// preview rather than 404 or 403. The one rule every list shares
	// (service/members_read.go): author and members in full, everyone
	// else redacted, a failed lookup redacted.
	h.svc.RedactDetailForViewer(c.Request.Context(), viewerUUID, p)

	api.JSON(c.Writer, http.StatusOK, p, nil)
}

func (h *Handler) GetRecentPosts(c *gin.Context) {
	cursor := c.DefaultQuery("cursor", "")
	limit := 20
	if l, err := strconv.Atoi(c.DefaultQuery("limit", "20")); err == nil && l > 0 {
		limit = l
	}

	// Optional content_type filter: comma-separated, legacy spellings
	// accepted ("video" -> long_video). An unknown type is a 400 rather
	// than a silently unfiltered page.
	contentTypes, err := parseContentTypeFilter(c.Query("content_type"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_CONTENT_TYPE", err.Error(), nil)
		return
	}

	// Optional category filter (Tube, 2026-09-05): exact taxonomy id. A
	// malformed value is a 400; an id no post carries is an empty page —
	// see service.NormalizeCategoryFilter for why the taxonomy is not
	// consulted here.
	category, err := service.NormalizeCategoryFilter(c.Query("category"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_CATEGORY", err.Error(), nil)
		return
	}

	var viewerID *uuid.UUID
	if v := c.GetHeader("X-User-Id"); v != "" {
		if id, err := uuid.Parse(v); err == nil {
			viewerID = &id
		}
	}

	posts, nextCursor, err := h.svc.GetRecentPosts(c.Request.Context(), viewerID, nil, contentTypes, category, limit, cursor)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	if posts == nil {
		posts = []service.PostDetail{}
	}

	var meta *api.Meta
	if nextCursor != "" {
		meta = &api.Meta{NextCursor: nextCursor}
	}

	api.JSON(c.Writer, http.StatusOK, posts, meta)
}

// GetLiveRecordings — GET /v1/posts/live-recordings?limit=&cursor=
//
// The public "recorded streams" shelf (MTube, 2026-09-27): promoted live
// recordings that are public and approved, newest first, in the same
// hydrated row shape as /by-author. The query is validated here so a bad
// limit or cursor is a 400 before the store is touched, rather than the
// silent default the older listings apply.
func (h *Handler) GetLiveRecordings(c *gin.Context) {
	limit := 20
	if raw := c.Query("limit"); raw != "" {
		l, err := strconv.Atoi(raw)
		if err != nil || l <= 0 || l > 50 {
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_REQUEST", "limit must be an integer between 1 and 50", nil)
			return
		}
		limit = l
	}
	cursor := c.Query("cursor")
	if cursor != "" {
		if _, err := time.Parse(time.RFC3339Nano, cursor); err != nil {
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_REQUEST", "cursor is not a value this endpoint issued", nil)
			return
		}
	}

	var viewerID *uuid.UUID
	if v := c.GetHeader("X-User-Id"); v != "" {
		if id, err := uuid.Parse(v); err == nil {
			viewerID = &id
		}
	}

	posts, nextCursor, err := h.svc.GetLiveRecordings(c.Request.Context(), viewerID, limit, cursor)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	if posts == nil {
		posts = []service.PostDetail{}
	}
	var meta *api.Meta
	if nextCursor != "" {
		meta = &api.Meta{NextCursor: nextCursor}
	}
	api.JSON(c.Writer, http.StatusOK, posts, meta)
}

func (h *Handler) GetPostsByAuthor(c *gin.Context) {
	authorIDStr := c.Param("authorId")
	authorID, err := uuid.Parse(authorIDStr)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid author ID", nil)
		return
	}

	contentType := c.DefaultQuery("type", "")
	cursor := c.DefaultQuery("cursor", "")
	limit := 20
	if l, err := strconv.Atoi(c.DefaultQuery("limit", "20")); err == nil && l > 0 {
		limit = l
	}
	// ?sort=latest|popular|oldest (T-21). Absent is latest, the old order.
	sort, err := postgres.ParseAuthorPostSort(c.Query("sort"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_SORT", "sort must be latest, popular or oldest", nil)
		return
	}

	// ?members_only=true (2026-10-23): only the author's members-only posts,
	// redacted for non-members like every other row. Absent or false is the
	// whole grid; any other value is 400.
	membersOnly, ok := parseMembersOnlyParam(c.Query("members_only"))
	if !ok {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_REQUEST", "members_only must be true or false", nil)
		return
	}

	var viewerID *uuid.UUID
	if v := c.GetHeader("X-User-Id"); v != "" {
		if id, err := uuid.Parse(v); err == nil {
			viewerID = &id
		}
	}

	posts, nextCursor, err := h.svc.GetPostsByAuthorFiltered(c.Request.Context(), service.AuthorListFilter{
		AuthorID: authorID, ContentType: contentType, Sort: sort, Limit: limit, Cursor: cursor, MembersOnly: membersOnly,
	}, viewerID)
	if errors.Is(err, postgres.ErrInvalidAuthorCursor) {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_CURSOR", "cursor is not valid for this sort", nil)
		return
	}
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	if posts == nil {
		posts = []service.PostDetail{}
	}

	var meta *api.Meta
	if nextCursor != "" {
		meta = &api.Meta{NextCursor: nextCursor}
	}

	api.JSON(c.Writer, http.StatusOK, posts, meta)
}

func (h *Handler) GetAuthorCounts(c *gin.Context) {
	authorIDStr := c.Param("authorId")
	authorID, err := uuid.Parse(authorIDStr)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid author ID", nil)
		return
	}

	// Same viewer rule as the listing beside it (security sweep #24): a
	// stranger to a private account, or a viewer the author blocked, reads
	// the counts of a profile they cannot see — zeros.
	var viewerID *uuid.UUID
	if v := c.GetHeader("X-User-Id"); v != "" {
		if id, err := uuid.Parse(v); err == nil {
			viewerID = &id
		}
	}
	counts, err := h.svc.GetAuthorCountsForViewer(c.Request.Context(), authorID, viewerID)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}

	api.JSON(c.Writer, http.StatusOK, counts, nil)
}

type PinRequest struct {
	Pinned bool `json:"pinned"`
}

func (h *Handler) TogglePin(c *gin.Context) {
	userIDStr := c.GetHeader("X-User-Id")
	userID, err := uuid.Parse(userIDStr)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Invalid user ID", nil)
		return
	}

	postIDStr := c.Param("postId")
	postID, err := uuid.Parse(postIDStr)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid post ID", nil)
		return
	}

	var req PinRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_REQUEST", err.Error(), nil)
		return
	}

	if err := h.svc.TogglePin(c.Request.Context(), postID, userID, req.Pinned); err != nil {
		if errors.Is(err, service.ErrPinLimit) {
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusConflict, "PIN_LIMIT",
				"You can pin up to 3 posts. Unpin one to pin this.", map[string]int{"max": service.MaxPinnedPosts})
			return
		}
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", "Internal server error", nil)
		return
	}

	api.JSON(c.Writer, http.StatusOK, map[string]bool{"pinned": req.Pinned}, nil)
}

type BatchGetPostsRequest struct {
	IDs []string `json:"ids" binding:"required"`
}

func (h *Handler) BatchGetPosts(c *gin.Context) {
	var req BatchGetPostsRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_REQUEST", err.Error(), nil)
		return
	}

	if len(req.IDs) == 0 {
		api.JSON(c.Writer, http.StatusOK, map[string]interface{}{}, nil)
		return
	}

	if len(req.IDs) > 100 {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_REQUEST", "Maximum 100 IDs per request", nil)
		return
	}

	ids := make([]uuid.UUID, 0, len(req.IDs))
	for _, idStr := range req.IDs {
		id, err := uuid.Parse(idStr)
		if err != nil {
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid post ID: "+idStr, nil)
			return
		}
		ids = append(ids, id)
	}

	viewerID := c.GetHeader("X-User-Id")
	var viewerUUID *uuid.UUID
	if id, err := uuid.Parse(viewerID); err == nil {
		viewerUUID = &id
	}

	result, err := h.svc.GetPostsByIDs(c.Request.Context(), ids, viewerUUID)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}

	// Convert uuid.UUID keys to strings for JSON serialization
	data := make(map[string]*service.PostDetail, len(result))
	for id, detail := range result {
		data[id.String()] = detail
	}

	api.JSON(c.Writer, http.StatusOK, data, nil)
}

// parseContentTypeFilter turns "?content_type=long_video,flick" into the
// canonical list the store filters on. Empty input means no filter.
func parseContentTypeFilter(raw string) ([]string, error) {
	if strings.TrimSpace(raw) == "" {
		return nil, nil
	}
	var out []string
	seen := map[string]bool{}
	for _, part := range strings.Split(raw, ",") {
		ct, ok := service.CanonicalContentType(strings.TrimSpace(part))
		if !ok {
			return nil, fmt.Errorf("unknown content_type %q", strings.TrimSpace(part))
		}
		if !seen[ct] {
			seen[ct] = true
			out = append(out, ct)
		}
	}
	return out, nil
}
