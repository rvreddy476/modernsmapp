// hashtag_trending_handler.go holds the hashtag and trending routes: posts
// under a hashtag (with its page meta), hashtag search, trending posts and
// the trending-hashtags feed. Moved out of handler.go unchanged.

package http

import (
	"log/slog"
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

// ============================================================
// Hashtag Handler
// ============================================================

func (h *Handler) GetPostsByHashtag(c *gin.Context) {
	tag := c.Param("tag")
	if tag == "" {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_REQUEST", "Hashtag is required", nil)
		return
	}
	// Strip leading # if present
	tag = strings.TrimPrefix(tag, "#")

	cursor := c.DefaultQuery("cursor", "")
	sort := c.DefaultQuery("sort", "recent")
	if sort != "top" && sort != "recent" {
		sort = "recent"
	}
	limit := 20
	if l, err := strconv.Atoi(c.DefaultQuery("limit", "20")); err == nil && l > 0 {
		limit = l
	}
	// content_type can be repeated: ?content_type=post&content_type=flick
	contentTypes := c.QueryArray("content_type")

	// The viewer decides which authors' posts the list (and its count) may
	// include: private accounts, blocks, deactivated authors. Absent or
	// malformed is the signed-out stranger.
	var viewerID *uuid.UUID
	if v := c.GetHeader("X-User-Id"); v != "" {
		if id, err := uuid.Parse(v); err == nil {
			viewerID = &id
		}
	}

	posts, nextCursor, err := h.svc.GetPostsByHashtag(c.Request.Context(), viewerID, tag, limit, cursor, sort, contentTypes)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	if posts == nil {
		posts = []service.PostDetail{}
	}

	// T-34: the first page carries the tag's post count ("N videos"). It
	// counts what this list pages over for this viewer, for the same
	// content_type filter and author gate, and is best-effort: a failed
	// count leaves `total` off, never the page.
	var total *int
	capped := false
	if cursor == "" {
		n, hitCap, err := h.svc.HashtagTotal(c.Request.Context(), viewerID, tag, contentTypes)
		if err != nil {
			slog.WarnContext(c.Request.Context(), "hashtag total unavailable", "tag", tag, "error", err)
		} else {
			total, capped = &n, hitCap
		}
	}

	c.JSON(http.StatusOK, hashtagPostsResponse{Data: posts, Meta: newHashtagPostsMeta(nextCursor, total, capped)})
}

// hashtagPostsMeta is the meta of GET /v1/hashtags/:tag/posts: api.Meta plus
// the tag's post count. total is present on the first page only (no cursor);
// total_capped means the count stopped at postgres.HashtagTotalCap, so the
// client shows "10,000+" rather than an exact figure.
type hashtagPostsMeta struct {
	NextCursor  string `json:"next_cursor,omitempty"`
	Total       *int   `json:"total,omitempty"`
	TotalCapped bool   `json:"total_capped,omitempty"`
}

type hashtagPostsResponse struct {
	Data []service.PostDetail `json:"data"`
	Meta *hashtagPostsMeta    `json:"meta,omitempty"`
}

// newHashtagPostsMeta returns nil when there is nothing to say, so a last
// page without a count keeps the old envelope (no meta at all).
func newHashtagPostsMeta(nextCursor string, total *int, capped bool) *hashtagPostsMeta {
	if nextCursor == "" && total == nil {
		return nil
	}
	return &hashtagPostsMeta{NextCursor: nextCursor, Total: total, TotalCapped: total != nil && capped}
}

// SearchHashtags handles GET /v1/hashtags/search?q=...&limit=...
// Returns hashtag suggestions whose normalized name starts with the query.
// Reads directly from posts.hashtags TEXT[]. Rate-limited at the route level.
func (h *Handler) SearchHashtags(c *gin.Context) {
	q := strings.TrimSpace(c.Query("q"))
	q = strings.TrimPrefix(q, "#")
	if len(q) < 2 {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_REQUEST", "q must be at least 2 characters", nil)
		return
	}
	if len(q) > 100 {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_REQUEST", "q is too long", nil)
		return
	}

	limit := 10
	if l, err := strconv.Atoi(c.DefaultQuery("limit", "10")); err == nil && l > 0 {
		if l > 20 {
			l = 20
		}
		limit = l
	}

	suggestions, err := h.svc.SearchHashtags(c.Request.Context(), q, limit)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	if suggestions == nil {
		suggestions = []postgres.HashtagSuggestion{}
	}

	api.JSON(c.Writer, http.StatusOK, gin.H{
		"query":    q,
		"hashtags": suggestions,
	}, nil)
}

// GetTrendingPosts handles GET /v1/posts/trending?content_type=...&limit=...&cursor=...
//
// Returns posts ranked by the same engagement score as the hashtag "top"
// sort, optionally filtered by content_type.
//
// ── Brought into line with /v1/posts/recent (2026-09-18) ──────────────────
// This endpoint cost every client three special cases that its neighbour
// does not. All three are fixed WITHOUT dropping the old form:
//
//   - the cursor was inside data ({"items":[…],"next_cursor":"…"}) while
//     every other list puts it in meta.next_cursor. It is now in BOTH: meta
//     carries it for a client that pages the way it pages /recent, and the
//     data envelope keeps items + next_cursor verbatim so nothing already
//     shipped breaks. The envelope is what could not be changed
//     compatibly — data cannot be an object and a bare array at once — so
//     it stays, and meta is the addition.
//   - content_type took REPEATED params only (?content_type=a&content_type=b)
//     while /recent takes a comma-separated list. Both work now: every
//     repetition is split on commas and merged.
//   - "video" and "reel", which /recent accepts and maps to long_video /
//     flick, were rejected here with a 400. They now normalise, through the
//     same parseContentTypeFilter → service.CanonicalContentType that
//     /recent uses, so the two endpoints cannot drift again.
func (h *Handler) GetTrendingPosts(c *gin.Context) {
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "20"))
	if limit <= 0 || limit > 50 {
		limit = 20
	}
	cursor := c.Query("cursor")

	contentTypes, err := mergeContentTypeFilters(c.QueryArray("content_type"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_CONTENT_TYPE",
			err.Error(), nil)
		return
	}

	posts, nextCursor, err := h.svc.GetTrendingPosts(c.Request.Context(), contentTypes, limit, cursor, optionalCallerID(c))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	writeTrendingPage(c, posts, nextCursor)
}

// mergeContentTypeFilters accepts BOTH shapes: /v1/posts/trending's repeated
// ?content_type=a&content_type=b and /v1/posts/recent's comma-separated
// ?content_type=a,b — every repetition is itself split on commas. Legacy
// spellings normalise through the same parseContentTypeFilter →
// service.CanonicalContentType path /recent uses ("video" → long_video,
// "reel" → flick), duplicates collapse, and an unknown type is an error
// naming the value.
func mergeContentTypeFilters(raws []string) ([]string, error) {
	var out []string
	seen := map[string]bool{}
	for _, raw := range raws {
		parsed, err := parseContentTypeFilter(raw)
		if err != nil {
			return nil, err
		}
		for _, ct := range parsed {
			if !seen[ct] {
				seen[ct] = true
				out = append(out, ct)
			}
		}
	}
	return out, nil
}

// writeTrendingPage writes BOTH cursor placements: meta.next_cursor, which is
// where /v1/posts/recent and the rest of this service put it, and the
// data.next_cursor this endpoint shipped with, kept so already-released
// clients keep paging. data.items is likewise unchanged.
func writeTrendingPage(c *gin.Context, posts []service.PostDetail, nextCursor string) {
	if posts == nil {
		posts = []service.PostDetail{}
	}
	var meta *api.Meta
	if nextCursor != "" {
		meta = &api.Meta{NextCursor: nextCursor}
	}
	api.JSON(c.Writer, http.StatusOK, gin.H{
		"items":       posts,
		"next_cursor": nextCursor,
	}, meta)
}

// GetTrendingHashtagsFeed handles GET /v1/hashtags/trending?limit=...
// Reads from Redis sorted set `trending:hashtags:{YYYY-MM-DD}` (UTC) populated
// by post-service on every CreatePost. Falls back to a 24h SQL aggregate when
// the Redis set is empty (cold start / wiped cache / no posts yet today).
func (h *Handler) GetTrendingHashtagsFeed(c *gin.Context) {
	ctx := c.Request.Context()

	limit := 15
	if l, err := strconv.Atoi(c.DefaultQuery("limit", "15")); err == nil && l > 0 {
		if l > 30 {
			l = 30
		}
		limit = l
	}

	today := time.Now().UTC().Format("2006-01-02")
	key := "trending:hashtags:" + today
	results, rerr := h.rdb.ZRevRangeWithScores(ctx, key, 0, int64(limit-1)).Result()
	if rerr == nil && len(results) > 0 {
		out := make([]postgres.HashtagTrending24h, 0, len(results))
		for _, z := range results {
			tag, ok := z.Member.(string)
			if !ok {
				continue
			}
			out = append(out, postgres.HashtagTrending24h{
				NormalizedName: tag,
				DisplayName:    "#" + tag,
				PostCount:      int64(z.Score),
			})
		}
		api.JSON(c.Writer, http.StatusOK, gin.H{"hashtags": out}, nil)
		return
	}

	trending, err := h.svc.GetTrendingHashtags24h(ctx, limit)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	if trending == nil {
		trending = []postgres.HashtagTrending24h{}
	}

	api.JSON(c.Writer, http.StatusOK, gin.H{
		"hashtags": trending,
	}, nil)
}
