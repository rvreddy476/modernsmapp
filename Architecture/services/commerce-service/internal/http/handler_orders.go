// Checkout and order route handlers: serviceability, coupon preview,
// quote, checkout, order list and detail, order items, cancel and payment
// confirmation. Moved out of handler.go unchanged.

package http

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/atpost/commerce-service/internal/service"
	"github.com/atpost/commerce-service/internal/store/postgres"
	"github.com/atpost/shared/api"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// ─── Order handlers ──────────────────────────────────────────────

type checkoutReq struct {
	AddressID      uuid.UUID `json:"address_id" binding:"required"`
	PaymentMethod  string    `json:"payment_method" binding:"required"`
	CouponCode     string    `json:"coupon_code"`
	GiftMessage    *string   `json:"gift_message"`
	IdempotencyKey string    `json:"idempotency_key"`

	// Phase 5 — optional B2B context.
	OrganizationID *uuid.UUID `json:"organization_id"`
	PONumber       *string    `json:"po_number"`
	CostCenter     *string    `json:"cost_center"`
	InvoiceEmail   *string    `json:"invoice_email"`
}

// quoteReq mirrors checkoutReq minus the persistence-only fields. Mobile
// and web call this BEFORE "Place order" so the customer sees server-
// authoritative totals; the client never recomputes pricing locally.
type quoteReq struct {
	AddressID     uuid.UUID `json:"address_id" binding:"required"`
	PaymentMethod string    `json:"payment_method" binding:"required"`
	CouponCode    string    `json:"coupon_code"`
}

// CheckServiceability GET /v1/commerce/serviceability — Phase 1.3.
// Customer-facing pincode + COD + ETA check used by the product detail
// page and checkout to replace the mobile pincode heuristic.
//
//	?pincode=560001&product_id=<uuid>[&variant_id=<uuid>][&seller_id=<uuid>][&payment_method=prepaid|cod]
func (h *Handler) CheckServiceability(c *gin.Context) {
	if _, ok := getUserID(c); !ok {
		return
	}
	pincode := c.Query("pincode")
	productIDStr := c.Query("product_id")
	if pincode == "" || productIDStr == "" {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_QUERY", "pincode and product_id are required", nil)
		return
	}
	productID, err := uuid.Parse(productIDStr)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_QUERY", "invalid product_id", nil)
		return
	}
	in := service.ServiceabilityInput{
		Pincode:       pincode,
		ProductID:     productID,
		PaymentMethod: c.DefaultQuery("payment_method", "prepaid"),
	}
	if v := c.Query("variant_id"); v != "" {
		if id, err := uuid.Parse(v); err == nil {
			in.VariantID = id
		}
	}
	if v := c.Query("seller_id"); v != "" {
		if id, err := uuid.Parse(v); err == nil {
			in.SellerID = id
		}
	}
	res, err := h.svc.CheckServiceability(c.Request.Context(), in)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "SERVICEABILITY_FAILED", err.Error(), nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, res, nil)
}

// PreviewCouponDiscount GET /v1/commerce/cart/coupon-preview?code=XYZ
// Lightweight endpoint the cart screen calls when the user types a
// coupon code. Returns the priced cart preview with the coupon
// applied so the UI can show "−₹X" without forcing the user through
// the full checkout-quote flow (which also needs address + payment
// method). Pure preview: no DB writes.
//
// commerce TODO M#1 — Coupon preview in cart.
func (h *Handler) PreviewCouponDiscount(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}
	code := strings.TrimSpace(c.Query("code"))
	q, err := h.svc.Quote(c.Request.Context(), service.QuoteInput{
		UserID:     userID,
		CouponCode: code,
	})
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "PREVIEW_FAILED", err.Error(), nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, gin.H{
		"coupon_code":     code,
		"coupon_discount": q.CouponDiscount,
		"subtotal":        q.Subtotal,
		"grand_total":     q.GrandTotal,
		"applied":         q.CouponDiscount > 0,
	}, nil)
}

// CheckoutQuote POST /v1/commerce/checkout/quote — Phase 1.1.
// Returns the same pricing the immediately-following Checkout call will
// produce, including per-line breakdown, unavailable items, and (Phase
// 1.3) serviceability + COD eligibility.
func (h *Handler) CheckoutQuote(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}
	var req quoteReq
	if err := c.ShouldBindJSON(&req); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_BODY", err.Error(), nil)
		return
	}
	q, err := h.svc.Quote(c.Request.Context(), service.QuoteInput{
		UserID:        userID,
		AddressID:     req.AddressID,
		PaymentMethod: req.PaymentMethod,
		CouponCode:    req.CouponCode,
	})
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "QUOTE_FAILED", err.Error(), nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, q, nil)
}

func (h *Handler) Checkout(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}
	var req checkoutReq
	if err := c.ShouldBindJSON(&req); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_BODY", err.Error(), nil)
		return
	}
	// H4 — accept idempotency from the standard header too; body field stays
	// for clients that can't set headers. Header wins when both are set.
	idemp := req.IdempotencyKey
	if hk := c.GetHeader("Idempotency-Key"); hk != "" {
		idemp = hk
	}
	order, err := h.svc.Checkout(c.Request.Context(), service.CheckoutInput{
		UserID:         userID,
		AddressID:      req.AddressID,
		PaymentMethod:  req.PaymentMethod,
		CouponCode:     req.CouponCode,
		GiftMessage:    req.GiftMessage,
		IdempotencyKey: idemp,
		OrganizationID: req.OrganizationID,
		PONumber:       req.PONumber,
		CostCenter:     req.CostCenter,
		InvoiceEmail:   req.InvoiceEmail,
	})
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "CHECKOUT_FAILED", err.Error(), nil)
		return
	}
	api.JSON(c.Writer, http.StatusCreated, order, nil)
}

// ListOrders GET /v1/commerce/orders — Phase 2.1.
// Returns rich order cards (item count, seller count, first item) with
// keyset pagination. next_cursor lives on the meta envelope; clients
// thread it back as ?cursor=... on the next page. The legacy ?offset=
// query param is accepted but ignored — keyset is the only path now,
// since offset over orders required a table-scanning COUNT(*) per page.
func (h *Handler) ListOrders(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "20"))
	cursor := c.Query("cursor")
	res, err := h.svc.ListOrderCards(c.Request.Context(), userID, limit, cursor)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "LIST_ORDERS_FAILED", err.Error(), nil)
		return
	}
	// `data` is an OBJECT holding the page, not a bare array.
	//
	// It was `data: [...]` with the cursor in `meta`. Android's OrderDto is
	// declared as ApiEnvelope<OrderListDto> — `data.items` plus
	// `data.next_cursor` — so an array where an object was expected does not
	// deserialise at all, and the buyer's order list could never render. The
	// envelope contract test asserts only that `data` EXISTS, which an array
	// satisfies, so nothing caught it.
	//
	// The cursor is repeated in `meta` so any caller already reading it there
	// keeps working.
	api.JSON(c.Writer, http.StatusOK, gin.H{
		"items":       res.Items,
		"next_cursor": res.NextCursor,
	}, &api.Meta{NextCursor: res.NextCursor})
}

func (h *Handler) GetOrder(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}
	orderID, ok := parseUUID(c, "orderId")
	if !ok {
		return
	}
	// The assembled screen, not the row. See service.OrderDetail for what
	// the raw row was doing to the order page (₹0.00, no lines, no address,
	// no cancel button) and why the shape now matches the list endpoint's.
	order, err := h.svc.GetOrderDetail(c.Request.Context(), orderID, userID)
	if err != nil {
		writeCommerceError(c, err)
		return
	}
	api.JSON(c.Writer, http.StatusOK, order, nil)
}

func (h *Handler) GetOrderItems(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}
	orderID, ok := parseUUID(c, "orderId")
	if !ok {
		return
	}
	order, items, err := h.svc.GetOrderWithItems(c.Request.Context(), orderID, userID)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusNotFound, "NOT_FOUND", "order not found", nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, gin.H{"order": order, "items": items}, nil)
}

type cancelOrderReq struct {
	Reason string `json:"reason"`
}

func (h *Handler) CancelOrder(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}
	orderID, ok := parseUUID(c, "orderId")
	if !ok {
		return
	}
	var req cancelOrderReq
	_ = c.ShouldBindJSON(&req)

	if err := h.svc.CancelOrder(c.Request.Context(), orderID, userID, "customer", req.Reason); err != nil {
		// The store's typed refusals keep their codes: a move the matrix
		// does not admit (a shipped order) is 409 CANCEL_NOT_PERMITTED and
		// a stranger's order is 404 ORDER_NOT_FOUND. Everything used to be
		// 400 CANCEL_FAILED with the raw error text, which told the client
		// nothing it could act on and told a stranger the order existed.
		if errors.Is(err, postgres.ErrCancelNotPermitted) || errors.Is(err, postgres.ErrNotOrderOwnerP0) ||
			errors.Is(err, postgres.ErrOrderNotFoundP0) || errors.Is(err, postgres.ErrTransitionNotPermitted) {
			writeCommerceError(c, err)
			return
		}
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "CANCEL_FAILED", err.Error(), nil)
		return
	}
	c.Status(http.StatusNoContent)
}

// confirmPaymentReq is the customer-facing Razorpay confirmation body.
// PaymentIntentID is the commerce-side payments-service intent id; the
// three razorpay_* fields are what Razorpay returns on successful checkout
// and are forwarded to payments-service for HMAC verification.
type confirmPaymentReq struct {
	PaymentIntentID   uuid.UUID `json:"payment_intent_id" binding:"required"`
	RazorpayOrderID   string    `json:"razorpay_order_id" binding:"required"`
	RazorpayPaymentID string    `json:"razorpay_payment_id" binding:"required"`
	RazorpaySignature string    `json:"razorpay_signature" binding:"required"`
	AmountMinor       int64     `json:"amount_minor,omitempty"`
	Gateway           string    `json:"gateway,omitempty"`
}

func (h *Handler) ConfirmPayment(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}
	orderID, ok := parseUUID(c, "orderId")
	if !ok {
		return
	}
	var req confirmPaymentReq
	if err := c.ShouldBindJSON(&req); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_BODY", err.Error(), nil)
		return
	}
	err := h.svc.ConfirmPayment(c.Request.Context(), orderID, userID, service.ConfirmPaymentInput{
		PaymentIntentID:   req.PaymentIntentID,
		RazorpayOrderID:   req.RazorpayOrderID,
		RazorpayPaymentID: req.RazorpayPaymentID,
		RazorpaySignature: req.RazorpaySignature,
		AmountMinor:       req.AmountMinor,
		Gateway:           req.Gateway,
	})
	if err != nil {
		// Map domain errors to specific HTTP codes — the old generic
		// handleErr swallowed wrong-user / wrong-state into 500, which
		// hid bugs and made misuse hard to debug.
		switch {
		case errors.Is(err, service.ErrOrderNotFound):
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusNotFound, "ORDER_NOT_FOUND", err.Error(), nil)
		case errors.Is(err, service.ErrNotOrderOwner):
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusForbidden, "FORBIDDEN", err.Error(), nil)
		case errors.Is(err, service.ErrOrderNotPaymentPending),
			errors.Is(err, service.ErrOrderNotPayable):
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusConflict, "ORDER_NOT_PAYMENT_PENDING", err.Error(), nil)
		case errors.Is(err, service.ErrPaymentVerifyFailed),
			errors.Is(err, service.ErrPaymentAmountMismatch):
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "PAYMENT_VERIFY_FAILED", err.Error(), nil)
		case errors.Is(err, service.ErrStubGatewayInProd):
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "STUB_GATEWAY_NOT_ALLOWED", err.Error(), nil)
		default:
			handleErr(c, err)
		}
		return
	}
	c.Status(http.StatusNoContent)
}
