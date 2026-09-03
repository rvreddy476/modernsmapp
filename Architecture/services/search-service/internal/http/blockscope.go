package http

import (
	"log/slog"
	"net/http"

	"github.com/atpost/search-service/internal/store/search"
	"github.com/atpost/shared/api"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// resolveBlockScope resolves the viewer's suppression set ONCE per
// request and attaches it to the request context, where the store layer
// applies it to every query it runs (Module 2 M2-P0-4).
//
// The set is: blocks in both directions, plus the viewer's own outgoing
// mutes. Reverse mutes are excluded — see graphclient.BlockedIDs.
//
// This is middleware rather than per-handler code on purpose. Search has
// many surfaces — direct post search, legacy universal search, ranked
// multi-entity search, hashtags, autocomplete, trending, suggested,
// products, events, messages — and a per-handler approach only has to be
// forgotten once to leave a hole. Resolving here means a surface added
// tomorrow is filtered by default.
//
// FAIL CLOSED: when the viewer is authenticated and the block set cannot
// be resolved, the request is rejected with 503 rather than answered with
// unfiltered results. An unavailable search is recoverable; showing
// someone content from an account they blocked is not.
//
// Anonymous viewers (no X-User-Id) have no block relationships, so they
// pass through with an empty scope. They only ever see public, approved
// content, which the index-time gate already guarantees — and, with the
// private-account rule below, never a private author's posts.
//
// PRIVATE ACCOUNTS: the scope also carries the viewer's follow set, which
// the posts query uses to let a follower through a private author's
// `author_is_private` exclusion. FollowingIDs is best-effort and cached
// 60s; when it cannot be resolved the follow set is EMPTY, which hides
// more, never less — a follower may briefly miss a private friend's post
// during a graph blip, but a stranger can never gain one. (It is capped at
// 500 follows; a viewer following more private accounts than that will not
// see the overflow in search.)
func (h *Handler) resolveBlockScope() gin.HandlerFunc {
	return func(c *gin.Context) {
		raw := c.GetHeader("X-User-Id")
		if raw == "" {
			c.Request = c.Request.WithContext(
				search.WithViewerScope(c.Request.Context(), "", nil, nil))
			c.Next()
			return
		}

		viewerID, err := uuid.Parse(raw)
		if err != nil {
			// A malformed id is not an anonymous viewer — we cannot tell
			// whose blocks to apply, so we must not answer.
			api.ErrorWithContext(c.Request.Context(), c.Writer,
				http.StatusUnauthorized, "UNAUTHORIZED", "Invalid X-User-Id", nil)
			c.Abort()
			return
		}

		blocked, err := h.graphClient.BlockedIDs(c.Request.Context(), viewerID)
		if err != nil {
			slog.Error("search: block scope unresolved; refusing to serve unfiltered results",
				"viewer", viewerID, "error", err)
			api.ErrorWithContext(c.Request.Context(), c.Writer,
				http.StatusServiceUnavailable, "BLOCK_STATE_UNAVAILABLE",
				"Search is temporarily unavailable", nil)
			c.Abort()
			return
		}

		following := h.graphClient.FollowingIDs(c.Request.Context(), viewerID, 500)

		c.Request = c.Request.WithContext(
			search.WithViewerScope(c.Request.Context(), viewerID.String(), blocked, following))
		c.Next()
	}
}
