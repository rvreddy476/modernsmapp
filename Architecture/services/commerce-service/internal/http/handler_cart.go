// Cart route handlers: read, add, remove and update cart lines, and the
// cart response writer. Moved out of handler.go unchanged.

package http

import (
	"errors"
	"net/http"

	"github.com/atpost/commerce-service/internal/store/postgres"
	"github.com/atpost/shared/api"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// ─── Cart handlers ───────────────────────────────────────────────

func (h *Handler) GetCart(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}
	view, err := h.svc.CartView(c.Request.Context(), userID)
	if err != nil {
		handleErr(c, err)
		return
	}
	api.JSON(c.Writer, http.StatusOK, view, nil)
}

type addToCartReq struct {
	VariantID uuid.UUID `json:"variant_id" binding:"required"`
	Quantity  int       `json:"quantity" binding:"required,min=1"`
}

func (h *Handler) AddToCart(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}
	var req addToCartReq
	if err := c.ShouldBindJSON(&req); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_BODY", err.Error(), nil)
		return
	}
	if err := h.svc.AddToCart(c.Request.Context(), userID, req.VariantID, req.Quantity); err != nil {
		// The three refusals a client renders copy for keep their own codes:
		// a second seller's item is 409 MULTIPLE_SELLERS (the bag is
		// single-seller, D2), a withdrawn listing is 409 PRODUCT_UNAVAILABLE
		// and too few units is 409 OUT_OF_STOCK with the line — the same
		// codes checkout answers with. All three used to be folded into the
		// generic 400 below, so the app could only say "could not add".
		if errors.Is(err, postgres.ErrMultipleSellers) || errors.Is(err, postgres.ErrProductUnavailable) ||
			errors.Is(err, postgres.ErrOutOfStock) {
			writeCommerceError(c, err)
			return
		}
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "ADD_TO_CART_FAILED", err.Error(), nil)
		return
	}
	// The cart, not 204. The client asks for a cart back from this route and
	// an empty body left it with nothing to render, so the badge and the cart
	// screen disagreed with the server until something else refetched. It is
	// also one round trip instead of add-then-reload.
	writeCart(c, h)
}

func (h *Handler) RemoveFromCart(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}
	variantID, ok := parseUUID(c, "variantId")
	if !ok {
		return
	}
	if err := h.svc.RemoveFromCart(c.Request.Context(), userID, variantID); err != nil {
		handleErr(c, err)
		return
	}
	writeCart(c, h)
}

// updateCartItemReq is the body for PATCH /cart/items/by-variant/:variantId
// — Phase 1.2. Quantity 0 deletes the line; otherwise it is the absolute
// new quantity (atomic upsert with stock check, not a delta).
type updateCartItemReq struct {
	Quantity int `json:"quantity"`
}

// UpdateCartItem PATCH /v1/commerce/cart/items/by-variant/:variantId.
// Returns the updated cart summary so the client renders immediately
// without a separate GET.
func (h *Handler) UpdateCartItem(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}
	variantID, ok := parseUUID(c, "variantId")
	if !ok {
		return
	}
	var req updateCartItemReq
	if err := c.ShouldBindJSON(&req); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_BODY", err.Error(), nil)
		return
	}
	if req.Quantity < 0 {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_BODY", "quantity must be >= 0", nil)
		return
	}
	if err := h.svc.UpdateCartItem(c.Request.Context(), userID, variantID, req.Quantity); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "UPDATE_CART_FAILED", err.Error(), nil)
		return
	}
	view, err := h.svc.CartView(c.Request.Context(), userID)
	if err != nil {
		handleErr(c, err)
		return
	}
	api.JSON(c.Writer, http.StatusOK, view, nil)
}

// writeCart answers with the caller's cart in the shape the client reads.
//
// Every cart mutation returns the resulting cart rather than 204. The client's
// API declares all four cart routes as returning a cart, and two of them
// answered with an empty body — so after an add or a remove the app held no
// authoritative state and the badge disagreed with the server until something
// else happened to refetch.
func writeCart(c *gin.Context, h *Handler) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}
	view, err := h.svc.CartView(c.Request.Context(), userID)
	if err != nil {
		handleErr(c, err)
		return
	}
	api.JSON(c.Writer, http.StatusOK, view, nil)
}
