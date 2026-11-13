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
// Service-only (the future admin console calls these through admin-service,
// in-cluster). Outside /v1 on purpose, like InternalIdentityPath: the gateway
// proxies /v1/profiles/* with the internal key attached, and refuses every
// path with an "internal" segment anyway (api-gateway pkg/internalroutes).
// Guarded by requireInternalServiceCaller: no end-user identity header, and
// the internal key. Not registered without a key.
//
//	POST /internal/v1/profiles/verification/organisation/applications/:applicationId/review
//	     {decision: approve|reject, reviewer_id, reason}
//	POST /internal/v1/profiles/verification/government/invitations
//	     {user_id, official_name, approver_ids: [a, b], reason}

// Service-only paths.
const (
	InternalOrgReviewPath     = "/internal/v1/profiles/verification/organisation/applications/:applicationId/review"
	InternalGovInvitationPath = "/internal/v1/profiles/verification/government/invitations"
)

// OfficialVerification is the official-marks service.
type OfficialVerification interface {
	SubmitOrganisation(ctx context.Context, userID uuid.UUID, req officialverify.OrgApplicationRequest) (*officialverify.OrgStatus, error)
	ConfirmOrganisationEmail(ctx context.Context, userID uuid.UUID, code string) (*officialverify.OrgStatus, error)
	OrganisationStatus(ctx context.Context, userID uuid.UUID) (*officialverify.OrgStatus, error)
	ReviewOrganisation(ctx context.Context, applicationID uuid.UUID, d officialverify.ReviewDecision) (*officialverify.OrgStatus, error)
	CreateGovInvitation(ctx context.Context, req officialverify.GovInvitationRequest) (*officialverify.GovInvitationResult, error)
	AcceptGovInvitation(ctx context.Context, userID uuid.UUID, code string) (*officialverify.OrgStatus, error)
}

// WithOfficialVerification wires the official-mark routes. Unwired, the user
// routes answer 503 and the service routes are not registered.
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

func (h *Handler) registerOfficialVerificationInternalRoutes(r *gin.Engine) {
	if h.internalKey == "" || h.official == nil {
		return
	}
	guard := requireInternalServiceCaller(h.internalKey)
	r.POST(InternalOrgReviewPath, guard, h.ReviewOrganisationApplication)
	r.POST(InternalGovInvitationPath, guard, h.CreateGovernmentInvitation)
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

// ReviewOrganisationApplication — service route, see the file comment.
func (h *Handler) ReviewOrganisationApplication(c *gin.Context) {
	appID, err := uuid.Parse(c.Param("applicationId"))
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_REQUEST", "invalid application id", nil, nil)
		return
	}
	var d officialverify.ReviewDecision
	if err := c.ShouldBindJSON(&d); err != nil {
		h.writeBindError(c, err)
		return
	}
	h.logger().Info("official verification review requested", "application_id", appID,
		"reviewer_id", d.ReviewerID, "caller", c.GetHeader(callerServiceHeader))
	out, err := h.official.ReviewOrganisation(c.Request.Context(), appID, d)
	h.writeOfficial(c, http.StatusOK, out, err)
}

// CreateGovernmentInvitation — service route, see the file comment.
func (h *Handler) CreateGovernmentInvitation(c *gin.Context) {
	var req officialverify.GovInvitationRequest
	if err := c.ShouldBindJSON(&req); err != nil || req.UserID == uuid.Nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_REQUEST", "user_id, official_name and approver_ids are required", nil, nil)
		return
	}
	h.logger().Info("government invitation requested", "user_id", req.UserID, "caller", c.GetHeader(callerServiceHeader))
	out, err := h.official.CreateGovInvitation(c.Request.Context(), req)
	h.writeOfficial(c, http.StatusCreated, out, err)
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
