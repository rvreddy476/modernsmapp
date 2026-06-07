package http

import (
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
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
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
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	if links == nil {
		links = []postgres.AffiliateLink{}
	}

	api.JSON(c.Writer, http.StatusOK, links, nil)
}

func (h *Handler) GetAffiliateLinkByCode(c *gin.Context) {
	linkCode := c.Param("linkCode")
	if linkCode == "" {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_REQUEST", "Missing link code", nil)
		return
	}

	link, err := h.svc.GetAffiliateLinkByCode(c.Request.Context(), linkCode)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	if link == nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusNotFound, "NOT_FOUND", "Affiliate link not found", nil)
		return
	}

	api.JSON(c.Writer, http.StatusOK, link, nil)
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
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	if link == nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusNotFound, "NOT_FOUND", "Affiliate link not found", nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, link, nil)
}

func (h *Handler) ListAffiliateConversions(c *gin.Context) {
	_, ok := getUserID(c)
	if !ok {
		return
	}

	affiliateIDStr := c.Query("affiliate_id")
	if affiliateIDStr == "" {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_REQUEST", "affiliate_id query param required", nil)
		return
	}
	affiliateID, err := uuid.Parse(affiliateIDStr)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid affiliate ID", nil)
		return
	}

	limit, offset := parsePagination(c)

	convs, err := h.svc.ListAffiliateConversions(c.Request.Context(), affiliateID, limit, offset)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
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
