package http

import (
	"log/slog"
	"net/http"

	"github.com/atpost/shared/api"
	"github.com/gin-gonic/gin"
)

// serverError answers an unexpected failure with a fixed message. The real
// error goes to the log, with the request's method and route, and never to
// the client: store and driver errors carry table, column and constraint
// names. Intentional 4xx validation messages do not use this.
func serverError(c *gin.Context, err error) {
	slog.ErrorContext(c.Request.Context(), "monetization: request failed",
		"error", err,
		"method", c.Request.Method,
		"route", c.FullPath(),
	)
	api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", "Something went wrong. Please try again.", nil)
}
