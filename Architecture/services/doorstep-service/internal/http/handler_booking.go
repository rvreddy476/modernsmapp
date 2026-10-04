package http

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/atpost/doorstep-service/internal/apperr"
	"github.com/atpost/doorstep-service/internal/model"
	"github.com/atpost/doorstep-service/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// Bookings and payments (A3): customer routes behind the gateway identity,
// and the admin booking pages behind admin-service tokens.

// IdempotencyKeyHeader names the idempotency header (POST /bookings, admin
// refunds forwarded by admin-service).
const IdempotencyKeyHeader = "Idempotency-Key"

func (h *Handler) registerBookingRoutes(user *gin.RouterGroup) {
	user.GET("/addresses", h.listAddresses)
	user.POST("/addresses", h.createAddress)
	user.PATCH("/addresses/:id", h.updateAddress)
	user.DELETE("/addresses/:id", h.deleteAddress)

	user.GET("/slots", h.getSlots)

	user.POST("/bookings", h.createBooking)
	user.GET("/bookings", h.listBookings)
	user.GET("/bookings/:id", h.getBooking)
	user.GET("/bookings/:id/cancel-preview", h.cancelPreview)
	user.POST("/bookings/:id/cancel", h.cancelBooking)
	user.POST("/bookings/:id/reschedule", h.rescheduleBooking)
	user.POST("/bookings/:id/payment/intent", h.bookingPaymentIntent)
	user.GET("/bookings/:id/payment", h.bookingPayment)
	// Development only (404 elsewhere): settle through payments' stub gateway.
	user.POST("/bookings/:id/payment/stub-confirm", h.bookingStubConfirm)
}

// bookingAdminRoutes: bookings read, cancel (step-up in admin-service),
// refund (step-up + two-person, Idempotency-Key replayed) and the counts.
func (h *Handler) bookingAdminRoutes() []adminRoute {
	return []adminRoute{
		{http.MethodGet, "/bookings", PermBookingsRead, h.adminListBookings},
		{http.MethodGet, "/bookings/:id", PermBookingsRead, h.adminGetBooking},
		{http.MethodPost, "/bookings/:id/cancel", PermBookingsCancel, h.adminCancelBooking},
		{http.MethodPost, "/bookings/:id/refund", PermRefundsIssue, h.adminRefundBooking},
		{http.MethodGet, "/stats", PermStatsRead, h.adminStats},
	}
}

// ---- addresses ----

func (h *Handler) listAddresses(c *gin.Context) {
	uid, ok := userID(c)
	if !ok {
		return
	}
	items, err := h.svc.ListAddresses(c.Request.Context(), uid)
	respond(c, http.StatusOK, model.List[model.Address]{Items: items}, err)
}

func (h *Handler) createAddress(c *gin.Context) {
	uid, ok := userID(c)
	if !ok {
		return
	}
	var in model.AddressInput
	if !bindJSON(c, &in) {
		return
	}
	v, err := h.svc.CreateAddress(c.Request.Context(), uid, in)
	respond(c, http.StatusCreated, v, err)
}

func (h *Handler) updateAddress(c *gin.Context) {
	uid, ok := userID(c)
	if !ok {
		return
	}
	id, ok := uuidParam(c, "id")
	if !ok {
		return
	}
	var in model.AddressInput
	if !bindJSON(c, &in) {
		return
	}
	v, err := h.svc.UpdateAddress(c.Request.Context(), uid, id, in)
	respond(c, http.StatusOK, v, err)
}

func (h *Handler) deleteAddress(c *gin.Context) {
	uid, ok := userID(c)
	if !ok {
		return
	}
	id, ok := uuidParam(c, "id")
	if !ok {
		return
	}
	if err := h.svc.DeleteAddress(c.Request.Context(), uid, id); err != nil {
		writeErr(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

// ---- slots ----

func (h *Handler) getSlots(c *gin.Context) {
	uid, ok := userID(c)
	if !ok {
		return
	}
	var q service.SlotQuery
	if q.QuoteID, ok = optionalUUIDQuery(c, "quote_id"); !ok {
		return
	}
	if q.BookingID, ok = optionalUUIDQuery(c, "booking_id"); !ok {
		return
	}
	if q.AddressID, ok = optionalUUIDQuery(c, "address_id"); !ok {
		return
	}
	if raw := strings.TrimSpace(c.Query("require_female_pro")); raw != "" {
		v, err := strconv.ParseBool(raw)
		if err != nil {
			writeErr(c, apperr.Invalid("require_female_pro", "require_female_pro must be true or false"))
			return
		}
		q.RequireFemale = v
	}
	v, err := h.svc.Slots(c.Request.Context(), uid, q)
	respond(c, http.StatusOK, v, err)
}

// ---- bookings ----

func (h *Handler) createBooking(c *gin.Context) {
	uid, ok := userID(c)
	if !ok {
		return
	}
	var in model.BookingCreateRequest
	if !bindJSON(c, &in) {
		return
	}
	v, err := h.svc.CreateBooking(c.Request.Context(), uid, c.GetHeader(IdempotencyKeyHeader), in)
	respond(c, http.StatusCreated, v, err)
}

func (h *Handler) listBookings(c *gin.Context) {
	uid, ok := userID(c)
	if !ok {
		return
	}
	limit, aerr := service.ParseLimit(c.Query("limit"))
	if aerr != nil {
		writeErr(c, aerr)
		return
	}
	v, err := h.svc.Bookings(c.Request.Context(), uid, strings.TrimSpace(c.Query("status")), c.Query("cursor"), limit)
	respond(c, http.StatusOK, v, err)
}

// bookingCall runs fn for the caller's booking :id.
func (h *Handler) bookingCall(c *gin.Context, status int, fn func(c *gin.Context, uid, id uuid.UUID) (any, error)) {
	uid, ok := userID(c)
	if !ok {
		return
	}
	id, ok := uuidParam(c, "id")
	if !ok {
		return
	}
	v, err := fn(c, uid, id)
	respond(c, status, v, err)
}

func (h *Handler) getBooking(c *gin.Context) {
	h.bookingCall(c, http.StatusOK, func(c *gin.Context, uid, id uuid.UUID) (any, error) {
		return h.svc.Booking(c.Request.Context(), uid, id)
	})
}

func (h *Handler) cancelPreview(c *gin.Context) {
	h.bookingCall(c, http.StatusOK, func(c *gin.Context, uid, id uuid.UUID) (any, error) {
		return h.svc.CancelPreview(c.Request.Context(), uid, id)
	})
}

func (h *Handler) cancelBooking(c *gin.Context) {
	var in model.CancelRequest
	h.bookingBody(c, &in, func(c *gin.Context, uid, id uuid.UUID) (any, error) {
		return h.svc.CancelBooking(c.Request.Context(), uid, id, in)
	})
}

func (h *Handler) rescheduleBooking(c *gin.Context) {
	var in model.RescheduleRequest
	h.bookingBody(c, &in, func(c *gin.Context, uid, id uuid.UUID) (any, error) {
		return h.svc.Reschedule(c.Request.Context(), uid, id, in)
	})
}

// bookingBody is bookingCall with a strictly decoded body.
func (h *Handler) bookingBody(c *gin.Context, dst any, fn func(c *gin.Context, uid, id uuid.UUID) (any, error)) {
	uid, ok := userID(c)
	if !ok {
		return
	}
	id, ok := uuidParam(c, "id")
	if !ok {
		return
	}
	if !bindJSON(c, dst) {
		return
	}
	v, err := fn(c, uid, id)
	respond(c, http.StatusOK, v, err)
}

func (h *Handler) bookingPaymentIntent(c *gin.Context) {
	h.bookingCall(c, http.StatusOK, func(c *gin.Context, uid, id uuid.UUID) (any, error) {
		return h.svc.PaymentIntent(c.Request.Context(), uid, id)
	})
}

func (h *Handler) bookingPayment(c *gin.Context) {
	h.bookingCall(c, http.StatusOK, func(c *gin.Context, uid, id uuid.UUID) (any, error) {
		return h.svc.BookingPayments(c.Request.Context(), uid, id)
	})
}

func (h *Handler) bookingStubConfirm(c *gin.Context) {
	h.bookingCall(c, http.StatusOK, func(c *gin.Context, uid, id uuid.UUID) (any, error) {
		return h.svc.StubConfirm(c.Request.Context(), uid, id)
	})
}

// ---- admin ----

func (h *Handler) adminListBookings(c *gin.Context) {
	v, err := h.svc.AdminBookings(c.Request.Context(), c.Query("status"), c.Query("city"), c.Query("date"), c.Query("cursor"))
	respond(c, http.StatusOK, v, err)
}

func (h *Handler) adminGetBooking(c *gin.Context) {
	id, ok := uuidParam(c, "id")
	if !ok {
		return
	}
	v, err := h.svc.AdminBooking(c.Request.Context(), id)
	respond(c, http.StatusOK, v, err)
}

func (h *Handler) adminCancelBooking(c *gin.Context) {
	id, ok := uuidParam(c, "id")
	if !ok {
		return
	}
	var in model.AdminCancelInput
	if !bindJSON(c, &in) {
		return
	}
	d, err := h.svc.AdminCancel(c.Request.Context(), adminActor(c), id, in)
	if err != nil {
		writeErr(c, err)
		return
	}
	writeJSON(c, http.StatusOK, d.Booking)
}

func (h *Handler) adminRefundBooking(c *gin.Context) {
	id, ok := uuidParam(c, "id")
	if !ok {
		return
	}
	var in model.AdminRefundInput
	if !bindJSON(c, &in) {
		return
	}
	r, _, err := h.svc.AdminRefund(c.Request.Context(), adminActor(c), id, c.GetHeader(IdempotencyKeyHeader), in)
	respond(c, http.StatusCreated, r, err)
}

func (h *Handler) adminStats(c *gin.Context) {
	v, err := h.svc.AdminStats(c.Request.Context())
	respond(c, http.StatusOK, v, err)
}
