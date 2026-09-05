package http

// Seller stock adjustment routes.
//
// Stock was set-once before these existed: `POST /products` wrote the number
// typed into the create form, and the only path that ever wrote it again —
// bulk import — is behind the launch fence. A seller who sold out stayed sold
// out, and a seller who typed 10 meaning 100 had no way back.

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/atpost/commerce-service/internal/service"
	"github.com/atpost/commerce-service/internal/store/postgres"
	"github.com/atpost/shared/api"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// adjustStockReq takes a signed delta, never a new total.
//
// A new total is a lost-update generator: the app renders "42 in stock", two
// units sell while the seller is typing, the seller submits 52 meaning "I
// added ten", and the two sold units are silently restored to the shelf. The
// seller knows one true number — how many they added or removed.
//
// Delta is *int rather than int so that a body which omits the field is a 400
// instead of a silent zero. `binding:"required"` cannot tell 0 from absent.
type adjustStockReq struct {
	Delta  *int   `json:"delta"`
	Reason string `json:"reason"`
	Notes  string `json:"notes"`
}

// AdjustStock PATCH /v1/commerce/seller/variants/:variantId/stock
func (h *Handler) AdjustStock(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}
	variantID, err := uuid.Parse(c.Param("variantId"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest,
			"INVALID_VARIANT_ID", "variantId must be a uuid", nil)
		return
	}
	var body adjustStockReq
	if err := c.ShouldBindJSON(&body); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest,
			"INVALID_BODY", err.Error(), nil)
		return
	}
	if body.Delta == nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest,
			"INVALID_BODY", "delta is required (positive to restock, negative to write down)", nil)
		return
	}

	level, err := h.svc.AdjustStock(c.Request.Context(), userID, service.AdjustStockInput{
		VariantID: variantID,
		Delta:     *body.Delta,
		Reason:    body.Reason,
		Notes:     body.Notes,
	})
	if err != nil {
		if errors.Is(err, service.ErrNoSellerProfile) {
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusForbidden,
				"NO_SELLER", "seller account not found", nil)
			return
		}
		writeCommerceError(c, err)
		return
	}
	api.JSON(c.Writer, http.StatusOK, level, nil)
}

// GetStock GET /v1/commerce/seller/variants/:variantId/stock
//
// Returns reserved separately from total. A seller looking at "42" needs to
// know how many of those are already spoken for before deciding what they can
// physically ship today.
func (h *Handler) GetStock(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}
	variantID, err := uuid.Parse(c.Param("variantId"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest,
			"INVALID_VARIANT_ID", "variantId must be a uuid", nil)
		return
	}
	level, err := h.svc.StockFor(c.Request.Context(), userID, variantID)
	if err != nil {
		if errors.Is(err, service.ErrNoSellerProfile) {
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusForbidden,
				"NO_SELLER", "seller account not found", nil)
			return
		}
		writeCommerceError(c, err)
		return
	}
	api.JSON(c.Writer, http.StatusOK, level, nil)
}

// ListMyProducts GET /v1/commerce/seller/products
//
// The seller's own catalogue, every status included.
//
// The public route `GET /sellers/:sellerId/products` used to return this same
// unfiltered set for any seller id in the URL, with no authentication — so a
// competitor's unreleased drafts and moderation rejections were readable by
// anyone who knew their seller id. That route is now storefront-filtered, and
// this one resolves the seller from the caller instead of the path.
func (h *Handler) ListMyProducts(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "20"))
	offset, _ := strconv.Atoi(c.DefaultQuery("offset", "0"))

	products, total, err := h.svc.ListMyProducts(c.Request.Context(), userID,
		c.Query("status"), limit, offset)
	if err != nil {
		if errors.Is(err, service.ErrNoSellerProfile) {
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusForbidden,
				"NO_SELLER", "seller account not found", nil)
			return
		}
		writeCommerceError(c, err)
		return
	}
	if products == nil {
		products = []*postgres.Product{}
	}
	api.JSON(c.Writer, http.StatusOK, gin.H{"items": products, "total": total}, nil)
}

// ListTaxClasses GET /v1/commerce/tax-classes
//
// The GST rate table a seller must choose from when creating a product.
//
// There was no endpoint exposing these rows at all, which is why
// `tax_class_id` stayed optional on the create route: a form had no way to
// offer the choice. A product created without one is unsellable — checkout
// refuses it with PRODUCT_TAX_UNCONFIGURED — so the missing endpoint and the
// optional field together produced listings that could never be bought.
//
// Public. These are statutory rates, not seller data, and a buyer-facing
// surface may want to explain a tax line with them.
func (h *Handler) ListTaxClasses(c *gin.Context) {
	classes, err := h.svc.TaxClasses(c.Request.Context())
	if err != nil {
		writeCommerceError(c, err)
		return
	}
	api.JSON(c.Writer, http.StatusOK, gin.H{"items": classes}, nil)
}

// GetSellerVariant GET /v1/commerce/seller/variants/:variantId
//
// One of the caller's own variants — its price, availability and whether it is
// on sale. The edit screen reads this rather than carrying a price through
// navigation: a figure carried from a list is a figure from whenever that list
// loaded, and repricing against a stale one is how a seller undoes a change
// they made a minute ago on another device.
func (h *Handler) GetSellerVariant(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}
	variantID, err := uuid.Parse(c.Param("variantId"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest,
			"INVALID_VARIANT_ID", "variantId must be a uuid", nil)
		return
	}
	variant, err := h.svc.SellerVariant(c.Request.Context(), userID, variantID)
	if err != nil {
		if errors.Is(err, service.ErrNoSellerProfile) {
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusForbidden,
				"NO_SELLER", "seller account not found", nil)
			return
		}
		writeCommerceError(c, err)
		return
	}
	api.JSON(c.Writer, http.StatusOK, variant, nil)
}

// GetSellerReadiness GET /v1/commerce/onboarding/readiness
//
// What a shop still needs before it can be reviewed.
//
// Exists so the app can show the remaining checklist rather than letting a
// seller press Submit and be told no. `POST /onboarding/submit` enforces the
// same rules — this is the friendly half, not the guard.
func (h *Handler) GetSellerReadiness(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}
	ready, err := h.svc.SellerReadiness(c.Request.Context(), userID)
	if err != nil {
		if errors.Is(err, service.ErrNoSellerProfile) {
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusForbidden,
				"NO_SELLER", "seller account not found", nil)
			return
		}
		writeCommerceError(c, err)
		return
	}
	api.JSON(c.Writer, http.StatusOK, gin.H{
		"ready":   ready.Complete(),
		"missing": ready.Missing(),
		"detail":  ready,
	}, nil)
}
