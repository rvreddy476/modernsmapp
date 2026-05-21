package http

import (
	"net/http"

	"github.com/atpost/shared/api"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// IssueRealtimeToken — POST /v1/rider/realtime/token
//
// Returns an HMAC-signed token granting the caller subscribe access to
// the SSE topics for their own rides + (if they're a partner) their
// offer topic. Client passes the token to /v1/realtime/sse on
// notification-service.
func (h *Handler) IssueRealtimeToken(c *gin.Context) {
	raw := c.GetHeader("X-User-Id")
	uid, err := uuid.Parse(raw)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "invalid user id", nil)
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
