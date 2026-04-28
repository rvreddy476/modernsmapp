package http

import (
	"net/http"

	"github.com/atpost/monetization-service/internal/store/postgres"
	"github.com/atpost/shared/api"
	"github.com/gin-gonic/gin"
)

// ---------------------------------------------------------------------------
// Request types
// ---------------------------------------------------------------------------

// SaveTaxProfileRequest is the request body for saving a creator's tax profile.
type SaveTaxProfileRequest struct {
	PANEncrypted *string `json:"pan_encrypted"`
	GSTIN        *string `json:"gstin"`
	TaxResidency string  `json:"tax_residency"`
}

// ---------------------------------------------------------------------------
// Handlers
// ---------------------------------------------------------------------------

// SaveTaxProfile saves or updates a creator's tax compliance profile.
func (h *Handler) SaveTaxProfile(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}

	var req SaveTaxProfileRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_REQUEST", err.Error(), nil)
		return
	}

	if err := h.svc.SaveCreatorTaxProfile(c.Request.Context(), userID, req.PANEncrypted, req.GSTIN, req.TaxResidency); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}

	api.JSON(c.Writer, http.StatusOK, map[string]string{"status": "saved"}, nil)
}

// GetTaxProfile returns the creator's tax compliance profile.
func (h *Handler) GetTaxProfile(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}

	profile, err := h.svc.GetCreatorTaxProfile(c.Request.Context(), userID)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	if profile == nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusNotFound, "NOT_FOUND", "Tax profile not found", nil)
		return
	}

	api.JSON(c.Writer, http.StatusOK, profile, nil)
}

// GetTDSSummary returns a summary of TDS deductions for a creator in a given financial year.
func (h *Handler) GetTDSSummary(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}

	year := c.Param("year")
	if year == "" {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_REQUEST", "Financial year parameter is required", nil)
		return
	}

	entries, totalPaise, err := h.svc.GetTDSSummary(c.Request.Context(), userID, year)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	if entries == nil {
		entries = []postgres.TDSEntry{}
	}

	result := map[string]interface{}{
		"financial_year":  year,
		"entries":         entries,
		"total_tds_paise": totalPaise,
	}

	api.JSON(c.Writer, http.StatusOK, result, nil)
}

// ListInvoices returns paginated invoices for the authenticated user.
func (h *Handler) ListInvoices(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}

	limit, offset := parsePagination(c)

	invoices, err := h.svc.ListInvoices(c.Request.Context(), userID, limit, offset)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	if invoices == nil {
		invoices = []postgres.Invoice{}
	}

	api.JSON(c.Writer, http.StatusOK, invoices, nil)
}
