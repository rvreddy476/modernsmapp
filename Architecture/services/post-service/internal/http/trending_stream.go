// SSE endpoint that streams trending-hashtag snapshots as they're
// published by the internal/trending.Publisher worker. One channel
// for the whole cluster — every connected client gets the same
// debounced top-N updates without any client-side filtering.

package http

import (
	"context"
	"fmt"
	"time"

	"github.com/atpost/post-service/internal/trending"
	"github.com/gin-gonic/gin"
)

// trendingStreamHeartbeat keeps the SSE connection alive through the
// Cloudflare 100 s idle timeout. Sent as an SSE comment (`:`), which
// clients ignore.
const trendingStreamHeartbeat = 25 * time.Second

// StreamTrendingHashtags handles GET /v1/hashtags/trending/stream.
// Subscribes to the trending:hashtags:updates Redis channel and
// forwards every published Snapshot as an `event: trending` SSE
// frame. The publish cadence is owned by trending.Publisher
// (default 30 s, debounced — only fires when the top-N actually
// changes), so even hundreds of connected clients put near-zero
// load on Redis.
func (h *Handler) StreamTrendingHashtags(c *gin.Context) {
	c.Writer.Header().Set("Content-Type", "text/event-stream")
	c.Writer.Header().Set("Cache-Control", "no-cache")
	c.Writer.Header().Set("Connection", "keep-alive")
	c.Writer.Header().Set("X-Accel-Buffering", "no")
	c.Writer.WriteHeaderNow()

	ctx, cancel := context.WithCancel(c.Request.Context())
	defer cancel()

	// Send an initial connected event so the client knows it's live
	// before the first publish tick fires.
	fmt.Fprintf(c.Writer, "event: connected\ndata: {\"channel\":%q}\n\n", trending.PubSubChannel)
	c.Writer.Flush()

	heartbeat := time.NewTicker(trendingStreamHeartbeat)
	defer heartbeat.Stop()

	if h.hub != nil {
		sub, err := h.hub.Subscribe(ctx, trending.PubSubChannel)
		if err != nil {
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
				fmt.Fprintf(c.Writer, "event: trending\ndata: %s\n\n", payload)
				c.Writer.Flush()
			}
		}
	}

	// Fallback when no hub is installed — one Redis SUB per HTTP client.
	sub := h.rdb.Subscribe(ctx, trending.PubSubChannel)
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
			fmt.Fprintf(c.Writer, "event: trending\ndata: %s\n\n", msg.Payload)
			c.Writer.Flush()
		}
	}
}
