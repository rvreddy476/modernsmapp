package http

import (
	"context"
	"errors"
	"net/http"

	"github.com/atpost/chat-message-service/internal/service"
	"github.com/atpost/chat-shared/api"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// DisappearingService is the conversation-wide disappearing-message timer
// (reached by a checked assertion so older ChatService fakes keep
// compiling). It is set through PUT /v1/chat/conversations/:id/settings
// {"disappear_after_ms": N} and read back from that route's GET, from
// GET /v1/chat/conversations/:id and from every inbox row.
type DisappearingService interface {
	SetDisappearingTimer(ctx context.Context, actorID, conversationID uuid.UUID, afterMs int64) (*service.DisappearingTimer, error)
}

// writeDisappearingError maps a timer change failure: 400 for a value
// outside the allowed set, 403 for a group member who is not owner/admin.
func (h *Handler) writeDisappearingError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, service.ErrInvalidDisappearTimer):
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_PARAM", err.Error(), nil, nil)
	case errors.Is(err, service.ErrNotPermittedRole):
		api.Error(c.Writer, http.StatusForbidden, "FORBIDDEN", "only the group owner or an admin can change disappearing messages", nil, nil)
	default:
		h.log.Warn("failed to update disappearing timer", "err", err, "request_id", RequestIDFromContext(c))
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_REQUEST", err.Error(), nil, nil)
	}
}
