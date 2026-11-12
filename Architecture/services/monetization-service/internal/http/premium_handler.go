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
//	GET  /v1/monetization/premium/invoices          the buyer's tax invoices and credit notes
//	GET  /v1/monetization/premium/invoices/:id      one document (JSON)
//	GET  /v1/monetization/premium/invoices/:id/document   printable HTML
//	GET  /v1/monetization/internal/premium/gst-register?from=&to=   GSTR-1 rows (service token)
//	GET  /v1/monetization/internal/premium/invoices/:id?user_id=    the buyer e-mail's facts (notification-service token)

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
	{http.MethodGet, "/v1/monetization/premium/invoices"},
	{http.MethodGet, "/v1/monetization/premium/invoices/:invoiceId"},
	{http.MethodGet, "/v1/monetization/premium/invoices/:invoiceId/document"},
	{http.MethodGet, "/v1/monetization/internal/premium/gst-register"},
	{http.MethodGet, "/v1/monetization/internal/premium/invoices/:invoiceId"},
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
	v1.GET("/premium/invoices", h.ListPremiumInvoices)
	v1.GET("/premium/invoices/:invoiceId", h.GetPremiumInvoice)
	v1.GET("/premium/invoices/:invoiceId/document", h.GetPremiumInvoiceDocument)
	// The accountant's GSTR-1 register: a signed admin-service token only;
	// the gateway refuses every /internal/ path at the edge.
	v1.GET("/internal/premium/gst-register", h.requireServiceCaller(OpPremiumGSTRegister, premiumGSTRegisterCallers...), h.PremiumGSTRegister)
	// The buyer's invoice e-mail: a signed notification-service token only
	// (premium_invoice_mail.go).
	v1.GET("/internal/premium/invoices/:invoiceId", h.requireServiceCaller(OpPremiumInvoiceMail, premiumInvoiceMailCallers...), h.PremiumInvoiceForMail)
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
	case errors.Is(err, service.ErrPremiumInvoiceNotFound):
		e(http.StatusNotFound, "NOT_FOUND", "Invoice not found")
	case errors.Is(err, service.ErrPremiumRegisterRange):
		e(http.StatusBadRequest, "BAD_REQUEST", "from and to are dates (YYYY-MM-DD), from <= to, at most 366 days apart")
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

// Premium tax documents (migration 029).
const (
	// OpPremiumGSTRegister is the operation a register token carries.
	OpPremiumGSTRegister = "monetization:premium-gst-register" //nolint:gosec // G101: service-token operation name, not a credential
)

// premiumGSTRegisterCallers: admin-service, for the accountant's export (the
// console screen is the founder's lane).
var premiumGSTRegisterCallers = []string{IssuerAdminService}

// ListPremiumInvoices — GET /v1/monetization/premium/invoices
//
//	200 {"data": {"invoices": [{"id","number","kind":"invoice|credit_note","date","total_paise","currency","pass_id"}]}}
//
// The caller's own documents only, newest first. A credit note's total is negative.
func (h *Handler) ListPremiumInvoices(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}
	out, err := h.premium.Invoices(c.Request.Context(), userID)
	if err != nil {
		writePremiumError(c, err)
		return
	}
	api.JSON(c.Writer, http.StatusOK, gin.H{"invoices": out}, nil)
}

func (h *Handler) premiumInvoiceID(c *gin.Context) (uuid.UUID, bool) {
	id, err := uuid.Parse(c.Param("invoiceId"))
	if err != nil {
		writePremiumError(c, service.ErrPremiumInvoiceNotFound)
		return uuid.Nil, false
	}
	return id, true
}

// GetPremiumInvoice — GET /v1/monetization/premium/invoices/:invoiceId
//
//	200 {"data": {"id","number","kind","date","pass_id","supplier":{...},"buyer_name"|null,
//	              "place_of_supply":{...},"reverse_charge":false,"lines":[...],"taxable_paise",
//	              "cgst_paise","sgst_paise","igst_paise","total_paise","currency","amount_in_words",
//	              "original_invoice":{"id","number","date"}|null}}
//	404 NOT_FOUND — no such document, or another buyer's (never 403).
func (h *Handler) GetPremiumInvoice(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}
	id, ok := h.premiumInvoiceID(c)
	if !ok {
		return
	}
	out, err := h.premium.Invoice(c.Request.Context(), userID, id)
	if err != nil {
		writePremiumError(c, err)
		return
	}
	api.JSON(c.Writer, http.StatusOK, out, nil)
}

// GetPremiumInvoiceDocument — GET /v1/monetization/premium/invoices/:invoiceId/document
//
//	200 text/html; Content-Disposition: attachment; filename="MP_26-27_000001.html"
//
// Printable HTML (the browser prints or saves it as PDF): the shared PDF
// renderer needs wkhtmltopdf, which no image ships.
func (h *Handler) GetPremiumInvoiceDocument(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}
	id, ok := h.premiumInvoiceID(c)
	if !ok {
		return
	}
	doc, err := h.premium.InvoiceDocument(c.Request.Context(), userID, id)
	if err != nil {
		writePremiumError(c, err)
		return
	}
	c.Header("Content-Disposition", `attachment; filename="`+doc.Filename+`"`)
	c.Header("Cache-Control", "private, no-store")
	c.Header("X-Content-Type-Options", "nosniff")
	c.Header("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'")
	c.Data(http.StatusOK, doc.ContentType, doc.Body)
}

// PremiumGSTRegister — GET /v1/monetization/internal/premium/gst-register?from=YYYY-MM-DD&to=YYYY-MM-DD
//
//	200 {"data": {"from","to","supplier_gstin","rows":[{"kind","number","date","original_number"?,"original_date"?,
//	              "place_of_supply_code","place_of_supply_name","supply_type","sac","gst_rate_bp",
//	              "taxable_paise","cgst_paise","sgst_paise","igst_paise","total_paise","tax_provisional"}],
//	              "totals":{...}}}
//
// Every document dated from..to (IST, inclusive; at most 366 days). B2C
// only: no buyer is registered. Admin-service token with
// monetization:premium-gst-register; never reachable through the gateway.
func (h *Handler) PremiumGSTRegister(c *gin.Context) {
	out, err := h.premium.GSTRegister(c.Request.Context(), c.Query("from"), c.Query("to"))
	if err != nil {
		writePremiumError(c, err)
		return
	}
	api.JSON(c.Writer, http.StatusOK, out, nil)
}
