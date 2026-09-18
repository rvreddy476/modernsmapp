package http

import (
	"net/http"
	"strconv"

	"github.com/atpost/rider-service/internal/http/middleware"
	"github.com/atpost/rider-service/internal/service"
	"github.com/atpost/shared/api"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// respondCouponError answers a typed coupon validation failure as 422 with
// the coupon code (COUPON_INVALID, COUPON_EXPIRED, COUPON_MIN_FARE,
// COUPON_FIRST_RIDE_ONLY, COUPON_USER_LIMIT_REACHED, COUPON_EXHAUSTED,
// COUPON_WRONG_CITY, COUPON_WRONG_VEHICLE, COUPONS_DISABLED). Reports
// whether it handled err.
func respondCouponError(c *gin.Context, err error) bool {
	ce, ok := service.AsCouponError(err)
	if !ok {
		return false
	}
	api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusUnprocessableEntity, ce.Code, ce.Message, nil)
	return true
}

// GetCouponValidate — GET /v1/rider/coupons/validate?code=&city_id=. Public;
// with a gateway identity the first-ride and per-user rules apply too.
func (h *Handler) GetCouponValidate(c *gin.Context) {
	code := c.Query("code")
	if code == "" {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_PARAMS", "code query parameter required", nil)
		return
	}
	var cityID *uuid.UUID
	if raw := c.Query("city_id"); raw != "" {
		id, err := uuid.Parse(raw)
		if err != nil {
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "invalid city_id", nil)
			return
		}
		cityID = &id
	}
	out, err := h.svc.CheckCoupon(c.Request.Context(), code, optionalUserID(c), cityID)
	if err != nil {
		if respondCouponError(c, err) {
			return
		}
		respondServiceError(c, err, http.StatusInternalServerError, "COUPON_VALIDATE_FAILED")
		return
	}
	api.JSONWithContext(c.Request.Context(), c.Writer, http.StatusOK, out)
}

// --- Admin: coupons ---------------------------------------------------------

// AdminListCoupons — GET /coupons?active=true&limit=&offset=.
func (h *Handler) AdminListCoupons(c *gin.Context) {
	c.Set(middleware.AuditActionKey, "coupon.list")
	c.Set(middleware.AuditTargetKindKey, "coupon")
	activeOnly, _ := strconv.ParseBool(c.Query("active"))
	limit, offset := readPaging(c, 100, 500)
	out, err := h.svc.ListCoupons(c.Request.Context(), activeOnly, limit, offset)
	if err != nil {
		respondServiceError(c, err, http.StatusInternalServerError, "COUPON_LIST_FAILED")
		return
	}
	api.JSONWithContext(c.Request.Context(), c.Writer, http.StatusOK, gin.H{"items": out})
}

// AdminCreateCoupon — POST /coupons.
func (h *Handler) AdminCreateCoupon(c *gin.Context) {
	c.Set(middleware.AuditActionKey, "coupon.create")
	c.Set(middleware.AuditTargetKindKey, "coupon")
	adminID, ok := adminUserID(c)
	if !ok {
		return
	}
	var body service.CreateCouponRequest
	if err := c.ShouldBindJSON(&body); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_BODY", err.Error(), nil)
		return
	}
	out, err := h.svc.CreateCoupon(c.Request.Context(), adminID, body)
	if err != nil {
		respondServiceError(c, err, http.StatusInternalServerError, "COUPON_CREATE_FAILED")
		return
	}
	c.Set(middleware.AuditTargetIDKey, out.ID)
	api.JSONWithContext(c.Request.Context(), c.Writer, http.StatusCreated, out)
}

// AdminUpdateCoupon — PATCH /coupons/:id.
func (h *Handler) AdminUpdateCoupon(c *gin.Context) {
	c.Set(middleware.AuditActionKey, "coupon.update")
	c.Set(middleware.AuditTargetKindKey, "coupon")
	adminID, ok := adminUserID(c)
	if !ok {
		return
	}
	couponID, ok := parseUUIDParam(c, "id")
	if !ok {
		return
	}
	c.Set(middleware.AuditTargetIDKey, couponID)
	var body service.UpdateCouponRequest
	if err := c.ShouldBindJSON(&body); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_BODY", err.Error(), nil)
		return
	}
	out, err := h.svc.UpdateCoupon(c.Request.Context(), adminID, couponID, body)
	if err != nil {
		respondServiceError(c, err, http.StatusInternalServerError, "COUPON_UPDATE_FAILED")
		return
	}
	api.JSONWithContext(c.Request.Context(), c.Writer, http.StatusOK, out)
}

// AdminDeactivateCoupon — POST /coupons/:id/deactivate.
func (h *Handler) AdminDeactivateCoupon(c *gin.Context) {
	c.Set(middleware.AuditActionKey, "coupon.deactivate")
	c.Set(middleware.AuditTargetKindKey, "coupon")
	adminID, ok := adminUserID(c)
	if !ok {
		return
	}
	couponID, ok := parseUUIDParam(c, "id")
	if !ok {
		return
	}
	c.Set(middleware.AuditTargetIDKey, couponID)
	var body reasonRequest
	_ = c.ShouldBindJSON(&body)
	out, err := h.svc.DeactivateCoupon(c.Request.Context(), adminID, couponID, body.Reason)
	if err != nil {
		respondServiceError(c, err, http.StatusInternalServerError, "COUPON_DEACTIVATE_FAILED")
		return
	}
	api.JSONWithContext(c.Request.Context(), c.Writer, http.StatusOK, out)
}

// AdminListCouponRedemptions — GET /coupons/redemptions?coupon_id=&limit=&offset=.
func (h *Handler) AdminListCouponRedemptions(c *gin.Context) {
	c.Set(middleware.AuditActionKey, "coupon.redemptions.list")
	c.Set(middleware.AuditTargetKindKey, "coupon_redemption")
	var couponID *uuid.UUID
	if raw := c.Query("coupon_id"); raw != "" {
		id, err := uuid.Parse(raw)
		if err != nil {
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "invalid coupon_id", nil)
			return
		}
		couponID = &id
	}
	limit, offset := readPaging(c, 100, 500)
	out, err := h.svc.ListCouponRedemptions(c.Request.Context(), couponID, limit, offset)
	if err != nil {
		respondServiceError(c, err, http.StatusInternalServerError, "COUPON_REDEMPTIONS_FAILED")
		return
	}
	api.JSONWithContext(c.Request.Context(), c.Writer, http.StatusOK, gin.H{"items": out})
}

// --- Admin: customer outstanding fees ----------------------------------------

// AdminListOutstanding — GET /outstanding?customer_id=&status=&limit=&offset=.
func (h *Handler) AdminListOutstanding(c *gin.Context) {
	c.Set(middleware.AuditActionKey, "outstanding.list")
	c.Set(middleware.AuditTargetKindKey, "customer_outstanding")
	var customerID *uuid.UUID
	if raw := c.Query("customer_id"); raw != "" {
		id, err := uuid.Parse(raw)
		if err != nil {
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "invalid customer_id", nil)
			return
		}
		customerID = &id
	}
	limit, offset := readPaging(c, 100, 500)
	out, err := h.svc.ListOutstanding(c.Request.Context(), customerID, c.Query("status"), limit, offset)
	if err != nil {
		respondServiceError(c, err, http.StatusInternalServerError, "OUTSTANDING_LIST_FAILED")
		return
	}
	api.JSONWithContext(c.Request.Context(), c.Writer, http.StatusOK, gin.H{"items": out})
}

// AdminWaiveOutstanding — POST /outstanding/:id/waive {reason}.
func (h *Handler) AdminWaiveOutstanding(c *gin.Context) {
	c.Set(middleware.AuditActionKey, "outstanding.waive")
	c.Set(middleware.AuditTargetKindKey, "customer_outstanding")
	adminID, ok := adminUserID(c)
	if !ok {
		return
	}
	id, ok := parseUUIDParam(c, "id")
	if !ok {
		return
	}
	c.Set(middleware.AuditTargetIDKey, id)
	var body reasonRequest
	if err := c.ShouldBindJSON(&body); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_BODY", err.Error(), nil)
		return
	}
	out, err := h.svc.WaiveOutstanding(c.Request.Context(), adminID, id, body.Reason)
	if err != nil {
		respondServiceError(c, err, http.StatusInternalServerError, "OUTSTANDING_WAIVE_FAILED")
		return
	}
	api.JSONWithContext(c.Request.Context(), c.Writer, http.StatusOK, out)
}
