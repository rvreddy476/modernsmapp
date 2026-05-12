// Real-time hashtag stream — SSE endpoint that pushes a small JSON
// envelope for every new post that includes a given hashtag. The
// client (web /hashtag/[tag] page, mobile HashtagFeedScreen) renders
// an inline "N new posts" pill and refetches the list on tap; we
// intentionally do *not* push the full Post body over the wire so we
// don't have to re-sign media URLs in the publisher.
//
// Transport: Redis pub/sub channel `hashtag:<normalized_tag>:new_post`.
// post-service's CreatePost goroutine writes to that channel for each
// hashtag in the new post (see service/post.go). Every running
// post-service instance can subscribe — Redis fanout handles it.

package http

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	sharedhttp "github.com/atpost/shared/api"
	"github.com/gin-gonic/gin"
)

// hashtagStreamHeartbeat is how often we send a `:` SSE comment line
// to keep the connection alive through Cloudflare / Caddy. The
// default Cloudflare proxy idle timeout is 100 s; we send well below
// that.
const hashtagStreamHeartbeat = 25 * time.Second

// HashtagChannel returns the Redis pub/sub channel name for a tag.
// Exposed so the service-layer publisher and the SSE subscriber agree
// on the format.
func HashtagChannel(tag string) string {
	return "hashtag:" + strings.ToLower(strings.TrimPrefix(tag, "#")) + ":new_post"
}

// StreamHashtagPosts handles GET /v1/hashtags/:tag/stream. Holds the
// HTTP connection open as an SSE stream and forwards every published
// envelope on the matching Redis channel until the client disconnects.
func (h *Handler) StreamHashtagPosts(c *gin.Context) {
	tag := strings.ToLower(strings.TrimPrefix(strings.TrimSpace(c.Param("tag")), "#"))
	if tag == "" || len(tag) > 50 {
		sharedhttp.ErrorWithContext(
			c.Request.Context(), c.Writer,
			http.StatusBadRequest, "INVALID_TAG",
			"tag must be 1-50 chars", nil,
		)
		return
	}

	// SSE headers. X-Accel-Buffering disables nginx/proxy buffering.
	c.Writer.Header().Set("Content-Type", "text/event-stream")
	c.Writer.Header().Set("Cache-Control", "no-cache")
	c.Writer.Header().Set("Connection", "keep-alive")
	c.Writer.Header().Set("X-Accel-Buffering", "no")
	c.Writer.WriteHeaderNow()

	ctx, cancel := context.WithCancel(c.Request.Context())
	defer cancel()

	// Initial event so clients know the stream is live even before the
	// first post lands.
	fmt.Fprintf(c.Writer, "event: connected\ndata: {\"tag\":%q}\n\n", tag)
	c.Writer.Flush()

	heartbeat := time.NewTicker(hashtagStreamHeartbeat)
	defer heartbeat.Stop()

	channel := HashtagChannel(tag)
	if h.hub != nil {
		// Pooled path: one Redis SUB per channel shared across all
		// HTTP listeners for that tag. See internal/streamhub.
		sub, err := h.hub.Subscribe(ctx, channel)
		if err != nil {
			// Hub failure → degrade gracefully. We've already sent the
			// `connected` event, so close cleanly.
			return
		}
		defer sub.Cancel()
		for {
			select {
			case <-ctx.Done():
				return
			case <-heartbeat.C:
				fmt.Fprintf(c.Writer, ": keepalive\n\n")
				c.Writer.Flush()
			case payload, ok := <-sub.Msgs:
				if !ok {
					return
				}
				fmt.Fprintf(c.Writer, "event: new_post\ndata: %s\n\n", payload)
				c.Writer.Flush()
			}
		}
	}

	// Fallback path (no hub installed): one Redis SUB per HTTP client.
	// Kept so the handler still works in a degraded config.
	sub := h.rdb.Subscribe(ctx, channel)
	defer sub.Close()
	ch := sub.Channel()
	for {
		select {
		case <-ctx.Done():
			return
		case <-heartbeat.C:
			fmt.Fprintf(c.Writer, ": keepalive\n\n")
			c.Writer.Flush()
		case msg, ok := <-ch:
			if !ok {
				return
			}
			fmt.Fprintf(c.Writer, "event: new_post\ndata: %s\n\n", msg.Payload)
			c.Writer.Flush()
		}
	}
}
