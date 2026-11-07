// Package http provides Gin HTTP handlers for commerce-service.
package http

import (
	"net/http"

	"github.com/atpost/commerce-service/internal/media"
	"github.com/atpost/commerce-service/internal/service"
	"github.com/atpost/shared/api"
	"github.com/atpost/shared/servicetoken"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// Handler wires all commerce HTTP routes.
type Handler struct {
	svc         *service.Service
	internalKey string
	// allowStubSettlement registers POST /orders/:orderId/payment/confirm.
	// Set ONLY from PAYMENTS_ALLOW_STUB. See StubSettlementPattern.
	allowStubSettlement bool
	// verifier admits admin-service tokens on InternalAdminPrefix. nil
	// refuses every token (see admin_token.go).
	verifier *servicetoken.Verifier
	// limits throttles the engagement writes (handler_engagement.go). nil
	// admits everything, which only a bare &Handler{} in a route-table test
	// ever is.
	limits *engagementLimits
}

func New(svc *service.Service) *Handler {
	return &Handler{svc: svc, limits: newEngagementLimits()}
}

func (h *Handler) WithInternalKey(key string) *Handler {
	h.internalKey = key
	return h
}

// WithStubSettlement registers the confirm route so a stub-gateway order can
// reach `paid` on a dev stack, where no PSP exists to send a webhook.
//
// It must be wired from the same PAYMENTS_ALLOW_STUB the service's
// WithAllowStubGateway reads, and the fence middleware must be built with
// the same value — the route is otherwise 404'd ahead of routing. A
// deployment holding real Razorpay credentials leaves it false, and the
// signature-verified webhook stays the only settlement path.
func (h *Handler) WithStubSettlement(allow bool) *Handler {
	h.allowStubSettlement = allow
	return h
}

func (h *Handler) RegisterRoutes(r *gin.Engine) {
	h.registerDeliveryRoutes(r)
	h.RegisterOnboardingRoutes(r)
	// The submit gate's reads: the seller's pre-submit checklist and
	// "action needed" list, and the reviewer's submission history and the
	// founder's compliance-gap queue. See handler_submissions.go.
	h.RegisterSubmissionRoutes(r)
	// Admin console: the token-only mirror of the admin routes for
	// admin-service (admin_token.go). The key routes above are unchanged.
	h.registerAdminTokenRoutes(r)

	v1 := r.Group("/v1/commerce")

	// Record WHO the request is for, so the media resolver can tell
	// media-service. Without it every batch reached media-service as the
	// anonymous all-zeroes viewer, its delivery gate refused every protected
	// asset, and the whole catalogue rendered as grey boxes — see
	// internal/media/viewer.go.
	//
	// It authorises nothing. Every commerce permission check still reads
	// X-User-Id at its own handler, exactly as before; this only decides
	// which images media-service is willing to hand back.
	v1.Use(recordMediaViewer)

	// ── Affiliate redirect (public; viewer click target) ─────
	// Lands the viewer on the canonical product page with the
	// affiliate code in the query string. See
	// internal/service/affiliate_redirect.go for the resolver.
	v1.GET("/affiliate/:linkId", h.AffiliateRedirect)

	// ── Catalog ──────────────────────────────────────────────
	v1.GET("/categories", h.ListCategories)
	// The form a category asks for: which attributes, in which groups, with
	// which options and units. Public, like /tax-classes — these are the
	// questions a listing must answer, not seller data.
	v1.GET("/categories/:categoryId/attribute-schema", h.GetCategoryAttributeSchema)
	v1.GET("/products", h.ListProducts)
	v1.GET("/products/:productId", h.GetProduct)
	v1.GET("/products/:productId/preview", h.GetProductPreview)
	v1.POST("/products", h.CreateProduct)
	// The update route, which did not exist. See handler_products.go: the
	// store method that would have served it took a map whose keys became
	// column names, so the body is now decoded against an explicit allowlist
	// and anything outside it is refused by name.
	v1.PATCH("/products/:productId", h.UpdateProduct)
	v1.GET("/products/:productId/media", h.ListProductMedia)
	v1.POST("/products/:productId/media", h.AddProductMedia)
	v1.GET("/products/:productId/attributes", h.GetProductAttributes)
	v1.PUT("/products/:productId/attributes", h.SetProductAttributes)
	// Try-on is published by the product's own seller; the read rides the
	// product detail body rather than a route of its own.
	v1.PUT("/products/:productId/try-on", h.SetProductTryOn)
	v1.DELETE("/products/:productId/try-on", h.ClearProductTryOn)
	// Variant CRUD for existing products (commerce TODO H#5). The
	// initial variant set is created with the product; this lets a
	// seller add/edit/archive variants after launch.
	v1.GET("/products/:productId/variants", h.ListProductVariants)
	v1.POST("/products/:productId/variants", h.AddProductVariant)
	v1.PATCH("/variants/:variantId", h.UpdateProductVariant)
	v1.DELETE("/variants/:variantId", h.ArchiveProductVariant)
	// Phase F2.1 — variant tier pricing.
	v1.GET("/variants/:variantId/price-tiers", h.GetPriceTiers)
	v1.PUT("/variants/:variantId/price-tiers", h.SetPriceTiers)
	v1.GET("/products/:productId/reviews", h.GetProductReviews)
	v1.POST("/products/:productId/reviews", h.CreateReview)

	// ── Seller ───────────────────────────────────────────────
	v1.POST("/sellers/onboard", h.OnboardSeller)
	v1.GET("/sellers/me", h.GetMySellerProfile)
	v1.GET("/sellers/:sellerId/products", h.ListSellerProducts)
	v1.GET("/seller/orders", h.ListMySellerOrders)
	v1.GET("/seller/orders/:orderId", h.GetSellerOrderDetail)
	v1.GET("/seller/fulfillment", h.ListSellerFulfillment)
	// The seller's fulfilment writes. The D6 matrix admitted these moves
	// for a seller since migration 010; no route performed them. See
	// handler_seller_fulfilment.go.
	v1.GET("/seller/orders/:orderId/history", h.SellerOrderHistory)
	v1.POST("/seller/orders/:orderId/pack", h.SellerPackOrder)
	v1.POST("/seller/orders/:orderId/cancel", h.SellerCancelOrder)
	v1.POST("/seller/orders/:orderId/ship", h.SellerShipOrder)

	// ── Cart ─────────────────────────────────────────────────
	v1.GET("/cart", h.GetCart)
	v1.GET("/cart/coupon-preview", h.PreviewCouponDiscount)
	v1.POST("/cart/items", h.AddToCart)
	v1.PATCH("/cart/items/by-variant/:variantId", h.UpdateCartItem)
	v1.DELETE("/cart/items/:variantId", h.RemoveFromCart)

	// ── Orders ───────────────────────────────────────────────
	//
	// B5 — three registrations REMOVED here. They are listed in
	// FencedRoutes (handler_p0.go) with the money defect each one carried,
	// and FenceMiddleware answers 404 for them ahead of routing, so this
	// deletion is enforced rather than merely performed:
	//
	//	POST /checkout/quote                  — legacy float pricing, AND a
	//	                                        duplicate of the P0
	//	                                        registration, which panics
	//	                                        gin at startup (B10)
	//	POST /orders/checkout                 — order created before its
	//	                                        reservations, failures logged
	//	                                        and swallowed: a payable order
	//	                                        holding only some of its stock
	//	POST /orders/:orderId/payment/confirm — a client asserting a payment
	//	                                        fact, the authority LB-3
	//	                                        removed from payments-service
	//
	// The replacements are POST /v1/commerce/checkout/quote (P0),
	// POST /v1/commerce/v2/orders/checkout, and the provider webhook.
	v1.GET("/serviceability", h.CheckServiceability)
	v1.GET("/orders", h.ListOrders)
	v1.GET("/orders/:orderId", h.GetOrder)
	v1.GET("/orders/:orderId/items", h.GetOrderItems)
	v1.POST("/orders/:orderId/cancel", h.CancelOrder)

	// The dev-stack settlement path, and nothing else.
	//
	// Registered only when PAYMENTS_ALLOW_STUB is set, because in stub mode
	// payments-service has no provider adapter and POST /v1/payments/webhook
	// answers 503 — so without this a prepaid order can never leave
	// payment_pending on dev, and refunds, shipments and delivery have
	// nothing to test against. The handler still refuses any gateway but
	// "stub", and the transition it performs is the same guarded UPDATE the
	// webhook consumer uses. StubSettlementPattern documents the whole rule;
	// FenceMiddlewareWithStubSettlement must be built with the same flag or
	// this registration is unreachable by design.
	if h.allowStubSettlement {
		v1.POST("/orders/:orderId/payment/confirm", h.ConfirmPayment)
	}

	// ── Returns ──────────────────────────────────────────────
	v1.POST("/orders/:orderId/returns", h.CreateReturn)
	v1.POST("/returns/:returnId/approve", h.ApproveReturn)
	v1.POST("/returns/:returnId/reject", h.RejectReturn)
	v1.GET("/returns/:returnId", h.GetReturn)
	v1.GET("/returns/:returnId/refund-preview", h.PreviewReturnRefund)
	v1.GET("/seller/returns", h.ListSellerReturnsInbox)
	v1.POST("/reviews/:reviewId/seller-response", h.SellerRespondToReview)

	// ── Addresses ────────────────────────────────────────────
	v1.GET("/addresses", h.ListAddresses)
	v1.POST("/addresses", h.AddAddress)
	v1.PATCH("/addresses/:addressId", h.UpdateAddress)
	v1.DELETE("/addresses/:addressId", h.DeleteAddress)
	v1.POST("/addresses/:addressId/default", h.SetDefaultAddress)

	// ── My returns ───────────────────────────────────────────
	v1.GET("/me/returns", h.ListMyReturns)

	// ── Payout preview ───────────────────────────────────────
	v1.GET("/payout/preview", h.PayoutPreview)

	// ── COD remittances (seller-facing) ──────────────────────
	v1.GET("/seller/cod-remittances", h.ListMyCODRemittances)
	v1.GET("/seller/earnings", h.ListSellerEarnings)
	v1.GET("/seller/earnings.csv", h.ExportSellerEarningsCSV)

	// ── Shipments + Invoices ─────────────────────────────────
	h.RegisterShipmentRoutes(v1)

	// ── Phase 5 — B2B / Organizations ─────────────────────────
	h.RegisterOrganizationRoutes(v1)

	// ── Phase F2.2 — RFQ (Request For Quote) ──────────────────
	h.RegisterRFQRoutes(v1)

	// ── Phase F2.3 — bulk SKU upload ──────────────────────────
	h.RegisterBulkImportRoutes(v1)

	// ── The shop front: /home, gallery writes, favourites, banners ──
	h.RegisterStorefrontRoutes(r, v1)

	// ── Engagement: delivery date, like/dislike, share, helpful votes ──
	h.RegisterEngagementRoutes(v1)

	// ── Coupons and bank offers (migration 038) ──
	h.RegisterCouponOfferRoutes(v1)
}

// ─── helpers ─────────────────────────────────────────────────────

// recordMediaViewer puts the caller's id on the request context for the media
// resolver. An unparseable or absent header records the anonymous viewer,
// which is a real audience for a product photograph and must NOT be an error
// here — refusing would 401 the public catalogue.
func recordMediaViewer(c *gin.Context) {
	viewer, err := uuid.Parse(c.GetHeader("X-User-Id"))
	if err != nil {
		viewer = uuid.Nil
	}
	c.Request = c.Request.WithContext(media.WithViewer(c.Request.Context(), viewer))
	c.Next()
}

func getUserID(c *gin.Context) (uuid.UUID, bool) {
	raw := c.GetHeader("X-User-Id")
	id, err := uuid.Parse(raw)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "missing or invalid X-User-Id header", nil)
		return uuid.Nil, false
	}
	return id, true
}

func parseUUID(c *gin.Context, param string) (uuid.UUID, bool) {
	id, err := uuid.Parse(c.Param(param))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_PARAM", "invalid "+param, nil)
		return uuid.Nil, false
	}
	return id, true
}

// handleErr is the legacy error path, kept because dozens of handlers call it.
//
// It used to answer 500 unconditionally, with `err.Error()` echoed into the
// response body. Both halves were wrong. Every typed refusal in the service —
// a media id belonging to someone else, a fenced surface, a retryable
// transport failure — arrived at the client as an internal error, and the raw
// message carried identifiers and SQL text out to whoever asked.
//
// It now delegates to writeCommerceError, which has the typed mapping and does
// not echo unexpected messages. The default arm is still 500, so a handler
// raising a genuinely unrecognised error behaves as before.
func handleErr(c *gin.Context, err error) {
	writeCommerceError(c, err)
}

// productReadable gates every single-product read a shopper can reach
// (detail, gallery, specifications, variants, reviews, preview): a product
// that is not live by the storefront's rule — including any product of a
// seller who is not approved — answers 404 PRODUCT_NOT_FOUND, exactly as an
// id that does not exist. The seller who owns it still reads it, because the
// listing editor uses these same routes. See service/buyervisibility.go.
func (h *Handler) productReadable(c *gin.Context, productID uuid.UUID) bool {
	if err := h.svc.RequireProductReadable(c.Request.Context(), productID, optionalUserID(c)); err != nil {
		handleErr(c, err)
		return false
	}
	return true
}

// rupeeMirrorPtr is the NUMERIC mirror of an optional paise amount, for the
// analytics readers still scanning the rupee columns. Nil stays nil.
func rupeeMirrorPtr(minor *int64) *float64 {
	if minor == nil {
		return nil
	}
	r := float64(*minor) / 100.0 // money-exempt: NUMERIC mirror of cost_price_minor
	return &r
}
