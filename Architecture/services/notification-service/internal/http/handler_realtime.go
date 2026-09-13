package http

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/atpost/shared/api"
	"github.com/atpost/shared/realtime"
	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
)

// Rate-limit defaults. The handler reads these at construction time
// from REALTIME_MAX_CONNS_PER_SUBJECT + REALTIME_MAX_OPENS_PER_MINUTE
// so production can tune without a redeploy.
//
//   - concurrent connections per token subject (user_id): protects
//     against a stolen token holding open unbounded streams.
//   - opens per minute per subject: protects against churn attacks
//     that open + abandon connections faster than the conn counter
//     can drain on disconnect.
const (
	defaultMaxConnsPerSubject = 8
	defaultMaxOpensPerMinute  = 60
)

func envInt(key string, fallback int) int {
	if raw := os.Getenv(key); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil && n > 0 {
			return n
		}
	}
	return fallback
}

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
	verifier           *realtime.TokenVerifier
	rdb                *redis.Client
	maxConnsPerSubject int
	maxOpensPerMinute  int
}

// NewRealtimeHandler returns a handler that verifies tokens with
// `secret` and subscribes via the supplied redis client. Rate limits
// are read from env at construction time.
func NewRealtimeHandler(secret []byte, rdb *redis.Client) *RealtimeHandler {
	return &RealtimeHandler{
		verifier:           realtime.NewTokenVerifier(secret),
		rdb:                rdb,
		maxConnsPerSubject: envInt("REALTIME_MAX_CONNS_PER_SUBJECT", defaultMaxConnsPerSubject),
		maxOpensPerMinute:  envInt("REALTIME_MAX_OPENS_PER_MINUTE", defaultMaxOpensPerMinute),
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

	// Rate limits — refuse the connection BEFORE we open the Pub/Sub
	// subscription so abusive clients can't even pin a Redis sub.
	if err := h.enforceOpenRate(c.Request.Context(), subject); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusTooManyRequests, "RATE_LIMIT_OPENS", err.Error(), nil)
		return
	}
	release, err := h.acquireConnSlot(c.Request.Context(), subject)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusTooManyRequests, "RATE_LIMIT_CONNS", err.Error(), nil)
		return
	}
	defer release()

	requested := splitCSV(c.Query("topics"))
	if len(requested) == 0 {
		requested = allowed
	}
	resolved := make([]string, 0, len(requested))
	seen := make(map[string]bool, len(requested))
	for _, t := range requested {
		// '=' and line breaks are reserved by the resume cursor and SSE
		// framing (realtime_cursor.go).
		if !validCursorTopic(t) {
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_TOPIC", "topic contains a reserved character: "+strconv.Quote(t), nil)
			return
		}
		if !realtime.MatchTopic(allowed, t) {
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusForbidden, "TOPIC_FORBIDDEN", "topic not in token: "+t, nil)
			return
		}
		if seen[t] {
			continue
		}
		seen[t] = true
		resolved = append(resolved, t)
	}
	if len(resolved) == 0 {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "NO_TOPICS", "no topics requested", nil)
		return
	}

	ctx, cancel := context.WithCancel(c.Request.Context())
	defer cancel()

	// CR3-rt: Streams (durable + replay). The client resumes with the
	// `Last-Event-ID` header (W3C SSE) or the `since` query param.
	//
	// B5b: ONE CURSOR PER TOPIC. Stream ids are per stream, so a single id
	// applied to every topic replayed or skipped events on multi-topic
	// connections. The cursor is a `topic=streamID,...` vector and every
	// emitted `id:` is the full vector, so the last id a client saw resumes
	// all of its topics exactly. A bare legacy id still resumes a one-topic
	// connection; a malformed cursor starts live and says so in the
	// connected frame ("resume":"invalid") instead of 400ing, because
	// EventSource never reconnects after a non-200. Format and rules:
	// realtime_cursor.go.
	since := c.GetHeader("Last-Event-ID")
	if since == "" {
		since = c.Query("since")
	}
	var client streamClient
	if h.rdb != nil {
		client = h.rdb
	}
	reader, resume := newTopicCursorReader(ctx, client, resolved, since, 25*time.Second)

	c.Writer.Header().Set("Content-Type", "text/event-stream")
	c.Writer.Header().Set("Cache-Control", "no-cache")
	c.Writer.Header().Set("Connection", "keep-alive")
	c.Writer.Header().Set("X-Accel-Buffering", "no")
	c.Writer.WriteHeaderNow()
	// The connected frame carries the starting vector as its id, so a
	// client that disconnects before any event still resumes without a gap.
	if initial := reader.Cursor(); initial != "" {
		fmt.Fprintf(c.Writer, "id: %s\n", initial)
	}
	fmt.Fprintf(c.Writer, "event: connected\ndata: {\"subject\":%q,\"topics\":%q,\"since\":%q,\"resume\":%q}\n\n",
		subject, strings.Join(resolved, ","), since, string(resume))
	c.Writer.Flush()

	// Loop: XREAD BLOCK is the heartbeat — it returns nil after 25s of
	// no events, which is also when we want to emit an SSE keepalive.
	// Cancel + writer-write errors break the loop and close cleanly.
	for {
		if ctx.Err() != nil {
			return
		}
		events, err := reader.Read(ctx)
		if err != nil {
			slog.Warn("realtime: XREAD failed", "subject", subject, "error", err)
			// Brief backoff then continue — a transient Redis blip
			// shouldn't kill the stream.
			select {
			case <-ctx.Done():
				return
			case <-time.After(1 * time.Second):
			}
			continue
		}
		if len(events) == 0 {
			if _, werr := fmt.Fprint(c.Writer, ": keepalive\n\n"); werr != nil {
				return
			}
			c.Writer.Flush()
			continue
		}
		for _, e := range events {
			body, _ := json.Marshal(e.Event)
			// id: the full per-topic resume vector as of this event.
			if _, werr := fmt.Fprintf(c.Writer, "id: %s\nevent: %s\ndata: %s\n\n",
				e.Cursor, e.Topic, string(body)); werr != nil {
				return
			}
		}
		c.Writer.Flush()
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

// enforceOpenRate increments a per-subject 60-second counter and
// returns an error when it exceeds maxOpensPerMinute. Treats Redis
// outages as fail-open so a Redis hiccup doesn't kill the gateway —
// the concurrent-connection cap below is the harder protection.
func (h *RealtimeHandler) enforceOpenRate(ctx context.Context, subject string) error {
	if h.rdb == nil || h.maxOpensPerMinute <= 0 {
		return nil
	}
	key := "rt:opens:" + subject
	pipe := h.rdb.Pipeline()
	incr := pipe.Incr(ctx, key)
	pipe.Expire(ctx, key, 60*time.Second)
	if _, err := pipe.Exec(ctx); err != nil {
		slog.Warn("realtime: open-rate counter unavailable; fail-open", "subject", subject, "error", err)
		return nil
	}
	if incr.Val() > int64(h.maxOpensPerMinute) {
		return fmt.Errorf("opens per minute exceeded (max %d)", h.maxOpensPerMinute)
	}
	return nil
}

// acquireConnSlot reserves one of the maxConnsPerSubject concurrent
// slots and returns a release func the caller must invoke on
// disconnect. The 5-minute TTL on the counter ensures abandoned
// connections from a crashed gateway eventually drain.
//
// Fail-open on Redis outage for the same reason as enforceOpenRate;
// the verified-token check above is still the primary auth gate.
func (h *RealtimeHandler) acquireConnSlot(ctx context.Context, subject string) (release func(), err error) {
	noop := func() {}
	if h.rdb == nil || h.maxConnsPerSubject <= 0 {
		return noop, nil
	}
	key := "rt:conns:" + subject
	pipe := h.rdb.Pipeline()
	incr := pipe.Incr(ctx, key)
	// Refresh TTL on every open so the counter stays alive while any
	// connection is held.
	pipe.Expire(ctx, key, 5*time.Minute)
	if _, perr := pipe.Exec(ctx); perr != nil {
		slog.Warn("realtime: conn-counter unavailable; fail-open", "subject", subject, "error", perr)
		return noop, nil
	}
	if incr.Val() > int64(h.maxConnsPerSubject) {
		// Decrement immediately — we're about to reject.
		h.rdb.Decr(ctx, key)
		return noop, fmt.Errorf("concurrent connections exceeded (max %d)", h.maxConnsPerSubject)
	}
	return func() {
		// Use background ctx so the release survives a client cancel.
		h.rdb.Decr(context.Background(), key)
	}, nil
}
