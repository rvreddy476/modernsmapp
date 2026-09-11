package http

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/atpost/monetization-service/internal/service"
	pgstore "github.com/atpost/monetization-service/internal/store/postgres"
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
		"row":              status.Row,
		"decision":         status.Decision,
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
	if summary == nil {
		summary = &pgstore.EarningsSummary{Breakdown: []pgstore.EarningsDailyBreakdown{}}
	}
	summary.Estimate, summary.Withdrawable = h.estimateFlags()
	api.JSON(c.Writer, http.StatusOK, summary, nil)
}

// estimateFlags is the beta label on every earnings figure (plan Phase
// 3C): while payouts are off the number is an estimate and none of it is
// withdrawable. Both flags flip together when MONETIZATION_PAYOUTS_ENABLED
// is set, so a client can key off either.
func (h *Handler) estimateFlags() (estimate, withdrawable bool) {
	return !h.payoutsEnabled, h.payoutsEnabled
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

// ForceAccrueCreatorFundDay re-measures one day for every eligible
// creator. It is NOT a payment any more: this endpoint used to credit
// wallets, and now writes accrual rows that the period settlement pays.
// Kept because "the analytics rollup for the 12th was wrong, re-measure
// it" is a real operation, and because a re-measure is harmless when the
// money for that period has not moved yet.
//
// day=YYYY-MM-DD. Idempotent: a day already accrued is skipped, and a day
// already paid cannot be re-measured into a second payment because its
// row carries credited = true.
func (h *Handler) ForceAccrueCreatorFundDay(c *gin.Context) {
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
	batch, err := h.svc.AccrueCreatorFundDayForAllEligible(c.Request.Context(), day, nil)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, gin.H{
		"day":          dayStr,
		"rows_accrued": batch.Accrued,
		"creators":     batch.Creators,
		"failed":       batch.Failed,
		"skipped":      batch.Skipped,
		"note":         "accrual only — no money moved. Run POST /admin/creator-fund/settle-period to pay.",
	}, nil)
}

// ---------------------------------------------------------------------------
// Corrections (Phase 2A)
// ---------------------------------------------------------------------------

// GetCreatorFundEarningAdmin returns one accrual row with its full audit
// trail — rate, band, rule version, input revision, carry, reversal —
// so an operator can read a row before deciding to reverse it.
func (h *Handler) GetCreatorFundEarningAdmin(c *gin.Context) {
	if _, ok := getAdminID(c); !ok {
		return
	}
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid earning ID", nil)
		return
	}
	e, err := h.svc.GetCreatorFundEarning(c.Request.Context(), id)
	if err != nil {
		if errors.Is(err, service.ErrEarningNotFound) {
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusNotFound, "EARNING_NOT_FOUND", "No accrual row with that id", nil)
			return
		}
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, e, nil)
}

// ReverseCreatorFundEarning marks one accrual row reversed. If the row
// had been credited, the net is taken back through an adjustment keyed
// on the row (cause creator_fund_earning_reversal:<id>), in the same
// transaction — so a retry of this call cannot take it back twice. The
// response says whether money moved, the balance after, and whether the
// ledger was frozen because the balance went below zero.
//
// Body: {"reason": "..."} — required; it is written on the row and on
// the adjustment, and into the audit log with the admin's id.
func (h *Handler) ReverseCreatorFundEarning(c *gin.Context) {
	adminID, ok := getAdminID(c)
	if !ok {
		return
	}
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid earning ID", nil)
		return
	}
	var req struct {
		Reason string `json:"reason"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || strings.TrimSpace(req.Reason) == "" {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "REASON_REQUIRED", "a non-empty reason is required", nil)
		return
	}
	res, err := h.svc.ReverseFundEarning(c.Request.Context(), id, req.Reason)
	if err != nil {
		if errors.Is(err, service.ErrEarningNotFound) {
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusNotFound, "EARNING_NOT_FOUND", "No accrual row with that id", nil)
			return
		}
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	if !res.AlreadyReversed {
		newData, _ := json.Marshal(res)
		if aerr := h.svc.WriteAuditLog(c.Request.Context(), &pgstore.AuditLogEntry{
			TableName:   "creator_fund_earnings",
			Operation:   "reverse",
			NewData:     newData,
			PerformerID: adminID,
			IPAddress:   c.ClientIP(),
		}); aerr != nil {
			slog.WarnContext(c.Request.Context(), "creator-fund reversal: audit log write failed",
				"earning_id", id, "admin_id", adminID, "error", aerr)
		}
	}
	api.JSON(c.Writer, http.StatusOK, res, nil)
}

// ---------------------------------------------------------------------------
// Budget cap (Phase 2C)
// ---------------------------------------------------------------------------

// ListCreatorFundBudgets returns every period's cap and how much of it
// has accrued, newest period first.
func (h *Handler) ListCreatorFundBudgets(c *gin.Context) {
	if _, ok := getAdminID(c); !ok {
		return
	}
	budgets, err := h.svc.ListCreatorFundBudgets(c.Request.Context())
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	if budgets == nil {
		budgets = []pgstore.CreatorFundBudget{}
	}
	cfg := h.svc.CreatorFundConfigSnapshot()
	api.JSON(c.Writer, http.StatusOK, gin.H{
		"cadence": service.NormalizeCadence(cfg.SettlementCadence),
		"budgets": budgets,
	}, nil)
}

// SetCreatorFundBudget creates or changes a period's cap.
//
// Body: {"period_key": "2026-09", "region_code": "IN", "cap_paise": N,
// "notes": "..."}. The period key must be of the configured cadence
// (409 CADENCE_MISMATCH otherwise). Lowering cap_paise below what has
// already accrued is refused with 409 BUDGET_BELOW_ACCRUED. Raising it
// re-opens an exhausted period only if accrued is below the new cap.
func (h *Handler) SetCreatorFundBudget(c *gin.Context) {
	adminID, ok := getAdminID(c)
	if !ok {
		return
	}
	var req service.BudgetInput
	if err := c.ShouldBindJSON(&req); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_REQUEST", err.Error(), nil)
		return
	}
	if strings.TrimSpace(req.PeriodKey) == "" {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_REQUEST", "period_key is required", nil)
		return
	}
	if req.CapPaise < 0 {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_BUDGET", "cap_paise must be >= 0", nil)
		return
	}
	b, err := h.svc.UpsertCreatorFundBudget(c.Request.Context(), req, &adminID)
	if err != nil {
		switch {
		case errors.Is(err, service.ErrBudgetBelowAccrued):
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusConflict, "BUDGET_BELOW_ACCRUED", err.Error(), nil)
		case errors.Is(err, service.ErrCadenceMismatch):
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusConflict, "CADENCE_MISMATCH", err.Error(), nil)
		case strings.HasPrefix(err.Error(), "INVALID_PERIOD"), strings.HasPrefix(err.Error(), "INVALID_BUDGET"):
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_REQUEST", err.Error(), nil)
		default:
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		}
		return
	}
	api.JSON(c.Writer, http.StatusOK, b, nil)
}

// SettleCreatorFundPeriod is the payment run, fired by hand. Same call
// the scheduled worker makes, so an operator re-running a period sees
// exactly what the worker would have done.
//
// period=YYYY-MM (calendar month) | YYYY-MM-H1 | YYYY-MM-H2 (twice
// monthly). Omit it and the most recently closed period for the
// configured cadence is used.
//
// Re-running a settled period is a no-op you can read off the response:
// creators_newly_credited and credited_paise both come back zero, while
// the statement figures stay whatever they were.
func (h *Handler) SettleCreatorFundPeriod(c *gin.Context) {
	if _, ok := getAdminID(c); !ok {
		return
	}
	cfg := h.svc.CreatorFundConfigSnapshot()
	var period service.SettlementPeriod
	if key := c.Query("period"); key != "" {
		p, err := service.ParsePeriodKey(key)
		if err != nil {
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_PERIOD", err.Error(), nil)
			return
		}
		period = p
	} else {
		period = service.PreviousPeriod(time.Now().UTC(), cfg.SettlementCadence)
	}

	res, err := h.svc.SettleCreatorFundPeriodForAll(c.Request.Context(), period, nil)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, gin.H{
		"period":             period,
		"period_label":       period.Label(),
		"configured_cadence": service.NormalizeCadence(cfg.SettlementCadence),
		"result":             res,
		"platform_fee_bps":   cfg.PlatformFeeBps,
	}, nil)
}

// SettleCreatorFundPeriodForCreator settles a single creator's period.
// Useful when one creator's analytics was repaired and the rest of the
// month is already correct.
func (h *Handler) SettleCreatorFundPeriodForCreator(c *gin.Context) {
	if _, ok := getAdminID(c); !ok {
		return
	}
	creatorID, err := uuid.Parse(c.Param("userId"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid user ID", nil)
		return
	}
	cfg := h.svc.CreatorFundConfigSnapshot()
	period := service.PreviousPeriod(time.Now().UTC(), cfg.SettlementCadence)
	if key := c.Query("period"); key != "" {
		p, perr := service.ParsePeriodKey(key)
		if perr != nil {
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_PERIOD", perr.Error(), nil)
			return
		}
		period = p
	}
	st, err := h.svc.SettleCreatorFundPeriod(c.Request.Context(), creatorID, period)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, st, nil)
}

// ---------------------------------------------------------------------------
// Creator-facing period statements
// ---------------------------------------------------------------------------

// ListCreatorFundStatements answers "what did I earn, in which period,
// and from what" — the three streams side by side rather than a fund
// total and a start date. Newest period first.
func (h *Handler) ListCreatorFundStatements(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}
	limit := 12
	if v := c.Query("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			limit = n
		}
	}
	statements, err := h.svc.ListCreatorPeriodStatements(c.Request.Context(), userID, limit)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	cfg := h.svc.CreatorFundConfigSnapshot()
	current := service.PeriodContaining(time.Now().UTC(), cfg.SettlementCadence)
	estimate, withdrawable := h.estimateFlags()
	if statements == nil {
		statements = []service.PeriodStatement{}
	}
	for i := range statements {
		statements[i].Estimate, statements[i].Withdrawable = estimate, withdrawable
	}
	api.JSON(c.Writer, http.StatusOK, gin.H{
		"cadence":        service.NormalizeCadence(cfg.SettlementCadence),
		"current_period": gin.H{"key": current.Key, "label": current.Label(), "start": current.Start, "end": current.End},
		"statements":     statements,
		"estimate":       estimate,
		"withdrawable":   withdrawable,
	}, nil)
}

// GetCreatorFundStatement returns one period statement with the per-day,
// per-content-type fund breakdown behind its fund line.
func (h *Handler) GetCreatorFundStatement(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}
	st, err := h.svc.GetCreatorPeriodStatement(c.Request.Context(), userID, c.Param("periodKey"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_PERIOD", err.Error(), nil)
		return
	}
	if st == nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusNotFound, "STATEMENT_NOT_FOUND", "No settlement for that period", nil)
		return
	}
	st.Estimate, st.Withdrawable = h.estimateFlags()
	api.JSON(c.Writer, http.StatusOK, st, nil)
}

// ---------------------------------------------------------------------------
// Quality multiplier band
// ---------------------------------------------------------------------------

// ListCreatorFundQualityBands is creator-facing: alongside the RPM rate
// sheet, a creator can read the exact curve their pay is scaled by —
// the floor they can never fall below, the ceiling, the score at which
// the multiplier is neutral, and how many impressions a score needs
// before it is fully trusted.
func (h *Handler) ListCreatorFundQualityBands(c *gin.Context) {
	bands, err := h.svc.ListActiveQualityBands(c.Request.Context())
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, bands, nil)
}

// SetCreatorFundQualityBand writes a new active quality band and closes
// off the previous one, mirroring SetCreatorFundRate. The admin's
// user_id is captured on the row.
func (h *Handler) SetCreatorFundQualityBand(c *gin.Context) {
	adminID, ok := getAdminID(c)
	if !ok {
		return
	}
	var req struct {
		ContentType           string   `json:"content_type" binding:"required"`
		RegionCode            string   `json:"region_code"`
		FloorBps              int64    `json:"floor_bps" binding:"required"`
		CeilingBps            int64    `json:"ceiling_bps" binding:"required"`
		PivotCQS              *float64 `json:"pivot_cqs"`
		ConfidenceImpressions *int64   `json:"confidence_impressions"`
		Enabled               *bool    `json:"enabled"`
		Notes                 string   `json:"notes"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_REQUEST", err.Error(), nil)
		return
	}
	band := pgstore.QualityBandRow{
		ContentType:           req.ContentType,
		RegionCode:            req.RegionCode,
		FloorBps:              req.FloorBps,
		CeilingBps:            req.CeilingBps,
		PivotCQS:              0.35,
		ConfidenceImpressions: 1000,
		Enabled:               true,
		Notes:                 req.Notes,
	}
	if req.PivotCQS != nil {
		band.PivotCQS = *req.PivotCQS
	}
	if req.ConfidenceImpressions != nil {
		band.ConfidenceImpressions = *req.ConfidenceImpressions
	}
	if req.Enabled != nil {
		band.Enabled = *req.Enabled
	}
	saved, err := h.svc.SetQualityBand(c.Request.Context(), band, &adminID)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_BAND", err.Error(), nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, saved, nil)
}
