package http

import (
	"net/http"
	"strconv"
	"time"

	"github.com/atpost/food-service/internal/store/postgres"
	"github.com/atpost/shared/api"
	"github.com/gin-gonic/gin"
)

// parseWindow reads `from` + `to` ISO-8601 query params with sensible
// defaults (last 24h). Returns the canonical window the reports take.
func parseWindow(c *gin.Context) postgres.ReportWindow {
	w := postgres.ReportWindow{}
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

// AdminRestaurantSLAReport — GET /v1/food/admin/reports/restaurant-sla
func (h *Handler) AdminRestaurantSLAReport(c *gin.Context) {
	rows, err := h.svc.ReportRestaurantSLA(c.Request.Context(), parseWindow(c))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "REPORT_FAILED", err.Error(), nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, gin.H{"rows": rows}, nil)
}

// AdminDeliverySLAReport — GET /v1/food/admin/reports/delivery-sla
func (h *Handler) AdminDeliverySLAReport(c *gin.Context) {
	rows, err := h.svc.ReportDeliverySLA(c.Request.Context(), parseWindow(c))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "REPORT_FAILED", err.Error(), nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, gin.H{"rows": rows}, nil)
}

// AdminPaymentReconReport — GET /v1/food/admin/reports/payment-recon
func (h *Handler) AdminPaymentReconReport(c *gin.Context) {
	rows, err := h.svc.ReportPaymentRecon(c.Request.Context(), parseWindow(c))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "REPORT_FAILED", err.Error(), nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, gin.H{"rows": rows}, nil)
}

// AdminRefundsReport — GET /v1/food/admin/reports/refunds
func (h *Handler) AdminRefundsReport(c *gin.Context) {
	rows, err := h.svc.ReportRefundsCancellations(c.Request.Context(), parseWindow(c))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "REPORT_FAILED", err.Error(), nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, gin.H{"rows": rows}, nil)
}

// AdminCouponAbuseReport — GET /v1/food/admin/reports/coupon-abuse?threshold=N
func (h *Handler) AdminCouponAbuseReport(c *gin.Context) {
	threshold := 5
	if t, err := strconv.Atoi(c.Query("threshold")); err == nil && t > 0 {
		threshold = t
	}
	rows, err := h.svc.ReportCouponAbuse(c.Request.Context(), parseWindow(c), threshold)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "REPORT_FAILED", err.Error(), nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, gin.H{"rows": rows}, nil)
}

// AdminComplianceReport — GET /v1/food/admin/reports/compliance
func (h *Handler) AdminComplianceReport(c *gin.Context) {
	rows, err := h.svc.ReportCompliance(c.Request.Context())
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "REPORT_FAILED", err.Error(), nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, gin.H{"rows": rows}, nil)
}
