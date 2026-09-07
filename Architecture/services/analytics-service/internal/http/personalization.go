package http

import (
	"net/http"
	"time"

	"github.com/atpost/analytics-service/internal/personalization"
	"github.com/gin-gonic/gin"
)

// The personalization warmer's operator surface.
//
// The warmer runs on a timer, which is right for production and useless
// for verifying a change: "ingest some watch events, then look at the
// ranking" becomes "ingest some watch events, wait up to fifteen minutes,
// then look at the ranking". These two routes make the pass observable and
// repeatable.
//
// They sit under /internal/ deliberately. The gateway refuses any path
// containing "/internal/" to a caller without an admin scope
// (api-gateway requireAdminForInternalPaths), and analytics-service
// requires the shared internal-service key on every /v1 route, so the
// endpoint is reachable by operators and by service-to-service calls and
// by nobody else. Running the pass is idempotent — it recomputes and
// rewrites, it never accumulates — so the worst an extra call costs is
// one extra scan.

// WithPersonalizationWarmer wires the warmer. Optional: without it the two
// routes are not registered at all, rather than registered and returning
// an error, so the route list reflects what the service can actually do.
func (h *Handler) WithPersonalizationWarmer(w *personalization.Warmer) *Handler {
	h.personalization = w
	return h
}

// RunPersonalization is POST /v1/analytics/internal/personalization/run.
// Synchronous: the caller wants to inspect Redis immediately afterwards,
// so returning before the write would defeat the purpose.
func (h *Handler) RunPersonalization(c *gin.Context) {
	if h.personalization == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": gin.H{
			"code": "SERVICE_UNAVAILABLE", "message": "personalization warmer not configured"}})
		return
	}
	stats, err := h.personalization.Run(c.Request.Context())
	if err != nil {
		// Partial success is the normal failure mode — the projections
		// are independent — so the stats go back with the error rather
		// than being swallowed by it.
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": gin.H{"code": "PERSONALIZATION_FAILED", "message": err.Error()},
			"data":  stats,
		})
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": stats})
}

// PersonalizationStatus is GET /v1/analytics/internal/personalization —
// when the last pass ran and what it wrote.
func (h *Handler) PersonalizationStatus(c *gin.Context) {
	if h.personalization == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": gin.H{
			"code": "SERVICE_UNAVAILABLE", "message": "personalization warmer not configured"}})
		return
	}
	last, stats := h.personalization.LastRun()
	body := gin.H{"last_run": nil, "stats": stats}
	if !last.IsZero() {
		body["last_run"] = last.UTC().Format(time.RFC3339)
	}
	c.JSON(http.StatusOK, gin.H{"data": body})
}
