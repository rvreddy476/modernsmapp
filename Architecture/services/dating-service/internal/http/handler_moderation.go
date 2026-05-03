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
	"os"

	"github.com/atpost/dating-service/internal/service"
	"github.com/atpost/shared/api"
	"github.com/gin-gonic/gin"
)

// internalKeyFromHeader returns the X-Internal-Key header for s2s auth.
func internalKeyFromHeader(c *gin.Context) string {
	return c.GetHeader("X-Internal-Key")
}

// requireInternalAuth gates internal endpoints. If INTERNAL_SERVICE_KEY is
// unset (typical local dev) the gate is open. In production the env var
// must be set and the caller must include it.
func requireInternalAuth(c *gin.Context) bool {
	expected := os.Getenv("INTERNAL_SERVICE_KEY")
	if expected == "" {
		return true
	}
	got := internalKeyFromHeader(c)
	if got != expected {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusUnauthorized, "AUTH_REQUIRED", "internal auth required", nil)
		return false
	}
	return true
}

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
func (h *Handler) PostScanMessage(c *gin.Context) {
	if !requireInternalAuth(c) {
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
