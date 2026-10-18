package http

import (
	"errors"
	"log/slog"
	"net/http"
	"strings"

	"github.com/atpost/shared/api"
	"github.com/atpost/trust-safety-service/internal/service"
	"github.com/atpost/trust-safety-service/internal/store/postgres"
	"github.com/gin-gonic/gin"
)

// ConversationReportPath is the service-only route chat message-service
// calls when a member reports a whole conversation from its chat settings
// (chat-parity fix pass 2). It mirrors MessageReportPath:
//
//	POST /v1/internal/reports/conversations
//	X-Internal-Service-Key: <key>     (no end-user identity headers)
//	{
//	  "reporter_id":       "<uuid>",                         required
//	  "entity_type":       "user" (a DM: the other person) |
//	                       "conversation" (a group),         required
//	  "entity_id":         "<uuid>" (the user, or the conversation id), required
//	  "conversation_id":   "<uuid>",                         required
//	  "conversation_type": "direct|group",                   required
//	  "reason":            "spam|harassment|hate|sexual|violence|scam|other"
//	                       (or any spec §10.5 category),     required
//	  "note":              "free text",                      optional
//	  "message_ids":       ["<uuid>", …] (≤20, newest first), optional
//	  "idempotency_key":   "≤150 chars"                      optional
//	}
//
// 201 {"data": {"created": true, "report": {...}}} for a new report;
// 200 {"data": {"created": false}} when the reporter already has an active
// report on that user or group, or the idempotency key was already used on
// this route or on the message route (one key space, so message-service's
// message-route fallback and this route never file the same report twice);
// 400 for a malformed body; 403 USER_CALLER_REFUSED when any gateway
// identity header is present. The report lands in the existing /v1/reports
// admin queue with entity_type "user" or "conversation" and evidence
// {conversation_id, conversation_type, message_ids, source, captured_at}.
const ConversationReportPath = "/v1/internal/reports/conversations"

type conversationReportRequest struct {
	ReporterID       string   `json:"reporter_id"`
	EntityType       string   `json:"entity_type"`
	EntityID         string   `json:"entity_id"`
	ConversationID   string   `json:"conversation_id"`
	ConversationType string   `json:"conversation_type"`
	Reason           string   `json:"reason"`
	Note             string   `json:"note"`
	Details          string   `json:"details"`
	MessageIDs       []string `json:"message_ids"`
	IdempotencyKey   string   `json:"idempotency_key"`
}

// FileConversationReportInternal — POST /v1/internal/reports/conversations.
func (h *Handler) FileConversationReportInternal(c *gin.Context) {
	ctx := c.Request.Context()
	for _, name := range gatewayIdentityHeaders {
		if strings.TrimSpace(c.GetHeader(name)) != "" {
			api.ErrorWithContext(ctx, c.Writer, http.StatusForbidden, "USER_CALLER_REFUSED",
				"service-only endpoint; user requests are not accepted", nil)
			return
		}
	}
	var req conversationReportRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		api.ErrorWithContext(ctx, c.Writer, http.StatusBadRequest, "BAD_REQUEST", err.Error(), nil)
		return
	}
	key := strings.TrimSpace(req.IdempotencyKey)
	if len(key) > maxMessageReportIdempotencyKey {
		api.ErrorWithContext(ctx, c.Writer, http.StatusBadRequest, "BAD_REQUEST", "idempotency_key is too long", nil)
		return
	}
	details := req.Note
	if details == "" {
		details = req.Details
	}
	cr := service.ConversationReport{
		ReporterID: req.ReporterID, EntityType: req.EntityType, EntityID: req.EntityID,
		ConversationID: req.ConversationID, ConversationType: req.ConversationType,
		Reason: req.Reason, Note: details, MessageIDs: req.MessageIDs,
	}
	if key != "" {
		// The message route's namespace on purpose: see ConversationReportPath.
		cr.SourceEventID = "message-service:" + key
	}
	in, err := service.ConversationReportInput(cr)
	if err != nil {
		api.ErrorWithContext(ctx, c.Writer, http.StatusBadRequest, "BAD_REQUEST",
			strings.TrimPrefix(err.Error(), service.ErrInvalidReport.Error()+": "), nil)
		return
	}
	filer := h.messageReportFiler()
	if filer == nil {
		api.ErrorWithContext(ctx, c.Writer, http.StatusServiceUnavailable, "UNAVAILABLE", "service not ready", nil)
		return
	}
	report, err := filer.FileReportWith(ctx, in)
	switch {
	case err == nil:
		api.JSON(c.Writer, http.StatusCreated, gin.H{"created": true, "report": report}, nil)
	case errors.Is(err, postgres.ErrActiveReportExists), errors.Is(err, postgres.ErrDuplicateSourceEvent):
		api.JSON(c.Writer, http.StatusOK, gin.H{"created": false}, nil)
	case errors.Is(err, service.ErrInvalidReport):
		api.ErrorWithContext(ctx, c.Writer, http.StatusBadRequest, "BAD_REQUEST",
			strings.TrimPrefix(err.Error(), service.ErrInvalidReport.Error()+": "), nil)
	default:
		slog.Error("FileConversationReportInternal error", "error", err)
		api.ErrorWithContext(ctx, c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to file report", nil)
	}
}
