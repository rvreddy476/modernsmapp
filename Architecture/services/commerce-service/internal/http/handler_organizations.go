// Phase 5 — B2B organization HTTP handlers. Mounted on /v1/commerce/organizations.
package http

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/atpost/commerce-service/internal/service"
	"github.com/atpost/commerce-service/internal/store/postgres"
	"github.com/atpost/shared/api"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

func (h *Handler) RegisterOrganizationRoutes(v1 *gin.RouterGroup) {
	v1.POST("/organizations", h.CreateOrganization)
	v1.GET("/organizations/me", h.ListMyOrganizations)
	v1.GET("/organizations/:orgId", h.GetOrganization)
	v1.PATCH("/organizations/:orgId", h.UpdateOrganization)
	v1.GET("/organizations/:orgId/members", h.ListOrganizationMembers)
	v1.POST("/organizations/:orgId/members", h.InviteOrganizationMember)
	v1.PATCH("/organizations/:orgId/members/:userId", h.UpdateOrganizationMemberRole)
	v1.DELETE("/organizations/:orgId/members/:userId", h.RemoveOrganizationMember)
	v1.POST("/organizations/invites/:token/accept", h.AcceptOrganizationInvite)
	// Phase 5.3 — approval routing
	v1.GET("/organizations/:orgId/orders", h.ListOrganizationOrders)
	v1.GET("/organizations/:orgId/orders/pending-approval", h.ListOrgPendingApprovals)
	v1.POST("/orders/:orderId/approve", h.ApproveOrgOrder)
	v1.POST("/orders/:orderId/reject", h.RejectOrgOrder)
}

type createOrgReq struct {
	Name              string     `json:"name" binding:"required"`
	LegalName         *string    `json:"legal_name"`
	GSTIN             *string    `json:"gstin"`
	PAN               *string    `json:"pan"`
	BillingEmail      *string    `json:"billing_email"`
	BillingPhone      *string    `json:"billing_phone"`
	BillingAddressID  *uuid.UUID `json:"billing_address_id"`
	ApprovalThreshold *float64   `json:"approval_threshold"`
	CreditTermsDays   int        `json:"credit_terms_days"`
	CreditLimit       *float64   `json:"credit_limit"`
}

func (h *Handler) CreateOrganization(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}
	var req createOrgReq
	if err := c.ShouldBindJSON(&req); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_BODY", err.Error(), nil)
		return
	}
	org, err := h.svc.CreateOrganization(c.Request.Context(), userID, service.CreateOrganizationInput{
		Name: req.Name, LegalName: req.LegalName, GSTIN: req.GSTIN, PAN: req.PAN,
		BillingEmail: req.BillingEmail, BillingPhone: req.BillingPhone,
		BillingAddressID: req.BillingAddressID, ApprovalThreshold: req.ApprovalThreshold,
		CreditTermsDays: req.CreditTermsDays, CreditLimit: req.CreditLimit,
	})
	if err != nil {
		handleErr(c, err)
		return
	}
	api.JSON(c.Writer, http.StatusCreated, org, nil)
}

func (h *Handler) ListMyOrganizations(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}
	orgs, err := h.svc.ListMyOrganizations(c.Request.Context(), userID)
	if err != nil {
		handleErr(c, err)
		return
	}
	if orgs == nil {
		orgs = []*postgres.Organization{}
	}
	api.JSON(c.Writer, http.StatusOK, gin.H{"organizations": orgs}, nil)
}

func (h *Handler) GetOrganization(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}
	orgID, ok := parseUUID(c, "orgId")
	if !ok {
		return
	}
	org, err := h.svc.GetOrganization(c.Request.Context(), orgID, userID)
	if err != nil {
		handleOrgErr(c, err)
		return
	}
	api.JSON(c.Writer, http.StatusOK, org, nil)
}

type updateOrgReq struct {
	Name              string     `json:"name"`
	LegalName         *string    `json:"legal_name"`
	GSTIN             *string    `json:"gstin"`
	PAN               *string    `json:"pan"`
	BillingEmail      *string    `json:"billing_email"`
	BillingPhone      *string    `json:"billing_phone"`
	BillingAddressID  *uuid.UUID `json:"billing_address_id"`
	ApprovalThreshold *float64   `json:"approval_threshold"`
	CreditTermsDays   int        `json:"credit_terms_days"`
	CreditLimit       *float64   `json:"credit_limit"`
}

func (h *Handler) UpdateOrganization(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}
	orgID, ok := parseUUID(c, "orgId")
	if !ok {
		return
	}
	var req updateOrgReq
	if err := c.ShouldBindJSON(&req); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_BODY", err.Error(), nil)
		return
	}
	patch := &postgres.Organization{
		Name: req.Name, LegalName: req.LegalName, GSTIN: req.GSTIN, PAN: req.PAN,
		BillingEmail: req.BillingEmail, BillingPhone: req.BillingPhone,
		BillingAddressID: req.BillingAddressID, ApprovalThreshold: req.ApprovalThreshold,
		CreditTermsDays: req.CreditTermsDays, CreditLimit: req.CreditLimit,
	}
	out, err := h.svc.UpdateOrganization(c.Request.Context(), orgID, userID, patch)
	if err != nil {
		handleOrgErr(c, err)
		return
	}
	api.JSON(c.Writer, http.StatusOK, out, nil)
}

func (h *Handler) ListOrganizationMembers(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}
	orgID, ok := parseUUID(c, "orgId")
	if !ok {
		return
	}
	members, err := h.svc.ListOrganizationMembers(c.Request.Context(), orgID, userID)
	if err != nil {
		handleOrgErr(c, err)
		return
	}
	if members == nil {
		members = []*postgres.OrganizationMember{}
	}
	api.JSON(c.Writer, http.StatusOK, gin.H{"members": members}, nil)
}

type inviteMemberReq struct {
	Email string `json:"email" binding:"required"`
	Role  string `json:"role" binding:"required"`
}

func (h *Handler) InviteOrganizationMember(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}
	orgID, ok := parseUUID(c, "orgId")
	if !ok {
		return
	}
	var req inviteMemberReq
	if err := c.ShouldBindJSON(&req); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_BODY", err.Error(), nil)
		return
	}
	inv, err := h.svc.InviteMember(c.Request.Context(), orgID, userID, req.Email, req.Role)
	if err != nil {
		handleOrgErr(c, err)
		return
	}
	api.JSON(c.Writer, http.StatusCreated, inv, nil)
}

func (h *Handler) AcceptOrganizationInvite(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}
	token := c.Param("token")
	inv, err := h.svc.AcceptInvite(c.Request.Context(), token, userID)
	if err != nil {
		handleOrgErr(c, err)
		return
	}
	api.JSON(c.Writer, http.StatusOK, inv, nil)
}

type updateMemberRoleReq struct {
	Role string `json:"role" binding:"required"`
}

func (h *Handler) UpdateOrganizationMemberRole(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}
	orgID, ok := parseUUID(c, "orgId")
	if !ok {
		return
	}
	targetID, ok := parseUUID(c, "userId")
	if !ok {
		return
	}
	var req updateMemberRoleReq
	if err := c.ShouldBindJSON(&req); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_BODY", err.Error(), nil)
		return
	}
	if err := h.svc.UpdateMemberRole(c.Request.Context(), orgID, userID, targetID, req.Role); err != nil {
		handleOrgErr(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

func (h *Handler) RemoveOrganizationMember(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}
	orgID, ok := parseUUID(c, "orgId")
	if !ok {
		return
	}
	targetID, ok := parseUUID(c, "userId")
	if !ok {
		return
	}
	if err := h.svc.RemoveMember(c.Request.Context(), orgID, userID, targetID); err != nil {
		handleOrgErr(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

// ─── Phase 5.3 — approval routing handlers ───────────────────

func (h *Handler) ListOrganizationOrders(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}
	orgID, ok := parseUUID(c, "orgId")
	if !ok {
		return
	}
	status := c.Query("status")
	limit := parseIntDefault(c.Query("limit"), 20)
	offset := parseIntDefault(c.Query("offset"), 0)
	orders, err := h.svc.ListOrgOrders(c.Request.Context(), orgID, userID, status, limit, offset)
	if err != nil {
		handleOrgErr(c, err)
		return
	}
	if orders == nil {
		orders = []*postgres.Order{}
	}
	api.JSON(c.Writer, http.StatusOK, gin.H{"orders": orders}, nil)
}

func (h *Handler) ListOrgPendingApprovals(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}
	orgID, ok := parseUUID(c, "orgId")
	if !ok {
		return
	}
	limit := parseIntDefault(c.Query("limit"), 20)
	offset := parseIntDefault(c.Query("offset"), 0)
	orders, err := h.svc.ListOrgPendingApprovals(c.Request.Context(), orgID, userID, limit, offset)
	if err != nil {
		handleOrgErr(c, err)
		return
	}
	if orders == nil {
		orders = []*postgres.Order{}
	}
	api.JSON(c.Writer, http.StatusOK, gin.H{"orders": orders}, nil)
}

type approvalActionReq struct {
	Notes  string `json:"notes"`
	Reason string `json:"reason"`
}

func (h *Handler) ApproveOrgOrder(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}
	orderID, ok := parseUUID(c, "orderId")
	if !ok {
		return
	}
	var req approvalActionReq
	_ = c.ShouldBindJSON(&req)
	order, err := h.svc.ApproveOrgOrder(c.Request.Context(), orderID, userID, req.Notes)
	if err != nil {
		handleOrgErr(c, err)
		return
	}
	api.JSON(c.Writer, http.StatusOK, order, nil)
}

func (h *Handler) RejectOrgOrder(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}
	orderID, ok := parseUUID(c, "orderId")
	if !ok {
		return
	}
	var req approvalActionReq
	_ = c.ShouldBindJSON(&req)
	if req.Reason == "" {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "REASON_REQUIRED", "reason is required", nil)
		return
	}
	order, err := h.svc.RejectOrgOrder(c.Request.Context(), orderID, userID, req.Reason)
	if err != nil {
		handleOrgErr(c, err)
		return
	}
	api.JSON(c.Writer, http.StatusOK, order, nil)
}

// parseIntDefault parses a query string as int with fallback. Local helper
// since the package's own version isn't exported.
func parseIntDefault(s string, def int) int {
	if s == "" {
		return def
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		return def
	}
	return n
}

// handleOrgErr maps the package's sentinel errors to HTTP codes so the
// generic handleErr (which 500s by default) doesn't swallow forbidden /
// not-found semantics.
func handleOrgErr(c *gin.Context, err error) {
	ctx := c.Request.Context()
	switch {
	case errors.Is(err, service.ErrOrgForbidden):
		api.ErrorWithContext(ctx, c.Writer, http.StatusForbidden, "FORBIDDEN", err.Error(), nil)
	case errors.Is(err, service.ErrOrgNotFound):
		api.ErrorWithContext(ctx, c.Writer, http.StatusNotFound, "NOT_FOUND", err.Error(), nil)
	case errors.Is(err, service.ErrOrgInvalidRole):
		api.ErrorWithContext(ctx, c.Writer, http.StatusBadRequest, "INVALID_ROLE", err.Error(), nil)
	case errors.Is(err, service.ErrOrgLastAdmin):
		api.ErrorWithContext(ctx, c.Writer, http.StatusConflict, "LAST_ADMIN", err.Error(), nil)
	case errors.Is(err, service.ErrOrgInviteNotFound):
		api.ErrorWithContext(ctx, c.Writer, http.StatusGone, "INVITE_EXPIRED", err.Error(), nil)
	default:
		handleErr(c, err)
	}
}
