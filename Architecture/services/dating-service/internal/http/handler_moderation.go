// HTTP handlers for /v1/dating/moderation (internal-only).
//
// SHADOW MODE FOR v1 (CRITICAL RULES #5):
// This endpoint is INTERNAL-ONLY (called by message-service when it sees
// a dating-context message). The response carries the layer-1 verdict;
// in shadow mode action_taken="shadow" so message-service must NOT take
// a user-visible action regardless of the confidence score.
package http

import (
	"net/http"

	"github.com/atpost/dating-service/internal/service"
	"github.com/atpost/shared/api"
	"github.com/gin-gonic/gin"
)

// scanRequest is the body for the layer-1 endpoint.
type scanRequest struct {
	MessageID      string `json:"message_id"`
	ConversationID string `json:"conversation_id"`
	SenderID       string `json:"sender_id"`
	Body           string `json:"body"`
}

// PostScanMessage — POST /v1/dating/moderation/scan.
//
// Internal-only. Response includes a "shadow_mode" flag so the caller can
// confirm it must NOT act on action_taken in shadow.
//
// Same service-caller check as the internal family (authorizeServiceCaller):
// X-Internal-Service-Key (the old X-Internal-Key header is gone), fails
// closed when no key is configured, and refuses a gateway user identity.
func (h *Handler) PostScanMessage(c *gin.Context) {
	if !h.authorizeServiceCaller(c, OpModerationScan) {
		return
	}
	var body scanRequest
	if err := c.ShouldBindJSON(&body); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_BODY", err.Error(), nil)
		return
	}
	msgID, err := parseUUIDValue(body.MessageID)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_REQUEST", "invalid message_id", nil)
		return
	}
	convID, err := parseUUIDValue(body.ConversationID)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_REQUEST", "invalid conversation_id", nil)
		return
	}
	senderID, _ := parseUUIDValue(body.SenderID)
	out, err := h.svc.ScanLayer1(c.Request.Context(), service.ScanRequest{
		MessageID:      msgID,
		ConversationID: convID,
		SenderID:       senderID,
		Body:           body.Body,
	})
	if err != nil {
		respondServiceError(c, err, http.StatusInternalServerError, "SCAN_FAILED")
		return
	}
	api.JSON(c.Writer, http.StatusOK, out, nil)
}
