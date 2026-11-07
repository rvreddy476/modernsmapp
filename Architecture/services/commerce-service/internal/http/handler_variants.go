// Variant route handlers: list, add, update and archive product variants,
// the revalidation acknowledgement, and variant tier pricing. Moved out of
// handler.go unchanged.

package http

import (
	"errors"
	"math"
	"net/http"

	"github.com/atpost/commerce-service/internal/service"
	"github.com/atpost/commerce-service/internal/store/postgres"
	"github.com/atpost/shared/api"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// ─── Variant CRUD handlers (commerce TODO H#5) ───────────────

// ListProductVariants — GET /v1/commerce/products/:productId/variants
// Public read; returns every variant (including archived) ordered by
// created_at. Sellers use this to manage their variants; customers see
// only active ones via the product detail endpoint.
func (h *Handler) ListProductVariants(c *gin.Context) {
	productID, err := uuid.Parse(c.Param("productId"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_PRODUCT_ID", err.Error(), nil)
		return
	}
	if !h.productReadable(c, productID) {
		return
	}
	variants, err := h.svc.ListProductVariants(c.Request.Context(), productID)
	if err != nil {
		handleErr(c, err)
		return
	}
	if variants == nil {
		variants = []*postgres.ProductVariant{}
	}
	api.JSON(c.Writer, http.StatusOK, gin.H{"items": variants}, nil)
}

// addVariantReq accepts money in PAISE, with the rupee floats as fallback.
//
// This was the ONE money-entry route still taking rupees only. POST
// /products and PATCH /variants/:id both accept `*_minor` and prefer it; a
// seller adding a variant to an existing listing had to send a float, and an
// app that speaks paise everywhere else had to convert on the way out and
// hope the server's rounding matched its own. `mrp`/`selling_price` keep
// working — they are simply no longer the only shape.
//
// Neither pair is `binding:"required"` any more: requiring the float made it
// impossible to send only paise. The service refuses a variant with no
// positive price, which is the check that actually matters.
type addVariantReq struct {
	SKU          string  `json:"sku" binding:"required"`
	Barcode      *string `json:"barcode"`
	Option1Name  *string `json:"option_1_name"`
	Option1Value *string `json:"option_1_value"`
	Option2Name  *string `json:"option_2_name"`
	Option2Value *string `json:"option_2_value"`
	Option3Name  *string `json:"option_3_name"`
	Option3Value *string `json:"option_3_value"`

	MRPMinor          *int64 `json:"mrp_minor"`
	SellingPriceMinor *int64 `json:"selling_price_minor"`
	CostPriceMinor    *int64 `json:"cost_price_minor"`

	// money-exempt: the legacy rupee shape, read only when the matching
	// *_minor field is absent.
	MRP float64 `json:"mrp"`
	// money-exempt: legacy rupee shape, superseded by selling_price_minor.
	SellingPrice float64 `json:"selling_price"`
	// money-exempt: legacy rupee shape, superseded by cost_price_minor.
	CostPrice    *float64 `json:"cost_price"`
	CurrencyCode string   `json:"currency_code"`
	WeightGrams  *int     `json:"weight_grams"`

	// StockQty is the opening stock, the same key POST /products takes per
	// variant. It was not on this body at all, so a variant added after
	// create always started at zero whatever the seller typed.
	StockQty int `json:"stock_qty"`
}

// AddProductVariant — POST /v1/commerce/products/:productId/variants
// Seller adds a new variant to an existing product, with its opening stock.
// A duplicate SKU is 409 SKU_TAKEN, as on create.
func (h *Handler) AddProductVariant(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}
	productID, err := uuid.Parse(c.Param("productId"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_PRODUCT_ID", err.Error(), nil)
		return
	}
	var req addVariantReq
	if err := c.BindJSON(&req); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_BODY", err.Error(), nil)
		return
	}
	// Paise are resolved once, here at the boundary, exactly as POST
	// /products does it — so the same price sent either way stores the same
	// integer. The rupee mirrors are derived FROM the paise rather than
	// copied from the request, which is what keeps the two columns from
	// disagreeing when a caller sends both.
	mrpMinor := minorOrRupees(req.MRPMinor, req.MRP)
	sellMinor := minorOrRupees(req.SellingPriceMinor, req.SellingPrice)
	costMinorPtr := addVariantCostMinor(req)
	v := &postgres.ProductVariant{
		SKU:                 req.SKU,
		Barcode:             req.Barcode,
		Option1Name:         req.Option1Name,
		Option1Value:        req.Option1Value,
		Option2Name:         req.Option2Name,
		Option2Value:        req.Option2Value,
		Option3Name:         req.Option3Name,
		Option3Value:        req.Option3Value,
		MRP:                 float64(mrpMinor) / 100.0,  // money-exempt: NUMERIC mirror of mrp_minor
		SellingPrice:        float64(sellMinor) / 100.0, // money-exempt: NUMERIC mirror of selling_price_minor
		CostPrice:           rupeeMirrorPtr(costMinorPtr),
		MRPMinorIn:          &mrpMinor,
		SellingPriceMinorIn: &sellMinor,
		CostPriceMinorIn:    costMinorPtr,
		CurrencyCode:        req.CurrencyCode,
		WeightGrams:         req.WeightGrams,
	}
	out, err := h.svc.AddProductVariant(c.Request.Context(), userID, productID, v, req.StockQty)
	if err != nil {
		writeProductWriteError(c, err)
		return
	}
	api.JSON(c.Writer, http.StatusCreated, out, nil)
}

// UpdateProductVariant — PATCH /v1/commerce/variants/:variantId
// Sparse update — only fields in the JSON body are written. Seller
// ownership is verified against the parent product.
//
// `revalidate: true` is the acknowledgement an APPROVED listing's option or
// image change needs — the same key, the same 409 REVALIDATION_REQUIRED
// refusal and the same `revalidated`/`notice` answer as PATCH
// /products/:id, so a client handles one flow. A SKU change after approval
// is 409 SKU_LOCKED whatever the acknowledgement says. A price change goes
// live at once, with no acknowledgement, and is refused with 400
// PRICE_ABOVE_MRP when it would put the selling price above the MRP.
func (h *Handler) UpdateProductVariant(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}
	variantID, err := uuid.Parse(c.Param("variantId"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_VARIANT_ID", err.Error(), nil)
		return
	}
	var raw map[string]any
	if err := c.BindJSON(&raw); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_BODY", err.Error(), nil)
		return
	}
	ack := false
	if v, present := raw["revalidate"]; present {
		b, isBool := v.(bool)
		if !isBool {
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest,
				"INVALID_BODY", "revalidate must be true or false", nil)
			return
		}
		ack = b
		delete(raw, "revalidate")
	}
	out, revalidated, err := h.svc.UpdateProductVariant(c.Request.Context(), userID, variantID, raw, revalidateAck(c, ack))
	if err != nil {
		writeProductWriteError(c, err)
		return
	}
	// `data` stays the variant, byte-for-byte what it was: two shipped
	// clients decode it. The bounce travels in `meta`, which both ignore
	// until they look for it.
	var meta *api.Meta
	if revalidated {
		meta = &api.Meta{Revalidated: true, Notice: revalidatedNotice}
	}
	api.JSON(c.Writer, http.StatusOK, out, meta)
}

// revalidatedNotice is said in the same response as a success that sent the
// listing back to review, so the seller finds out here and not from their
// sales figures.
const revalidatedNotice = "this edit changed reviewed content, so the listing has returned to " +
	"review and is no longer on sale until it is approved again"

// revalidateAck reads the revalidation acknowledgement: the body flag, or
// `?revalidate=true` for the routes that carry no body (DELETE).
func revalidateAck(c *gin.Context, body bool) bool {
	return body || c.Query("revalidate") == "true"
}

// ArchiveProductVariant — DELETE /v1/commerce/variants/:variantId
// Soft delete (status='archived'). Existing orders + carts referencing
// the variant keep resolving; the customer just can't add new ones.
func (h *Handler) ArchiveProductVariant(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}
	variantID, err := uuid.Parse(c.Param("variantId"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_VARIANT_ID", err.Error(), nil)
		return
	}
	if err := h.svc.ArchiveProductVariant(c.Request.Context(), userID, variantID); err != nil {
		handleErr(c, err)
		return
	}
	api.JSON(c.Writer, http.StatusOK, gin.H{"variant_id": variantID, "status": "archived"}, nil)
}

// ─── Phase F2.1 — variant tier pricing handlers ──────────────

type priceTierReq struct {
	Tiers []struct {
		MinQty int     `json:"min_qty" binding:"required"`
		MaxQty *int    `json:"max_qty"`
		Price  float64 `json:"price" binding:"required"`
	} `json:"tiers"`
}

// SetPriceTiers PUT /v1/commerce/variants/:variantId/price-tiers —
// seller manages the qty discount ladder for one variant. Empty
// `tiers` clears the ladder (variant returns to flat selling_price).
func (h *Handler) SetPriceTiers(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}
	variantID, ok := parseUUID(c, "variantId")
	if !ok {
		return
	}
	seller, sellerOK := h.sellerForCaller(c, userID)
	if !sellerOK {
		return
	}
	var req priceTierReq
	if err := c.ShouldBindJSON(&req); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_BODY", err.Error(), nil)
		return
	}
	tiers := make([]service.PriceTierInput, 0, len(req.Tiers))
	for _, t := range req.Tiers {
		tiers = append(tiers, service.PriceTierInput{MinQty: t.MinQty, MaxQty: t.MaxQty, Price: t.Price})
	}
	out, err := h.svc.SetPriceTiers(c.Request.Context(), seller.ID, service.SetPriceTiersInput{
		VariantID: variantID, Tiers: tiers,
	})
	if err != nil {
		switch {
		case errors.Is(err, service.ErrTierOverlap), errors.Is(err, service.ErrTierInvalidBand):
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_TIERS", err.Error(), nil)
			return
		case errors.Is(err, service.ErrTierNotSeller):
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusForbidden, "NOT_OWNER", err.Error(), nil)
			return
		}
		handleErr(c, err)
		return
	}
	api.JSON(c.Writer, http.StatusOK, gin.H{"tiers": out}, nil)
}

// GetPriceTiers GET /v1/commerce/variants/:variantId/price-tiers —
// public read so the PDP can render the discount ladder to buyers.
func (h *Handler) GetPriceTiers(c *gin.Context) {
	variantID, ok := parseUUID(c, "variantId")
	if !ok {
		return
	}
	// The ladder is a price of a product; it is readable exactly when the
	// product is (handler.go productReadable).
	if err := h.svc.RequireVariantReadable(c.Request.Context(), variantID, optionalUserID(c)); err != nil {
		handleErr(c, err)
		return
	}
	out, err := h.svc.GetPriceTiers(c.Request.Context(), variantID)
	if err != nil {
		handleErr(c, err)
		return
	}
	api.JSON(c.Writer, http.StatusOK, gin.H{"tiers": out}, nil)
}

// addVariantCostMinor is costMinor for the add-a-variant-to-an-existing-
// product body, which carries the same optional pair.
func addVariantCostMinor(v addVariantReq) *int64 {
	if v.CostPriceMinor != nil {
		return v.CostPriceMinor
	}
	if v.CostPrice == nil {
		return nil
	}
	minor := int64(math.Round(*v.CostPrice * 100))
	return &minor
}
