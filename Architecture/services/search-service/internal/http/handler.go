package http

import (
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/atpost/search-service/internal/graphclient"
	"github.com/atpost/search-service/internal/store/postgres"
	"github.com/atpost/search-service/internal/store/search"
	"github.com/atpost/shared/api"
	sharedmiddleware "github.com/atpost/shared/middleware"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

type Handler struct {
	store             *search.Store
	rdb               *redis.Client
	internalKey       string
	extrasStore       *postgres.SearchExtrasStore
	analyticsStore    *postgres.AnalyticsStore
	graphClient       *graphclient.Client
	profileServiceURL string
	httpClient        *http.Client
}

func New(store *search.Store, rdb *redis.Client) *Handler {
	return &Handler{
		store:      store,
		rdb:        rdb,
		httpClient: &http.Client{Timeout: 15 * time.Second},
	}
}

// WithExtrasStore sets the Postgres extras store (saved searches, history).
func (h *Handler) WithExtrasStore(s *postgres.SearchExtrasStore) *Handler {
	h.extrasStore = s
	return h
}

// WithAnalyticsStore sets the search_queries / search_clicks store.
func (h *Handler) WithAnalyticsStore(s *postgres.AnalyticsStore) *Handler {
	h.analyticsStore = s
	return h
}

// WithGraphClient injects the graph-service client used to fetch the
// viewer's follow graph for author-affinity boosts.
func (h *Handler) WithGraphClient(gc *graphclient.Client) *Handler {
	h.graphClient = gc
	return h
}

// WithReindexSource sets the profile-service base URL used by the admin
// reindex endpoint to rebuild users_v1 from the source of truth.
func (h *Handler) WithReindexSource(profileServiceURL string) *Handler {
	h.profileServiceURL = profileServiceURL
	return h
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
		v1.POST("/click", h.RecordClick)
		v1.POST("/saved", h.SaveSearch)
		v1.GET("/saved", h.GetSavedSearches)
		v1.DELETE("/saved/:id", h.DeleteSavedSearch)
		v1.GET("/history", h.GetSearchHistory)
		v1.DELETE("/history", h.ClearSearchHistory)
		v1.GET("/products", h.SearchProducts)
		v1.GET("/events", h.SearchEvents)
		v1.GET("/messages", h.SearchMessages)

		// Admin reconciliation — rebuild users_v1 from profile-service.
		// Internal-key gated (the whole engine is when internalKey is
		// set). Use this after an OpenSearch wipe or any time search
		// has drifted from reality.
		v1.POST("/internal/reindex/users", h.ReindexUsers)
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
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "BAD_REQUEST", errMsg, nil)
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
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", "Search failed", nil)
		return
	}

	api.JSON(c.Writer, http.StatusOK, map[string]interface{}{"items": results}, nil)
}

func (h *Handler) BulkSyncUsers(c *gin.Context) {
	var req struct {
		Users []search.UserDoc `json:"users"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "BAD_REQUEST", err.Error(), nil)
		return
	}

	count, err := h.store.BulkIndexUsers(c.Request.Context(), req.Users)
	if err != nil {
		slog.Error("BulkSyncUsers error", "error", err)
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", "Bulk sync failed", nil)
		return
	}

	api.JSON(c.Writer, http.StatusOK, map[string]interface{}{"indexed": count}, nil)
}

func (h *Handler) SearchPosts(c *gin.Context) {
	query := c.Query("q")
	if errMsg := validateSearchQuery(query); errMsg != "" {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "BAD_REQUEST", errMsg, nil)
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
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", "Search failed", nil)
		return
	}

	api.JSON(c.Writer, http.StatusOK, map[string]interface{}{"items": results}, nil)
}

// UniversalSearch handles GET /v1/search.
//
// Two API shapes coexist for back-compat:
//
//  1. Legacy: ?type=all|profiles|posts|videos|flicks — returns
//     {users:[...], posts:[...]} via the old multi_match query.
//     Kept so existing mobile/web builds keep working through the rollout.
//
//  2. New multi-entity ranked: ?types=posts,users,hashtags,products,communities,channels
//     — function_score query per type, results grouped by entity, per-entity
//     cursor pagination, search_queries analytics row written best-effort.
//
// The new shape is selected the moment ?types= is present.
func (h *Handler) UniversalSearch(c *gin.Context) {
	query := c.Query("q")
	if errMsg := validateSearchQuery(query); errMsg != "" {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "BAD_REQUEST", errMsg, nil)
		return
	}

	if c.Query("types") != "" {
		h.universalSearchMultiEntity(c, query)
		return
	}

	// --- Legacy path ---
	searchType := c.DefaultQuery("type", "all")
	switch searchType {
	case "all", "profiles", "posts", "videos", "flicks":
		// valid — videos = long_video content_type, flicks = flick content_type
	default:
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "BAD_REQUEST",
			"Parameter 'type' must be one of: all, profiles, posts, videos, flicks", nil)
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
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", "Search failed", nil)
		return
	}

	api.JSON(c.Writer, http.StatusOK, results, nil)
}

// universalSearchMultiEntity implements the new ranked + grouped shape.
// Each requested type runs an independent function_score query with the
// viewer's follow graph layered in as author-affinity boost. Cursors
// are per-entity (each entity's pagination is independent).
//
// Response shape:
//
//	{
//	  "data": {
//	    "query_id": "...",                   // for /v1/search/click joins
//	    "results": {
//	      "posts":       {"items": [...], "next_cursor": "..."},
//	      "users":       {"items": [...], "next_cursor": "..."},
//	      ...
//	    }
//	  }
//	}
func (h *Handler) universalSearchMultiEntity(c *gin.Context, query string) {
	limit := 20
	if l := c.Query("limit"); l != "" {
		if v, err := strconv.Atoi(l); err == nil && v > 0 && v <= 100 {
			limit = v
		}
	}

	types := parseTypes(c.Query("types"))
	if len(types) == 0 {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "BAD_REQUEST",
			"types must be a comma-separated subset of: posts,users,hashtags,products,communities,channels", nil)
		return
	}

	// Per-entity cursors arrive as ?cursor.posts=... &cursor.users=...
	cursorFor := func(t string) string { return c.Query("cursor." + t) }

	// Viewer (optional) — used for follow-graph affinity boost.
	var viewerID uuid.UUID
	if raw := c.GetHeader("X-User-Id"); raw != "" {
		if id, err := uuid.Parse(raw); err == nil {
			viewerID = id
		}
	}
	var followedIDs []string
	if h.graphClient != nil && viewerID != uuid.Nil {
		followedIDs = h.graphClient.FollowingIDs(c.Request.Context(), viewerID, 500)
	}

	results := make(map[string]*search.RankedSearchResult, len(types))
	counts := make(map[string]int, len(types))
	for _, t := range types {
		opts := search.RankedSearchOptions{
			Limit:             limit,
			Cursor:            cursorFor(t),
			FollowedAuthorIDs: followedIDs,
		}
		r, err := h.store.RankedSearch(c.Request.Context(), t, query, opts)
		if err != nil {
			// Per-entity failure is non-fatal — log and continue so a
			// missing index doesn't 500 the whole search.
			slog.Warn("multi-entity search: per-entity failed", "type", t, "err", err)
			results[t] = &search.RankedSearchResult{Items: []map[string]any{}}
			counts[t] = 0
			continue
		}
		if r.Items == nil {
			r.Items = []map[string]any{}
		}
		results[t] = r
		counts[t] = len(r.Items)
	}

	// Best-effort analytics. Failure here must not break the search.
	var queryID uuid.UUID
	if h.analyticsStore != nil {
		id, err := h.analyticsStore.LogQuery(c.Request.Context(), viewerID, query, types, counts)
		if err != nil {
			slog.Warn("search analytics: LogQuery failed", "err", err)
		} else {
			queryID = id
		}
	}

	resp := gin.H{"results": results}
	if queryID != uuid.Nil {
		resp["query_id"] = queryID
	}
	api.JSON(c.Writer, http.StatusOK, resp, nil)
}

// parseTypes splits a comma-list and filters down to known entities.
// Empty / all-unknown inputs return nil so the handler can error out.
func parseTypes(raw string) []string {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	known := map[string]bool{
		search.EntityPosts:       true,
		search.EntityUsers:       true,
		search.EntityHashtags:    true,
		search.EntityProducts:    true,
		search.EntityCommunities: true,
		search.EntityChannels:    true,
	}
	out := make([]string, 0, len(parts))
	seen := make(map[string]bool, len(parts))
	for _, p := range parts {
		t := strings.TrimSpace(p)
		if known[t] && !seen[t] {
			seen[t] = true
			out = append(out, t)
		}
	}
	return out
}

// RecordClick handles POST /v1/search/click.
// Body: {"query_id":"...", "entity_type":"posts", "entity_id":"...", "position":0}
// Best-effort write to search_clicks. Returns 204 always (even on
// validation failure) so the SDK can fire-and-forget without retry storms.
func (h *Handler) RecordClick(c *gin.Context) {
	if h.analyticsStore == nil {
		c.Writer.WriteHeader(http.StatusNoContent)
		return
	}
	var req struct {
		QueryID    string `json:"query_id"`
		EntityType string `json:"entity_type"`
		EntityID   string `json:"entity_id"`
		Position   int    `json:"position"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.Writer.WriteHeader(http.StatusNoContent)
		return
	}
	qid, err := uuid.Parse(req.QueryID)
	if err != nil || req.EntityType == "" || req.EntityID == "" {
		c.Writer.WriteHeader(http.StatusNoContent)
		return
	}
	var viewer uuid.UUID
	if raw := c.GetHeader("X-User-Id"); raw != "" {
		if id, err := uuid.Parse(raw); err == nil {
			viewer = id
		}
	}
	if err := h.analyticsStore.LogClick(c.Request.Context(), qid, viewer, req.EntityType, req.EntityID, req.Position); err != nil {
		slog.Warn("search analytics: LogClick failed", "err", err)
	}
	c.Writer.WriteHeader(http.StatusNoContent)
}

// SearchHashtags handles GET /v1/search/hashtags
// Query params: q (required), limit (default: 10)
// Returns autocomplete suggestions for hashtags.
func (h *Handler) SearchHashtags(c *gin.Context) {
	query := c.Query("q")
	if errMsg := validateSearchQuery(query); errMsg != "" {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "BAD_REQUEST", errMsg, nil)
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
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", "Hashtag search failed", nil)
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
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to fetch trending hashtags", nil)
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
//
// HS3 — per-caller rate limit. Autocomplete is the cheapest user-
// enumeration vector on the platform: each request returns ≤20 user
// matches for a prefix, so a worker can iterate aa..zz and enumerate
// the userbase without any other auth gate. We cap at 60 req/min per
// caller (X-User-Id when authenticated, IP otherwise) — that's three
// per second, generous for legitimate typeahead use.
func (h *Handler) Autocomplete(c *gin.Context) {
	if !h.allowAutocomplete(c) {
		c.Header("Retry-After", "60")
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusTooManyRequests, "RATE_LIMITED", "too many autocomplete requests", nil)
		return
	}
	query := c.Query("q")
	if query == "" {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "BAD_REQUEST", "q is required", nil)
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

	// `?kinds=users` keeps the original users-only behavior for callers
	// (e.g. the @mention picker) that don't want hashtag/community
	// suggestions mixed in. Default is multi-entity.
	kinds := c.DefaultQuery("kinds", "all")

	var results []search.AutocompleteResult
	var err error
	if kinds == "users" {
		results, err = h.store.Autocomplete(c.Request.Context(), query, limit)
	} else {
		results, err = h.store.AutocompleteMulti(c.Request.Context(), query, limit)
	}
	if err != nil {
		slog.Error("autocomplete: search failed", "error", err)
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "SEARCH_ERROR", "search failed", nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, map[string]interface{}{"results": results}, nil)
}

// allowAutocomplete enforces 60 requests/minute per (user OR IP). Uses
// the standard Redis sliding-window INCR + EXPIRE pattern. Fails
// CLOSED on Redis error — autocomplete is non-critical; better to
// drop a few requests than open the enumeration door if Redis blips.
func (h *Handler) allowAutocomplete(c *gin.Context) bool {
	if h.rdb == nil {
		return true
	}
	subject := c.GetHeader("X-User-Id")
	if subject == "" {
		subject = c.ClientIP()
	}
	if subject == "" {
		return true // can't key the limit; let it through
	}
	key := "ac_rl:" + subject
	ctx := c.Request.Context()
	pipe := h.rdb.Pipeline()
	incr := pipe.Incr(ctx, key)
	pipe.Expire(ctx, key, 60*time.Second)
	if _, err := pipe.Exec(ctx); err != nil {
		slog.Warn("autocomplete: rate-limit Redis error — failing closed", "key", key, "err", err)
		return false
	}
	return incr.Val() <= 60
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
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to fetch suggested posts", nil)
		return
	}

	api.JSON(c.Writer, http.StatusOK, map[string]interface{}{"items": posts}, nil)
}

// requireExtras returns false and writes a 503 if the extras store is not configured.
func (h *Handler) requireExtras(c *gin.Context) bool {
	if h.extrasStore == nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusServiceUnavailable, "SERVICE_UNAVAILABLE", "Postgres extras store not configured", nil)
		return false
	}
	return true
}

// requireUserID parses X-User-Id and returns the UUID. On failure it writes 401 and returns false.
func requireUserID(c *gin.Context) (uuid.UUID, bool) {
	raw := c.GetHeader("X-User-Id")
	id, err := uuid.Parse(raw)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Invalid or missing X-User-Id", nil)
		return uuid.Nil, false
	}
	return id, true
}

// SaveSearch handles POST /v1/search/saved
// Body: {"query":"...", "search_type":"universal"}
func (h *Handler) SaveSearch(c *gin.Context) {
	if !h.requireExtras(c) {
		return
	}
	userID, ok := requireUserID(c)
	if !ok {
		return
	}

	var req struct {
		Query      string `json:"query" binding:"required"`
		SearchType string `json:"search_type"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "BAD_REQUEST", err.Error(), nil)
		return
	}
	if req.SearchType == "" {
		req.SearchType = "universal"
	}

	ss, err := h.extrasStore.SaveSearch(c.Request.Context(), userID, req.Query, req.SearchType)
	if err != nil {
		slog.Error("SaveSearch error", "error", err)
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to save search", nil)
		return
	}
	api.JSON(c.Writer, http.StatusCreated, ss, nil)
}

// GetSavedSearches handles GET /v1/search/saved
func (h *Handler) GetSavedSearches(c *gin.Context) {
	if !h.requireExtras(c) {
		return
	}
	userID, ok := requireUserID(c)
	if !ok {
		return
	}

	items, err := h.extrasStore.GetSavedSearches(c.Request.Context(), userID)
	if err != nil {
		slog.Error("GetSavedSearches error", "error", err)
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to get saved searches", nil)
		return
	}
	if items == nil {
		items = []postgres.SavedSearch{}
	}
	api.JSON(c.Writer, http.StatusOK, map[string]interface{}{"items": items}, nil)
}

// DeleteSavedSearch handles DELETE /v1/search/saved/:id
func (h *Handler) DeleteSavedSearch(c *gin.Context) {
	if !h.requireExtras(c) {
		return
	}
	userID, ok := requireUserID(c)
	if !ok {
		return
	}

	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "BAD_REQUEST", "Invalid saved search ID", nil)
		return
	}

	if err := h.extrasStore.DeleteSavedSearch(c.Request.Context(), id, userID); err != nil {
		if err == postgres.ErrNotFound {
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusNotFound, "NOT_FOUND", "Saved search not found", nil)
			return
		}
		slog.Error("DeleteSavedSearch error", "error", err)
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to delete saved search", nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, map[string]interface{}{"status": "deleted"}, nil)
}

// GetSearchHistory handles GET /v1/search/history
// Query params: limit (default: 20, max: 20)
func (h *Handler) GetSearchHistory(c *gin.Context) {
	if !h.requireExtras(c) {
		return
	}
	userID, ok := requireUserID(c)
	if !ok {
		return
	}

	limit := 20
	if l := c.Query("limit"); l != "" {
		if val, err := strconv.Atoi(l); err == nil && val > 0 && val <= 20 {
			limit = val
		}
	}

	items, err := h.extrasStore.GetSearchHistory(c.Request.Context(), userID, limit)
	if err != nil {
		slog.Error("GetSearchHistory error", "error", err)
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to get search history", nil)
		return
	}
	if items == nil {
		items = []postgres.SearchHistoryItem{}
	}
	api.JSON(c.Writer, http.StatusOK, map[string]interface{}{"items": items}, nil)
}

// ClearSearchHistory handles DELETE /v1/search/history
func (h *Handler) ClearSearchHistory(c *gin.Context) {
	if !h.requireExtras(c) {
		return
	}
	userID, ok := requireUserID(c)
	if !ok {
		return
	}

	if err := h.extrasStore.ClearSearchHistory(c.Request.Context(), userID); err != nil {
		slog.Error("ClearSearchHistory error", "error", err)
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to clear search history", nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, map[string]interface{}{"status": "cleared"}, nil)
}

// SearchProducts handles GET /v1/search/products
// Query params: q (required), category (optional), limit (default: 20)
func (h *Handler) SearchProducts(c *gin.Context) {
	query := c.Query("q")
	if errMsg := validateSearchQuery(query); errMsg != "" {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "BAD_REQUEST", errMsg, nil)
		return
	}

	category := c.Query("category")
	limit := 20
	if l := c.Query("limit"); l != "" {
		if val, err := strconv.Atoi(l); err == nil && val > 0 && val <= 100 {
			limit = val
		}
	}

	results, err := h.store.SearchProducts(c.Request.Context(), query, category, limit)
	if err != nil {
		slog.Error("SearchProducts error", "error", err)
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", "Product search failed", nil)
		return
	}
	if results == nil {
		results = []map[string]any{}
	}
	api.JSON(c.Writer, http.StatusOK, map[string]interface{}{"items": results}, nil)
}

// SearchEvents handles GET /v1/search/events
// Query params: q (required), limit (default: 20)
func (h *Handler) SearchEvents(c *gin.Context) {
	query := c.Query("q")
	if errMsg := validateSearchQuery(query); errMsg != "" {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "BAD_REQUEST", errMsg, nil)
		return
	}

	limit := 20
	if l := c.Query("limit"); l != "" {
		if val, err := strconv.Atoi(l); err == nil && val > 0 && val <= 100 {
			limit = val
		}
	}

	results, err := h.store.SearchEvents(c.Request.Context(), query, limit)
	if err != nil {
		slog.Error("SearchEvents error", "error", err)
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", "Event search failed", nil)
		return
	}
	if results == nil {
		results = []map[string]any{}
	}
	api.JSON(c.Writer, http.StatusOK, map[string]interface{}{"items": results}, nil)
}

// SearchMessages handles GET /v1/search/messages
// Query params: q (required), conv_id (optional), limit (default: 20)
// Requires X-User-Id header.
func (h *Handler) SearchMessages(c *gin.Context) {
	userID, ok := requireUserID(c)
	if !ok {
		return
	}

	query := c.Query("q")
	if errMsg := validateSearchQuery(query); errMsg != "" {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "BAD_REQUEST", errMsg, nil)
		return
	}

	convID := c.Query("conv_id")
	limit := 20
	if l := c.Query("limit"); l != "" {
		if val, err := strconv.Atoi(l); err == nil && val > 0 && val <= 100 {
			limit = val
		}
	}

	results, err := h.store.SearchMessages(c.Request.Context(), userID.String(), convID, query, limit)
	if err != nil {
		slog.Error("SearchMessages error", "error", err)
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", "Message search failed", nil)
		return
	}
	if results == nil {
		results = []map[string]any{}
	}
	api.JSON(c.Writer, http.StatusOK, map[string]interface{}{"items": results}, nil)
}
