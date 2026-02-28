package http

import (
	"log"
	"net/http"
	"strconv"
	"time"

	"github.com/facebook-like/search-service/internal/store/search"
	"github.com/facebook-like/shared/api"
	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
)

type Handler struct {
	store *search.Store
	rdb   *redis.Client
}

func New(store *search.Store, rdb *redis.Client) *Handler {
	return &Handler{store: store, rdb: rdb}
}

func (h *Handler) RegisterRoutes(r *gin.Engine) {
	v1 := r.Group("/v1/search")
	{
		v1.GET("", h.UniversalSearch)
		v1.GET("/users", h.SearchUsers)
		v1.GET("/posts", h.SearchPosts)
		v1.POST("/users/bulk-sync", h.BulkSyncUsers)
		v1.GET("/hashtags", h.SearchHashtags)
	}

	discover := r.Group("/v1/discover")
	{
		discover.GET("/trending", h.GetTrending)
		discover.GET("/suggested", h.GetSuggested)
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

// UniversalSearch handles GET /v1/search
// Query params: q (required), type (all|profiles|posts, default: all), limit (default: 20)
func (h *Handler) UniversalSearch(c *gin.Context) {
	query := c.Query("q")
	if query == "" {
		api.Error(c.Writer, http.StatusBadRequest, "BAD_REQUEST", "Query parameter 'q' is required", nil, nil)
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
		log.Printf("UniversalSearch error: %v", err)
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
	if query == "" {
		api.Error(c.Writer, http.StatusBadRequest, "BAD_REQUEST", "Query parameter 'q' is required", nil, nil)
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
		log.Printf("SearchHashtags error: %v", err)
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
		log.Printf("GetTrending Redis error: %v", err)
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
		log.Printf("GetSuggested error: %v", err)
		api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to fetch suggested posts", nil, nil)
		return
	}

	api.JSON(c.Writer, http.StatusOK, map[string]interface{}{"items": posts}, nil)
}
