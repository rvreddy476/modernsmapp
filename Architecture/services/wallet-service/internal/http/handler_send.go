package http

import (
	"net/http"

	"github.com/atpost/shared/api"
	"github.com/atpost/wallet-service/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

type sendRequest struct {
	RecipientUserID *string `json:"recipient_user_id,omitempty"`
	RecipientPhone  string  `json:"recipient_phone,omitempty"`
	AmountPaise     int64   `json:"amount_paise"`
	Label           string  `json:"label,omitempty"`
	IdempotencyKey  string  `json:"idempotency_key"`
}

// PostSend handles POST /v1/wallet/send.
func (h *Handler) PostSend(c *gin.Context) {
	senderID, ok := getUserID(c)
	if !ok {
		return
	}
	var req sendRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_BODY", err.Error(), nil)
		return
	}
	if req.IdempotencyKey == "" {
		req.IdempotencyKey = c.GetHeader("Idempotency-Key")
	}
	var recipientUUID *uuid.UUID
	if req.RecipientUserID != nil && *req.RecipientUserID != "" {
		parsed, err := uuid.Parse(*req.RecipientUserID)
		if err != nil {
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "invalid recipient_user_id", nil)
			return
		}
		recipientUUID = &parsed
	}
	out, err := h.svc.Send(c.Request.Context(), senderID, service.SendRequest{
		RecipientUserID: recipientUUID,
		RecipientPhone:  req.RecipientPhone,
		AmountPaise:     req.AmountPaise,
		Label:           req.Label,
		IdempotencyKey:  req.IdempotencyKey,
	})
	if err != nil {
		respondServiceError(c, err, http.StatusInternalServerError, "SEND_FAILED")
		return
	}
	api.JSONWithContext(c.Request.Context(), c.Writer, http.StatusOK, out)
}
