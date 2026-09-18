package http

// Online ride payments (payments lane): the customer's intent, status,
// switch-to-cash and callback routes, the outstanding-fee routes, and the
// admin refund / list routes. Fare windows and the surge state (admin) are
// here too.

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/atpost/rider-service/internal/http/middleware"
	"github.com/atpost/rider-service/internal/service"
	"github.com/atpost/rider-service/internal/store"
	"github.com/atpost/shared/api"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// respondPaymentError answers a typed payments refusal (service.PaymentError)
// with its status and code. Reports whether it handled err.
func respondPaymentError(c *gin.Context, err error) bool {
	pe, ok := service.AsPaymentError(err)
	if !ok {
		return false
	}
	api.ErrorWithContext(c.Request.Context(), c.Writer, pe.Status, pe.Code, pe.Message, nil)
	return true
}

type paymentMethodRequest struct {
	Method string `json:"method"`
}

// PostRidePaymentIntent — POST /v1/rider/rides/:id/payment/intent {method}.
func (h *Handler) PostRidePaymentIntent(c *gin.Context) {
	uid, ok := getUserID(c)
	if !ok {
		return
	}
	rideID, ok := parseUUIDParam(c, "id")
	if !ok {
		return
	}
	var body paymentMethodRequest
	if err := c.ShouldBindJSON(&body); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_BODY", err.Error(), nil)
		return
	}
	out, err := h.svc.CreateRidePaymentIntent(c.Request.Context(), uid, rideID, body.Method)
	if err != nil {
		if respondPaymentError(c, err) {
			return
		}
		respondServiceError(c, err, http.StatusInternalServerError, "PAYMENT_INTENT_FAILED")
		return
	}
	api.JSONWithContext(c.Request.Context(), c.Writer, http.StatusOK, out)
}

// GetRidePayment — GET /v1/rider/rides/:id/payment (customer or assigned
// partner).
func (h *Handler) GetRidePayment(c *gin.Context) {
	uid, ok := getUserID(c)
	if !ok {
		return
	}
	rideID, ok := parseUUIDParam(c, "id")
	if !ok {
		return
	}
	out, err := h.svc.GetRidePaymentStatus(c.Request.Context(), uid, rideID)
	if err != nil {
		respondServiceError(c, err, http.StatusInternalServerError, "PAYMENT_STATUS_FAILED")
		return
	}
	api.JSONWithContext(c.Request.Context(), c.Writer, http.StatusOK, out)
}

// PostRidePaymentSwitchToCash — POST /v1/rider/rides/:id/payment/switch-to-cash.
func (h *Handler) PostRidePaymentSwitchToCash(c *gin.Context) {
	uid, ok := getUserID(c)
	if !ok {
		return
	}
	rideID, ok := parseUUIDParam(c, "id")
	if !ok {
		return
	}
	out, err := h.svc.SwitchRidePaymentToCash(c.Request.Context(), uid, rideID)
	if err != nil {
		if respondPaymentError(c, err) {
			return
		}
		respondServiceError(c, err, http.StatusInternalServerError, "PAYMENT_SWITCH_FAILED")
		return
	}
	api.JSONWithContext(c.Request.Context(), c.Writer, http.StatusOK, out)
}

type paymentCallbackRequest struct {
	ProviderOrderID   string `json:"provider_order_id"`
	ProviderPaymentID string `json:"provider_payment_id"`
	Signature         string `json:"signature"`
}

// PostRidePaymentCallback — POST /v1/rider/rides/:id/payment/callback.
// Advisory: the answer's status is the row's, unchanged.
func (h *Handler) PostRidePaymentCallback(c *gin.Context) {
	uid, ok := getUserID(c)
	if !ok {
		return
	}
	rideID, ok := parseUUIDParam(c, "id")
	if !ok {
		return
	}
	var body paymentCallbackRequest
	if err := c.ShouldBindJSON(&body); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_BODY", err.Error(), nil)
		return
	}
	if strings.TrimSpace(body.ProviderOrderID) == "" || strings.TrimSpace(body.ProviderPaymentID) == "" || strings.TrimSpace(body.Signature) == "" {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_BODY", "provider_order_id, provider_payment_id and signature are required", nil)
		return
	}
	out, err := h.svc.VerifyRidePaymentCallback(c.Request.Context(), uid, rideID, service.RidePaymentCallback{
		ProviderOrderID: body.ProviderOrderID, ProviderPaymentID: body.ProviderPaymentID, Signature: body.Signature,
	})
	if err != nil {
		if respondPaymentError(c, err) {
			return
		}
		respondServiceError(c, err, http.StatusInternalServerError, "PAYMENT_CALLBACK_FAILED")
		return
	}
	api.JSONWithContext(c.Request.Context(), c.Writer, http.StatusOK, out)
}

// GetMyOutstanding — GET /v1/rider/me/outstanding.
func (h *Handler) GetMyOutstanding(c *gin.Context) {
	uid, ok := getUserID(c)
	if !ok {
		return
	}
	out, err := h.svc.ListMyOutstanding(c.Request.Context(), uid)
	if err != nil {
		respondServiceError(c, err, http.StatusInternalServerError, "OUTSTANDING_LIST_FAILED")
		return
	}
	// A plain array under data: the Android app reads
	// [{id, ride_id, amount_paise, reason, status, created_at}].
	api.JSONWithContext(c.Request.Context(), c.Writer, http.StatusOK, out)
}

// PostOutstandingPaymentIntent — POST /v1/rider/me/outstanding/:id/payment/intent {method}.
func (h *Handler) PostOutstandingPaymentIntent(c *gin.Context) {
	uid, ok := getUserID(c)
	if !ok {
		return
	}
	id, ok := parseUUIDParam(c, "id")
	if !ok {
		return
	}
	var body paymentMethodRequest
	if err := c.ShouldBindJSON(&body); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_BODY", err.Error(), nil)
		return
	}
	out, err := h.svc.CreateOutstandingPaymentIntent(c.Request.Context(), uid, id, body.Method)
	if err != nil {
		if respondPaymentError(c, err) {
			return
		}
		respondServiceError(c, err, http.StatusInternalServerError, "PAYMENT_INTENT_FAILED")
		return
	}
	api.JSONWithContext(c.Request.Context(), c.Writer, http.StatusOK, out)
}

// --- Admin: refunds and ride payments -------------------------------------

type refundRequest struct {
	AmountPaise int64  `json:"amount_paise"`
	Reason      string `json:"reason"`
}

// AdminRefundRidePayment — POST /rides/:id/refund {amount_paise?, reason}.
func (h *Handler) AdminRefundRidePayment(c *gin.Context) {
	c.Set(middleware.AuditActionKey, "ride_payment.refund")
	c.Set(middleware.AuditTargetKindKey, "ride")
	adminID, ok := adminUserID(c)
	if !ok {
		return
	}
	rideID, ok := parseUUIDParam(c, "id")
	if !ok {
		return
	}
	c.Set(middleware.AuditTargetIDKey, rideID)
	var body refundRequest
	if err := c.ShouldBindJSON(&body); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_BODY", err.Error(), nil)
		return
	}
	out, err := h.svc.RefundRidePayment(c.Request.Context(), adminID, rideID, body.AmountPaise, body.Reason)
	if err != nil {
		if respondPaymentError(c, err) {
			return
		}
		respondServiceError(c, err, http.StatusInternalServerError, "REFUND_FAILED")
		return
	}
	api.JSONWithContext(c.Request.Context(), c.Writer, http.StatusAccepted, out)
}

// AdminListRefunds — GET /refunds?status=&ride_id=&limit=&offset=.
func (h *Handler) AdminListRefunds(c *gin.Context) {
	c.Set(middleware.AuditActionKey, "ride_payment.refunds.list")
	c.Set(middleware.AuditTargetKindKey, "ride_refund")
	var rideID *uuid.UUID
	if raw := c.Query("ride_id"); raw != "" {
		id, err := uuid.Parse(raw)
		if err != nil {
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "invalid ride_id", nil)
			return
		}
		rideID = &id
	}
	limit, offset := readPaging(c, 100, 500)
	out, err := h.svc.ListRideRefunds(c.Request.Context(), c.Query("status"), rideID, limit, offset)
	if err != nil {
		respondServiceError(c, err, http.StatusInternalServerError, "REFUND_LIST_FAILED")
		return
	}
	api.JSONWithContext(c.Request.Context(), c.Writer, http.StatusOK, gin.H{"items": out})
}

// AdminListRidePayments — GET /ride-payments?status=&method=&from=&to=&cursor=&limit=.
func (h *Handler) AdminListRidePayments(c *gin.Context) {
	c.Set(middleware.AuditActionKey, "ride_payment.list")
	c.Set(middleware.AuditTargetKindKey, "ride_payment")
	f := store.RidePaymentFilter{Status: c.Query("status"), Method: c.Query("method"), Cursor: c.Query("cursor")}
	if raw := c.Query("limit"); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil && n > 0 {
			f.Limit = n
		}
	}
	for name, dst := range map[string]**time.Time{"from": &f.From, "to": &f.To} {
		raw := c.Query(name)
		if raw == "" {
			continue
		}
		t, err := time.Parse(time.RFC3339, raw)
		if err != nil {
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_PARAMS", name+" must be RFC 3339", nil)
			return
		}
		*dst = &t
	}
	out, next, err := h.svc.ListRidePaymentsAdmin(c.Request.Context(), f)
	if err != nil {
		respondServiceError(c, err, http.StatusInternalServerError, "RIDE_PAYMENT_LIST_FAILED")
		return
	}
	api.JSONWithContext(c.Request.Context(), c.Writer, http.StatusOK, gin.H{"items": out, "next_cursor": next})
}

// --- Admin: fare windows and surge ----------------------------------------

// AdminListFareWindows — GET /fare-windows?city_id=.
func (h *Handler) AdminListFareWindows(c *gin.Context) {
	c.Set(middleware.AuditActionKey, "fare_window.list")
	c.Set(middleware.AuditTargetKindKey, "fare_window")
	var cityID *uuid.UUID
	if raw := c.Query("city_id"); raw != "" {
		id, err := uuid.Parse(raw)
		if err != nil {
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "invalid city_id", nil)
			return
		}
		cityID = &id
	}
	out, err := h.svc.ListFareWindowsAdmin(c.Request.Context(), cityID)
	if err != nil {
		respondServiceError(c, err, http.StatusInternalServerError, "FARE_WINDOW_LIST_FAILED")
		return
	}
	api.JSONWithContext(c.Request.Context(), c.Writer, http.StatusOK, gin.H{"items": out})
}

// AdminCreateFareWindow — POST /fare-windows.
func (h *Handler) AdminCreateFareWindow(c *gin.Context) {
	c.Set(middleware.AuditActionKey, "fare_window.create")
	c.Set(middleware.AuditTargetKindKey, "fare_window")
	adminID, ok := adminUserID(c)
	if !ok {
		return
	}
	var body service.FareWindowRequest
	if err := c.ShouldBindJSON(&body); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_BODY", err.Error(), nil)
		return
	}
	out, err := h.svc.CreateFareWindow(c.Request.Context(), adminID, body)
	if err != nil {
		respondServiceError(c, err, http.StatusInternalServerError, "FARE_WINDOW_CREATE_FAILED")
		return
	}
	c.Set(middleware.AuditTargetIDKey, out.ID)
	api.JSONWithContext(c.Request.Context(), c.Writer, http.StatusCreated, out)
}

// AdminUpdateFareWindow — PATCH /fare-windows/:id.
func (h *Handler) AdminUpdateFareWindow(c *gin.Context) {
	c.Set(middleware.AuditActionKey, "fare_window.update")
	c.Set(middleware.AuditTargetKindKey, "fare_window")
	adminID, ok := adminUserID(c)
	if !ok {
		return
	}
	id, ok := parseUUIDParam(c, "id")
	if !ok {
		return
	}
	c.Set(middleware.AuditTargetIDKey, id)
	var body service.FareWindowPatchRequest
	if err := c.ShouldBindJSON(&body); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_BODY", err.Error(), nil)
		return
	}
	out, err := h.svc.UpdateFareWindow(c.Request.Context(), adminID, id, body)
	if err != nil {
		respondServiceError(c, err, http.StatusInternalServerError, "FARE_WINDOW_UPDATE_FAILED")
		return
	}
	api.JSONWithContext(c.Request.Context(), c.Writer, http.StatusOK, out)
}

// AdminDeactivateFareWindow — POST /fare-windows/:id/deactivate.
func (h *Handler) AdminDeactivateFareWindow(c *gin.Context) {
	c.Set(middleware.AuditActionKey, "fare_window.deactivate")
	c.Set(middleware.AuditTargetKindKey, "fare_window")
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
	_ = c.ShouldBindJSON(&body)
	out, err := h.svc.DeactivateFareWindow(c.Request.Context(), adminID, id, body.Reason)
	if err != nil {
		respondServiceError(c, err, http.StatusInternalServerError, "FARE_WINDOW_DEACTIVATE_FAILED")
		return
	}
	api.JSONWithContext(c.Request.Context(), c.Writer, http.StatusOK, out)
}

// AdminSurgeState — GET /surge?city_id=.
func (h *Handler) AdminSurgeState(c *gin.Context) {
	c.Set(middleware.AuditActionKey, "surge.view")
	c.Set(middleware.AuditTargetKindKey, "city")
	raw := c.Query("city_id")
	if raw == "" {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_PARAMS", "city_id query parameter required", nil)
		return
	}
	cityID, err := uuid.Parse(raw)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "invalid city_id", nil)
		return
	}
	c.Set(middleware.AuditTargetIDKey, cityID)
	out, err := h.svc.SurgeState(c.Request.Context(), cityID)
	if err != nil {
		respondServiceError(c, err, http.StatusInternalServerError, "SURGE_STATE_FAILED")
		return
	}
	api.JSONWithContext(c.Request.Context(), c.Writer, http.StatusOK, gin.H{"city_id": cityID, "items": out})
}
