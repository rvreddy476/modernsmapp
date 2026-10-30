package http

import (
	"net/http"
	"time"

	"github.com/atpost/identity-shared/api"
	"github.com/gin-gonic/gin"
)

// Server limits. Every request this service handles is a small JSON body, so
// 64 KiB is generous; a larger body is refused before anything reads it. The
// rate limiters and the idempotency middleware buffer the whole body with
// io.ReadAll, which without this cap let one client make the process hold a
// multi-hundred-megabyte /login body in memory before any limit applied.
const (
	MaxRequestBodyBytes int64 = 64 << 10

	serverReadHeaderTimeout = 5 * time.Second
	serverReadTimeout       = 15 * time.Second
	serverWriteTimeout      = 30 * time.Second
	serverIdleTimeout       = 120 * time.Second
	serverMaxHeaderBytes    = 32 << 10
)

// NewServer is the auth-service HTTP server with timeouts set. Without
// ReadHeaderTimeout a client that sends headers one byte at a time holds a
// goroutine and a file descriptor for as long as it likes.
func NewServer(addr string, handler http.Handler) *http.Server {
	return &http.Server{
		Addr:              addr,
		Handler:           handler,
		ReadHeaderTimeout: serverReadHeaderTimeout,
		ReadTimeout:       serverReadTimeout,
		WriteTimeout:      serverWriteTimeout,
		IdleTimeout:       serverIdleTimeout,
		MaxHeaderBytes:    serverMaxHeaderBytes,
	}
}

// BodyLimitMiddleware caps the request body at limit bytes. It must be the
// first middleware registered, ahead of anything that reads the body. A
// declared Content-Length over the limit is refused with 413 at once; a
// chunked body is wrapped in http.MaxBytesReader, so a read past the limit
// fails instead of buffering it.
func BodyLimitMiddleware(limit int64) gin.HandlerFunc {
	return func(c *gin.Context) {
		if c.Request.ContentLength > limit {
			api.Error(c.Writer, http.StatusRequestEntityTooLarge, "PAYLOAD_TOO_LARGE", "Request body too large", nil, nil)
			c.Abort()
			return
		}
		if c.Request.Body != nil {
			c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, limit)
		}
		c.Next()
	}
}

// uniqueNonEmpty returns the non-empty values in order, without repeats.
func uniqueNonEmpty(values ...string) []string {
	out := make([]string, 0, len(values))
	for _, v := range values {
		if v == "" {
			continue
		}
		dup := false
		for _, o := range out {
			if o == v {
				dup = true
				break
			}
		}
		if !dup {
			out = append(out, v)
		}
	}
	return out
}
