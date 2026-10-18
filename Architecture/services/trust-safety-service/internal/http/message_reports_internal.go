package http

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strings"

	"github.com/atpost/shared/api"
	"github.com/atpost/trust-safety-service/internal/service"
	"github.com/atpost/trust-safety-service/internal/store/postgres"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// MessageReportPath is the service-only route chat message-service calls to
// file a message report on behalf of a user (chat-parity CONTRACT §2.6):
//
//	POST /v1/internal/reports/messages
//	X-Internal-Service-Key: <key>     (no end-user identity headers)
//	{
//	  "reporter_id":     "<uuid>",                          required
//	  "entity_id":       "<conversation_id>:<message_id>",  required
//	  "reason":          "spam|harassment|hate|sexual|violence|scam|other"
//	                     (or any spec §10.5 category),      required
//	  "note":            "free text",                       optional
//	  "evidence":        {"text": "≤2000 chars", "media_id": "<uuid>",
//	                      "sender_id": "<uuid>", "message_type": "text"},  optional
//	  "idempotency_key": "≤150 chars"                        optional
//	}
//
// 201 {"data": {"created": true, "report": {...}}} for a new report;
// 200 {"data": {"created": false}} when the reporter already has an active
// report on that message or the idempotency key was already used (a retry
// is success); 400 for a malformed body; 403 USER_CALLER_REFUSED when any
// gateway identity header is present. The report lands in the existing
// /v1/reports admin queue with entity_type "message".
const MessageReportPath = "/v1/internal/reports/messages"

// maxMessageReportIdempotencyKey leaves room for the "message-service:"
// prefix under the 200-character source key ceiling.
const maxMessageReportIdempotencyKey = 150

// messageReportFiler is the slice of the service the route uses.
type messageReportFiler interface {
	FileReportWith(ctx context.Context, in service.FileReportInput) (*postgres.Report, error)
}

type messageReportRequest struct {
	ReporterID     string           `json:"reporter_id"`
	EntityID       string           `json:"entity_id"`
	Reason         string           `json:"reason"`
	Note           string           `json:"note"`
	Details        string           `json:"details"`
	Evidence       *EvidenceRequest `json:"evidence"`
	IdempotencyKey string           `json:"idempotency_key"`
}

func (h *Handler) messageReportFiler() messageReportFiler {
	if h.messageReports != nil {
		return h.messageReports
	}
	if h.svc != nil {
		return h.svc
	}
	return nil
}

// FileMessageReportInternal — POST /v1/internal/reports/messages.
func (h *Handler) FileMessageReportInternal(c *gin.Context) {
	ctx := c.Request.Context()
	for _, name := range gatewayIdentityHeaders {
		if strings.TrimSpace(c.GetHeader(name)) != "" {
			api.ErrorWithContext(ctx, c.Writer, http.StatusForbidden, "USER_CALLER_REFUSED",
				"service-only endpoint; user requests are not accepted", nil)
			return
		}
	}
	var req messageReportRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		api.ErrorWithContext(ctx, c.Writer, http.StatusBadRequest, "BAD_REQUEST", err.Error(), nil)
		return
	}
	reporterID, err := uuid.Parse(strings.TrimSpace(req.ReporterID))
	if err != nil || reporterID == uuid.Nil {
		api.ErrorWithContext(ctx, c.Writer, http.StatusBadRequest, "BAD_REQUEST", "reporter_id must be a UUID", nil)
		return
	}
	conv, _, ok := service.ParseMessageEntityRef(req.EntityID)
	if !ok {
		api.ErrorWithContext(ctx, c.Writer, http.StatusBadRequest, "BAD_REQUEST",
			`entity_id must be "<conversation_id>:<message_id>"`, nil)
		return
	}
	key := strings.TrimSpace(req.IdempotencyKey)
	if len(key) > maxMessageReportIdempotencyKey {
		api.ErrorWithContext(ctx, c.Writer, http.StatusBadRequest, "BAD_REQUEST", "idempotency_key is too long", nil)
		return
	}
	filer := h.messageReportFiler()
	if filer == nil {
		api.ErrorWithContext(ctx, c.Writer, http.StatusServiceUnavailable, "UNAVAILABLE", "service not ready", nil)
		return
	}
	details := req.Note
	if details == "" {
		details = req.Details
	}
	in := service.FileReportInput{
		ReporterID: reporterID,
		EntityType: service.ReportEntityMessage,
		EntityRef:  req.EntityID,
		Reason:     req.Reason,
		Details:    details,
		Evidence:   req.Evidence.toService(service.EvidenceSourceMessageService, conv.String()),
	}
	if key != "" {
		in.SourceEventID = "message-service:" + key
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
		slog.Error("FileMessageReportInternal error", "error", err)
		api.ErrorWithContext(ctx, c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to file report", nil)
	}
}
