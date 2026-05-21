package http

import (
	"net/http"
	"time"

	"github.com/atpost/rider-service/internal/store"
	"github.com/atpost/shared/api"
	"github.com/gin-gonic/gin"
)

// parseReportWindow reads from/to ISO-8601 query params.
func parseReportWindow(c *gin.Context) store.ReportWindow {
	w := store.ReportWindow{}
	if v := c.Query("from"); v != "" {
		if t, err := time.Parse(time.RFC3339, v); err == nil {
			w.From = t
		}
	}
	if v := c.Query("to"); v != "" {
		if t, err := time.Parse(time.RFC3339, v); err == nil {
			w.To = t
		}
	}
	return w
}

// AdminMatchingHealthReport — GET /v1/rider/admin/reports/matching-health
func (h *Handler) AdminMatchingHealthReport(c *gin.Context) {
	rows, err := h.svc.ReportMatchingHealth(c.Request.Context(), parseReportWindow(c))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "REPORT_FAILED", err.Error(), nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, gin.H{"rows": rows}, nil)
}

// AdminPartnerQualityReport — GET /v1/rider/admin/reports/partner-quality
func (h *Handler) AdminPartnerQualityReport(c *gin.Context) {
	rows, err := h.svc.ReportPartnerQuality(c.Request.Context(), parseReportWindow(c))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "REPORT_FAILED", err.Error(), nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, gin.H{"rows": rows}, nil)
}

// AdminSupplyDemandReport — GET /v1/rider/admin/reports/supply-demand
func (h *Handler) AdminSupplyDemandReport(c *gin.Context) {
	rows, err := h.svc.ReportSupplyDemand(c.Request.Context(), parseReportWindow(c))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "REPORT_FAILED", err.Error(), nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, gin.H{"rows": rows}, nil)
}

// AdminSafetyIncidentReport — GET /v1/rider/admin/reports/safety
func (h *Handler) AdminSafetyIncidentReport(c *gin.Context) {
	rows, err := h.svc.ReportSafetyIncidents(c.Request.Context(), parseReportWindow(c))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "REPORT_FAILED", err.Error(), nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, gin.H{"rows": rows}, nil)
}

// AdminPartnerComplianceReport — GET /v1/rider/admin/reports/compliance?city=Bengaluru
func (h *Handler) AdminPartnerComplianceReport(c *gin.Context) {
	rows, err := h.svc.ReportPartnerCompliance(c.Request.Context(), c.Query("city"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "REPORT_FAILED", err.Error(), nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, gin.H{"rows": rows}, nil)
}
