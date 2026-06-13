package http

import (
	"errors"
	"net/http"

	"github.com/atpost/commerce-service/internal/service"
	"github.com/atpost/shared/api"
	"github.com/gin-gonic/gin"
)

// AffiliateRedirect — GET /v1/commerce/affiliate/:linkId
//
// Public endpoint. The web + mobile players link the in-video product
// overlay to this URL; the redirect lands the viewer on the canonical
// product page with the affiliate code embedded so the eventual
// checkout can attribute commission.
//
// We respond with a 302 + Location header so the browser fully
// re-navigates (instead of a 307 that preserves method, since this is
// a GET-followed-by-GET flow). The path is a relative URL — the
// public host is whatever the request landed on.
//
// Errors map honestly:
//   404 — link not found OR target product missing (we collapse the
//         two so a probe attack can't enumerate which links exist)
//   410 Gone — link is inactive (the creator turned it off; tells
//         the client to drop the local overlay state)
//   503 — monetization-service unreachable; the click event is lost
//         intentionally (we don't want to redirect to a placeholder)
func (h *Handler) AffiliateRedirect(c *gin.Context) {
	id, ok := parseUUID(c, "linkId")
	if !ok {
		return
	}

	target, err := h.svc.ResolveAffiliateRedirect(c.Request.Context(), id)
	switch {
	case err == nil:
		c.Redirect(http.StatusFound, target)
		return
	case errors.Is(err, service.ErrAffiliateRedirectLinkNotFound),
		errors.Is(err, service.ErrAffiliateRedirectProductMissing):
		api.ErrorWithContext(c.Request.Context(), c.Writer,
			http.StatusNotFound, "NOT_FOUND", "Affiliate link not available", nil)
	case errors.Is(err, service.ErrAffiliateRedirectLinkInactive):
		api.ErrorWithContext(c.Request.Context(), c.Writer,
			http.StatusGone, "GONE", "Affiliate link is no longer active", nil)
	default:
		api.ErrorWithContext(c.Request.Context(), c.Writer,
			http.StatusServiceUnavailable, "UPSTREAM_UNAVAILABLE", "Affiliate redirect temporarily unavailable", nil)
	}
}

// GetProductPreview — GET /v1/commerce/products/:productId/preview
//
// Compact projection of a product for the in-video tag composer. The
// composer needs label + image_url + price + currency to render the
// search-result cards; the full GetProduct response is overkill for
// that use case (carries seller / variants / attributes / shipping
// dims that the picker doesn't show).
//
// Authoring intent vs view intent. The composer is the only known
// caller today, so we keep the field set minimal. Add only when a
// future composer feature genuinely needs more — JSON growth is
// permanent in a public API.
type productPreviewResponse struct {
	ID                 string  `json:"id"`
	Title              string  `json:"title"`
	Slug               string  `json:"slug"`
	PrimaryImageMediaID *string `json:"primary_image_media_id,omitempty"` // client resolves via media-service
	Price              float64 `json:"price,omitempty"`
	Currency           string  `json:"currency,omitempty"` // ISO 4217
	Status             string  `json:"status"`
	Visibility         string  `json:"visibility"`
}

func (h *Handler) GetProductPreview(c *gin.Context) {
	id, ok := parseUUID(c, "productId")
	if !ok {
		return
	}
	preview, err := h.svc.GetProductPreview(c.Request.Context(), id)
	if err != nil {
		handleErr(c, err)
		return
	}
	if preview == nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer,
			http.StatusNotFound, "NOT_FOUND", "Product not found", nil)
		return
	}
	resp := productPreviewResponse{
		ID:         preview.ID.String(),
		Title:      preview.Title,
		Slug:       preview.Slug,
		Price:      preview.Price,
		Currency:   preview.Currency,
		Status:     preview.Status,
		Visibility: preview.Visibility,
	}
	if preview.PrimaryImageMediaID != nil {
		s := preview.PrimaryImageMediaID.String()
		resp.PrimaryImageMediaID = &s
	}
	api.JSON(c.Writer, http.StatusOK, resp, nil)
}
