package http

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
)

// Search result bounds (audit M6): search pages are smaller and shallower
// than the general list bound (parsePagination allows 100 / 10,000).
const (
	maxSearchLimit   = 50
	maxSearchOffset  = 500
	maxSimilarLimit  = 10
	maxSearchQueryLn = 200
)

// clampSearchPage bounds a search page.
func clampSearchPage(limit, offset int) (int, int) {
	if limit > maxSearchLimit {
		limit = maxSearchLimit
	}
	if offset > maxSearchOffset {
		offset = maxSearchOffset
	}
	return limit, offset
}

// clipSearchQuery bounds the text a search matches on.
func clipSearchQuery(q string) string {
	if len(q) > maxSearchQueryLn {
		// Drop a rune cut in half at the boundary.
		q = strings.ToValidUTF8(q[:maxSearchQueryLn], "")
	}
	return q
}

// allowSearch applies the anonymous search limit. Signed-in callers pass;
// a signed-out caller is keyed by the client address the gateway resolved
// (X-Real-IP, which the gateway overwrites; else the connection's address).
// It writes the refusal and returns false when the caller is over the limit.
func (h *Handler) allowSearch(c *gin.Context) bool {
	if optionalUserID(c) != nil {
		return true
	}
	addr := strings.TrimSpace(c.GetHeader("X-Real-IP"))
	if addr == "" {
		addr = c.ClientIP()
	}
	if err := h.svc.AllowAnonymousSearch(c.Request.Context(), addr); err != nil {
		respondServiceError(c, err, http.StatusTooManyRequests, "RATE_LIMITED")
		return false
	}
	return true
}
