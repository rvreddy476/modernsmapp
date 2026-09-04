package http

// The P0 HTTP surface.
//
// Three things happen in this file:
//
//	1. The endpoints that replace the removed public payments routes
//	   (LB-1/LB-4): the client names an ORDER and never an amount.
//	2. Typed error responses. The old handler mapped EVERY store error to
//	   `INTERNAL_ERROR` with a 500, so a client could not tell "this item is
//	   out of stock" from "our database is down" and therefore could not
//	   render either (v1 §4.5).
//	3. The fence (§4, A5, LB-11). Surfaces outside the launch loop are not
//	   registered at all, and a default-deny guard answers 404 for them even
//	   if a future edit re-registers one by accident.

import (
	"errors"
	"log/slog"
	"net/http"
	"strings"

	"github.com/atpost/commerce-service/internal/media"
	"github.com/atpost/commerce-service/internal/money"
	"github.com/atpost/commerce-service/internal/service"
	"github.com/atpost/commerce-service/internal/store/postgres"
	"github.com/atpost/shared/api"
	"github.com/atpost/shared/paymentmethod"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// FencedPrefixes are route families outside the Commerce P0 loop.
//
// Review §7: "Fencing is sufficient only if there is a production-default-deny
// route allowlist, gateway reachability test, disabled worker/consumer entry
// points, and safe handling/quarantine of legacy queued messages. Hiding
// controls in Android is not a fence."
//
// This is the HTTP half. The worker half is in main.go, and migration 012
// puts triggers on the tables so even a replayed legacy job cannot write.
var FencedPrefixes = []string{
	"/v1/commerce/rfqs",
	"/v1/commerce/seller/rfqs",
	"/v1/commerce/organizations",
	"/v1/commerce/seller/bulk-import",
	"/v1/commerce/affiliate",
	"/v1/commerce/payout",
	"/v1/commerce/seller/cod-remittances",
	"/v1/commerce/seller/earnings",
	// LB-11 / M-3: return creation accepted caller-supplied order and seller
	// ids with no relational check, so a caller could attach a return to a
	// stranger's order. Returns are not in the launch loop, so the whole
	// family is unreachable rather than repaired.
	"/v1/commerce/returns",
	"/v1/commerce/seller/returns",
	"/v1/commerce/me/returns",
}

// FencedRoute is a single method+path that must not be reachable, for the
// cases a prefix cannot express.
type FencedRoute struct {
	Method string
	// Pattern is a gin-style path with `:param` segments, matched
	// segment-by-segment so `/v1/commerce/orders/:orderId/payment/confirm`
	// can be fenced without fencing `/v1/commerce/orders` itself.
	Pattern string
	// Why is surfaced in the reachability proof's failure message, so a
	// regression names the money defect rather than just a path.
	Why string
}

// FencedRoutes are the legacy money surfaces (B5).
//
// A prefix cannot express these: `/v1/commerce/orders` is a LIVE P0 prefix
// (order reads, and the payment-intent endpoints that replaced the removed
// public payments routes), so fencing the prefix would take the launch loop
// down with it. These three are fenced by exact method+shape instead.
//
// All three are additionally UNREGISTERED in RegisterRoutes. The fence is the
// backstop, not the control — a later edit that re-adds the route still gets
// a 404, which is the same belt-and-braces shape api-gateway/pkg/routepolicy
// uses for the payments upstream.
var FencedRoutes = []FencedRoute{
	{
		Method:  http.MethodPost,
		Pattern: "/v1/commerce/orders/checkout",
		Why: "the legacy checkout created the order first and then reserved each line in a " +
			"separate operation, logging and continuing on reservation failure — so a two-line " +
			"cart where only one line could be held still produced a payable order, and the " +
			"customer was charged for stock that was never secured. It also priced in float64 " +
			"rupees and predates GST extraction. Use POST /v1/commerce/v2/orders/checkout",
	},
	// NOTE: POST /v1/commerce/checkout/quote is deliberately NOT fenced.
	// The duplicate registration that panicked gin (B10) is fixed by
	// DELETING the legacy registration in RegisterRoutes; the path itself is
	// the live P0 quote endpoint and must stay reachable. Fencing it here
	// would take the launch loop down — which is what the first version of
	// this list did, and what TestFencedRoutesExplainThemselves caught.
	{
		Method:  http.MethodPost,
		Pattern: "/v1/commerce/orders/:orderId/payment/confirm",
		Why: "a client-asserted payment fact. The buyer posted their own gateway triple and the " +
			"order became paid on the strength of it, which is the authority A1/LB-3 removed " +
			"from payments-service and must not survive here. Terminal payment state arrives " +
			"only on the signature-verified provider webhook, via the payments outbox",
	},
	{
		Method:  http.MethodPost,
		Pattern: "/v1/commerce/orders/:orderId/returns",
		Why: "return CREATION escaped the returns fence. FencedPrefixes covers " +
			"/v1/commerce/returns, /seller/returns and /me/returns, but this route lives under " +
			"the LIVE /v1/commerce/orders prefix, so IsFencedPath never matched it and the one " +
			"route the fence was written for stayed reachable. It is the route that takes " +
			"caller-supplied order_item_id and seller_id, which is why the family was fenced: " +
			"a caller could attach a return to a stranger's order. Returns are not in the P0 " +
			"loop and have no refund money path behind them",
	},
}

// matchesPattern compares a request path against a gin-style pattern,
// treating `:param` segments as single-segment wildcards.
func matchesPattern(path, pattern string) bool {
	ps := strings.Split(strings.Trim(path, "/"), "/")
	qs := strings.Split(strings.Trim(pattern, "/"), "/")
	if len(ps) != len(qs) {
		return false
	}
	for i := range qs {
		if strings.HasPrefix(qs[i], ":") {
			if ps[i] == "" {
				return false
			}
			continue
		}
		if ps[i] != qs[i] {
			return false
		}
	}
	return true
}

// FencedRouteReason reports why a method+path is fenced, or "" if it is not.
// Exported so the reachability proof enumerates the server's own list.
func FencedRouteReason(method, path string) string {
	for _, r := range FencedRoutes {
		if r.Method == method && matchesPattern(path, r.Pattern) {
			return r.Why
		}
	}
	return ""
}

// FenceMiddleware answers 404 for any fenced path.
//
// Default-deny, and it runs before routing, so it holds even if a route is
// re-registered by a later edit. 404 rather than 403: a fenced surface
// should not confirm it exists.
func FenceMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		p := c.Request.URL.Path
		for _, f := range FencedPrefixes {
			if p == f || strings.HasPrefix(p, f+"/") {
				api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusNotFound,
					"NOT_FOUND", "not found", nil)
				c.Abort()
				return
			}
		}
		// B5: the legacy money routes, fenced by exact method+shape because
		// they sit under prefixes the launch loop still uses.
		if why := FencedRouteReason(c.Request.Method, p); why != "" {
			slog.Warn("commerce: refused a fenced legacy money route",
				"method", c.Request.Method, "path", p, "why", why)
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusNotFound,
				"NOT_FOUND", "not found", nil)
			c.Abort()
			return
		}
		c.Next()
	}
}

// IsFencedPath reports whether a path is outside the P0 loop. Exported so
// the reachability proof can enumerate the same list the server uses,
// instead of a copy that could drift.
func IsFencedPath(p string) bool {
	for _, f := range FencedPrefixes {
		if p == f || strings.HasPrefix(p, f+"/") {
			return true
		}
	}
	return false
}

// RegisterP0Routes adds the launch-loop endpoints.
func (h *Handler) RegisterP0Routes(r *gin.Engine) {
	v1 := r.Group("/v1/commerce")

	// A4: the quote is taken BEFORE checkout, because it is a network call
	// and no network call may happen before the checkout commit.
	// The seller's pickup address — the origin of every shipment, and the
	// seller half of the GST place-of-supply comparison.
	v1.PUT("/seller/address", h.SaveSellerAddress)

	// Stock after creation. Before these, `POST /products` set stock once and
	// the only route that wrote it again was behind the launch fence.
	v1.PATCH("/seller/variants/:variantId/stock", h.AdjustStock)
	v1.GET("/seller/variants/:variantId/stock", h.GetStock)
	v1.GET("/seller/variants/:variantId", h.GetSellerVariant)

	// The seller's own catalogue. The public /sellers/:sellerId/products route
	// is storefront-filtered; this one resolves the seller from the caller.
	v1.GET("/seller/products", h.ListMyProducts)

	// What the shop still needs before it can be reviewed. The submit route
	// enforces the same rules; this is the checklist, not the guard.
	v1.GET("/onboarding/readiness", h.GetSellerReadiness)

	// The GST rate table. Public, and required before a product can be
	// created: a listing with no tax class is one checkout refuses.
	v1.GET("/tax-classes", h.ListTaxClasses)

	v1.POST("/checkout/quote", h.PrepareQuote)

	// LB-14/LB-15. Registered as v2 because the contract changed in ways a
	// v1 client cannot satisfy: Idempotency-Key is mandatory, a quote id is
	// required, and every money field is minor units.
	v1.POST("/v2/orders/checkout", h.CheckoutP0)

	// LB-1/LB-4: these replace the public payments routes. The client names
	// an order; commerce authors the amount from the order it owns.
	v1.POST("/orders/:orderId/payment/intent", h.OpenPaymentIntent)
	v1.GET("/orders/:orderId/payment/status", h.PaymentStatus)
}

// PrepareQuote POST /v1/commerce/checkout/quote
func (h *Handler) PrepareQuote(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}
	// C3-LB-2: the quote now prices the whole cart, so it needs everything
	// that changes the price. Both fields are bound into the stored quote and
	// re-checked at checkout.
	var body struct {
		AddressID     string `json:"address_id" binding:"required"`
		CouponCode    string `json:"coupon_code"`
		PaymentMethod string `json:"payment_method"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_BODY", err.Error(), nil)
		return
	}
	addrID, err := uuid.Parse(body.AddressID)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_BODY", "invalid address_id", nil)
		return
	}
	// A quote is bound to its payment method, so an unsupported one is
	// refused HERE rather than producing a quote checkout will reject.
	// Absent is allowed: a client may price a cart before choosing.
	if body.PaymentMethod != "" {
		if mErr := paymentmethod.Validate(body.PaymentMethod); mErr != nil {
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest,
				"PAYMENT_METHOD_NOT_SUPPORTED", mErr.Error(), nil)
			return
		}
	}
	res, err := h.svc.PrepareQuote(c.Request.Context(), service.QuoteInputP0{
		UserID:        userID,
		AddressID:     addrID,
		CouponCode:    body.CouponCode,
		PaymentMethod: body.PaymentMethod,
	})
	if err != nil {
		writeCommerceError(c, err)
		return
	}
	if !res.Serviceable {
		// A first-class state, not an error: the address is real, we simply
		// cannot deliver to it, and the app renders a specific message.
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusUnprocessableEntity,
			"NOT_SERVICEABLE", res.Reason, nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, res, nil)
}

// CheckoutP0 POST /v1/commerce/v2/orders/checkout
func (h *Handler) CheckoutP0(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}
	// LB-15: REQUIRED. The old handler fell back to a fabricated
	// "userID-<nanotime>" key, which cannot match a retry and therefore
	// deduped nothing — the unique index existed but never fired.
	idem := strings.TrimSpace(c.GetHeader("Idempotency-Key"))
	if idem == "" {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest,
			"IDEMPOTENCY_KEY_REQUIRED",
			"an Idempotency-Key header is required so a retried checkout cannot create a second order", nil)
		return
	}

	var body struct {
		AddressID          string `json:"address_id" binding:"required"`
		QuoteID            string `json:"quote_id" binding:"required"`
		PaymentMethod      string `json:"payment_method" binding:"required"`
		CouponCode         string `json:"coupon_code"`
		TermsVersion       string `json:"terms_version"`
		ExpectedTotalMinor int64  `json:"expected_total_minor"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_BODY", err.Error(), nil)
		return
	}
	addrID, err := uuid.Parse(body.AddressID)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_BODY", "invalid address_id", nil)
		return
	}
	quoteID, err := uuid.Parse(body.QuoteID)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_BODY", "invalid quote_id", nil)
		return
	}
	// N6 — `expected_total_minor` is MANDATORY and must be positive.
	//
	// It was optional, and both price-change comparisons in the checkout
	// transaction were written as `if p.ExpectedTotalMinor > 0`. So a client
	// that omitted the field, or sent zero, disabled the entire price-change
	// promise: the cart could show ₹100, the seller could raise the price to
	// ₹1,000 between quote and checkout, and the order committed at ₹1,000
	// with no ErrPriceChanged and nothing shown to the customer.
	//
	// A protection the client can switch off by leaving a field out is not a
	// protection. Rejected here, at the edge, with a specific code so a stale
	// client gets a diagnosable 400 rather than a silent overcharge.
	// C3-LB-3 — the launch payment-method vocabulary, refused at the edge.
	//
	// This is the FIRST of three refusals (edge, service, store) plus the
	// database CHECK. It matters that it is first: everything below this line
	// eventually reserves stock and claims coupon capacity, and an order that
	// commits with a method payments cannot open is an order holding
	// inventory that nobody can pay for.
	if err := paymentmethod.Validate(body.PaymentMethod); err != nil {
		// COD keeps its own code: the client renders dedicated copy for it,
		// and A5 is a named launch decision rather than a mistyped field.
		code := "PAYMENT_METHOD_NOT_SUPPORTED"
		if strings.EqualFold(strings.TrimSpace(body.PaymentMethod), "cod") {
			code = "COD_NOT_SUPPORTED"
		}
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest,
			code, err.Error(), nil)
		return
	}
	if body.ExpectedTotalMinor <= 0 {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest,
			"EXPECTED_TOTAL_REQUIRED",
			"expected_total_minor is required and must be positive: it is the price the customer "+
				"approved, and checkout refuses rather than charging a total they were never shown", nil)
		return
	}

	res, err := h.svc.CheckoutP0(c.Request.Context(), service.CheckoutInputP0{
		UserID:             userID,
		AddressID:          addrID,
		QuoteID:            quoteID,
		IdempotencyKey:     idem,
		CouponCode:         body.CouponCode,
		PaymentMethod:      body.PaymentMethod,
		TermsVersion:       body.TermsVersion,
		ExpectedTotalMinor: money.Paise(body.ExpectedTotalMinor),
	})
	if err != nil {
		writeCommerceError(c, err)
		return
	}
	api.JSON(c.Writer, http.StatusCreated, res, nil)
}

// OpenPaymentIntent POST /v1/commerce/orders/:orderId/payment/intent
//
// LB-4. This is what replaces the public POST /v1/payments/intents. The
// difference that matters: the request body has no amount. Commerce reads
// the order it owns and authors the payable itself, so there is no path by
// which a buyer chooses what their own order costs.
func (h *Handler) OpenPaymentIntent(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}
	orderID, ok := parseUUID(c, "orderId")
	if !ok {
		return
	}
	intent, err := h.svc.OpenPaymentForOrder(c.Request.Context(), orderID, userID)
	if err != nil {
		writeCommerceError(c, err)
		return
	}
	body := gin.H{
		"payment_intent_id": intent.ID,
		"amount_minor":      intent.AmountMinor,
		"currency":          intent.Currency,
		"provider_ref":      intent.ProviderRef,
		"status":            intent.Status,
	}
	// What the client SDK needs to open checkout. It originates in
	// payments-service from the adapter that created the provider order, so
	// the publishable key always matches that order — an app-compiled key
	// could disagree with the server's environment and fail confusingly.
	//
	// Note what is NOT here: an amount. LB-4 is that the client never names
	// what it is paying; the provider order already fixes it, and the app
	// displays the figure the ORDER carries.
	if len(intent.ClientSession) > 0 {
		body["client_session"] = intent.ClientSession
	}
	api.JSON(c.Writer, http.StatusOK, body, nil)
}

// PaymentStatus GET /v1/commerce/orders/:orderId/payment/status
//
// A1: the app polls this. A client redirect is never proof of payment, so
// the UI shows "confirming your payment" until this reports otherwise.
func (h *Handler) PaymentStatus(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}
	orderID, ok := parseUUID(c, "orderId")
	if !ok {
		return
	}
	st, err := h.svc.PaymentStatusForOrder(c.Request.Context(), orderID, userID)
	if err != nil {
		writeCommerceError(c, err)
		return
	}
	api.JSON(c.Writer, http.StatusOK, st, nil)
}

// ─── Typed errors ────────────────────────────────────────────────────

// writeCommerceError maps a domain error to a status and a machine-readable
// code.
//
// The old `handleErr` was:
//
//	api.ErrorWithContext(..., http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
//
// for everything. So an out-of-stock race, a price change, a permission
// failure and a database outage were indistinguishable to the client — and
// the raw error string was leaked into the response, which is its own
// problem when the message contains identifiers.
func writeCommerceError(c *gin.Context, err error) {
	ctx := c.Request.Context()
	w := c.Writer

	// Out of stock carries per-line detail so the cart can grey the right
	// rows instead of showing a generic failure.
	var oos *postgres.OutOfStockError
	if errors.As(err, &oos) {
		api.ErrorWithContext(ctx, w, http.StatusConflict, "OUT_OF_STOCK",
			"one or more items are no longer available", gin.H{"lines": oos.Lines})
		return
	}
	// Price changed: the client must show old→new and get an explicit
	// acknowledgement before retrying.
	var pc *postgres.PriceChangedError
	if errors.As(err, &pc) {
		api.ErrorWithContext(ctx, w, http.StatusConflict, "PRICE_CHANGED",
			"prices changed since you last saw them", gin.H{
				"lines":           pc.Lines,
				"new_total_minor": pc.NewTotalMinor,
			})
		return
	}

	switch {
	case errors.Is(err, postgres.ErrIdempotencyConflict):
		// M-7: same key, different request. Returning the old order here is
		// how a client that changed its address after a timeout would have
		// silently shipped to the wrong place.
		api.ErrorWithContext(ctx, w, http.StatusConflict, "IDEMPOTENCY_CONFLICT",
			"this Idempotency-Key was used for a different request", nil)
	case errors.Is(err, postgres.ErrQuoteExpired):
		api.ErrorWithContext(ctx, w, http.StatusConflict, "QUOTE_EXPIRED",
			"the delivery quote expired; please re-quote", nil)
	case errors.Is(err, postgres.ErrQuoteMismatch), errors.Is(err, postgres.ErrCartChanged):
		api.ErrorWithContext(ctx, w, http.StatusConflict, "QUOTE_STALE",
			"your cart or address changed after the delivery quote; please re-quote", nil)
	case errors.Is(err, postgres.ErrQuoteConsumed):
		api.ErrorWithContext(ctx, w, http.StatusConflict, "QUOTE_CONSUMED",
			"that delivery quote has already been used", nil)
	case errors.Is(err, postgres.ErrCartEmpty):
		api.ErrorWithContext(ctx, w, http.StatusBadRequest, "CART_EMPTY", "your cart is empty", nil)
	case errors.Is(err, postgres.ErrExpectedTotalRequired):
		// N6: reached when an internal caller bypasses the HTTP edge check.
		api.ErrorWithContext(ctx, w, http.StatusBadRequest, "EXPECTED_TOTAL_REQUIRED",
			"expected_total_minor is required and must be positive", nil)
	case errors.Is(err, postgres.ErrAddressNotOwned):
		// 404, not 403: a caller should not be able to probe which address
		// ids exist by watching the status code change.
		api.ErrorWithContext(ctx, w, http.StatusNotFound, "ADDRESS_NOT_FOUND", "address not found", nil)
	case errors.Is(err, postgres.ErrProductUnavailable):
		api.ErrorWithContext(ctx, w, http.StatusConflict, "PRODUCT_UNAVAILABLE",
			"an item in your cart is no longer available", nil)
	case errors.Is(err, postgres.ErrMultipleSellers):
		api.ErrorWithContext(ctx, w, http.StatusConflict, "MULTIPLE_SELLERS",
			"your cart contains items from more than one seller", nil)
	case errors.Is(err, postgres.ErrCouponExhausted):
		api.ErrorWithContext(ctx, w, http.StatusConflict, "COUPON_UNAVAILABLE",
			"that coupon is no longer available", nil)
	case errors.Is(err, postgres.ErrCouponNotApplicable):
		// B9: distinct from exhausted. The coupon is valid and has capacity;
		// it simply does not apply to these items — which is what the client
		// needs to tell the customer.
		api.ErrorWithContext(ctx, w, http.StatusConflict, "COUPON_NOT_APPLICABLE",
			"that coupon does not apply to the items in your cart", nil)
	case errors.Is(err, postgres.ErrTaxClassMissing), errors.Is(err, postgres.ErrTaxClassInvalid):
		// B9: a listing we cannot compute GST for is not sellable. 409 with
		// its own code so the seller-facing surface can say what to fix
		// rather than reporting a generic outage.
		slog.Error("commerce: checkout refused — a cart line has no usable GST tax class",
			"error", err)
		api.ErrorWithContext(ctx, w, http.StatusConflict, "PRODUCT_TAX_UNCONFIGURED",
			"an item in your cart cannot be sold right now (tax configuration incomplete)", nil)
	case errors.Is(err, postgres.ErrCODNotSupported):
		api.ErrorWithContext(ctx, w, http.StatusBadRequest, "COD_NOT_SUPPORTED",
			"cash on delivery is not available", nil)
	case errors.Is(err, media.ErrNotYourMedia):
		// 403. Never 404: telling the caller the id does not exist when it
		// belongs to someone else, and 403 when it is theirs, is an ownership
		// oracle over every media id in the platform.
		api.ErrorWithContext(ctx, w, http.StatusForbidden, "NOT_YOUR_MEDIA",
			"that media was uploaded by another account", nil)
	case errors.Is(err, media.ErrMediaNotFound):
		api.ErrorWithContext(ctx, w, http.StatusBadRequest, "MEDIA_NOT_FOUND",
			"no such media", nil)
	case errors.Is(err, media.ErrMediaNotReady):
		api.ErrorWithContext(ctx, w, http.StatusConflict, "MEDIA_NOT_READY",
			"that upload has not finished processing yet", nil)
	case errors.Is(err, media.ErrMediaNotPassed):
		api.ErrorWithContext(ctx, w, http.StatusConflict, "MEDIA_NOT_MODERATED",
			"that media has not passed moderation", nil)
	case errors.Is(err, media.ErrMediaWrongKind):
		api.ErrorWithContext(ctx, w, http.StatusBadRequest, "MEDIA_WRONG_KIND", err.Error(), nil)
	case errors.Is(err, media.ErrMediaUnavailable):
		// 503, not 403. The caller did nothing wrong; media-service is down
		// and this service fails CLOSED rather than storing an unverified
		// reference. Retryable, and it must read that way.
		slog.Error("commerce: media verification could not be performed", "error", err)
		api.ErrorWithContext(ctx, w, http.StatusServiceUnavailable, "MEDIA_UNAVAILABLE",
			"media could not be verified right now; please retry", nil)
	case errors.Is(err, service.ErrApplicationIncomplete):
		// 409, not 400: nothing about the REQUEST is malformed. The shop is
		// not ready yet, which is a state the seller can resolve, and the
		// message names everything still missing.
		api.ErrorWithContext(ctx, w, http.StatusConflict, "APPLICATION_INCOMPLETE", err.Error(), nil)
	case errors.Is(err, postgres.ErrPriceDisagreement):
		// Both shapes of the same price, disagreeing. Refused rather than
		// resolved: picking one silently decides what the buyer pays.
		api.ErrorWithContext(ctx, w, http.StatusBadRequest, "PRICE_DISAGREEMENT", err.Error(), nil)
	case errors.Is(err, postgres.ErrPriceNotPositive):
		api.ErrorWithContext(ctx, w, http.StatusBadRequest, "PRICE_NOT_POSITIVE",
			"a price must be greater than zero", nil)
	case errors.Is(err, service.ErrNotProductOwner):
		// 403, matching the variant-ownership rule: the seller reached this
		// route holding an id from their own catalogue view, so 404 would read
		// as "your product was deleted".
		api.ErrorWithContext(ctx, w, http.StatusForbidden, "NOT_YOUR_PRODUCT",
			"that product belongs to another seller", nil)
	case errors.Is(err, postgres.ErrVariantNotFound):
		api.ErrorWithContext(ctx, w, http.StatusNotFound, "VARIANT_NOT_FOUND", "variant not found", nil)
	case errors.Is(err, postgres.ErrNotYourVariant):
		// 403, not 404. The seller reached this route holding a variant id
		// from their own catalogue view, so "not found" would read as "your
		// product was deleted" and send them looking for a listing that is
		// still there. Before this case the sentinel fell through to the
		// default and every ownership failure was a 500.
		api.ErrorWithContext(ctx, w, http.StatusForbidden, "NOT_YOUR_VARIANT",
			"that variant belongs to another seller", nil)
	case errors.Is(err, postgres.ErrStockBelowReserved):
		// The units are promised to orders that are mid-checkout. A seller
		// writing down damaged stock needs to be told that, not handed a 500
		// from the chk_inv_reserved_le_total CHECK.
		api.ErrorWithContext(ctx, w, http.StatusConflict, "STOCK_RESERVED",
			"those units are reserved for orders being placed right now", nil)
	case errors.Is(err, postgres.ErrZeroAdjustment), errors.Is(err, service.ErrInvalidStockReason):
		api.ErrorWithContext(ctx, w, http.StatusBadRequest, "INVALID_ADJUSTMENT", err.Error(), nil)
	case errors.Is(err, postgres.ErrNotOrderOwnerP0), errors.Is(err, service.ErrNotOrderOwner):
		api.ErrorWithContext(ctx, w, http.StatusNotFound, "ORDER_NOT_FOUND", "order not found", nil)
	case errors.Is(err, postgres.ErrOrderNotFoundP0), errors.Is(err, service.ErrOrderNotFound):
		api.ErrorWithContext(ctx, w, http.StatusNotFound, "ORDER_NOT_FOUND", "order not found", nil)
	case errors.Is(err, postgres.ErrCancelNotPermitted):
		api.ErrorWithContext(ctx, w, http.StatusConflict, "CANCEL_NOT_PERMITTED",
			"this order can no longer be cancelled", nil)
	case errors.Is(err, service.ErrOrderNotPaymentPending):
		api.ErrorWithContext(ctx, w, http.StatusConflict, "ORDER_NOT_PAYABLE",
			"this order is not awaiting payment", nil)
	case postgres.IsFenced(err):
		api.ErrorWithContext(ctx, w, http.StatusNotFound, "NOT_FOUND", "not found", nil)
	case postgres.IsRetryable(err):
		api.ErrorWithContext(ctx, w, http.StatusServiceUnavailable, "TRY_AGAIN",
			"please try again", nil)
	case errors.Is(err, postgres.ErrPlaceOfSupplyUnknown):
		// The seller's place of supply is missing, so GST cannot be
		// determined and the store refused to guess. That refusal is
		// correct; answering it with an unexplained 500 was not. It is a
		// 409 because nothing about the request is malformed — the shop is
		// not in a state that can be sold from, and the buyer retrying the
		// same call changes nothing until the seller fixes their address.
		api.ErrorWithContext(ctx, w, http.StatusConflict, "PLACE_OF_SUPPLY_UNKNOWN",
			"this seller cannot be checked out from yet: their pickup address is incomplete", nil)
	default:
		// Genuinely unexpected. The message is NOT echoed: it can contain
		// identifiers, and the client has nothing useful to do with it.
		//
		// It IS logged. Before this line an unmapped sentinel became a bare
		// 500 that appeared nowhere — not in the response, not in the log —
		// so the only way to find out which error a production 500 meant was
		// to reproduce it locally and read the code. ErrPlaceOfSupplyUnknown
		// reached this branch for exactly that reason and cost a code trace
		// to identify.
		slog.ErrorContext(ctx, "commerce: unmapped error returned to the client as 500",
			"error", err,
			"method", c.Request.Method,
			"path", c.FullPath())
		api.ErrorWithContext(ctx, w, http.StatusInternalServerError, "INTERNAL_ERROR",
			"something went wrong", nil)
	}
}

// ─── Seller pickup address ───────────────────────────────────────────

type sellerAddressReq struct {
	AddressType  string  `json:"address_type"`
	ContactName  string  `json:"contact_name"`
	FullName     string  `json:"full_name"`
	Phone        string  `json:"phone" binding:"required"`
	AddressLine1 string  `json:"address_line_1" binding:"required"`
	AddressLine2 *string `json:"address_line_2"`
	City         string  `json:"city" binding:"required"`
	State        string  `json:"state" binding:"required"`
	PostalCode   string  `json:"postal_code" binding:"required"`
	Country      string  `json:"country"`
	IsDefault    bool    `json:"is_default"`
}

// SaveSellerAddress PUT /v1/commerce/seller/address
//
// The seller's pickup point — the origin of every shipment they send.
//
// Nothing in production wrote `seller_addresses` before this. `POST
// /sellers/onboard` stores only the flat `state`/`city`/`postal_code` columns
// on `sellers`, and the onboarding wizard leaves `pickup_address_id` NULL. So
// `SellerPickupPin`'s fallback branch was the only live one, reading an
// optional column — and a seller who skipped it had every delivery quoted from
// an empty origin.
//
// `state` and `postal_code` are required here rather than optional, because
// both decide money: the PIN is the courier's origin, and the state is the
// seller half of the GST place-of-supply comparison.
func (h *Handler) SaveSellerAddress(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}
	var body sellerAddressReq
	if err := c.ShouldBindJSON(&body); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_BODY", err.Error(), nil)
		return
	}
	// Both wire names, for the same reason the customer address accepts both.
	name := strings.TrimSpace(body.ContactName)
	if name == "" {
		name = strings.TrimSpace(body.FullName)
	}
	if name == "" {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_BODY",
			"contact_name is required", nil)
		return
	}

	err := h.svc.SaveSellerAddress(c.Request.Context(), userID, service.SellerAddressInput{
		AddressType:  body.AddressType,
		ContactName:  name,
		Phone:        body.Phone,
		AddressLine1: body.AddressLine1,
		AddressLine2: body.AddressLine2,
		City:         body.City,
		State:        body.State,
		PostalCode:   body.PostalCode,
		Country:      body.Country,
		IsDefault:    body.IsDefault,
	})
	switch {
	case errors.Is(err, service.ErrNoSellerProfile):
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusForbidden,
			"NO_SELLER", "seller account not found", nil)
		return
	case err != nil:
		writeCommerceError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}
