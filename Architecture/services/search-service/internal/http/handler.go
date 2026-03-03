package http

import (
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/atpost/search-service/internal/store/search"
	"github.com/atpost/shared/api"
	sharedmiddleware "github.com/atpost/shared/middleware"
	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
)

type Handler struct {
	store       *search.Store
	rdb         *redis.Client
	internalKey string
}

func New(store *search.Store, rdb *redis.Client) *Handler {
	return &Handler{store: store, rdb: rdb}
}

// WithInternalKey sets the internal service key used to authenticate
// service-to-service requests via the X-Internal-Service-Key header.
func (h *Handler) WithInternalKey(key string) *Handler {
	h.internalKey = key
	return h
}

// validateSearchQuery checks that a query string is non-empty (after trimming)
// and does not exceed 500 characters. Returns an error message suitable for
// returning directly to the caller, or an empty string if valid.
func validateSearchQuery(query string) string {
	if len(strings.TrimSpace(query)) == 0 {
		return "query cannot be empty"
	}
	if len(query) > 500 {
		return "query too long: maximum 500 characters"
	}
	return ""
}

func (h *Handler) RegisterRoutes(r *gin.Engine) {
	// Apply internal service key enforcement to all /v1 routes.
	if h.internalKey != "" {
		r.Use(sharedmiddleware.RequireInternalKey(h.internalKey))
	}

	v1 := r.Group("/v1/search")
	{
		v1.GET("", h.UniversalSearch)
		v1.GET("/users", h.SearchUsers)
		v1.GET("/posts", h.SearchPosts)
		v1.POST("/users/bulk-sync", h.BulkSyncUsers)
		v1.GET("/hashtags", h.SearchHashtags)
		v1.GET("/autocomplete", h.Autocomplete)
	}

	discover := r.Group("/v1/discover")
	{
		discover.GET("/trending", h.GetTrending)
		discover.GET("/suggested", h.GetSuggested)
	}
}

func (h *Handler) SearchUsers(c *gin.Context) {
	query := c.Query("q")
	if errMsg := validateSearchQuery(query); errMsg != "" {
		api.Error(c.Writer, http.StatusBadRequest, "BAD_REQUEST", errMsg, nil, nil)
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
		slog.Error("SearchUsers error", "error", err)
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
		slog.Error("BulkSyncUsers error", "error", err)
		api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", "Bulk sync failed", nil, nil)
		return
	}

	api.JSON(c.Writer, http.StatusOK, map[string]interface{}{"indexed": count}, nil)
}

func (h *Handler) SearchPosts(c *gin.Context) {
	query := c.Query("q")
	if errMsg := validateSearchQuery(query); errMsg != "" {
		api.Error(c.Writer, http.StatusBadRequest, "BAD_REQUEST", errMsg, nil, nil)
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
		slog.Error("SearchPosts error", "error", err)
		api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", "Search failed", nil, nil)
		return
	}

	api.JSON(c.Writer, http.StatusOK, map[string]interface{}{"items": results}, nil)
}

// UniversalSearch handles GET /v1/search
// Query params: q (required), type (all|profiles|posts, default: all), limit (default: 20)
func (h *Handler) UniversalSearch(c *gin.Context) {
	query := c.Query("q")
	if errMsg := validateSearchQuery(query); errMsg != "" {
		api.Error(c.Writer, http.StatusBadRequest, "BAD_REQUEST", errMsg, nil, nil)
		return
	}

	searchType := c.DefaultQuery("type", "all")
	switch searchType {
	case "all", "profiles", "posts":
		// valid
	default:
		api.Error(c.Writer, http.StatusBadRequest, "BAD_REQUEST", "Parameter 'type' must be one of: all, profiles, posts", nil, nil)
		return
	}

	limit := 20
	if l := c.Query("limit"); l != "" {
		if val, err := strconv.Atoi(l); err == nil && val > 0 && val <= 100 {
			limit = val
		}
	}

	results, err := h.store.UniversalSearch(c.Request.Context(), query, searchType, limit)
	if err != nil {
		slog.Error("UniversalSearch error", "error", err)
		api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", "Search failed", nil, nil)
		return
	}

	api.JSON(c.Writer, http.StatusOK, results, nil)
}

// SearchHashtags handles GET /v1/search/hashtags
// Query params: q (required), limit (default: 10)
// Returns autocomplete suggestions for hashtags.
func (h *Handler) SearchHashtags(c *gin.Context) {
	query := c.Query("q")
	if errMsg := validateSearchQuery(query); errMsg != "" {
		api.Error(c.Writer, http.StatusBadRequest, "BAD_REQUEST", errMsg, nil, nil)
		return
	}

	limit := 10
	if l := c.Query("limit"); l != "" {
		if val, err := strconv.Atoi(l); err == nil && val > 0 && val <= 50 {
			limit = val
		}
	}

	hashtags, err := h.store.SearchHashtags(c.Request.Context(), query, limit)
	if err != nil {
		slog.Error("SearchHashtags error", "error", err)
		api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", "Hashtag search failed", nil, nil)
		return
	}

	api.JSON(c.Writer, http.StatusOK, map[string]interface{}{"hashtags": hashtags}, nil)
}

// GetTrending handles GET /v1/discover/trending
// Reads from Redis sorted set `trending:hashtags:{YYYY-MM-DD}` and returns top 20.
func (h *Handler) GetTrending(c *gin.Context) {
	today := time.Now().UTC().Format("2006-01-02")
	key := "trending:hashtags:" + today

	// ZRevRangeWithScores returns top members by score descending
	results, err := h.rdb.ZRevRangeWithScores(c.Request.Context(), key, 0, 19).Result()
	if err != nil {
		slog.Error("GetTrending Redis error", "error", err)
		api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to fetch trending hashtags", nil, nil)
		return
	}

	type TrendingItem struct {
		Hashtag string  `json:"hashtag"`
		Score   float64 `json:"score"`
	}

	items := make([]TrendingItem, 0, len(results))
	for _, z := range results {
		items = append(items, TrendingItem{
			Hashtag: z.Member.(string),
			Score:   z.Score,
		})
	}

	api.JSON(c.Writer, http.StatusOK, map[string]interface{}{"trending": items}, nil)
}

// Autocomplete handles GET /v1/search/autocomplete
// Query params: q (required), limit (default: 10, max: 20)
// Returns username/display_name suggestions for the given prefix.
func (h *Handler) Autocomplete(c *gin.Context) {
	query := c.Query("q")
	if query == "" {
		api.Error(c.Writer, http.StatusBadRequest, "BAD_REQUEST", "q is required", nil, nil)
		return
	}
	if len(query) > 100 {
		query = query[:100]
	}

	limit := 10
	if l := c.Query("limit"); l != "" {
		if val, err := strconv.Atoi(l); err == nil && val > 0 && val <= 20 {
			limit = val
		}
	}

	results, err := h.store.Autocomplete(c.Request.Context(), query, limit)
	if err != nil {
		slog.Error("autocomplete: search failed", "error", err)
		api.Error(c.Writer, http.StatusInternalServerError, "SEARCH_ERROR", "search failed", nil, nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, map[string]interface{}{"results": results}, nil)
}

// GetSuggested handles GET /v1/discover/suggested
// Returns recent popular posts from OpenSearch sorted by like_count desc.
func (h *Handler) GetSuggested(c *gin.Context) {
	limit := 20
	if l := c.Query("limit"); l != "" {
		if val, err := strconv.Atoi(l); err == nil && val > 0 && val <= 100 {
			limit = val
		}
	}

	posts, err := h.store.GetPopularPosts(c.Request.Context(), limit)
	if err != nil {
		slog.Error("GetSuggested error", "error", err)
		api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to fetch suggested posts", nil, nil)
		return
	}

	api.JSON(c.Writer, http.StatusOK, map[string]interface{}{"items": posts}, nil)
}
