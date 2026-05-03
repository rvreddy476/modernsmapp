package http

import (
	"net/http"

	"github.com/atpost/rider-service/internal/service"
	"github.com/atpost/shared/api"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// createPartnerRequest is the body for POST /v1/rider/partners.
type createPartnerRequest struct {
	PartnerType string     `json:"partner_type"`
	FullName    string     `json:"full_name"`
	Phone       string     `json:"phone"`
	Email       *string    `json:"email,omitempty"`
	CityID      *uuid.UUID `json:"city_id,omitempty"`
}

// PostPartner — POST /v1/rider/partners. Creates a partner profile in
// `draft` status for the calling user.
func (h *Handler) PostPartner(c *gin.Context) {
	uid, ok := getUserID(c)
	if !ok {
		return
	}
	var body createPartnerRequest
	if err := c.ShouldBindJSON(&body); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_BODY", err.Error(), nil)
		return
	}
	p, err := h.svc.CreatePartnerProfile(c.Request.Context(), uid, service.CreatePartnerRequest{
		PartnerType: body.PartnerType,
		FullName:    body.FullName,
		Phone:       body.Phone,
		Email:       body.Email,
		CityID:      body.CityID,
	})
	if err != nil {
		respondServiceError(c, err, http.StatusInternalServerError, "PARTNER_CREATE_FAILED")
		return
	}
	api.JSONWithContext(c.Request.Context(), c.Writer, http.StatusCreated, p)
}

// GetMyPartner — GET /v1/rider/partners/me.
func (h *Handler) GetMyPartner(c *gin.Context) {
	uid, ok := getUserID(c)
	if !ok {
		return
	}
	p, err := h.svc.GetMyPartner(c.Request.Context(), uid)
	if err != nil {
		respondServiceError(c, err, http.StatusInternalServerError, "PARTNER_FETCH_FAILED")
		return
	}
	api.JSONWithContext(c.Request.Context(), c.Writer, http.StatusOK, p)
}

// patchPartnerRequest is the partial-update body for PATCH /partners/me.
type patchPartnerRequest struct {
	FullName        *string    `json:"full_name,omitempty"`
	Email           *string    `json:"email,omitempty"`
	ProfilePhotoURL *string    `json:"profile_photo_url,omitempty"`
	CityID          *uuid.UUID `json:"city_id,omitempty"`
}

// PatchMyPartner — PATCH /v1/rider/partners/me.
func (h *Handler) PatchMyPartner(c *gin.Context) {
	uid, ok := getUserID(c)
	if !ok {
		return
	}
	var body patchPartnerRequest
	if err := c.ShouldBindJSON(&body); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_BODY", err.Error(), nil)
		return
	}
	p, err := h.svc.UpdatePartnerProfile(c.Request.Context(), uid, service.UpdatePartnerProfileRequest{
		FullName:        body.FullName,
		Email:           body.Email,
		ProfilePhotoURL: body.ProfilePhotoURL,
		CityID:          body.CityID,
	})
	if err != nil {
		respondServiceError(c, err, http.StatusInternalServerError, "PARTNER_UPDATE_FAILED")
		return
	}
	api.JSONWithContext(c.Request.Context(), c.Writer, http.StatusOK, p)
}

// PostAadhaarStart — POST /v1/rider/partners/me/aadhaar/start.
func (h *Handler) PostAadhaarStart(c *gin.Context) {
	uid, ok := getUserID(c)
	if !ok {
		return
	}
	partner, err := h.svc.GetMyPartner(c.Request.Context(), uid)
	if err != nil {
		respondServiceError(c, err, http.StatusInternalServerError, "PARTNER_FETCH_FAILED")
		return
	}
	out, err := h.svc.StartAadhaarFlow(c.Request.Context(), uid, partner.ID)
	if err != nil {
		respondServiceError(c, err, http.StatusInternalServerError, "AADHAAR_START_FAILED")
		return
	}
	api.JSONWithContext(c.Request.Context(), c.Writer, http.StatusOK, out)
}

// aadhaarCallbackRequest — body for the callback. Only the OAuth-style code +
// PKCE state nonce. NO Aadhaar number is accepted here.
type aadhaarCallbackRequest struct {
	Code  string `json:"code"`
	State string `json:"state"`
}

// PostAadhaarCallback — POST /v1/rider/partners/me/aadhaar/callback.
func (h *Handler) PostAadhaarCallback(c *gin.Context) {
	uid, ok := getUserID(c)
	if !ok {
		return
	}
	partner, err := h.svc.GetMyPartner(c.Request.Context(), uid)
	if err != nil {
		respondServiceError(c, err, http.StatusInternalServerError, "PARTNER_FETCH_FAILED")
		return
	}
	var body aadhaarCallbackRequest
	if err := c.ShouldBindJSON(&body); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_BODY", err.Error(), nil)
		return
	}
	out, err := h.svc.CompleteAadhaarFlow(c.Request.Context(), uid, partner.ID, body.Code, body.State)
	if err != nil {
		respondServiceError(c, err, http.StatusInternalServerError, "AADHAAR_CALLBACK_FAILED")
		return
	}
	api.JSONWithContext(c.Request.Context(), c.Writer, http.StatusOK, out)
}
