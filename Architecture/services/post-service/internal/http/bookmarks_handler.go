// bookmarks_handler.go holds the bookmark and saved-item routes: add,
// remove, list and toggle a bookmark, and save / list / unsave an item.
// Moved out of handler.go unchanged.

package http

import (
	"net/http"
	"strconv"

	"github.com/atpost/post-service/internal/service"
	"github.com/atpost/post-service/internal/store/postgres"
	"github.com/atpost/shared/api"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

func (h *Handler) AddBookmark(c *gin.Context) {
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

	// Optional body {"collection_id": "<uuid>"} saves straight into one of
	// the caller's collections (2026-10-24); no body is the plain save,
	// which keeps an already-saved post where it is.
	collectionID, err := optionalCollectionBody(c)
	if err != nil {
		writeSavedCollectionError(c, err)
		return
	}
	if err := h.svc.AddBookmarkTo(c.Request.Context(), userID, postID, collectionID); err != nil {
		if isSavedCollectionError(err) {
			writeSavedCollectionError(c, err)
			return
		}
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}

	api.JSON(c.Writer, http.StatusOK, map[string]bool{"bookmarked": true}, nil)
}

func (h *Handler) RemoveBookmark(c *gin.Context) {
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

	if err := h.svc.RemoveBookmark(c.Request.Context(), userID, postID); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}

	api.JSON(c.Writer, http.StatusOK, map[string]bool{"bookmarked": false}, nil)
}

func (h *Handler) GetBookmarks(c *gin.Context) {
	userIDStr := c.GetHeader("X-User-Id")
	userID, err := uuid.Parse(userIDStr)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Invalid user ID", nil)
		return
	}

	cursor := c.DefaultQuery("cursor", "")
	// Clamped to 100, not reset to 20, so a client asking for more gets the
	// largest page we serve rather than silently the default.
	limit := pageLimit(c.Query("limit"), 20, 100)

	// Optional ?type= filter (Tube "You" page, 2026-09-12): the same values
	// and parsing as by-author's ?type= and recent's ?content_type=, legacy
	// spellings folded ("video" is long_video). Absent means every type;
	// an unknown value is a 400 rather than a silently unfiltered page.
	contentTypes, err := parseContentTypeFilter(c.Query("type"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_CONTENT_TYPE", err.Error(), nil)
		return
	}

	// Optional ?collection_id= (saved collections, 2026-10-24): "default"
	// is the default collection, a UUID one of the caller's collections
	// (404 COLLECTION_NOT_FOUND for anyone else's); absent is every saved
	// post.
	scope, err := h.svc.ResolveSavedScope(c.Request.Context(), userID, c.Query("collection_id"))
	if err != nil {
		writeSavedCollectionError(c, err)
		return
	}

	posts, nextCursor, err := h.svc.GetBookmarks(c.Request.Context(), userID, contentTypes, limit, cursor, scope)
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

func (h *Handler) ToggleBookmark(c *gin.Context) {
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

	result, err := h.svc.ToggleBookmarkNew(c.Request.Context(), postID, userID)
	if err != nil {
		switch err.Error() {
		case "RATE_LIMITED":
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusTooManyRequests, "RATE_LIMITED", "Too many bookmark toggles, please slow down", nil)
		default:
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		}
		return
	}

	api.JSON(c.Writer, http.StatusOK, result, nil)
}

// ============================================================
// Saved Item Handlers
// ============================================================

type SaveItemRequest struct {
	TargetType string `json:"target_type" binding:"required,oneof=post video reel"`
	TargetID   string `json:"target_id" binding:"required"`
	// CollectionID (2026-10-24) saves into one of the caller's collections;
	// it wins over CollectionName. CollectionName is the pre-collections
	// field: an existing name (case-insensitive) is that collection, a new
	// one is created under the collection rules (1-40 characters, at most
	// 50), "" / "All Saved" is the default collection.
	CollectionID   *string `json:"collection_id"`
	CollectionName string  `json:"collection_name"`
}

func (h *Handler) SaveItem(c *gin.Context) {
	userIDStr := c.GetHeader("X-User-Id")
	userID, err := uuid.Parse(userIDStr)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Invalid user ID", nil)
		return
	}

	var req SaveItemRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_REQUEST", err.Error(), nil)
		return
	}

	targetID, err := uuid.Parse(req.TargetID)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_REQUEST", "Invalid target ID", nil)
		return
	}

	// The saved post must be one the caller may open (read_gate.go, 2026-09-29).
	if !h.requirePostReadable(c, targetID, &userID) {
		return
	}
	var collectionID *uuid.UUID
	if req.CollectionID != nil {
		if collectionID, err = parseCollectionRef(*req.CollectionID); err != nil {
			writeSavedCollectionError(c, err)
			return
		}
	}
	item, err := h.svc.SaveItem(c.Request.Context(), userID, req.TargetType, targetID, collectionID, req.CollectionName)
	if err != nil {
		writeSavedCollectionError(c, err)
		return
	}

	api.JSON(c.Writer, http.StatusCreated, item, nil)
}

func (h *Handler) ListSavedItems(c *gin.Context) {
	userIDStr := c.GetHeader("X-User-Id")
	userID, err := uuid.Parse(userIDStr)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Invalid user ID", nil)
		return
	}

	cursor := c.DefaultQuery("cursor", "")
	limit := 20
	if l, err := strconv.Atoi(c.DefaultQuery("limit", "20")); err == nil && l > 0 {
		limit = l
	}

	// ?collection_id=default|<uuid> (2026-10-24) wins over the
	// pre-collections ?collection=<name>.
	scope, err := h.svc.ResolveSavedScope(c.Request.Context(), userID, c.Query("collection_id"))
	if err != nil {
		writeSavedCollectionError(c, err)
		return
	}
	if c.Query("collection_id") == "" {
		scope.CollectionName = c.DefaultQuery("collection", "")
	}

	items, nextCursor, err := h.svc.ListSavedItems(c.Request.Context(), userID, scope, limit, cursor)
	if err != nil {
		writeSavedCollectionError(c, err)
		return
	}
	if items == nil {
		items = []postgres.SavedItem{}
	}

	var meta *api.Meta
	if nextCursor != "" {
		meta = &api.Meta{NextCursor: nextCursor}
	}

	api.JSON(c.Writer, http.StatusOK, items, meta)
}

func (h *Handler) UnsaveItem(c *gin.Context) {
	userIDStr := c.GetHeader("X-User-Id")
	userID, err := uuid.Parse(userIDStr)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Invalid user ID", nil)
		return
	}

	savedIDStr := c.Param("savedId")
	savedID, err := uuid.Parse(savedIDStr)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid saved item ID", nil)
		return
	}

	if err := h.svc.UnsaveItem(c.Request.Context(), savedID, userID); err != nil {
		if err.Error() == "SAVED_ITEM_NOT_FOUND" {
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusNotFound, "NOT_FOUND", "Saved item not found", nil)
			return
		}
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}

	api.JSON(c.Writer, http.StatusOK, map[string]string{"status": "unsaved"}, nil)
}

// The collection routes are in saved_collections_handler.go.
