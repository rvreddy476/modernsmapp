package http

import (
	"net/http"

	"github.com/atpost/shared/api"
	"github.com/gin-gonic/gin"
)

// RegisterReelFeedRoutes keeps the retired reel feed routes answering 410.
//
// GET /v1/reels/feed and POST /v1/reels/feed/refresh were a legacy
// chronological reel feed (retired 2026-10-30, audit MEDIUM 7). It served
// reels from private accounts, blocked users and hidden authors (it never
// ran the author gate every other listing runs), read the viewer's
// reaction from the legacy reel_reactions store, made two Scylla reads and
// a Redis call per reel, and its cursor skipped reels and ended the feed
// early. No first-party client calls it: Android and the web read the
// reels feed from feed-service (GET /v1/feed/reels). The routes answer 410
// with that replacement named, as the retired view route does.
func (h *Handler) RegisterReelFeedRoutes(r *gin.Engine) {
	r.GET("/v1/reels/feed", reelFeedGone)
	r.POST("/v1/reels/feed/refresh", reelFeedGone)
}

func reelFeedGone(c *gin.Context) {
	api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusGone, "ROUTE_RETIRED",
		"The legacy reel feed has been retired; use GET /v1/feed/reels",
		map[string]string{"replacement": "GET /v1/feed/reels"})
}
