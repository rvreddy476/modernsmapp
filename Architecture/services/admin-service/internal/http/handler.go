package http

import (
	"context"
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	"github.com/atpost/admin-service/internal/approvals"
	"github.com/atpost/admin-service/internal/service"
	"github.com/atpost/admin-service/internal/store/postgres"
	"github.com/atpost/shared/api"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

type adminService interface {
	GetDashboard(ctx context.Context) (*postgres.DashboardStats, error)
	GetAuditLogs(ctx context.Context, limit, offset int) ([]postgres.AuditLog, int, error)
	ListAuditTrail(ctx context.Context, f postgres.AuditTrailFilter) (postgres.AuditTrailPage, error)
	ListReports(ctx context.Context, status string, limit, offset int) ([]postgres.Report, int, error)
	ListSuspensions(ctx context.Context, limit, offset int) ([]postgres.Suspension, int, error)
	RequestDataExport(ctx context.Context, userID uuid.UUID) (*postgres.DataExportRequest, error)
	GetDataExportStatus(ctx context.Context, id, userID uuid.UUID) (*postgres.DataExportRequest, error)
	CreateMiniApp(ctx context.Context, app *postgres.MiniApp) error
	ListMiniApps(ctx context.Context, category string, limit, offset int) ([]postgres.MiniApp, error)
	GetMiniApp(ctx context.Context, id uuid.UUID) (*postgres.MiniApp, error)
	UpdateMiniAppStatus(ctx context.Context, id uuid.UUID, status string) error
	InstallApp(ctx context.Context, appID, userID uuid.UUID, permissions []string) (*postgres.AppInstallation, error)
	UninstallApp(ctx context.Context, appID, userID uuid.UUID) error
	GetUserInstalledApps(ctx context.Context, userID uuid.UUID) ([]postgres.MiniApp, error)
	CreateMiniAppSession(ctx context.Context, appID, userID uuid.UUID) (*service.MiniAppSession, error)
	CreateOAuthClient(ctx context.Context, client *postgres.OAuthClient, secret string) error
	GetOAuthClientByClientID(ctx context.Context, clientID string) (*postgres.OAuthClient, error)
	auditRecorder
}

// hasScope reports whether the space-separated scopes string contains the exact target scope.
func hasScope(scopes, target string) bool {
	for _, s := range strings.Fields(scopes) {
		if s == target {
			return true
		}
	}
	return false
}

// requireAnyScope is the X-Scopes compatibility check. No /v1/admin route uses
// it any more (identity permissions via the gate are the only source there); it
// remains only on the mini-app status route outside /v1/admin until that moves.
// requireAnyScope returns true and continues if the user has any of the given scopes.
// It writes a 403 and returns false otherwise.
func requireAnyScope(c *gin.Context, scopes ...string) bool {
	userScopes := c.GetHeader("X-Scopes")
	for _, scope := range scopes {
		if hasScope(userScopes, scope) {
			return true
		}
	}
	c.JSON(http.StatusForbidden, gin.H{"error": gin.H{
		"code":    "FORBIDDEN",
		"message": "Insufficient scope. Required: " + strings.Join(scopes, " or "),
	}})
	return false
}

// Handler serves admin-service's HTTP routes. Every /v1/admin route is declared
// through gate, which resolves the caller's permissions, enforces MFA, step-up
// and the declared permission, and writes the request's audit row.
type Handler struct {
	svc       adminService
	gate      *Gate
	approvals *approvals.Service
	// Product clients for each application's token-only admin family. A nil
	// client, or one without a signing key, keeps the routes declared; each
	// answers 503 PRODUCT_UNAVAILABLE.
	dating   *service.ProductClient
	food     *service.ProductClient
	commerce *service.ProductClient
	trust    *service.ProductClient
	// Money: monetization and payments.
	monetization *service.ProductClient
	payments     *service.ProductClient
	// refundThresholdPaise: a Feast or monetization refund, or a payments
	// refund resolved as money, at or above it is two-person.
	refundThresholdPaise int64
}

// WithMonetization installs the Monetization client.
func (h *Handler) WithMonetization(mc *service.ProductClient) *Handler {
	h.monetization = mc
	return h
}

// WithPayments installs the Payments client.
func (h *Handler) WithPayments(pc *service.ProductClient) *Handler {
	h.payments = pc
	return h
}

// WithDating installs the Dating client.
func (h *Handler) WithDating(dc *service.ProductClient) *Handler {
	h.dating = dc
	return h
}

// WithFood installs the Feast client and the refund two-person threshold
// (paise; <= 0 means DefaultRefundTwoPersonThresholdPaise).
func (h *Handler) WithFood(fc *service.ProductClient, refundThresholdPaise int64) *Handler {
	h.food = fc
	if refundThresholdPaise > 0 {
		h.refundThresholdPaise = refundThresholdPaise
	}
	return h
}

// WithCommerce installs the MStore client.
func (h *Handler) WithCommerce(cc *service.ProductClient) *Handler {
	h.commerce = cc
	return h
}

// WithTrustSafety installs the Trust & safety client.
func (h *Handler) WithTrustSafety(tc *service.ProductClient) *Handler {
	h.trust = tc
	return h
}

func New(svc adminService, gate *Gate, appr *approvals.Service) *Handler {
	return &Handler{svc: svc, gate: gate, approvals: appr, refundThresholdPaise: DefaultRefundTwoPersonThresholdPaise}
}

// RegisterAllRoutes registers every route and refuses a table with an
// undeclared /v1/admin route. main exits on the error.
func (h *Handler) RegisterAllRoutes(r *gin.Engine) error {
	h.RegisterRoutes(r)
	h.RegisterMeRoute(r)
	h.RegisterApprovalRoutes(r)
	h.RegisterAuditRoute(r)
	h.RegisterCommerceRoutes(r)
	h.RegisterCatalogueRoutes(r)
	h.RegisterDatingRoutes(r, h.dating)
	h.RegisterFoodRoutes(r)
	h.RegisterTrustRoutes(r)
	h.RegisterMonetizationRoutes(r)
	h.RegisterPaymentsRoutes(r)
	if err := h.gate.VerifyDeclared(r); err != nil {
		return err
	}
	return h.gate.VerifyExecutors(h.approvals.HasExecutor)
}

func (h *Handler) RegisterRoutes(r *gin.Engine) {
	g := h.gate
	g.Handle(r, http.MethodGet, "/v1/admin/dashboard",
		Requirement{Operation: "dashboard.read", Permission: "platform:users.read"}, h.GetDashboard)
	g.Handle(r, http.MethodGet, "/v1/admin/audit-log",
		Requirement{Operation: "audit.read", Permission: "*:audit.read"}, h.GetAuditLog)
	g.Handle(r, http.MethodGet, "/v1/admin/reports",
		Requirement{Operation: "reports.read", Permission: "trust_safety:reports.read"}, h.ListReports)
	g.Handle(r, http.MethodGet, "/v1/admin/suspensions",
		Requirement{Operation: "suspensions.read", Permission: "platform:users.read"}, h.ListSuspensions)

	// Retired: platform-wide takedown and suspension were hard-disabled here.
	// Enforcement belongs to each application's admin routes (Wave 2) and to
	// identity for platform-wide user actions.
	for _, rt := range []struct{ method, path string }{
		{http.MethodPost, "/v1/admin/takedown"},
		{http.MethodPost, "/v1/admin/users/:userId/suspend"},
		{http.MethodDelete, "/v1/admin/users/:userId/suspend"},
	} {
		g.Handle(r, rt.method, rt.path, Requirement{Operation: "retired", Access: AccessRetired}, nil)
	}

	// Data export is a signed-in user asking for their own data, not an admin
	// action; it is declared self-service and skips the admin gate.
	g.Handle(r, http.MethodPost, "/v1/admin/data-export",
		Requirement{Operation: "data_export.request", Access: AccessSelfService}, h.RequestDataExport)
	g.Handle(r, http.MethodGet, "/v1/admin/data-export/:id",
		Requirement{Operation: "data_export.status", Access: AccessSelfService}, h.GetDataExportStatus)

	// Mini Apps
	apps := r.Group("/v1/apps")
	{
		apps.POST("", h.CreateMiniApp)
		apps.GET("", h.ListMiniApps)
		apps.GET("/installed", h.GetUserInstalledApps)
		apps.GET("/:id/session", h.GetMiniAppSession)
		apps.GET("/:id", h.GetMiniApp)
		apps.PATCH("/:id/status", h.UpdateMiniAppStatus)
		apps.POST("/:id/install", h.InstallApp)
		apps.DELETE("/:id/install", h.UninstallApp)
	}

	// OAuth
	oauth := r.Group("/v1/oauth")
	{
		oauth.POST("/clients", h.CreateOAuthClient)
		oauth.GET("/authorize", h.OAuthAuthorize)
		oauth.POST("/token", h.OAuthToken)
	}
}

// GetDashboard returns aggregate platform stats.
func (h *Handler) GetDashboard(c *gin.Context) {
	stats, err := h.svc.GetDashboard(c.Request.Context())
	if err != nil {
		slog.Error("Dashboard error", "error", err)
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to load dashboard", nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, stats, nil)
}

// GetAuditLog returns paginated audit log entries.
func (h *Handler) GetAuditLog(c *gin.Context) {
	limit, offset := parsePagination(c)

	logs, total, err := h.svc.GetAuditLogs(c.Request.Context(), limit, offset)
	if err != nil {
		slog.Error("Audit log error", "error", err)
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to load audit log", nil)
		return
	}

	api.JSON(c.Writer, http.StatusOK, map[string]interface{}{
		"items":  logs,
		"total":  total,
		"limit":  limit,
		"offset": offset,
	}, nil)
}

// ListReports returns paginated reports, optionally filtered by status.
func (h *Handler) ListReports(c *gin.Context) {
	limit, offset := parsePagination(c)
	status := c.Query("status")

	reports, total, err := h.svc.ListReports(c.Request.Context(), status, limit, offset)
	if err != nil {
		slog.Error("List reports error", "error", err)
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to load reports", nil)
		return
	}

	api.JSON(c.Writer, http.StatusOK, map[string]interface{}{
		"items":  reports,
		"total":  total,
		"limit":  limit,
		"offset": offset,
	}, nil)
}

// ListSuspensions returns paginated active suspensions.
func (h *Handler) ListSuspensions(c *gin.Context) {
	limit, offset := parsePagination(c)

	suspensions, total, err := h.svc.ListSuspensions(c.Request.Context(), limit, offset)
	if err != nil {
		slog.Error("List suspensions error", "error", err)
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to load suspensions", nil)
		return
	}

	api.JSON(c.Writer, http.StatusOK, map[string]interface{}{
		"items":  suspensions,
		"total":  total,
		"limit":  limit,
		"offset": offset,
	}, nil)
}

// parsePagination extracts limit and offset query params with defaults.
func parsePagination(c *gin.Context) (int, int) {
	limit := 20
	if l, err := strconv.Atoi(c.Query("limit")); err == nil && l > 0 && l <= 100 {
		limit = l
	}
	offset := 0
	if o, err := strconv.Atoi(c.Query("offset")); err == nil && o >= 0 {
		offset = o
	}
	return limit, offset
}

// --- Data Export ---

// RequestDataExport handles POST /v1/admin/data-export.
// Users request their own data export.
func (h *Handler) RequestDataExport(c *gin.Context) {
	userIDStr := c.GetHeader("X-User-Id")
	userID, err := uuid.Parse(userIDStr)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Invalid user ID", nil)
		return
	}

	req, err := h.svc.RequestDataExport(c.Request.Context(), userID)
	if err != nil {
		slog.Error("RequestDataExport error", "error", err)
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to create export request", nil)
		return
	}

	api.JSON(c.Writer, http.StatusCreated, req, nil)
}

// GetDataExportStatus handles GET /v1/admin/data-export/:id.
func (h *Handler) GetDataExportStatus(c *gin.Context) {
	userIDStr := c.GetHeader("X-User-Id")
	userID, err := uuid.Parse(userIDStr)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Invalid user ID", nil)
		return
	}

	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid export request ID", nil)
		return
	}

	req, err := h.svc.GetDataExportStatus(c.Request.Context(), id, userID)
	if err != nil {
		if err.Error() == "NOT_FOUND" {
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusNotFound, "NOT_FOUND", "Export request not found", nil)
			return
		}
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to get export status", nil)
		return
	}
	if req == nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusNotFound, "NOT_FOUND", "Export request not found", nil)
		return
	}

	api.JSON(c.Writer, http.StatusOK, req, nil)
}

// --- Mini Apps ---

type CreateMiniAppRequest struct {
	Name        string   `json:"name" binding:"required"`
	Description string   `json:"description" binding:"required"`
	IconURL     string   `json:"icon_url"`
	ManifestURL string   `json:"manifest_url" binding:"required"`
	Permissions []string `json:"permissions"`
	Category    string   `json:"category"`
}

func (h *Handler) CreateMiniApp(c *gin.Context) {
	developerIDStr := c.GetHeader("X-User-Id")
	developerID, err := uuid.Parse(developerIDStr)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Invalid developer ID", nil)
		return
	}

	var req CreateMiniAppRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_REQUEST", err.Error(), nil)
		return
	}

	app := &postgres.MiniApp{
		DeveloperID: developerID,
		Name:        req.Name,
		Description: req.Description,
		IconURL:     req.IconURL,
		ManifestURL: req.ManifestURL,
		Permissions: req.Permissions,
		Category:    req.Category,
	}
	if app.Permissions == nil {
		app.Permissions = []string{}
	}

	if err := h.svc.CreateMiniApp(c.Request.Context(), app); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}

	api.JSON(c.Writer, http.StatusCreated, app, nil)
}

func (h *Handler) ListMiniApps(c *gin.Context) {
	limit, offset := parsePagination(c)
	category := c.Query("category")

	apps, err := h.svc.ListMiniApps(c.Request.Context(), category, limit, offset)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	if apps == nil {
		apps = []postgres.MiniApp{}
	}

	api.JSON(c.Writer, http.StatusOK, apps, nil)
}

func (h *Handler) GetMiniApp(c *gin.Context) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid app ID", nil)
		return
	}

	app, err := h.svc.GetMiniApp(c.Request.Context(), id)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	if app == nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusNotFound, "NOT_FOUND", "App not found", nil)
		return
	}

	api.JSON(c.Writer, http.StatusOK, app, nil)
}

type UpdateAppStatusRequest struct {
	Status string `json:"status" binding:"required"`
}

func (h *Handler) UpdateMiniAppStatus(c *gin.Context) {
	if !requireAnyScope(c, "admin", "superadmin") {
		return
	}

	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid app ID", nil)
		return
	}

	var req UpdateAppStatusRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_REQUEST", err.Error(), nil)
		return
	}

	if err := h.svc.UpdateMiniAppStatus(c.Request.Context(), id, req.Status); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}

	api.JSON(c.Writer, http.StatusOK, map[string]string{"status": req.Status}, nil)
}

type InstallAppRequest struct {
	GrantedPermissions []string `json:"granted_permissions"`
}

func (h *Handler) InstallApp(c *gin.Context) {
	userID, err := uuid.Parse(c.GetHeader("X-User-Id"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Invalid user ID", nil)
		return
	}

	appID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid app ID", nil)
		return
	}

	var req InstallAppRequest
	_ = c.ShouldBindJSON(&req)
	if req.GrantedPermissions == nil {
		req.GrantedPermissions = []string{}
	}

	inst, err := h.svc.InstallApp(c.Request.Context(), appID, userID, req.GrantedPermissions)
	if err != nil {
		if err.Error() == "APP_NOT_AVAILABLE" {
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "APP_NOT_AVAILABLE", "App is not available for installation", nil)
			return
		}
		if err.Error() == "INVALID_PERMISSIONS" {
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_PERMISSIONS", "Granted permissions must be a subset of the app permissions", nil)
			return
		}
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}

	api.JSON(c.Writer, http.StatusCreated, inst, nil)
}

func (h *Handler) UninstallApp(c *gin.Context) {
	userID, err := uuid.Parse(c.GetHeader("X-User-Id"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Invalid user ID", nil)
		return
	}

	appID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid app ID", nil)
		return
	}

	if err := h.svc.UninstallApp(c.Request.Context(), appID, userID); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}

	api.JSON(c.Writer, http.StatusOK, map[string]string{"status": "uninstalled"}, nil)
}

func (h *Handler) GetUserInstalledApps(c *gin.Context) {
	userID, err := uuid.Parse(c.GetHeader("X-User-Id"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Invalid user ID", nil)
		return
	}

	apps, err := h.svc.GetUserInstalledApps(c.Request.Context(), userID)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	if apps == nil {
		apps = []postgres.MiniApp{}
	}

	api.JSON(c.Writer, http.StatusOK, apps, nil)
}

func (h *Handler) GetMiniAppSession(c *gin.Context) {
	userID, err := uuid.Parse(c.GetHeader("X-User-Id"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Invalid user ID", nil)
		return
	}

	appID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid app ID", nil)
		return
	}

	session, err := h.svc.CreateMiniAppSession(c.Request.Context(), appID, userID)
	if err != nil {
		switch err.Error() {
		case "APP_NOT_INSTALLED":
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusForbidden, "APP_NOT_INSTALLED", "App must be installed before requesting a session", nil)
			return
		case "APP_NOT_AVAILABLE":
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "APP_NOT_AVAILABLE", "App is not available for runtime sessions", nil)
			return
		case "SESSION_UNAVAILABLE":
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusServiceUnavailable, "SESSION_UNAVAILABLE", "Mini app sessions are not configured on this environment", nil)
			return
		default:
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
			return
		}
	}

	api.JSON(c.Writer, http.StatusOK, session, nil)
}

// --- OAuth ---

type CreateOAuthClientRequest struct {
	Name         string   `json:"name" binding:"required"`
	ClientID     string   `json:"client_id" binding:"required"`
	ClientSecret string   `json:"client_secret" binding:"required"`
	RedirectURIs []string `json:"redirect_uris"`
	Scopes       []string `json:"scopes"`
}

func (h *Handler) CreateOAuthClient(c *gin.Context) {
	developerID, err := uuid.Parse(c.GetHeader("X-User-Id"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Invalid developer ID", nil)
		return
	}

	var req CreateOAuthClientRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_REQUEST", err.Error(), nil)
		return
	}

	client := &postgres.OAuthClient{
		DeveloperID:  developerID,
		Name:         req.Name,
		ClientID:     req.ClientID,
		RedirectURIs: req.RedirectURIs,
		Scopes:       req.Scopes,
	}
	if client.RedirectURIs == nil {
		client.RedirectURIs = []string{}
	}
	if client.Scopes == nil {
		client.Scopes = []string{}
	}

	// The service stores only an argon2id hash of the secret.
	if err := h.svc.CreateOAuthClient(c.Request.Context(), client, req.ClientSecret); err != nil {
		slog.ErrorContext(c.Request.Context(), "create oauth client failed", "error", err)
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to create OAuth client", nil)
		return
	}

	// Never echo the hash back.
	client.ClientSecretHash = ""
	api.JSON(c.Writer, http.StatusCreated, client, nil)
}

// OAuthAuthorize is a stub returning consent page data.
// Full PKCE/authorization-code flow would be implemented here.
func (h *Handler) OAuthAuthorize(c *gin.Context) {
	clientID := c.Query("client_id")
	redirectURI := c.Query("redirect_uri")
	scope := c.Query("scope")
	state := c.Query("state")

	if clientID == "" {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_REQUEST", "client_id is required", nil)
		return
	}

	client, err := h.svc.GetOAuthClientByClientID(c.Request.Context(), clientID)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	if client == nil || !client.IsActive {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_CLIENT", "Unknown or inactive client", nil)
		return
	}

	api.JSON(c.Writer, http.StatusOK, map[string]interface{}{
		"client_name":  client.Name,
		"client_id":    clientID,
		"redirect_uri": redirectURI,
		"scopes":       strings.Fields(scope),
		"state":        state,
	}, nil)
}

// OAuthToken is the token exchange endpoint. It used to answer every request
// with a fixed fake bearer token; until a real authorization-code exchange
// exists (verifying the client secret by hash, the code, PKCE and redirect
// URI), it refuses rather than issue anything.
func (h *Handler) OAuthToken(c *gin.Context) {
	api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusNotImplemented,
		"OAUTH_TOKEN_NOT_IMPLEMENTED", "OAuth token exchange is not implemented", nil)
}
