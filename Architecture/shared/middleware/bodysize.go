package middleware

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// MaxBodySize returns a Gin middleware that limits the size of incoming
// request bodies to maxBytes. Requests that exceed the limit receive a
// 413 Request Entity Too Large response. This protects services against
// oversized uploads and denial-of-service via large payloads.
func MaxBodySize(maxBytes int64) gin.HandlerFunc {
	return func(c *gin.Context) {
		if c.Request.Body != nil {
			c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxBytes)
		}
		c.Next()

		// If the body exceeded the limit, MaxBytesReader surfaces an error
		// when the handler tries to read. Detect it via the MaxBytesError
		// type and send a structured response if the handler hasn't already
		// written one.
		if c.IsAborted() {
			return
		}
		for _, err := range c.Errors {
			if _, ok := err.Err.(*http.MaxBytesError); ok {
				c.AbortWithStatusJSON(http.StatusRequestEntityTooLarge, gin.H{
					"error": gin.H{
						"code":    "BODY_TOO_LARGE",
						"message": "request body exceeds maximum allowed size",
					},
				})
				return
			}
		}
	}
}
