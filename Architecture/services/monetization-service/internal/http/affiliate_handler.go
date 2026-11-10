package http

import (
	"context"
	"net/http"
	"strconv"

	"github.com/atpost/monetization-service/internal/store/postgres"
	"github.com/atpost/shared/api"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// ---------------------------------------------------------------------------
// Request types
// ---------------------------------------------------------------------------

type CreateAffiliateLinkRequest struct {
	ListingID      string   `json:"listing_id" binding:"required"`
	CommissionPct  float32  `json:"commission_pct"`
	CommissionFlat *float64 `json:"commission_flat"`
}

// ---------------------------------------------------------------------------
// Handlers
// ---------------------------------------------------------------------------

func (h *Handler) CreateAffiliateLink(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}

	var req CreateAffiliateLinkRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_REQUEST", err.Error(), nil)
		return
	}

	listingID, err := uuid.Parse(req.ListingID)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid listing ID", nil)
		return
	}

	link, err := h.svc.CreateAffiliateLink(c.Request.Context(), userID, listingID, req.CommissionPct, req.CommissionFlat)
	if err != nil {
		serverError(c, err)
		return
	}

	api.JSON(c.Writer, http.StatusCreated, link, nil)
}

func (h *Handler) ListAffiliateLinks(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}

	limit, offset := parsePagination(c)

	links, err := h.svc.ListAffiliateLinks(c.Request.Context(), userID, limit, offset)
	if err != nil {
		serverError(c, err)
		return
	}
	if links == nil {
		links = []postgres.AffiliateLink{}
	}

	api.JSON(c.Writer, http.StatusOK, links, nil)
}

// affiliateService is what the affiliate handlers read; an interface so the
// ownership rules are testable without a database.
type affiliateService interface {
	GetAffiliateLinkByCode(ctx context.Context, code string) (*postgres.AffiliateLink, error)
	GetAffiliateLinkByID(ctx context.Context, id uuid.UUID) (*postgres.AffiliateLink, error)
	RecordAffiliateClick(ctx context.Context, code string) (bool, error)
	ListAffiliateConversions(ctx context.Context, creatorID uuid.UUID, linkID *uuid.UUID, limit, offset int) ([]postgres.AffiliateConversion, error)
}

func (h *Handler) affiliateSvc() affiliateService {
	if h.affiliates != nil {
		return h.affiliates
	}
	return h.svc
}

// PublicAffiliateLink is all a buyer-facing redirect needs. Creator id, click
// and conversion counts, earnings and commission terms are owner-only
// (GET /affiliate/links).
type PublicAffiliateLink struct {
	LinkCode  string    `json:"link_code"`
	ListingID uuid.UUID `json:"listing_id"`
	IsActive  bool      `json:"is_active"`
}

// GetAffiliateLinkByCode is the open lookup. It is a pure read: no click is
// counted (RecordAffiliateClick is the explicit counter).
func (h *Handler) GetAffiliateLinkByCode(c *gin.Context) {
	linkCode := c.Param("linkCode")
	if linkCode == "" {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_REQUEST", "Missing link code", nil)
		return
	}

	link, err := h.affiliateSvc().GetAffiliateLinkByCode(c.Request.Context(), linkCode)
	if err != nil {
		serverError(c, err)
		return
	}
	if link == nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusNotFound, "NOT_FOUND", "Affiliate link not found", nil)
		return
	}

	api.JSON(c.Writer, http.StatusOK, PublicAffiliateLink{LinkCode: link.LinkCode, ListingID: link.ListingID, IsActive: link.IsActive}, nil)
}

// RecordAffiliateClick — POST /v1/monetization/affiliate/:linkCode/click.
// The one place a click is counted; unknown or inactive codes are a 404.
func (h *Handler) RecordAffiliateClick(c *gin.Context) {
	counted, err := h.affiliateSvc().RecordAffiliateClick(c.Request.Context(), c.Param("linkCode"))
	if err != nil {
		serverError(c, err)
		return
	}
	if !counted {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusNotFound, "NOT_FOUND", "Affiliate link not found", nil)
		return
	}
	c.Status(http.StatusNoContent)
}

// GetAffiliateLinkByID is the internal validator endpoint used by
// post-service when creating an in-video product tag. Looks the link
// up by UUID (not by link_code) so the post-service tag → affiliate
// reference stays stable across human-friendly code edits.
//
// Internal-only: gated by RequireInternalKey at the route level.
func (h *Handler) GetAffiliateLinkByID(c *gin.Context) {
	id, err := uuid.Parse(c.Param("linkId"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_REQUEST", "linkId must be a UUID", nil)
		return
	}
	link, err := h.svc.GetAffiliateLinkByID(c.Request.Context(), id)
	if err != nil {
		serverError(c, err)
		return
	}
	if link == nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusNotFound, "NOT_FOUND", "Affiliate link not found", nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, link, nil)
}

// ListAffiliateConversions returns the CALLER's conversions (those on links
// they created). The optional affiliate_id query narrows to one link id; one
// the caller does not own is a 404, the same as one that does not exist.
// buyer_id is never returned.
func (h *Handler) ListAffiliateConversions(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}

	var linkID *uuid.UUID
	if raw := c.Query("affiliate_id"); raw != "" {
		id, err := uuid.Parse(raw)
		if err != nil {
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid affiliate ID", nil)
			return
		}
		link, err := h.affiliateSvc().GetAffiliateLinkByID(c.Request.Context(), id)
		if err != nil {
			serverError(c, err)
			return
		}
		if link == nil || link.CreatorID != userID {
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusNotFound, "NOT_FOUND", "Affiliate link not found", nil)
			return
		}
		linkID = &id
	}

	limit, offset := parsePagination(c)

	convs, err := h.affiliateSvc().ListAffiliateConversions(c.Request.Context(), userID, linkID, limit, offset)
	if err != nil {
		serverError(c, err)
		return
	}
	if convs == nil {
		convs = []postgres.AffiliateConversion{}
	}

	api.JSON(c.Writer, http.StatusOK, convs, nil)
}

// ---------------------------------------------------------------------------
// Pagination helper
// ---------------------------------------------------------------------------

func parsePagination(c *gin.Context) (limit, offset int) {
	limit = 20
	offset = 0
	if v := c.Query("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			limit = n
		}
	}
	if v := c.Query("offset"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 {
			offset = n
		}
	}
	return
}
