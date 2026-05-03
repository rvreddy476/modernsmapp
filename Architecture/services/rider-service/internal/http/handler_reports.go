// Admin reports HTTP handlers — Sprint 4. Routed under
// /v1/rider/admin/reports/* and gated by the existing AdminGuard +
// AuditAdmin middleware so every report read is audit-logged.
//
// Spec ref: mopedu/MOPEDU_SPEC.md §17 (admin reports).
package http

import (
	"net/http"
	"time"

	"github.com/atpost/rider-service/internal/http/middleware"
	"github.com/atpost/shared/api"
	"github.com/gin-gonic/gin"
)

// AdminRevenueReport — GET /v1/rider/admin/reports/revenue?by=plan|city&since=&until=
func (h *Handler) AdminRevenueReport(c *gin.Context) {
	c.Set(middleware.AuditActionKey, "report.revenue")
	c.Set(middleware.AuditTargetKindKey, "report")

	by := c.DefaultQuery("by", "plan")
	since, sinceErr := parseQueryDate(c, "since")
	if sinceErr != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_QUERY", "invalid since: "+sinceErr.Error(), nil)
		return
	}
	until, untilErr := parseQueryDate(c, "until")
	if untilErr != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_QUERY", "invalid until: "+untilErr.Error(), nil)
		return
	}
	switch by {
	case "plan":
		rows, err := h.svc.RevenueByPlan(c.Request.Context(), since, until)
		if err != nil {
			respondServiceError(c, err, http.StatusInternalServerError, "REPORT_FAILED")
			return
		}
		api.JSONWithContext(c.Request.Context(), c.Writer, http.StatusOK, gin.H{"by": "plan", "items": rows})
	case "city":
		rows, err := h.svc.RevenueByCity(c.Request.Context(), since, until)
		if err != nil {
			respondServiceError(c, err, http.StatusInternalServerError, "REPORT_FAILED")
			return
		}
		api.JSONWithContext(c.Request.Context(), c.Writer, http.StatusOK, gin.H{"by": "city", "items": rows})
	default:
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_QUERY", "by must be plan or city", nil)
	}
}

// AdminCohortRetention — GET /v1/rider/admin/reports/cohort-retention?cohort_month=YYYY-MM
func (h *Handler) AdminCohortRetention(c *gin.Context) {
	c.Set(middleware.AuditActionKey, "report.cohort_retention")
	c.Set(middleware.AuditTargetKindKey, "report")

	cohort := c.Query("cohort_month")
	out, err := h.svc.PartnerCohortRetention(c.Request.Context(), cohort)
	if err != nil {
		respondServiceError(c, err, http.StatusInternalServerError, "REPORT_FAILED")
		return
	}
	api.JSONWithContext(c.Request.Context(), c.Writer, http.StatusOK, out)
}

// AdminCustomerCohort — GET /v1/rider/admin/reports/customer-cohort?cohort_month=YYYY-MM
func (h *Handler) AdminCustomerCohort(c *gin.Context) {
	c.Set(middleware.AuditActionKey, "report.customer_cohort")
	c.Set(middleware.AuditTargetKindKey, "report")

	cohort := c.Query("cohort_month")
	out, err := h.svc.CustomerCohortBookingRate(c.Request.Context(), cohort)
	if err != nil {
		respondServiceError(c, err, http.StatusInternalServerError, "REPORT_FAILED")
		return
	}
	api.JSONWithContext(c.Request.Context(), c.Writer, http.StatusOK, out)
}

// AdminCronRuns — GET /v1/rider/admin/reports/cron-runs?job=&since=&limit=
func (h *Handler) AdminCronRuns(c *gin.Context) {
	c.Set(middleware.AuditActionKey, "report.cron_runs")
	c.Set(middleware.AuditTargetKindKey, "report")

	job := c.Query("job")
	limit, _ := readPaging(c, 100, 500)

	var since *time.Time
	if v, err := parseQueryDate(c, "since"); err == nil && !v.IsZero() {
		t := v
		since = &t
	}
	out, err := h.svc.ListCronRuns(c.Request.Context(), job, since, limit)
	if err != nil {
		respondServiceError(c, err, http.StatusInternalServerError, "REPORT_FAILED")
		return
	}
	api.JSONWithContext(c.Request.Context(), c.Writer, http.StatusOK, gin.H{"items": out})
}

// parseQueryDate parses ?key=YYYY-MM-DD into a UTC time. Empty value
// returns (zero, nil).
func parseQueryDate(c *gin.Context, key string) (time.Time, error) {
	raw := c.Query(key)
	if raw == "" {
		return time.Time{}, nil
	}
	t, err := time.Parse("2006-01-02", raw)
	if err != nil {
		return time.Time{}, err
	}
	return t.UTC(), nil
}
