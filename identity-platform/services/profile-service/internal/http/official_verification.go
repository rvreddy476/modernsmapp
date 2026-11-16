package http

import (
	"context"
	"errors"
	"net/http"

	"github.com/atpost/identity-profile-service/internal/officialverify"
	"github.com/atpost/identity-shared/api"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// Official (organisation) and Government mark routes (internal/officialverify).
//
// Signed in; 503 ORG_VERIFICATION_DISABLED / GOVERNMENT_VERIFICATION_DISABLED
// unless the matching flag is on:
//
//	GET  /v1/profiles/me/verification/organisation                status
//	POST /v1/profiles/me/verification/organisation                {legal_name, gstin|cin, official_domain, signatory_email, document_media_id?}
//	POST /v1/profiles/me/verification/organisation/confirm-email  {code}
//	POST /v1/profiles/me/verification/government/accept           {invite_code}
//
// Reviewing applications and issuing Government invitations is the admin
// console's, through admin-service's signed token: see
// official_verification_admin.go. (The two internal-key routes that did it
// before, with an admin-role read against auth-service, were removed on 16
// Nov 2026.)

// OfficialVerification is the official-marks service.
type OfficialVerification interface {
	SubmitOrganisation(ctx context.Context, userID uuid.UUID, req officialverify.OrgApplicationRequest) (*officialverify.OrgStatus, error)
	ConfirmOrganisationEmail(ctx context.Context, userID uuid.UUID, code string) (*officialverify.OrgStatus, error)
	OrganisationStatus(ctx context.Context, userID uuid.UUID) (*officialverify.OrgStatus, error)
	AcceptGovInvitation(ctx context.Context, userID uuid.UUID, code string) (*officialverify.OrgStatus, error)
	// Admin console (official_verification_admin.go).
	Stats(ctx context.Context) (*officialverify.MarksStats, error)
	ListApplications(ctx context.Context, q officialverify.ListQuery) (*officialverify.ApplicationPage, error)
	GetApplication(ctx context.Context, id uuid.UUID) (*officialverify.Application, error)
	ApplicationDocument(ctx context.Context, id uuid.UUID) (uuid.UUID, error)
	ReviewOrganisation(ctx context.Context, applicationID uuid.UUID, d officialverify.ReviewDecision) (*officialverify.Application, error)
	ListInvitations(ctx context.Context, q officialverify.ListQuery) (*officialverify.InvitationPage, error)
	CreateGovInvitation(ctx context.Context, req officialverify.GovInvitationRequest) (*officialverify.GovInvitationResult, error)
	RevokeInvitation(ctx context.Context, id, actor uuid.UUID, reason string) (*officialverify.Invitation, error)
}

// WithOfficialVerification wires the official-mark routes. Unwired, the user
// routes answer 503 and the console family is not registered.
func (h *Handler) WithOfficialVerification(o OfficialVerification) *Handler {
	h.official = o
	return h
}

func (h *Handler) registerOfficialVerificationRoutes(protected *gin.RouterGroup, csrf gin.HandlerFunc) {
	protected.GET("/me/verification/organisation", h.GetOrganisationVerification)
	protected.POST("/me/verification/organisation", csrf, h.SubmitOrganisationVerification)
	protected.POST("/me/verification/organisation/confirm-email", csrf, h.ConfirmOrganisationEmail)
	protected.POST("/me/verification/government/accept", csrf, h.AcceptGovernmentInvitation)
}

// GetOrganisationVerification — GET /v1/profiles/me/verification/organisation.
func (h *Handler) GetOrganisationVerification(c *gin.Context) {
	userID, ok := h.officialUser(c, officialverify.ErrOrgDisabled)
	if !ok {
		return
	}
	out, err := h.official.OrganisationStatus(c.Request.Context(), userID)
	h.writeOfficial(c, http.StatusOK, out, err)
}

// SubmitOrganisationVerification — POST /v1/profiles/me/verification/organisation.
func (h *Handler) SubmitOrganisationVerification(c *gin.Context) {
	userID, ok := h.officialUser(c, officialverify.ErrOrgDisabled)
	if !ok {
		return
	}
	var req officialverify.OrgApplicationRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		h.writeBindError(c, err)
		return
	}
	out, err := h.official.SubmitOrganisation(c.Request.Context(), userID, req)
	h.writeOfficial(c, http.StatusCreated, out, err)
}

// ConfirmOrganisationEmail — POST /v1/profiles/me/verification/organisation/confirm-email.
func (h *Handler) ConfirmOrganisationEmail(c *gin.Context) {
	userID, ok := h.officialUser(c, officialverify.ErrOrgDisabled)
	if !ok {
		return
	}
	var req struct {
		Code string `json:"code"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || req.Code == "" || len(req.Code) > 16 {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_REQUEST", "code is required", nil, nil)
		return
	}
	out, err := h.official.ConfirmOrganisationEmail(c.Request.Context(), userID, req.Code)
	h.writeOfficial(c, http.StatusOK, out, err)
}

// AcceptGovernmentInvitation — POST /v1/profiles/me/verification/government/accept.
func (h *Handler) AcceptGovernmentInvitation(c *gin.Context) {
	userID, ok := h.officialUser(c, officialverify.ErrGovDisabled)
	if !ok {
		return
	}
	var req struct {
		InviteCode string `json:"invite_code"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || req.InviteCode == "" || len(req.InviteCode) > 64 {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_REQUEST", "invite_code is required", nil, nil)
		return
	}
	out, err := h.official.AcceptGovInvitation(c.Request.Context(), userID, req.InviteCode)
	h.writeOfficial(c, http.StatusOK, out, err)
}

// officialUser answers the disabled refusal when unwired and 401 without a user.
func (h *Handler) officialUser(c *gin.Context, disabled *officialverify.Error) (uuid.UUID, bool) {
	if h.official == nil {
		api.Error(c.Writer, disabled.Status, disabled.Code, disabled.Message, nil, nil)
		return uuid.Nil, false
	}
	userID, err := parseUserHeader(c)
	if err != nil {
		api.Error(c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Invalid user ID", nil, nil)
		return uuid.Nil, false
	}
	return userID, true
}

func (h *Handler) writeOfficial(c *gin.Context, status int, out any, err error) {
	var refusal *officialverify.Error
	switch {
	case errors.As(err, &refusal):
		api.Error(c.Writer, refusal.Status, refusal.Code, refusal.Message, nil, nil)
	case err != nil:
		h.writeInternalError(c, "official verification failed", err)
	default:
		api.JSON(c.Writer, status, out, nil)
	}
}
