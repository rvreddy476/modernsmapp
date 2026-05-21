package http

import (
	"net/http"

	"github.com/atpost/shared/api"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// IssueRealtimeToken — POST /v1/food/realtime/token
//
// Returns an HMAC-signed token granting the caller subscribe access to
// the SSE topics for their own orders / restaurants / delivery
// assignments. Client passes the token to /v1/realtime/sse on
// notification-service.
func (h *Handler) IssueRealtimeToken(c *gin.Context) {
	uid, ok := h.requireUser(c)
	if !ok {
		return
	}
	token, topics, err := h.svc.IssueRealtimeToken(c.Request.Context(), uid)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "REALTIME_TOKEN_FAILED", err.Error(), nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, gin.H{
		"token":  token,
		"topics": topics,
	}, nil)
}

// requireUser extracts the X-User-Id. Mirrors the helper used by the
// other handler files; kept private to avoid polluting handler.go.
func (h *Handler) requireUser(c *gin.Context) (uuid.UUID, bool) {
	raw := c.GetHeader("X-User-Id")
	id, err := uuid.Parse(raw)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "invalid user id", nil)
		return uuid.Nil, false
	}
	return id, true
}
