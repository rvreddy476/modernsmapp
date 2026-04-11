package http

import (
	"context"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/atpost/admin-service/internal/service"
	"github.com/atpost/admin-service/internal/store/postgres"
	"github.com/atpost/shared/api"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

type adminService interface {
	TakedownContent(ctx context.Context, actor string, entityType, entityID, reason string) error
	SuspendUser(ctx context.Context, actor string, userID uuid.UUID, until time.Time, reason string) error
	GetDashboard(ctx context.Context) (*postgres.DashboardStats, error)
	GetAuditLogs(ctx context.Context, limit, offset int) ([]postgres.AuditLog, int, error)
	ListReports(ctx context.Context, status string, limit, offset int) ([]postgres.Report, int, error)
	ListSuspensions(ctx context.Context, limit, offset int) ([]postgres.Suspension, int, error)
	UnsuspendUser(ctx context.Context, actor string, userID uuid.UUID) error
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
	CreateOAuthClient(ctx context.Context, client *postgres.OAuthClient) error
	GetOAuthClientByClientID(ctx context.Context, clientID string) (*postgres.OAuthClient, error)
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

type Handler struct {
	svc adminService
}

func New(svc adminService) *Handler {
	return &Handler{svc: svc}
}

func (h *Handler) RegisterRoutes(r *gin.Engine) {
	v1 := r.Group("/v1/admin")
	{
		v1.GET("/dashboard", h.GetDashboard)
		v1.GET("/audit-log", h.GetAuditLog)
		v1.GET("/reports", h.ListReports)
		v1.GET("/suspensions", h.ListSuspensions)
		v1.POST("/takedown", h.TakedownContent)
		v1.POST("/users/:userId/suspend", h.SuspendUser)
		v1.DELETE("/users/:userId/suspend", h.UnsuspendUser)

		// Data export
		v1.POST("/data-export", h.RequestDataExport)
		v1.GET("/data-export/:id", h.GetDataExportStatus)
	}

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

type TakedownRequest struct {
	EntityType string `json:"entity_type" binding:"required,oneof=post comment user message"`
	EntityID   string `json:"entity_id" binding:"required"`
	Reason     string `json:"reason" binding:"required"`
}

func (h *Handler) TakedownContent(c *gin.Context) {
	if !requireAnyScope(c, "admin", "superadmin") {
		return
	}

	var req TakedownRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "BAD_REQUEST", err.Error(), nil, nil)
		return
	}

	// In V1, we assume the API Key user is "system-admin"
	adminActor := "system-admin"

	if err := h.svc.TakedownContent(c.Request.Context(), adminActor, req.EntityType, req.EntityID, req.Reason); err != nil {
		slog.Error("Takedown error", "error", err)
		api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", "Takedown failed", nil, nil)
		return
	}

	api.JSON(c.Writer, http.StatusOK, map[string]string{"status": "taken_down"}, nil)
}

type SuspendRequest struct {
	Until  time.Time `json:"until" binding:"required"`
	Reason string    `json:"reason" binding:"required"`
}

func (h *Handler) SuspendUser(c *gin.Context) {
	if !requireAnyScope(c, "admin", "superadmin") {
		return
	}

	userIDStr := c.Param("userId")
	userID, err := uuid.Parse(userIDStr)
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "BAD_REQUEST", "Invalid User ID", nil, nil)
		return
	}

	var req SuspendRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "BAD_REQUEST", err.Error(), nil, nil)
		return
	}

	adminActor := "system-admin"

	if err := h.svc.SuspendUser(c.Request.Context(), adminActor, userID, req.Until, req.Reason); err != nil {
		slog.Error("Suspend error", "error", err)
		api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", "Suspension failed", nil, nil)
		return
	}

	api.JSON(c.Writer, http.StatusOK, map[string]string{"status": "suspended"}, nil)
}

// GetDashboard returns aggregate platform stats.
func (h *Handler) GetDashboard(c *gin.Context) {
	if !requireAnyScope(c, "admin", "superadmin") {
		return
	}

	stats, err := h.svc.GetDashboard(c.Request.Context())
	if err != nil {
		slog.Error("Dashboard error", "error", err)
		api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to load dashboard", nil, nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, stats, nil)
}

// GetAuditLog returns paginated audit log entries.
func (h *Handler) GetAuditLog(c *gin.Context) {
	if !requireAnyScope(c, "admin", "superadmin") {
		return
	}

	limit, offset := parsePagination(c)

	logs, total, err := h.svc.GetAuditLogs(c.Request.Context(), limit, offset)
	if err != nil {
		slog.Error("Audit log error", "error", err)
		api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to load audit log", nil, nil)
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
	if !requireAnyScope(c, "moderator", "admin", "superadmin") {
		return
	}

	limit, offset := parsePagination(c)
	status := c.Query("status")

	reports, total, err := h.svc.ListReports(c.Request.Context(), status, limit, offset)
	if err != nil {
		slog.Error("List reports error", "error", err)
		api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to load reports", nil, nil)
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
	if !requireAnyScope(c, "admin", "superadmin") {
		return
	}

	limit, offset := parsePagination(c)

	suspensions, total, err := h.svc.ListSuspensions(c.Request.Context(), limit, offset)
	if err != nil {
		slog.Error("List suspensions error", "error", err)
		api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to load suspensions", nil, nil)
		return
	}

	api.JSON(c.Writer, http.StatusOK, map[string]interface{}{
		"items":  suspensions,
		"total":  total,
		"limit":  limit,
		"offset": offset,
	}, nil)
}

// UnsuspendUser removes a user's suspension.
func (h *Handler) UnsuspendUser(c *gin.Context) {
	if !requireAnyScope(c, "admin", "superadmin") {
		return
	}

	userIDStr := c.Param("userId")
	userID, err := uuid.Parse(userIDStr)
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "BAD_REQUEST", "Invalid User ID", nil, nil)
		return
	}

	adminActor := "system-admin"

	if err := h.svc.UnsuspendUser(c.Request.Context(), adminActor, userID); err != nil {
		slog.Error("Unsuspend error", "error", err)
		api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", "Unsuspend failed", nil, nil)
		return
	}

	api.JSON(c.Writer, http.StatusOK, map[string]string{"status": "unsuspended"}, nil)
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
		api.Error(c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Invalid user ID", nil, nil)
		return
	}

	req, err := h.svc.RequestDataExport(c.Request.Context(), userID)
	if err != nil {
		slog.Error("RequestDataExport error", "error", err)
		api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to create export request", nil, nil)
		return
	}

	api.JSON(c.Writer, http.StatusCreated, req, nil)
}

// GetDataExportStatus handles GET /v1/admin/data-export/:id.
func (h *Handler) GetDataExportStatus(c *gin.Context) {
	userIDStr := c.GetHeader("X-User-Id")
	userID, err := uuid.Parse(userIDStr)
	if err != nil {
		api.Error(c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Invalid user ID", nil, nil)
		return
	}

	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid export request ID", nil, nil)
		return
	}

	req, err := h.svc.GetDataExportStatus(c.Request.Context(), id, userID)
	if err != nil {
		if err.Error() == "NOT_FOUND" {
			api.Error(c.Writer, http.StatusNotFound, "NOT_FOUND", "Export request not found", nil, nil)
			return
		}
		api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to get export status", nil, nil)
		return
	}
	if req == nil {
		api.Error(c.Writer, http.StatusNotFound, "NOT_FOUND", "Export request not found", nil, nil)
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
		api.Error(c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Invalid developer ID", nil, nil)
		return
	}

	var req CreateMiniAppRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_REQUEST", err.Error(), nil, nil)
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
		api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil, nil)
		return
	}

	api.JSON(c.Writer, http.StatusCreated, app, nil)
}

func (h *Handler) ListMiniApps(c *gin.Context) {
	limit, offset := parsePagination(c)
	category := c.Query("category")

	apps, err := h.svc.ListMiniApps(c.Request.Context(), category, limit, offset)
	if err != nil {
		api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil, nil)
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
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid app ID", nil, nil)
		return
	}

	app, err := h.svc.GetMiniApp(c.Request.Context(), id)
	if err != nil {
		api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil, nil)
		return
	}
	if app == nil {
		api.Error(c.Writer, http.StatusNotFound, "NOT_FOUND", "App not found", nil, nil)
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
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid app ID", nil, nil)
		return
	}

	var req UpdateAppStatusRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_REQUEST", err.Error(), nil, nil)
		return
	}

	if err := h.svc.UpdateMiniAppStatus(c.Request.Context(), id, req.Status); err != nil {
		api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil, nil)
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
		api.Error(c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Invalid user ID", nil, nil)
		return
	}

	appID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid app ID", nil, nil)
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
			api.Error(c.Writer, http.StatusBadRequest, "APP_NOT_AVAILABLE", "App is not available for installation", nil, nil)
			return
		}
		if err.Error() == "INVALID_PERMISSIONS" {
			api.Error(c.Writer, http.StatusBadRequest, "INVALID_PERMISSIONS", "Granted permissions must be a subset of the app permissions", nil, nil)
			return
		}
		api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil, nil)
		return
	}

	api.JSON(c.Writer, http.StatusCreated, inst, nil)
}

func (h *Handler) UninstallApp(c *gin.Context) {
	userID, err := uuid.Parse(c.GetHeader("X-User-Id"))
	if err != nil {
		api.Error(c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Invalid user ID", nil, nil)
		return
	}

	appID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid app ID", nil, nil)
		return
	}

	if err := h.svc.UninstallApp(c.Request.Context(), appID, userID); err != nil {
		api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil, nil)
		return
	}

	api.JSON(c.Writer, http.StatusOK, map[string]string{"status": "uninstalled"}, nil)
}

func (h *Handler) GetUserInstalledApps(c *gin.Context) {
	userID, err := uuid.Parse(c.GetHeader("X-User-Id"))
	if err != nil {
		api.Error(c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Invalid user ID", nil, nil)
		return
	}

	apps, err := h.svc.GetUserInstalledApps(c.Request.Context(), userID)
	if err != nil {
		api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil, nil)
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
		api.Error(c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Invalid user ID", nil, nil)
		return
	}

	appID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid app ID", nil, nil)
		return
	}

	session, err := h.svc.CreateMiniAppSession(c.Request.Context(), appID, userID)
	if err != nil {
		switch err.Error() {
		case "APP_NOT_INSTALLED":
			api.Error(c.Writer, http.StatusForbidden, "APP_NOT_INSTALLED", "App must be installed before requesting a session", nil, nil)
			return
		case "APP_NOT_AVAILABLE":
			api.Error(c.Writer, http.StatusBadRequest, "APP_NOT_AVAILABLE", "App is not available for runtime sessions", nil, nil)
			return
		case "SESSION_UNAVAILABLE":
			api.Error(c.Writer, http.StatusServiceUnavailable, "SESSION_UNAVAILABLE", "Mini app sessions are not configured on this environment", nil, nil)
			return
		default:
			api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil, nil)
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
		api.Error(c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Invalid developer ID", nil, nil)
		return
	}

	var req CreateOAuthClientRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_REQUEST", err.Error(), nil, nil)
		return
	}

	client := &postgres.OAuthClient{
		DeveloperID:      developerID,
		Name:             req.Name,
		ClientID:         req.ClientID,
		ClientSecretHash: req.ClientSecret, // In production: hash this
		RedirectURIs:     req.RedirectURIs,
		Scopes:           req.Scopes,
	}
	if client.RedirectURIs == nil {
		client.RedirectURIs = []string{}
	}
	if client.Scopes == nil {
		client.Scopes = []string{}
	}

	if err := h.svc.CreateOAuthClient(c.Request.Context(), client); err != nil {
		api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil, nil)
		return
	}

	// Return without secret hash
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
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_REQUEST", "client_id is required", nil, nil)
		return
	}

	client, err := h.svc.GetOAuthClientByClientID(c.Request.Context(), clientID)
	if err != nil {
		api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil, nil)
		return
	}
	if client == nil || !client.IsActive {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_CLIENT", "Unknown or inactive client", nil, nil)
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

// OAuthToken is a stub for the token exchange endpoint.
func (h *Handler) OAuthToken(c *gin.Context) {
	api.JSON(c.Writer, http.StatusOK, map[string]interface{}{
		"token_type":   "Bearer",
		"access_token": "stub_token_exchange_not_implemented",
		"expires_in":   3600,
	}, nil)
}
