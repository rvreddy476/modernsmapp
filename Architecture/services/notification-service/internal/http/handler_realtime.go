package http

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/atpost/shared/api"
	"github.com/atpost/shared/realtime"
	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
)

// RealtimeHandler hosts the generic topic-subscription SSE endpoint
// used by FiGo + Mopedu (and any future domain) to push events to
// foreground clients without polling.
//
//	GET /v1/realtime/sse?token=<topic-token>&topics=t1,t2
//
// The token is HMAC-signed by the domain service that owns the topics
// (food-service, rider-service) and lists which topics the user is
// authorized to subscribe to. The `topics` query is the actual subset
// the client wants this connection — anything not in the token is
// rejected.
type RealtimeHandler struct {
	verifier *realtime.TokenVerifier
	rdb      *redis.Client
}

// NewRealtimeHandler returns a handler that verifies tokens with
// `secret` and subscribes via the supplied redis client.
func NewRealtimeHandler(secret []byte, rdb *redis.Client) *RealtimeHandler {
	return &RealtimeHandler{
		verifier: realtime.NewTokenVerifier(secret),
		rdb:      rdb,
	}
}

// Register attaches the SSE route. The /v1/realtime path is registered
// before the internal-key middleware in main.go so end-user clients can
// reach it via the api-gateway.
func (h *RealtimeHandler) Register(r *gin.Engine) {
	r.GET("/v1/realtime/sse", h.stream)
}

func (h *RealtimeHandler) stream(c *gin.Context) {
	token := c.Query("token")
	if token == "" {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusUnauthorized, "MISSING_TOKEN", "token query param is required", nil)
		return
	}
	allowed, subject, err := h.verifier.Verify(token)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusUnauthorized, "INVALID_TOKEN", err.Error(), nil)
		return
	}

	requested := splitCSV(c.Query("topics"))
	if len(requested) == 0 {
		requested = allowed
	}
	resolved := make([]string, 0, len(requested))
	for _, t := range requested {
		if !realtime.MatchTopic(allowed, t) {
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusForbidden, "TOPIC_FORBIDDEN", "topic not in token: "+t, nil)
			return
		}
		resolved = append(resolved, t)
	}
	if len(resolved) == 0 {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "NO_TOPICS", "no topics requested", nil)
		return
	}

	channels := make([]string, len(resolved))
	for i, t := range resolved {
		channels[i] = realtime.Channel(t)
	}

	ctx, cancel := context.WithCancel(c.Request.Context())
	defer cancel()
	sub := h.rdb.Subscribe(ctx, channels...)
	defer sub.Close()

	c.Writer.Header().Set("Content-Type", "text/event-stream")
	c.Writer.Header().Set("Cache-Control", "no-cache")
	c.Writer.Header().Set("Connection", "keep-alive")
	c.Writer.Header().Set("X-Accel-Buffering", "no")
	c.Writer.WriteHeaderNow()
	fmt.Fprintf(c.Writer, "event: connected\ndata: {\"subject\":%q,\"topics\":%q}\n\n", subject, strings.Join(resolved, ","))
	c.Writer.Flush()

	heartbeat := time.NewTicker(25 * time.Second)
	defer heartbeat.Stop()
	ch := sub.Channel()
	for {
		select {
		case <-ctx.Done():
			return
		case <-heartbeat.C:
			fmt.Fprint(c.Writer, ": keepalive\n\n")
			c.Writer.Flush()
		case msg, ok := <-ch:
			if !ok {
				return
			}
			topic := realtime.TopicFromChannel(msg.Channel)
			fmt.Fprintf(c.Writer, "event: %s\ndata: %s\n\n", topic, msg.Payload)
			c.Writer.Flush()
		}
	}
}

func splitCSV(s string) []string {
	if s == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	out := parts[:0]
	for _, p := range parts {
		if t := strings.TrimSpace(p); t != "" {
			out = append(out, t)
		}
	}
	return out
}
