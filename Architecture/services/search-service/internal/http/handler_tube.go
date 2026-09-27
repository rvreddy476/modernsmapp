package http

// MTube search (2026-09-27).
//
//	GET  /v1/search/posts?type=videos&sort=&duration=&date=&features=
//	GET  /v1/search/channels?q=&limit=&cursor=      Tube channels
//	GET  /v1/search/collections?q=&limit=&cursor=   public playlists
//	POST /v1/search/internal/reindex/tube           feed both from post-service
//
// The two list endpoints answer {items, next_cursor} under `data`, like the
// ranked multi-entity search: the cursor is an opaque offset string, empty
// when the page came back short. Rows carry ids and, best-effort, the URL
// the id resolves to through media-service — the index holds ids, not
// signed URLs, for the reason results.go gives.

import (
	"context"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/atpost/search-service/internal/postclient"
	"github.com/atpost/search-service/internal/reindex"
	"github.com/atpost/search-service/internal/store/search"
	"github.com/atpost/shared/api"
	"github.com/gin-gonic/gin"
)

// WithPostClient wires the post-service client the tube reindex walks.
func (h *Handler) WithPostClient(pc *postclient.Client) *Handler {
	h.postClient = pc
	return h
}

// videoSearchOptions parses the MTube video parameters off the request.
// Any of them on a non-video type is refused: the fields they filter on
// are video fields, and silently ignoring a filter is how a client shows
// "long videos over 20 minutes" that are neither. ok=false means a 400
// was written.
func videoSearchOptions(c *gin.Context, contentTypes []string) (search.VideoSearchOptions, bool) {
	sortBy, duration, date, features := c.Query("sort"), c.Query("duration"), c.Query("date"), c.Query("features")
	if sortBy == "" && duration == "" && date == "" && features == "" {
		return search.VideoSearchOptions{}, true
	}
	opts, err := search.ParseVideoSearchOptions(sortBy, duration, date, features)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "BAD_REQUEST", err.Error(), nil)
		return search.VideoSearchOptions{}, false
	}
	if !isVideoKind(contentTypes) {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "BAD_REQUEST",
			"Parameters 'sort', 'duration', 'date' and 'features' require type=videos", nil)
		return search.VideoSearchOptions{}, false
	}
	return opts, true
}

// isVideoKind reports whether the content-type terms are the videos kind.
func isVideoKind(contentTypes []string) bool {
	if len(contentTypes) == 0 {
		return false
	}
	for _, ct := range contentTypes {
		if ct != "long_video" && ct != "video" {
			return false
		}
	}
	return true
}

// tubeListParams reads ?q (required, ≤500 chars), ?limit (default 20, max
// 50) and ?cursor (offset; malformed = first page). ok=false means a 400
// was written.
func tubeListParams(c *gin.Context) (query string, limit, from int, ok bool) {
	query = c.Query("q")
	if errMsg := validateSearchQuery(query); errMsg != "" {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "BAD_REQUEST", errMsg, nil)
		return "", 0, 0, false
	}
	limit = 20
	if l := c.Query("limit"); l != "" {
		if val, err := strconv.Atoi(l); err == nil && val > 0 && val <= 50 {
			limit = val
		}
	}
	if cur := c.Query("cursor"); cur != "" {
		if n, err := strconv.Atoi(cur); err == nil && n >= 0 && n <= 10000 {
			from = n
		}
	}
	return query, limit, from, true
}

// tubeNextCursor advertises a continuation only when the page was full.
func tubeNextCursor(from, limit, got int) string {
	if got < limit {
		return ""
	}
	return strconv.Itoa(from + limit)
}

// TubeChannelResult is one channel row.
type TubeChannelResult struct {
	ID            string  `json:"id"`
	OwnerID       string  `json:"owner_id"`
	Name          string  `json:"name"`
	Handle        string  `json:"handle"`
	AvatarMediaID *string `json:"avatar_media_id"`
	AvatarURL     *string `json:"avatar_url"`
	FollowerCount int     `json:"follower_count"`
}

// TubeCollectionResult is one public-playlist row.
type TubeCollectionResult struct {
	ID           string  `json:"id"`
	OwnerID      string  `json:"owner_id"`
	Title        string  `json:"title"`
	ItemCount    int     `json:"item_count"`
	CoverMediaID *string `json:"cover_media_id"`
	CoverURL     *string `json:"cover_url"`
}

type tubeListResponse[T any] struct {
	Items      []T    `json:"items"`
	NextCursor string `json:"next_cursor"`
}

// SearchTubeChannels handles GET /v1/search/channels.
func (h *Handler) SearchTubeChannels(c *gin.Context) {
	query, limit, from, ok := tubeListParams(c)
	if !ok {
		return
	}
	docs, err := h.store.SearchTubeChannels(c.Request.Context(), query, limit, from)
	if err != nil {
		slog.Error("SearchTubeChannels error", "error", err)
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", "Search failed", nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, tubeListResponse[TubeChannelResult]{
		Items:      h.tubeChannelResults(c.Request.Context(), viewerString(viewerFrom(c)), docs),
		NextCursor: tubeNextCursor(from, limit, len(docs)),
	}, nil)
}

// tubeChannelResults projects documents to rows, resolving avatars in one
// media-service batch. Nil-safe on the media client.
func (h *Handler) tubeChannelResults(ctx context.Context, viewerID string, docs []search.TubeChannelDoc) []TubeChannelResult {
	rows := make([]TubeChannelResult, 0, len(docs))
	mediaIDs := make([]string, 0, len(docs))
	for _, d := range docs {
		if d.AvatarMediaID != "" {
			mediaIDs = append(mediaIDs, d.AvatarMediaID)
		}
	}
	assets := h.mediaClient.Resolve(ctx, viewerID, mediaIDs)
	for _, d := range docs {
		row := TubeChannelResult{
			ID: d.ChannelID, OwnerID: d.OwnerID, Name: d.Name, Handle: d.Handle,
			AvatarMediaID: strPtr(d.AvatarMediaID), FollowerCount: d.FollowerCount,
		}
		if asset, ok := assets[d.AvatarMediaID]; ok && d.AvatarMediaID != "" {
			row.AvatarURL = strPtr(asset.ThumbnailURL())
		}
		rows = append(rows, row)
	}
	return rows
}

// SearchTubeCollections handles GET /v1/search/collections.
func (h *Handler) SearchTubeCollections(c *gin.Context) {
	query, limit, from, ok := tubeListParams(c)
	if !ok {
		return
	}
	docs, err := h.store.SearchTubeCollections(c.Request.Context(), query, limit, from)
	if err != nil {
		slog.Error("SearchTubeCollections error", "error", err)
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", "Search failed", nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, tubeListResponse[TubeCollectionResult]{
		Items:      h.tubeCollectionResults(c.Request.Context(), viewerString(viewerFrom(c)), docs),
		NextCursor: tubeNextCursor(from, limit, len(docs)),
	}, nil)
}

// tubeCollectionResults projects documents to rows, resolving covers in
// one media-service batch; a stored cover URL is passed through as-is.
func (h *Handler) tubeCollectionResults(ctx context.Context, viewerID string, docs []search.TubeCollectionDoc) []TubeCollectionResult {
	rows := make([]TubeCollectionResult, 0, len(docs))
	mediaIDs := make([]string, 0, len(docs))
	for _, d := range docs {
		if d.CoverMediaID != "" {
			mediaIDs = append(mediaIDs, d.CoverMediaID)
		}
	}
	assets := h.mediaClient.Resolve(ctx, viewerID, mediaIDs)
	for _, d := range docs {
		if d.Visibility != "public" {
			continue // never, but the row layer is the last fence
		}
		row := TubeCollectionResult{
			ID: d.PlaylistID, OwnerID: d.OwnerID, Title: d.Title, ItemCount: d.ItemCount,
			CoverMediaID: strPtr(d.CoverMediaID),
		}
		if asset, ok := assets[d.CoverMediaID]; ok && d.CoverMediaID != "" {
			row.CoverURL = strPtr(asset.ThumbnailURL())
		} else if d.CoverMediaID == "" && d.CoverURL != "" {
			row.CoverURL = strPtr(d.CoverURL)
		}
		rows = append(rows, row)
	}
	return rows
}

// ReindexTube handles POST /v1/search/internal/reindex/tube — synchronous,
// like the product reindex, because the operator running it wants the
// counts, and above all wants removed_non_public.
func (h *Handler) ReindexTube(c *gin.Context) {
	if h.postClient == nil || !h.postClient.Configured() {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusServiceUnavailable,
			"REINDEX_UNCONFIGURED", "POST_SERVICE_URL is not configured on this deployment", nil)
		return
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(c.Request.Context()), 15*time.Minute)
	defer cancel()

	res, err := reindex.ReindexTube(ctx, h.postClient, h.store, slog.Default())
	if err != nil {
		slog.Error("admin reindex/tube failed", "err", err,
			"channels_indexed", res.ChannelsIndexed, "collections_indexed", res.CollectionsIndexed)
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadGateway,
			"REINDEX_FAILED", err.Error(), res)
		return
	}
	api.JSON(c.Writer, http.StatusOK, res, nil)
}
