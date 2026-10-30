package http

import (
	"errors"
	"net/http"
	"time"

	"github.com/atpost/chat-shared/api"
	"github.com/gin-gonic/gin"
)

// Server timeouts (audit L1). gin.Engine.Run set none, so anything that
// reached the pod (inside the cluster) could hold a connection open
// indefinitely with a slow header or body. message-service has no
// long-lived routes — websockets live in ws-gateway — so plain request
// bounds fit every route. WriteTimeout is generous because a few routes make
// serial calls to peer services (presence, group creation).
const (
	ServerReadHeaderTimeout = 5 * time.Second
	ServerReadTimeout       = 15 * time.Second
	ServerWriteTimeout      = 60 * time.Second
	ServerIdleTimeout       = 120 * time.Second
)

// NewServer is message-service's HTTP server with the timeouts above.
func NewServer(addr string, handler http.Handler) *http.Server {
	return &http.Server{
		Addr:              addr,
		Handler:           handler,
		ReadHeaderTimeout: ServerReadHeaderTimeout,
		ReadTimeout:       ServerReadTimeout,
		WriteTimeout:      ServerWriteTimeout,
		IdleTimeout:       ServerIdleTimeout,
	}
}

// MaxMessageBodyBytes bounds the body of the routes that carry message text
// (send, edit, schedule; audit H4). The text itself is capped at 2000
// characters; 64 KiB leaves room for JSON escaping (up to 12 bytes per
// emoji) and every other field, and stops a multi-megabyte body at the
// handler instead of the gateway's 10 MB edge cap.
const MaxMessageBodyBytes int64 = 64 << 10

// limitBody caps the request body; reading past n fails the JSON bind,
// which bindError answers 413.
func limitBody(n int64) gin.HandlerFunc {
	return func(c *gin.Context) {
		if c.Request.Body != nil {
			c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, n)
		}
		c.Next()
	}
}

// writeBindError answers a failed JSON bind: 413 PAYLOAD_TOO_LARGE past the
// body cap, otherwise the usual 400 BAD_REQUEST.
func writeBindError(c *gin.Context, err error) {
	var tooLarge *http.MaxBytesError
	if errors.As(err, &tooLarge) {
		api.Error(c.Writer, http.StatusRequestEntityTooLarge, "PAYLOAD_TOO_LARGE", "request body is too large", nil, nil)
		return
	}
	api.Error(c.Writer, http.StatusBadRequest, "BAD_REQUEST", "Invalid request body", err.Error(), nil)
}
