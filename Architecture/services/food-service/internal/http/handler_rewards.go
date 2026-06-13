package http

import (
	"net/http"

	"github.com/atpost/shared/api"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// ── Loyalty ───────────────────────────────────────────────────────────────

// GetMyLoyalty — GET /v1/food/me/loyalty
func (h *Handler) GetMyLoyalty(c *gin.Context) {
	uid, ok := h.requireUser(c)
	if !ok {
		return
	}
	b, err := h.svc.GetLoyaltyBalance(c.Request.Context(), uid)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "LOYALTY_READ_FAILED", err.Error(), nil)
		return
	}
	rows, _ := h.svc.ListLoyaltyLedger(c.Request.Context(), uid, 50)
	api.JSON(c.Writer, http.StatusOK, gin.H{"balance": b, "ledger": rows}, nil)
}

// RedeemLoyaltyRequest is the customer redeem body.
type RedeemLoyaltyRequest struct {
	OrderID *uuid.UUID `json:"order_id,omitempty"`
	Points  int        `json:"points"`
}

// RedeemLoyalty — POST /v1/food/me/loyalty/redeem
func (h *Handler) RedeemLoyalty(c *gin.Context) {
	uid, ok := h.requireUser(c)
	if !ok {
		return
	}
	var req RedeemLoyaltyRequest
	if err := c.BindJSON(&req); err != nil || req.Points <= 0 {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_BODY", "points must be > 0", nil)
		return
	}
	b, err := h.svc.RedeemPoints(c.Request.Context(), uid, req.OrderID, req.Points)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "REDEEM_FAILED", err.Error(), nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, b, nil)
}

// ── Referrals ─────────────────────────────────────────────────────────────

// GetMyReferralCode — GET /v1/food/me/referral
func (h *Handler) GetMyReferralCode(c *gin.Context) {
	uid, ok := h.requireUser(c)
	if !ok {
		return
	}
	code, err := h.svc.EnsureReferralCode(c.Request.Context(), uid)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "REFERRAL_CODE_FAILED", err.Error(), nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, gin.H{"code": code}, nil)
}

// ApplyReferralRequest carries the code the referee was given.
type ApplyReferralRequest struct {
	Code string `json:"code"`
}

// ApplyReferralCode — POST /v1/food/me/referral/apply
//
// Binds the calling user to the referrer of `code`. One-shot — repeated
// calls fail with "referee already referred". The reward only lands
// after the referee's first DELIVERED order (a job-side worker).
func (h *Handler) ApplyReferralCode(c *gin.Context) {
	uid, ok := h.requireUser(c)
	if !ok {
		return
	}
	var req ApplyReferralRequest
	if err := c.BindJSON(&req); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_BODY", err.Error(), nil)
		return
	}
	r, err := h.svc.RecordReferral(c.Request.Context(), uid, req.Code)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "REFERRAL_APPLY_FAILED", err.Error(), nil)
		return
	}
	api.JSON(c.Writer, http.StatusCreated, r, nil)
}

// ── Prep-time prediction (admin/partner read) ─────────────────────────────

// GetRestaurantPrepTime — GET /v1/food/restaurants/:restaurantId/prep-time
//
// Public read — the customer storefront uses it to render a more
// accurate ETA than the static avg_preparation_minutes.
func (h *Handler) GetRestaurantPrepTime(c *gin.Context) {
	rid, err := uuid.Parse(c.Param("restaurantId"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_RESTAURANT_ID", err.Error(), nil)
		return
	}
	p, err := h.svc.PredictPrepTime(c.Request.Context(), rid)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "PREP_TIME_FAILED", err.Error(), nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, p, nil)
}
