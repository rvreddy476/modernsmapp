package http

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"

	"github.com/atpost/food-service/internal/store/postgres"
	"github.com/atpost/shared/api"
	"github.com/gin-gonic/gin"
)

// maxFingerprintBody bounds the body the fingerprint reads. Every food route
// that takes an Idempotency-Key has a small JSON body; the gateway's limit
// is 10 MB.
const maxFingerprintBody = 1 << 20

// requestFingerprint is the request the Idempotency-Key is bound to (audit
// L5): method, path (which names the order, assignment or refund), query,
// and a hash of the body. The store adds the caller it keys the row on.
func requestFingerprint(method, path, rawQuery string, body []byte) string {
	sum := sha256.Sum256(body)
	return method + " " + path + "?" + rawQuery + " body:" + hex.EncodeToString(sum[:])
}

// idempotencyFingerprint puts the fingerprint on the request context when an
// Idempotency-Key is sent; the body is restored for the handler.
func idempotencyFingerprint() gin.HandlerFunc {
	return func(c *gin.Context) {
		if c.GetHeader("Idempotency-Key") == "" {
			c.Next()
			return
		}
		var body []byte
		if c.Request.Body != nil {
			b, err := io.ReadAll(io.LimitReader(c.Request.Body, maxFingerprintBody+1))
			_ = c.Request.Body.Close()
			if err != nil {
				api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_BODY", "request body could not be read", nil)
				c.Abort()
				return
			}
			if len(b) > maxFingerprintBody {
				api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusRequestEntityTooLarge, "BODY_TOO_LARGE", "request body is too large", nil)
				c.Abort()
				return
			}
			body = b
			c.Request.Body = io.NopCloser(bytes.NewReader(b))
		}
		fp := requestFingerprint(c.Request.Method, c.Request.URL.Path, c.Request.URL.RawQuery, body)
		c.Request = c.Request.WithContext(postgres.WithRequestFingerprint(c.Request.Context(), fp))
		c.Next()
	}
}
