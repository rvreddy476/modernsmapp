// Face AR try-on: the seller's publish and withdraw routes.
//
// The READ has no route of its own — it rides the product detail body, so the
// "Try on" action can be drawn in the first paint beside the buy strip. See
// GetProduct.
package http

import (
	"context"
	"errors"
	"log/slog"
	"net/http"

	"github.com/atpost/commerce-service/internal/service"
	"github.com/atpost/commerce-service/internal/store/postgres"
	"github.com/atpost/shared/api"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// logTryOnReadFailure records a soft read failure on the detail path. The
// product id, never the descriptor: a malformed blob is exactly the case
// where the value is untrusted.
func logTryOnReadFailure(ctx context.Context, productID uuid.UUID, err error) {
	slog.ErrorContext(ctx, "commerce: the try-on descriptor could not be read; the product page will show no try-on",
		"product_id", productID.String(), "error", err)
}

// tryOnVariantReq is one look, snake_case on the wire like every other
// commerce body.
type tryOnVariantReq struct {
	ID    string `json:"id"`
	Label string `json:"label"`
	Hex   string `json:"hex"`
	JS    string `json:"js"`
}

type setTryOnReq struct {
	Kind       string            `json:"kind"`
	EffectSlug string            `json:"effect_slug"`
	Variants   []tryOnVariantReq `json:"variants"`
}

// SetProductTryOn — PUT /v1/commerce/products/:productId/try-on
//
// Replaces the whole descriptor. Seller-gated on the product, then fenced on
// the product's root category so a kind cannot be claimed that the category
// tree does not admit.
func (h *Handler) SetProductTryOn(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}
	productID, ok := parseUUID(c, "productId")
	if !ok {
		return
	}
	var req setTryOnReq
	if err := c.ShouldBindJSON(&req); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer,
			http.StatusBadRequest, "INVALID_BODY", err.Error(), nil)
		return
	}
	variants := make([]postgres.TryOnVariant, 0, len(req.Variants))
	for _, v := range req.Variants {
		variants = append(variants, postgres.TryOnVariant{
			ID: v.ID, Label: v.Label, Hex: v.Hex, JS: v.JS,
		})
	}
	descriptor, err := h.svc.SetProductTryOn(
		c.Request.Context(), productID, userID, req.Kind, req.EffectSlug, variants)
	if err != nil {
		writeTryOnErr(c, err)
		return
	}
	api.JSON(c.Writer, http.StatusOK, gin.H{"try_on": descriptor}, nil)
}

// ClearProductTryOn — DELETE /v1/commerce/products/:productId/try-on
//
// Withdraws the capability. Idempotent: withdrawing one that is not there is
// the state the caller asked for, so it answers 204 either way.
func (h *Handler) ClearProductTryOn(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}
	productID, ok := parseUUID(c, "productId")
	if !ok {
		return
	}
	if err := h.svc.ClearProductTryOn(c.Request.Context(), productID, userID); err != nil {
		writeTryOnErr(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

// writeTryOnErr maps the two try-on error classes and defers everything else
// — ownership, missing seller profile, database faults — to the shared
// mapper, so this route cannot disagree with the rest of the service about
// what "not your product" answers.
func writeTryOnErr(c *gin.Context, err error) {
	switch {
	case errors.Is(err, service.ErrTryOnInvalid):
		api.ErrorWithContext(c.Request.Context(), c.Writer,
			http.StatusBadRequest, "INVALID_TRY_ON", err.Error(), nil)
	case errors.Is(err, service.ErrTryOnKindNotAllowed):
		api.ErrorWithContext(c.Request.Context(), c.Writer,
			http.StatusUnprocessableEntity, "TRY_ON_KIND_NOT_ALLOWED", err.Error(), nil)
	default:
		handleErr(c, err)
	}
}
