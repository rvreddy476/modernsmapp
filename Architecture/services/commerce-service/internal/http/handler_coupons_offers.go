package http

// MStore coupons and bank offers — the HTTP surface.
//
//	GET   /v1/commerce/payment-offers?amount_minor=   PUBLIC  bank offers for an amount
//	GET   /v1/commerce/cart/coupons                   AUTH    what the public coupons do to my bag
//	GET   /v1/commerce/seller/coupons                 SELLER  my shop's coupons (approved shop)
//	POST  /v1/commerce/seller/coupons                 SELLER  create one
//	PATCH /v1/commerce/seller/coupons/:couponId       SELLER  edit / deactivate (is_active=false)
//
//	GET   /v1/commerce/internal/admin/coupons?funded_by=seller|platform&limit=&offset=
//	POST  /v1/commerce/internal/admin/coupons                   commerce:coupons.manage
//	PATCH /v1/commerce/internal/admin/coupons/:couponId         (admin-service token; step-up
//	                                                            and its audit row are admin-service's)
//
// There is no DELETE: a coupon is deactivated. PATCH reads every key in three
// states — absent is no change, a value sets, an explicit null clears
// (description, max_discount_minor, max_uses, expires_at; min_order_minor →
// 0); a null starts_at is refused. Code, discount type and value, scope and seller are
// fixed at create and a PATCH naming any of them is refused.
//
// Coupon refusals at quote and checkout are 422 with a code per cause; see
// writeCouponRefusal.

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/atpost/commerce-service/internal/store/postgres"
	"github.com/atpost/shared/api"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// PermCouponsManage is the admin permission for the coupon family
// (identity's catalogue spells it the same; 5afc3dd0).
const PermCouponsManage = "commerce:coupons.manage"

// RegisterCouponOfferRoutes mounts the buyer and seller routes.
func (h *Handler) RegisterCouponOfferRoutes(v1 *gin.RouterGroup) {
	v1.GET("/payment-offers", h.ListPaymentOffers)
	v1.GET("/cart/coupons", h.ListCartCoupons)
	v1.GET("/seller/coupons", h.ListSellerCoupons)
	v1.POST("/seller/coupons", h.CreateSellerCoupon)
	v1.PATCH("/seller/coupons/:couponId", h.PatchSellerCoupon)
}

// registerAdminCouponRoutes mounts the admin console's coupon family on the
// token-only admin group.
func (h *Handler) registerAdminCouponRoutes(g *gin.RouterGroup) {
	gate := h.requireAdminToken
	g.GET("/coupons", gate(PermCouponsManage), h.AdminListCoupons)
	g.POST("/coupons", gate(PermCouponsManage), h.AdminCreateCoupon)
	g.PATCH("/coupons/:couponId", gate(PermCouponsManage), h.AdminPatchCoupon)
}

// ─── Bank offers ────────────────────────────────────────────────────────

// ListPaymentOffers GET /v1/commerce/payment-offers?amount_minor=
//
// Public. The product page passes the product's price, checkout the quote's
// total. Each offer carries estimated_discount_minor — payments-service's
// own cap formula applied to amount_minor — for "Save up to ₹X". Display
// only: payments decides what a capture is worth.
func (h *Handler) ListPaymentOffers(c *gin.Context) {
	ctx := c.Request.Context()
	amount, err := strconv.ParseInt(strings.TrimSpace(c.Query("amount_minor")), 10, 64)
	if err != nil || amount <= 0 {
		api.ErrorWithContext(ctx, c.Writer, http.StatusBadRequest, "INVALID_AMOUNT",
			"amount_minor is required and must be a positive integer (paise)", nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, h.svc.PaymentOffers(ctx, amount), nil)
}

// ─── The bag ────────────────────────────────────────────────────────────

// ListCartCoupons GET /v1/commerce/cart/coupons
func (h *Handler) ListCartCoupons(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}
	res, err := h.svc.CartCoupons(c.Request.Context(), userID)
	if err != nil {
		writeCommerceError(c, err)
		return
	}
	api.JSON(c.Writer, http.StatusOK, res, nil)
}

// ─── Bodies ─────────────────────────────────────────────────────────────

// couponCreateBody is POST /seller/coupons and POST /internal/admin/coupons.
// discount_value is basis points for a percentage coupon (1000 = 10%) and
// paise for a flat one. reason / note are the admin console's audit reason.
type couponCreateBody struct {
	Code             string     `json:"code"`
	Description      *string    `json:"description"`
	DiscountType     string     `json:"discount_type"`
	DiscountValue    *int64     `json:"discount_value"`
	MaxDiscountMinor *int64     `json:"max_discount_minor"`
	MinOrderMinor    *int64     `json:"min_order_minor"`
	MaxUses          *int       `json:"max_uses"`
	MaxUsesPerUser   *int       `json:"max_uses_per_user"`
	ApplicableTo     string     `json:"applicable_to"`
	ApplicableIDs    []string   `json:"applicable_ids"`
	StartsAt         *time.Time `json:"starts_at"`
	ExpiresAt        *time.Time `json:"expires_at"`
	IsPublic         *bool      `json:"is_public"`
	IsActive         *bool      `json:"is_active"`
	Reason           *string    `json:"reason"`
	Note             *string    `json:"note"`
}

// couponPatchBody is PATCH …/coupons/:couponId. The json.RawMessage fields
// are the immutable ones: present at all is a refusal.
type couponPatchBody struct {
	Description      postgres.Opt[string]    `json:"description"`
	MaxDiscountMinor postgres.Opt[int64]     `json:"max_discount_minor"`
	MinOrderMinor    postgres.Opt[int64]     `json:"min_order_minor"`
	MaxUses          postgres.Opt[int]       `json:"max_uses"`
	MaxUsesPerUser   postgres.Opt[int]       `json:"max_uses_per_user"`
	StartsAt         postgres.Opt[time.Time] `json:"starts_at"`
	ExpiresAt        postgres.Opt[time.Time] `json:"expires_at"`
	IsActive         postgres.Opt[bool]      `json:"is_active"`
	IsPublic         postgres.Opt[bool]      `json:"is_public"`

	Code          json.RawMessage `json:"code"`
	DiscountType  json.RawMessage `json:"discount_type"`
	DiscountValue json.RawMessage `json:"discount_value"`
	ApplicableTo  json.RawMessage `json:"applicable_to"`
	ApplicableIDs json.RawMessage `json:"applicable_ids"`
	SellerID      json.RawMessage `json:"seller_id"`
	FundedBy      json.RawMessage `json:"funded_by"`

	Reason *string `json:"reason"`
	Note   *string `json:"note"`
}

func decodeStrict(c *gin.Context, out any) bool {
	dec := json.NewDecoder(c.Request.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(out); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_BODY",
			"body must be a JSON coupon: "+err.Error(), nil)
		return false
	}
	return true
}

func (b couponCreateBody) toCreate(c *gin.Context) (postgres.CouponCreate, bool) {
	in := postgres.CouponCreate{
		Code:             b.Code,
		DiscountType:     strings.TrimSpace(b.DiscountType),
		MaxDiscountMinor: b.MaxDiscountMinor,
		MaxUses:          b.MaxUses,
		ApplicableTo:     strings.TrimSpace(b.ApplicableTo),
		StartsAt:         b.StartsAt,
		ExpiresAt:        b.ExpiresAt,
		MaxUsesPerUser:   1,
		IsPublic:         true,
		IsActive:         true,
		Reason:           reasonOf(b.Reason, b.Note),
	}
	if b.DiscountValue == nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_BODY",
			"discount_value is required", nil)
		return in, false
	}
	in.DiscountValue = *b.DiscountValue
	if b.Description != nil {
		in.Description = *b.Description
	}
	if b.MinOrderMinor != nil {
		in.MinOrderMinor = *b.MinOrderMinor
	}
	if b.MaxUsesPerUser != nil {
		in.MaxUsesPerUser = *b.MaxUsesPerUser
	}
	if b.IsPublic != nil {
		in.IsPublic = *b.IsPublic
	}
	if b.IsActive != nil {
		in.IsActive = *b.IsActive
	}
	if in.ApplicableTo == "" {
		in.ApplicableTo = "all"
	}
	for _, raw := range b.ApplicableIDs {
		id, err := uuid.Parse(strings.TrimSpace(raw))
		if err != nil {
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_BODY",
				"applicable_ids must be ids", nil)
			return in, false
		}
		in.ApplicableIDs = append(in.ApplicableIDs, id)
	}
	return in, true
}

func (b couponPatchBody) toPatch(c *gin.Context) (postgres.CouponPatch, bool) {
	for _, imm := range []json.RawMessage{b.Code, b.DiscountType, b.DiscountValue, b.ApplicableTo,
		b.ApplicableIDs, b.SellerID, b.FundedBy} {
		if imm != nil {
			writeCommerceError(c, postgres.ErrCouponImmutable)
			return postgres.CouponPatch{}, false
		}
	}
	return postgres.CouponPatch{
		Description: b.Description, MaxDiscountMinor: b.MaxDiscountMinor, MinOrderMinor: b.MinOrderMinor,
		MaxUses: b.MaxUses, MaxUsesPerUser: b.MaxUsesPerUser, StartsAt: b.StartsAt, ExpiresAt: b.ExpiresAt,
		IsActive: b.IsActive, IsPublic: b.IsPublic,
	}, true
}

func reasonOf(reason, note *string) *string {
	for _, r := range []*string{reason, note} {
		if r != nil && strings.TrimSpace(*r) != "" {
			v := strings.TrimSpace(*r)
			return &v
		}
	}
	return nil
}

// ─── Seller ─────────────────────────────────────────────────────────────

// ListSellerCoupons GET /v1/commerce/seller/coupons
func (h *Handler) ListSellerCoupons(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}
	rows, err := h.svc.SellerCoupons(c.Request.Context(), userID)
	if err != nil {
		writeCommerceError(c, err)
		return
	}
	api.JSON(c.Writer, http.StatusOK, rows, nil)
}

// CreateSellerCoupon POST /v1/commerce/seller/coupons
func (h *Handler) CreateSellerCoupon(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}
	var body couponCreateBody
	if !decodeStrict(c, &body) {
		return
	}
	in, ok := body.toCreate(c)
	if !ok {
		return
	}
	rec, err := h.svc.CreateSellerCoupon(c.Request.Context(), userID, in)
	if err != nil {
		writeCommerceError(c, err)
		return
	}
	api.JSON(c.Writer, http.StatusCreated, rec, nil)
}

// PatchSellerCoupon PATCH /v1/commerce/seller/coupons/:couponId
func (h *Handler) PatchSellerCoupon(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}
	id, ok := parseUUID(c, "couponId")
	if !ok {
		return
	}
	var body couponPatchBody
	if !decodeStrict(c, &body) {
		return
	}
	p, ok := body.toPatch(c)
	if !ok {
		return
	}
	rec, err := h.svc.PatchSellerCoupon(c.Request.Context(), userID, id, p)
	if err != nil {
		writeCommerceError(c, err)
		return
	}
	api.JSON(c.Writer, http.StatusOK, rec, nil)
}

// ─── Admin console ──────────────────────────────────────────────────────

// adminCouponListMeta is the list's meta, which the console reads the
// platform switch from (banner while it is off).
type adminCouponListMeta struct {
	PlatformCouponsEnabled bool `json:"platform_coupons_enabled"`
	Total                  int  `json:"total"`
	Limit                  int  `json:"limit"`
	Offset                 int  `json:"offset"`
}

// AdminListCoupons GET /v1/commerce/internal/admin/coupons?funded_by=&limit=&offset=
//
//	{"data":[…coupon rows…],"meta":{"platform_coupons_enabled":false,"total":N,"limit":L,"offset":O}}
func (h *Handler) AdminListCoupons(c *gin.Context) {
	ctx := c.Request.Context()
	fundedBy := strings.TrimSpace(c.Query("funded_by"))
	if fundedBy != "" && fundedBy != "seller" && fundedBy != "platform" {
		api.ErrorWithContext(ctx, c.Writer, http.StatusBadRequest, "INVALID_FILTER",
			"funded_by must be seller or platform", nil)
		return
	}
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "100"))
	offset, _ := strconv.Atoi(c.DefaultQuery("offset", "0"))
	if limit <= 0 || limit > 200 {
		limit = 100
	}
	if offset < 0 {
		offset = 0
	}
	rows, total, err := h.svc.AdminCoupons(ctx, postgres.CouponListFilter{FundedBy: fundedBy, Limit: limit, Offset: offset})
	if err != nil {
		writeCommerceError(c, err)
		return
	}
	c.Header("Content-Type", "application/json")
	c.Status(http.StatusOK)
	_ = json.NewEncoder(c.Writer).Encode(struct {
		Data []*postgres.CouponRecord `json:"data"`
		Meta adminCouponListMeta      `json:"meta"`
	}{rows, adminCouponListMeta{
		PlatformCouponsEnabled: h.svc.PlatformCouponsEnabled(),
		Total:                  total, Limit: limit, Offset: offset,
	}})
}

// AdminCreateCoupon POST /v1/commerce/internal/admin/coupons — a PLATFORM
// coupon. Allowed while the switch is off; buyers cannot apply it until on.
func (h *Handler) AdminCreateCoupon(c *gin.Context) {
	actor, ok := tokenActor(c)
	if !ok {
		writeCommerceError(c, postgres.ErrActorRequired)
		return
	}
	var body couponCreateBody
	if !decodeStrict(c, &body) {
		return
	}
	in, ok := body.toCreate(c)
	if !ok {
		return
	}
	rec, err := h.svc.CreatePlatformCoupon(c.Request.Context(), actor, in)
	if err != nil {
		writeCommerceError(c, err)
		return
	}
	api.JSON(c.Writer, http.StatusCreated, rec, nil)
}

// AdminPatchCoupon PATCH /v1/commerce/internal/admin/coupons/:couponId —
// platform coupons only; a seller's coupon is read-only to the console.
func (h *Handler) AdminPatchCoupon(c *gin.Context) {
	actor, ok := tokenActor(c)
	if !ok {
		writeCommerceError(c, postgres.ErrActorRequired)
		return
	}
	id, ok := parseUUID(c, "couponId")
	if !ok {
		return
	}
	var body couponPatchBody
	if !decodeStrict(c, &body) {
		return
	}
	p, ok := body.toPatch(c)
	if !ok {
		return
	}
	rec, err := h.svc.PatchPlatformCoupon(c.Request.Context(), actor, id, p, reasonOf(body.Reason, body.Note))
	if err != nil {
		writeCommerceError(c, err)
		return
	}
	api.JSON(c.Writer, http.StatusOK, rec, nil)
}

// ─── Errors ─────────────────────────────────────────────────────────────

// writeCouponError answers the coupon refusals and the coupon-write errors.
// false when err is neither, so writeCommerceError carries on.
//
// Refusals at quote and checkout are 422 (the contract's codes):
//
//	COUPON_INVALID         unknown, inactive, not started yet (details.starts_at),
//	                       or a type the launch loop does not price
//	COUPON_EXPIRED         past expires_at
//	COUPON_MIN_ORDER       details.min_order_minor
//	COUPON_USED_UP         total cap or this buyer's cap reached
//	COUPON_NOT_APPLICABLE  not for these items (or another seller's coupon)
//	COUPON_NOT_AVAILABLE   a platform coupon while COMMERCE_PLATFORM_COUPONS_ENABLED is off
func writeCouponError(c *gin.Context, err error) bool {
	ctx := c.Request.Context()
	w := c.Writer
	if code, ok := postgres.CouponRefusalCode(err); ok {
		var details any
		var minErr *postgres.CouponMinOrderError
		var startErr *postgres.CouponNotStartedError
		msg := couponRefusalMessage(code)
		switch {
		case errors.As(err, &minErr):
			details = gin.H{"min_order_minor": minErr.MinOrderMinor}
		case errors.As(err, &startErr):
			details = gin.H{"starts_at": startErr.StartsAt.UTC().Format(time.RFC3339)}
			msg = "that coupon is not active yet"
		}
		api.ErrorWithContext(ctx, w, http.StatusUnprocessableEntity, code, msg, details)
		return true
	}
	switch {
	case errors.Is(err, postgres.ErrInvalidCoupon):
		api.ErrorWithContext(ctx, w, http.StatusBadRequest, "INVALID_BODY",
			strings.TrimPrefix(err.Error(), postgres.ErrInvalidCoupon.Error()+": "), nil)
	case errors.Is(err, postgres.ErrCouponNotFound):
		api.ErrorWithContext(ctx, w, http.StatusNotFound, "COUPON_NOT_FOUND", "coupon not found", nil)
	case errors.Is(err, postgres.ErrCouponCodeTaken):
		api.ErrorWithContext(ctx, w, http.StatusConflict, "COUPON_CODE_TAKEN", "that coupon code already exists", nil)
	case errors.Is(err, postgres.ErrCouponProductNotOwned):
		api.ErrorWithContext(ctx, w, http.StatusUnprocessableEntity, "COUPON_PRODUCT_NOT_OWNED",
			"a coupon may only name your own products", nil)
	case errors.Is(err, postgres.ErrCouponImmutable):
		api.ErrorWithContext(ctx, w, http.StatusUnprocessableEntity, "COUPON_IMMUTABLE", err.Error(), nil)
	case errors.Is(err, postgres.ErrCouponSellerReadOnly):
		api.ErrorWithContext(ctx, w, http.StatusForbidden, "SELLER_COUPON_READ_ONLY",
			"a seller's coupon is managed by the seller; the console lists it read-only", nil)
	default:
		return false
	}
	return true
}

func couponRefusalMessage(code string) string {
	switch code {
	case "COUPON_EXPIRED":
		return "that coupon has expired"
	case "COUPON_MIN_ORDER":
		return "your order is below that coupon's minimum"
	case "COUPON_USED_UP":
		return "that coupon has been used up"
	case "COUPON_NOT_APPLICABLE":
		return "that coupon does not apply to the items in your bag"
	case "COUPON_NOT_AVAILABLE":
		return "that coupon is not available"
	}
	return "that coupon code is not valid"
}
