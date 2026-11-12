package http

// Momentum Premium routes (migration 027; short-posts design 3.1, phase 0).
//
// PREMIUM_ENABLED (default false) opens them; off, every premium route
// answers 503 PREMIUM_NOT_ENABLED whatever the writes and memberships flags
// say. Maintenance mode closes them (503 MAINTENANCE). There is no route that
// confirms a payment: a pass is granted only by the signed payments event.
//
//	GET  /v1/monetization/premium/plans             the catalogue and prices
//	POST /v1/monetization/premium/checkout          {"plan": "premium_30d"}
//	GET  /v1/monetization/premium/me                active / ends_at / in_grace
//	GET  /v1/monetization/premium/passes/:passId    the app's poll after paying

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/atpost/monetization-service/internal/premium"
	"github.com/atpost/monetization-service/internal/service"
	"github.com/atpost/shared/api"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// WithPremium installs the Premium service and mirrors PREMIUM_ENABLED.
// Enabled with a nil service still answers 503 PREMIUM_NOT_ENABLED.
func (h *Handler) WithPremium(p *service.Premium, enabled bool) *Handler {
	h.premium, h.premiumEnabled = p, enabled
	return h
}

// premiumRoutes are the routes PREMIUM_ENABLED opens.
var premiumRoutes = []betaRule{
	{http.MethodGet, "/v1/monetization/premium/plans"},
	{http.MethodPost, "/v1/monetization/premium/checkout"},
	{http.MethodGet, "/v1/monetization/premium/me"},
	{http.MethodGet, "/v1/monetization/premium/passes/:passId"},
}

// premiumGate is the boundary's premium step: handled=true for a premium
// route, then allow says whether it is open.
func (h *Handler) premiumGate(method, pattern string) (handled, allow bool) {
	if !ruleIn(premiumRoutes, method, pattern) {
		return false, false
	}
	return true, h.premiumEnabled && h.premium != nil
}

func (h *Handler) registerPremiumRoutes(v1 *gin.RouterGroup) {
	v1.GET("/premium/plans", h.ListPremiumPlans)
	v1.POST("/premium/checkout", h.PremiumCheckout)
	v1.GET("/premium/me", h.GetMyPremium)
	v1.GET("/premium/passes/:passId", h.GetPremiumPass)
}

func writePremiumError(c *gin.Context, err error) {
	ctx := c.Request.Context()
	e := func(status int, code, msg string) { api.ErrorWithContext(ctx, c.Writer, status, code, msg, nil) }
	switch {
	case errors.Is(err, premium.ErrPriceNotSet):
		e(http.StatusServiceUnavailable, "PREMIUM_PRICE_NOT_SET", "This plan can't be bought yet")
	case errors.Is(err, premium.ErrTaxUnavailable), errors.Is(err, service.ErrPremiumAmountMismatch):
		e(http.StatusServiceUnavailable, "PREMIUM_TAX_UNAVAILABLE", "This plan can't be bought right now")
	case errors.Is(err, premium.ErrUnknownPlan):
		e(http.StatusBadRequest, "UNKNOWN_PLAN", "No such Premium plan")
	case errors.Is(err, service.ErrPremiumPaymentsUnavailable):
		e(http.StatusServiceUnavailable, "PREMIUM_PAYMENTS_UNAVAILABLE", "Payments are unavailable right now; try again")
	case errors.Is(err, service.ErrPaymentSessionUnavailable):
		e(http.StatusBadGateway, "PAYMENT_SESSION_UNAVAILABLE", "Checkout could not be opened; try again")
	case errors.Is(err, service.ErrPremiumPassNotFound):
		e(http.StatusNotFound, "NOT_FOUND", "Pass not found")
	default:
		e(http.StatusInternalServerError, "INTERNAL_ERROR", "Something went wrong")
	}
}

// ListPremiumPlans — GET /v1/monetization/premium/plans
//
//	200 {"data": {"plans": [{"code","name","duration_days","price_paise"|null,"taxable_paise"|null,
//	                         "gst_paise"|null,"total_paise"|null,"gst_rate_bp"|null,"prices_exclude_gst":true,
//	                         "tax_provisional","currency","available"}]}}
//
// Prices exclude GST (13 Nov 2026): price_paise is the taxable value and
// total_paise is what the buyer pays (testdata/contracts/premium).
func (h *Handler) ListPremiumPlans(c *gin.Context) {
	if _, ok := getUserID(c); !ok {
		return
	}
	plans, err := h.premium.Plans(c.Request.Context())
	if err != nil {
		writePremiumError(c, err)
		return
	}
	api.JSON(c.Writer, http.StatusOK, gin.H{"plans": plans}, nil)
}

type premiumCheckoutRequest struct {
	Plan string `json:"plan"`
}

// PremiumCheckout — POST /v1/monetization/premium/checkout {"plan":"premium_30d"}
//
//	201 {"data": {"pass_id","plan","duration_days","status":"pending_payment",
//	              "amount_paise","currency","intent_id","client_session":{...}|null,
//	              "taxable_paise","gst_paise","cgst_paise","sgst_paise","igst_paise","total_paise",
//	              "gst_rate_bp","sac","tax_provisional","prices_exclude_gst":true}}
//
// amount_paise is the GST-inclusive total (= total_paise): the intent amount.
//
// The amount is the server's configured price; a body that names anything
// but the plan (an amount, say) is refused with 400.
func (h *Handler) PremiumCheckout(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}
	var req premiumCheckoutRequest
	dec := json.NewDecoder(io.LimitReader(c.Request.Body, 4<<10))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil || strings.TrimSpace(req.Plan) == "" {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "BAD_REQUEST",
			`the body is {"plan": "<plan code>"} and nothing else`, nil)
		return
	}
	out, err := h.premium.Checkout(c.Request.Context(), userID, strings.TrimSpace(req.Plan))
	if err != nil {
		writePremiumError(c, err)
		return
	}
	api.JSON(c.Writer, http.StatusCreated, out, nil)
}

// GetMyPremium — GET /v1/monetization/premium/me
//
//	200 {"data": {"active","in_grace","ends_at"|null,"grace_ends_at"|null,"renewal_plans":[...]}}
func (h *Handler) GetMyPremium(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}
	out, err := h.premium.Status(c.Request.Context(), userID)
	if err != nil {
		writePremiumError(c, err)
		return
	}
	api.JSON(c.Writer, http.StatusOK, out, nil)
}

// GetPremiumPass — GET /v1/monetization/premium/passes/:passId
//
//	200 {"data": {"pass_id","plan","status":"confirming|paid|failed|refunded",
//	              "amount_paise","currency","starts_at"|null,"ends_at"|null, + the checkout's GST split
//	              (every split field null and prices_exclude_gst false on a pass from before 028)}}
func (h *Handler) GetPremiumPass(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}
	passID, err := uuid.Parse(c.Param("passId"))
	if err != nil {
		writePremiumError(c, service.ErrPremiumPassNotFound)
		return
	}
	out, err := h.premium.Pass(c.Request.Context(), userID, passID)
	if err != nil {
		writePremiumError(c, err)
		return
	}
	api.JSON(c.Writer, http.StatusOK, out, nil)
}
