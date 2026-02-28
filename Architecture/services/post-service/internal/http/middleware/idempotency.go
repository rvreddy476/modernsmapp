package middleware

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
)

const (
	idempotencyTTL    = 24 * time.Hour
	idempotencyPrefix = "idempotency:"
)

// cachedResponse stores the status code and body of a previously executed request.
type cachedResponse struct {
	Status int
	Body   string
}

// responseCapture wraps gin.ResponseWriter to capture the response body and status.
type responseCapture struct {
	gin.ResponseWriter
	body   *bytes.Buffer
	status int
}

func (r *responseCapture) Write(b []byte) (int, error) {
	r.body.Write(b)
	return r.ResponseWriter.Write(b)
}

func (r *responseCapture) WriteHeader(code int) {
	r.status = code
	r.ResponseWriter.WriteHeader(code)
}

// Idempotency returns a Gin middleware that prevents duplicate mutations.
// If the request includes an Idempotency-Key header, the middleware checks Redis
// for a cached response. On cache miss, it proceeds with the handler and caches
// the response for 24 hours.
func Idempotency(rdb *redis.Client) gin.HandlerFunc {
	return func(c *gin.Context) {
		// Only apply to mutating methods
		if c.Request.Method == http.MethodGet || c.Request.Method == http.MethodDelete {
			c.Next()
			return
		}

		key := c.GetHeader("Idempotency-Key")
		if key == "" {
			// No key provided — pass through without idempotency checking
			c.Next()
			return
		}

		redisKey := fmt.Sprintf("%s%s", idempotencyPrefix, key)
		ctx := context.Background()

		// Check if we already processed this key
		cached, err := rdb.HGetAll(ctx, redisKey).Result()
		if err == nil && len(cached) > 0 {
			// Cache hit — return the stored response
			status := http.StatusOK
			if s, ok := cached["status"]; ok {
				if parsed, err := strconv.Atoi(s); err == nil {
					status = parsed
				}
			}
			body := cached["body"]
			c.Data(status, "application/json", []byte(body))
			c.Abort()
			return
		}

		// Cache miss — capture the response
		capture := &responseCapture{
			ResponseWriter: c.Writer,
			body:           &bytes.Buffer{},
			status:         http.StatusOK,
		}
		c.Writer = capture

		c.Next()

		// Only cache successful responses (2xx)
		if capture.status >= 200 && capture.status < 300 {
			pipe := rdb.Pipeline()
			pipe.HSet(ctx, redisKey, map[string]interface{}{
				"status": strconv.Itoa(capture.status),
				"body":   capture.body.String(),
			})
			pipe.Expire(ctx, redisKey, idempotencyTTL)
			if _, err := pipe.Exec(ctx); err != nil {
				// Log but don't fail — the request already succeeded
				fmt.Printf("[idempotency] failed to cache response for key %s: %v\n", key, err)
			}
		}
	}
}
