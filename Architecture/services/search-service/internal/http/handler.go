package http

import (
	"log"
	"net/http"
	"strconv"

	"github.com/facebook-like/search-service/internal/store/search"
	"github.com/facebook-like/shared/api"
	"github.com/gin-gonic/gin"
)

type Handler struct {
	store *search.Store
}

func New(store *search.Store) *Handler {
	return &Handler{store: store}
}

func (h *Handler) RegisterRoutes(r *gin.Engine) {
	v1 := r.Group("/v1/search")
	{
		v1.GET("/users", h.SearchUsers)
		v1.GET("/posts", h.SearchPosts)
		v1.POST("/users/bulk-sync", h.BulkSyncUsers)
	}
}

func (h *Handler) SearchUsers(c *gin.Context) {
	query := c.Query("q")
	if query == "" {
		api.Error(c.Writer, http.StatusBadRequest, "BAD_REQUEST", "Query parameter 'q' is required", nil, nil)
		return
	}

	limit := 20
	if l := c.Query("limit"); l != "" {
		if val, err := strconv.Atoi(l); err == nil && val > 0 && val <= 100 {
			limit = val
		}
	}

	results, err := h.store.SearchUsers(c.Request.Context(), query, limit)
	if err != nil {
		log.Printf("SearchUsers error: %v", err)
		api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", "Search failed", nil, nil)
		return
	}

	api.JSON(c.Writer, http.StatusOK, map[string]interface{}{"items": results}, nil)
}

func (h *Handler) BulkSyncUsers(c *gin.Context) {
	var req struct {
		Users []search.UserDoc `json:"users"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "BAD_REQUEST", err.Error(), nil, nil)
		return
	}

	count, err := h.store.BulkIndexUsers(c.Request.Context(), req.Users)
	if err != nil {
		log.Printf("BulkSyncUsers error: %v", err)
		api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", "Bulk sync failed", nil, nil)
		return
	}

	api.JSON(c.Writer, http.StatusOK, map[string]interface{}{"indexed": count}, nil)
}

func (h *Handler) SearchPosts(c *gin.Context) {
	query := c.Query("q")
	if query == "" {
		api.Error(c.Writer, http.StatusBadRequest, "BAD_REQUEST", "Query parameter 'q' is required", nil, nil)
		return
	}

	limit := 20
	if l := c.Query("limit"); l != "" {
		if val, err := strconv.Atoi(l); err == nil && val > 0 && val <= 100 {
			limit = val
		}
	}

	results, err := h.store.SearchPosts(c.Request.Context(), query, limit)
	if err != nil {
		log.Printf("SearchPosts error: %v", err)
		api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", "Search failed", nil, nil)
		return
	}

	api.JSON(c.Writer, http.StatusOK, map[string]interface{}{"items": results}, nil)
}
