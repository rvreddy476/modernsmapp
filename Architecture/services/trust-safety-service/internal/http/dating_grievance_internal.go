package http

import (
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/atpost/shared/api"
	"github.com/atpost/trust-safety-service/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// DatingReportGrievancePath is the service-only route dating-service calls
// to open a grievance for each dating report (Dating plan lane D8).
const DatingReportGrievancePath = "/v1/internal/grievances/dating-reports"

// gatewayIdentityHeaders are set by the api-gateway only on a request with a
// verified end-user token. The gateway also injects the internal service key
// on every proxied request, so the key alone does not prove a service
// caller: a request carrying any of these is a proxied user and is refused.
var gatewayIdentityHeaders = []string{"X-User-Id", "X-Verified-User-Id", "X-Scopes", "X-Admin-Role"}

type datingReportGrievanceRequest struct {
	ReportID   string    `json:"report_id"`
	ReporterID string    `json:"reporter_id"`
	TargetID   string    `json:"target_id"`
	Reason     string    `json:"reason"`
	Details    string    `json:"details"`
	ReportedAt time.Time `json:"reported_at"`
}

// LinkDatingReportGrievance — POST /v1/internal/grievances/dating-reports.
//
// Service callers only: the whole router already requires the internal
// service key, and this route additionally refuses any request carrying an
// end-user identity header (403 USER_CALLER_REFUSED). Idempotent on
// report_id: 201 with created=true for a new grievance, 200 with the
// existing grievance otherwise.
func (h *Handler) LinkDatingReportGrievance(c *gin.Context) {
	for _, name := range gatewayIdentityHeaders {
		if strings.TrimSpace(c.GetHeader(name)) != "" {
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusForbidden, "USER_CALLER_REFUSED",
				"service-only endpoint; user requests are not accepted", nil)
			return
		}
	}
	var req datingReportGrievanceRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "BAD_REQUEST", err.Error(), nil)
		return
	}
	parse := func(raw string) (uuid.UUID, bool) {
		id, err := uuid.Parse(strings.TrimSpace(raw))
		return id, err == nil && id != uuid.Nil
	}
	reportID, ok1 := parse(req.ReportID)
	reporterID, ok2 := parse(req.ReporterID)
	targetID, ok3 := parse(req.TargetID)
	if !ok1 || !ok2 || !ok3 {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "BAD_REQUEST",
			"report_id, reporter_id and target_id must be UUIDs", nil)
		return
	}
	if h.svc == nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusServiceUnavailable, "UNAVAILABLE", "service not ready", nil)
		return
	}
	g, created, err := h.svc.LinkDatingReportGrievance(c.Request.Context(), service.DatingReportGrievanceInput{
		ReportID:   reportID,
		ReporterID: reporterID,
		TargetID:   targetID,
		Reason:     req.Reason,
		Details:    req.Details,
		ReportedAt: req.ReportedAt,
	}, requestIDOf(c))
	if err != nil {
		if errors.Is(err, service.ErrInvalidDatingReport) {
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "BAD_REQUEST", err.Error(), nil)
			return
		}
		slog.Error("link dating report grievance failed", "report_id", reportID, "error", err)
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to link grievance", nil)
		return
	}
	status := http.StatusOK
	if created {
		status = http.StatusCreated
	}
	api.JSON(c.Writer, status, gin.H{
		"grievance_id": g.ID,
		"status":       g.Status,
		"due_at":       g.DueAt,
		"created":      created,
	}, nil)
}
