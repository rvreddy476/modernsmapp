package http

import (
	"errors"
	"net/http"

	"github.com/atpost/commerce-service/internal/store/postgres"
	"github.com/atpost/shared/api"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// RegisterWishlistRoutes registers the customer wishlist endpoints the
// mobile app already targets (canonical /v1/commerce/wishlist paths —
// the schema shipped in setup.sql long ago; this wires the HTTP
// surface). Called from RegisterRoutes.
func (h *Handler) RegisterWishlistRoutes(v1 *gin.RouterGroup) {
	v1.GET("/wishlist", h.GetWishlist)
	v1.POST("/wishlist", h.AddToWishlist)
	v1.DELETE("/wishlist/:productId", h.RemoveFromWishlist)
}

// GetWishlist GET /v1/commerce/wishlist — the user's saved products,
// newest first, each with a product tile snapshot.
func (h *Handler) GetWishlist(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}
	entries, err := h.svc.GetWishlist(c.Request.Context(), userID)
	if err != nil {
		handleErr(c, err)
		return
	}
	api.JSON(c.Writer, http.StatusOK, gin.H{"items": entries}, nil)
}

// AddToWishlist POST /v1/commerce/wishlist {product_id} — idempotent.
func (h *Handler) AddToWishlist(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}
	var req struct {
		ProductID uuid.UUID `json:"product_id" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_BODY", err.Error(), nil)
		return
	}
	if err := h.svc.AddToWishlist(c.Request.Context(), userID, req.ProductID); err != nil {
		if errors.Is(err, postgres.ErrWishlistProductUnavailable) {
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusUnprocessableEntity,
				"WISHLIST_PRODUCT_UNAVAILABLE", "This product cannot be saved right now", nil)
			return
		}
		handleErr(c, err)
		return
	}
	api.JSON(c.Writer, http.StatusOK, gin.H{"saved": true}, nil)
}

// RemoveFromWishlist DELETE /v1/commerce/wishlist/:productId — no-op
// when the product isn't on the list.
func (h *Handler) RemoveFromWishlist(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}
	productID, err := uuid.Parse(c.Param("productId"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_PRODUCT_ID", "invalid product id", nil)
		return
	}
	if err := h.svc.RemoveFromWishlist(c.Request.Context(), userID, productID); err != nil {
		handleErr(c, err)
		return
	}
	api.JSON(c.Writer, http.StatusOK, gin.H{"removed": true}, nil)
}
