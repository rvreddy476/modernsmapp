package http

import (
	"errors"
	"net/http"

	"github.com/atpost/post-service/internal/service"
	"github.com/atpost/shared/api"
	"github.com/gin-gonic/gin"
)

// writeHeartOnlyError answers the heart-only and no-reel-repost refusals
// (service/heart_only.go, founder decision 24 Oct 2026) and reports whether
// it wrote one:
//
//	400 REACTION_NOT_ALLOWED {details: {allowed: ["like"]}}
//	400 REPOST_NOT_ALLOWED   {details: {content_type}}
func writeHeartOnlyError(c *gin.Context, err error) bool {
	ctx := c.Request.Context()
	if errors.Is(err, service.ErrReactionNotAllowed) {
		api.ErrorWithContext(ctx, c.Writer, http.StatusBadRequest, "REACTION_NOT_ALLOWED",
			"Posts, reels and videos take a heart only",
			map[string]any{"allowed": service.PostReactionsAllowed()})
		return true
	}
	var repost *service.RepostNotAllowedError
	if errors.As(err, &repost) {
		api.ErrorWithContext(ctx, c.Writer, http.StatusBadRequest, "REPOST_NOT_ALLOWED",
			"Reels cannot be reposted",
			map[string]any{"content_type": repost.ContentType})
		return true
	}
	return false
}
