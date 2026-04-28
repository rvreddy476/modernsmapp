package http

import (
	"net/http"
	"strconv"
	"time"

	"github.com/atpost/shared/api"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// ---------------------------------------------------------------------------
// Creator-facing endpoints
// ---------------------------------------------------------------------------

// GetCreatorFundStatus returns the caller's current eligibility row +
// a fresh threshold-vs-stats decision so the dashboard can surface
// "you need 200 more view-score to qualify". Always 200 OK; the body
// describes whether the creator is eligible / pending / suspended.
func (h *Handler) GetCreatorFundStatus(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}
	status, err := h.svc.GetCreatorFundStatus(c.Request.Context(), userID)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	cfg := h.svc.CreatorFundConfigSnapshot()
	api.JSON(c.Writer, http.StatusOK, gin.H{
		"row":             status.Row,
		"decision":        status.Decision,
		"platform_fee_bps": cfg.PlatformFeeBps,
	}, nil)
}

// ApplyCreatorFund forces a fresh evaluation. Useful after a creator
// hits their thresholds and wants to opt in immediately rather than
// waiting for tomorrow's nightly sweep. Cannot lift suspension — that's
// admin-only.
func (h *Handler) ApplyCreatorFund(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}
	row, err := h.svc.EvaluateEligibility(c.Request.Context(), userID)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, row, nil)
}

// GetCreatorFundEarnings returns the creator's settled earnings over
// the last `days` days (default 30, max 365). Body has totals + per-day
// per-content-type breakdown.
func (h *Handler) GetCreatorFundEarnings(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}
	days := 30
	if v := c.Query("days"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			days = n
		}
	}
	summary, err := h.svc.GetCreatorFundEarnings(c.Request.Context(), userID, days)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, summary, nil)
}

// ListCreatorFundRates is unauthenticated-but-public-ish: any creator
// can see the rate sheet they will be paid against. (No PII; rates are
// platform-wide.) Sits under /v1/monetization/creator-fund/rates so it
// shows up next to the other creator-facing endpoints.
func (h *Handler) ListCreatorFundRates(c *gin.Context) {
	rates, err := h.svc.ListActiveRpmRates(c.Request.Context())
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, rates, nil)
}

// ---------------------------------------------------------------------------
// Admin endpoints
// ---------------------------------------------------------------------------

// SuspendCreatorFund is the moderation hammer: immediately blocks the
// creator from receiving any further fund earnings. Past settled
// earnings stay in the wallet (admin can reverse via the dispute path
// if needed).
func (h *Handler) SuspendCreatorFund(c *gin.Context) {
	if _, ok := getAdminID(c); !ok {
		return
	}
	userID, err := uuid.Parse(c.Param("userId"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid user ID", nil)
		return
	}
	var req struct {
		Reason string `json:"reason"`
	}
	_ = c.ShouldBindJSON(&req)
	if err := h.svc.SuspendCreatorFund(c.Request.Context(), userID, req.Reason); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, gin.H{"status": "suspended"}, nil)
}

// UnsuspendCreatorFund clears the suspension flag, dropping the row to
// 'pending' so the next nightly evaluator (or a creator's POST /apply)
// can re-rate them.
func (h *Handler) UnsuspendCreatorFund(c *gin.Context) {
	if _, ok := getAdminID(c); !ok {
		return
	}
	userID, err := uuid.Parse(c.Param("userId"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid user ID", nil)
		return
	}
	if err := h.svc.ClearCreatorFundSuspension(c.Request.Context(), userID); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, gin.H{"status": "cleared"}, nil)
}

// SetCreatorFundRate writes a new active rate (long_video|flick) and
// closes off the previous active rate for the same content type +
// region. Audit: the admin's user_id is captured on the row.
func (h *Handler) SetCreatorFundRate(c *gin.Context) {
	adminID, ok := getAdminID(c)
	if !ok {
		return
	}
	var req struct {
		ContentType string `json:"content_type" binding:"required"`
		RegionCode  string `json:"region_code"`
		RpmPaise    int64  `json:"rpm_paise" binding:"required"`
		Notes       string `json:"notes"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_REQUEST", err.Error(), nil)
		return
	}
	if req.RpmPaise < 0 {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_RATE", "rpm_paise must be >= 0", nil)
		return
	}
	rate, err := h.svc.SetRpmRate(c.Request.Context(), req.ContentType, req.RegionCode, req.RpmPaise, req.Notes, &adminID)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_REQUEST", err.Error(), nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, rate, nil)
}

// ListCreatorFundRatesAdmin is the admin twin of ListCreatorFundRates;
// same body but gated behind the X-Admin-Id check so an admin tool can
// confirm exactly what's active before it pushes a new rate.
func (h *Handler) ListCreatorFundRatesAdmin(c *gin.Context) {
	if _, ok := getAdminID(c); !ok {
		return
	}
	h.ListCreatorFundRates(c)
}

// ForceSettleCreatorFund is an admin escape hatch for re-running a
// specific day's settlement (e.g. after a rate fix or after manually
// repairing the analytics rollup). day=YYYY-MM-DD; idempotent.
func (h *Handler) ForceSettleCreatorFund(c *gin.Context) {
	if _, ok := getAdminID(c); !ok {
		return
	}
	dayStr := c.Query("day")
	if dayStr == "" {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_REQUEST", "day=YYYY-MM-DD required", nil)
		return
	}
	day, err := time.Parse("2006-01-02", dayStr)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_REQUEST", "day must be YYYY-MM-DD", nil)
		return
	}
	rows, err := h.svc.SettleCreatorFundDayForAllEligible(c.Request.Context(), day, nil)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, gin.H{"day": dayStr, "rows_credited": rows}, nil)
}
