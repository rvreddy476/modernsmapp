package http

import (
	"net/http"
	"time"

	"github.com/atpost/rider-service/internal/service"
	"github.com/atpost/shared/api"
	"github.com/gin-gonic/gin"
)

// submitDocumentRequest — body for POST /v1/rider/partners/me/documents.
type submitDocumentRequest struct {
	DocumentType   string  `json:"document_type"`
	DocumentNumber *string `json:"document_number,omitempty"`
	FileURL        string  `json:"file_url"`
	ExpiresAt      *string `json:"expires_at,omitempty"` // RFC3339
}

// PostMyDocument — POST /v1/rider/partners/me/documents.
func (h *Handler) PostMyDocument(c *gin.Context) {
	uid, ok := getUserID(c)
	if !ok {
		return
	}
	partner, err := h.svc.GetMyPartner(c.Request.Context(), uid)
	if err != nil {
		respondServiceError(c, err, http.StatusInternalServerError, "PARTNER_FETCH_FAILED")
		return
	}
	var body submitDocumentRequest
	if err := c.ShouldBindJSON(&body); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_BODY", err.Error(), nil)
		return
	}
	var expiresAt *time.Time
	if body.ExpiresAt != nil && *body.ExpiresAt != "" {
		t, perr := time.Parse(time.RFC3339, *body.ExpiresAt)
		if perr != nil {
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_REQUEST", "expires_at must be RFC3339", nil)
			return
		}
		expiresAt = &t
	}
	out, err := h.svc.SubmitKYCDocument(c.Request.Context(), uid, partner.ID, service.SubmitKYCDocumentRequest{
		DocumentType:   body.DocumentType,
		DocumentNumber: body.DocumentNumber,
		FileURL:        body.FileURL,
		ExpiresAt:      expiresAt,
	})
	if err != nil {
		respondServiceError(c, err, http.StatusInternalServerError, "DOCUMENT_SUBMIT_FAILED")
		return
	}
	api.JSONWithContext(c.Request.Context(), c.Writer, http.StatusCreated, out)
}

// GetMyDocuments — GET /v1/rider/partners/me/documents.
func (h *Handler) GetMyDocuments(c *gin.Context) {
	uid, ok := getUserID(c)
	if !ok {
		return
	}
	docs, err := h.svc.ListPartnerDocuments(c.Request.Context(), uid)
	if err != nil {
		respondServiceError(c, err, http.StatusInternalServerError, "DOCUMENT_LIST_FAILED")
		return
	}
	api.JSONWithContext(c.Request.Context(), c.Writer, http.StatusOK, docs)
}

// PostVehicleDocument — POST /v1/rider/vehicles/:id/documents.
func (h *Handler) PostVehicleDocument(c *gin.Context) {
	uid, ok := getUserID(c)
	if !ok {
		return
	}
	vehicleID, ok := parseUUIDParam(c, "id")
	if !ok {
		return
	}
	var body submitDocumentRequest
	if err := c.ShouldBindJSON(&body); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_BODY", err.Error(), nil)
		return
	}
	var expiresAt *time.Time
	if body.ExpiresAt != nil && *body.ExpiresAt != "" {
		t, perr := time.Parse(time.RFC3339, *body.ExpiresAt)
		if perr != nil {
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_REQUEST", "expires_at must be RFC3339", nil)
			return
		}
		expiresAt = &t
	}
	out, err := h.svc.SubmitVehicleDocument(c.Request.Context(), uid, vehicleID, service.SubmitVehicleDocumentRequest{
		DocumentType:   body.DocumentType,
		DocumentNumber: body.DocumentNumber,
		FileURL:        body.FileURL,
		ExpiresAt:      expiresAt,
	})
	if err != nil {
		respondServiceError(c, err, http.StatusInternalServerError, "VEHICLE_DOCUMENT_FAILED")
		return
	}
	api.JSONWithContext(c.Request.Context(), c.Writer, http.StatusCreated, out)
}

// GetVehicleDocuments — GET /v1/rider/vehicles/:id/documents.
func (h *Handler) GetVehicleDocuments(c *gin.Context) {
	uid, ok := getUserID(c)
	if !ok {
		return
	}
	vehicleID, ok := parseUUIDParam(c, "id")
	if !ok {
		return
	}
	docs, err := h.svc.ListVehicleDocuments(c.Request.Context(), uid, vehicleID)
	if err != nil {
		respondServiceError(c, err, http.StatusInternalServerError, "VEHICLE_DOCUMENT_LIST_FAILED")
		return
	}
	api.JSONWithContext(c.Request.Context(), c.Writer, http.StatusOK, docs)
}
