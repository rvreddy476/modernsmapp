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

// B1 (4 Oct 2026): professionals' own prices (/pro), the admin price review
// (doorstep:prices.review), the customer's professionals list, and the
// change of professional on a pro_unavailable booking.

// PermPricesReview reviews professionals' prices (B1).
const PermPricesReview = "doorstep:prices.review"

func (h *Handler) registerPricingRoutes(user *gin.RouterGroup) {
	user.GET("/services/:id/professionals", h.serviceProfessionals)
	user.GET("/bookings/:id/professionals", h.bookingProfessionals)
	user.POST("/bookings/:id/change-professional", h.changeProfessional)

	user.GET("/pro/me/prices", h.proPrices)
	user.POST("/pro/me/prices", h.proSubmitPrice)
	user.POST("/pro/me/prices/:id/withdraw", h.proWithdrawPrice)
	user.PUT("/pro/me/services/:id/same-day", h.proSameDay)
}

// pricingAdminRoutes: the price review queue and decisions.
func (h *Handler) pricingAdminRoutes() []adminRoute {
	return []adminRoute{
		{http.MethodGet, "/pro-prices", PermPricesReview, h.adminPriceReviews},
		{http.MethodPost, "/pro-prices/:id/approve", PermPricesReview, h.adminApprovePrice},
		{http.MethodPost, "/pro-prices/:id/reject", PermPricesReview, h.adminRejectPrice},
	}
}

func boolQuery(c *gin.Context, name string) (bool, bool) {
	raw := strings.TrimSpace(c.Query(name))
	if raw == "" {
		return false, true
	}
	v, err := strconv.ParseBool(raw)
	if err != nil {
		writeErr(c, apperr.Invalid(name, name+" must be true or false"))
		return false, false
	}
	return v, true
}

func (h *Handler) serviceProfessionals(c *gin.Context) {
	uid, ok := userID(c)
	if !ok {
		return
	}
	id, ok := uuidParam(c, "id")
	if !ok {
		return
	}
	q := service.ProfessionalsQuery{ServiceID: id, Date: c.Query("date"), Sort: c.Query("sort")}
	if q.OptionID, ok = optionalUUIDQuery(c, "option_id"); !ok {
		return
	}
	if q.AddressID, ok = optionalUUIDQuery(c, "address_id"); !ok {
		return
	}
	if raw := strings.TrimSpace(c.Query("quantity")); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil {
			writeErr(c, apperr.Invalid("quantity", "quantity must be a whole number"))
			return
		}
		q.Quantity = &n
	}
	for _, raw := range c.QueryArray("addon_id") {
		a, err := uuid.Parse(strings.TrimSpace(raw))
		if err != nil || a == uuid.Nil {
			writeErr(c, apperr.Invalid("addon_id", "addon_id must be a UUID"))
			return
		}
		q.AddonIDs = append(q.AddonIDs, a)
	}
	if q.Asap, ok = boolQuery(c, "asap"); !ok {
		return
	}
	if q.RequireFemale, ok = boolQuery(c, "require_female_pro"); !ok {
		return
	}
	v, err := h.svc.ServiceProfessionals(c.Request.Context(), uid, q)
	respond(c, http.StatusOK, v, err)
}

func (h *Handler) bookingProfessionals(c *gin.Context) {
	asap, ok := boolQuery(c, "asap")
	if !ok {
		return
	}
	h.bookingCall(c, http.StatusOK, func(c *gin.Context, uid, id uuid.UUID) (any, error) {
		return h.svc.BookingProfessionals(c.Request.Context(), uid, id, c.Query("date"), asap, c.Query("sort"))
	})
}

func (h *Handler) changeProfessional(c *gin.Context) {
	var in model.ProChangeRequest
	h.bookingBody(c, &in, func(c *gin.Context, uid, id uuid.UUID) (any, error) {
		return h.svc.ChangeProfessional(c.Request.Context(), uid, id, c.GetHeader(IdempotencyKeyHeader), in)
	})
}

// ---- /pro ----

func (h *Handler) proPrices(c *gin.Context) {
	proGet(c, func(uid uuid.UUID) (model.List[model.ProServicePricing], error) {
		items, err := h.svc.ProPrices(c.Request.Context(), uid)
		if items == nil {
			items = []model.ProServicePricing{}
		}
		return model.List[model.ProServicePricing]{Items: items}, err
	})
}

func (h *Handler) proSubmitPrice(c *gin.Context) {
	proBody(c, http.StatusCreated, func(uid uuid.UUID, in model.ProPriceInput) (*model.ProPrice, error) {
		return h.svc.ProSubmitPrice(c.Request.Context(), uid, in)
	})
}

func (h *Handler) proWithdrawPrice(c *gin.Context) {
	proWithID(c, http.StatusOK, func(uid, id uuid.UUID) (*model.ProPrice, error) {
		return h.svc.ProWithdrawPrice(c.Request.Context(), uid, id)
	})
}

func (h *Handler) proSameDay(c *gin.Context) {
	uid, ok := userID(c)
	if !ok {
		return
	}
	id, ok := uuidParam(c, "id")
	if !ok {
		return
	}
	var in model.SameDayInput
	if !bindJSON(c, &in) {
		return
	}
	v, err := h.svc.ProSetSameDay(c.Request.Context(), uid, id, in)
	respond(c, http.StatusOK, v, err)
}

// ---- admin ----

func (h *Handler) adminPriceReviews(c *gin.Context) {
	v, err := h.svc.AdminPriceReviews(c.Request.Context(), c.Query("status"), c.Query("city"), c.Query("pro_id"), c.Query("cursor"), c.Query("limit"))
	respond(c, http.StatusOK, v, err)
}

func (h *Handler) adminDecidePrice(c *gin.Context, approve bool) {
	id, ok := uuidParam(c, "id")
	if !ok {
		return
	}
	var in model.PriceDecisionInput
	if !bindJSON(c, &in) {
		return
	}
	v, err := h.svc.AdminDecidePrice(c.Request.Context(), adminActor(c), id, approve, in)
	respond(c, http.StatusOK, v, err)
}

func (h *Handler) adminApprovePrice(c *gin.Context) { h.adminDecidePrice(c, true) }
func (h *Handler) adminRejectPrice(c *gin.Context)  { h.adminDecidePrice(c, false) }
