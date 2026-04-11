package http

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/atpost/admin-service/internal/service"
	"github.com/atpost/admin-service/internal/store/postgres"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

type stubAdminService struct {
	installAppFn           func(ctx context.Context, appID, userID uuid.UUID, permissions []string) (*postgres.AppInstallation, error)
	createMiniAppSessionFn func(ctx context.Context, appID, userID uuid.UUID) (*service.MiniAppSession, error)
	getUserInstalledAppsFn func(ctx context.Context, userID uuid.UUID) ([]postgres.MiniApp, error)
	getMiniAppFn           func(ctx context.Context, id uuid.UUID) (*postgres.MiniApp, error)
	listMiniAppsFn         func(ctx context.Context, category string, limit, offset int) ([]postgres.MiniApp, error)
	createMiniAppFn        func(ctx context.Context, app *postgres.MiniApp) error
	updateMiniAppStatusFn  func(ctx context.Context, id uuid.UUID, status string) error
	uninstallAppFn         func(ctx context.Context, appID, userID uuid.UUID) error
	createOAuthClientFn    func(ctx context.Context, client *postgres.OAuthClient) error
	getOAuthClientByIDFn   func(ctx context.Context, clientID string) (*postgres.OAuthClient, error)
	requestDataExportFn    func(ctx context.Context, userID uuid.UUID) (*postgres.DataExportRequest, error)
	getDataExportStatusFn  func(ctx context.Context, id, userID uuid.UUID) (*postgres.DataExportRequest, error)
	takedownContentFn      func(ctx context.Context, actor string, entityType, entityID, reason string) error
	suspendUserFn          func(ctx context.Context, actor string, userID uuid.UUID, until time.Time, reason string) error
	getDashboardFn         func(ctx context.Context) (*postgres.DashboardStats, error)
	getAuditLogsFn         func(ctx context.Context, limit, offset int) ([]postgres.AuditLog, int, error)
	listReportsFn          func(ctx context.Context, status string, limit, offset int) ([]postgres.Report, int, error)
	listSuspensionsFn      func(ctx context.Context, limit, offset int) ([]postgres.Suspension, int, error)
	unsuspendUserFn        func(ctx context.Context, actor string, userID uuid.UUID) error
}

func (s *stubAdminService) TakedownContent(ctx context.Context, actor string, entityType, entityID, reason string) error {
	if s.takedownContentFn == nil {
		return nil
	}
	return s.takedownContentFn(ctx, actor, entityType, entityID, reason)
}

func (s *stubAdminService) SuspendUser(ctx context.Context, actor string, userID uuid.UUID, until time.Time, reason string) error {
	if s.suspendUserFn == nil {
		return nil
	}
	return s.suspendUserFn(ctx, actor, userID, until, reason)
}

func (s *stubAdminService) GetDashboard(ctx context.Context) (*postgres.DashboardStats, error) {
	if s.getDashboardFn == nil {
		return &postgres.DashboardStats{}, nil
	}
	return s.getDashboardFn(ctx)
}

func (s *stubAdminService) GetAuditLogs(ctx context.Context, limit, offset int) ([]postgres.AuditLog, int, error) {
	if s.getAuditLogsFn == nil {
		return nil, 0, nil
	}
	return s.getAuditLogsFn(ctx, limit, offset)
}

func (s *stubAdminService) ListReports(ctx context.Context, status string, limit, offset int) ([]postgres.Report, int, error) {
	if s.listReportsFn == nil {
		return nil, 0, nil
	}
	return s.listReportsFn(ctx, status, limit, offset)
}

func (s *stubAdminService) ListSuspensions(ctx context.Context, limit, offset int) ([]postgres.Suspension, int, error) {
	if s.listSuspensionsFn == nil {
		return nil, 0, nil
	}
	return s.listSuspensionsFn(ctx, limit, offset)
}

func (s *stubAdminService) UnsuspendUser(ctx context.Context, actor string, userID uuid.UUID) error {
	if s.unsuspendUserFn == nil {
		return nil
	}
	return s.unsuspendUserFn(ctx, actor, userID)
}

func (s *stubAdminService) RequestDataExport(ctx context.Context, userID uuid.UUID) (*postgres.DataExportRequest, error) {
	if s.requestDataExportFn == nil {
		return &postgres.DataExportRequest{}, nil
	}
	return s.requestDataExportFn(ctx, userID)
}

func (s *stubAdminService) GetDataExportStatus(ctx context.Context, id, userID uuid.UUID) (*postgres.DataExportRequest, error) {
	if s.getDataExportStatusFn == nil {
		return &postgres.DataExportRequest{}, nil
	}
	return s.getDataExportStatusFn(ctx, id, userID)
}

func (s *stubAdminService) CreateMiniApp(ctx context.Context, app *postgres.MiniApp) error {
	if s.createMiniAppFn == nil {
		return nil
	}
	return s.createMiniAppFn(ctx, app)
}

func (s *stubAdminService) ListMiniApps(ctx context.Context, category string, limit, offset int) ([]postgres.MiniApp, error) {
	if s.listMiniAppsFn == nil {
		return nil, nil
	}
	return s.listMiniAppsFn(ctx, category, limit, offset)
}

func (s *stubAdminService) GetMiniApp(ctx context.Context, id uuid.UUID) (*postgres.MiniApp, error) {
	if s.getMiniAppFn == nil {
		return nil, nil
	}
	return s.getMiniAppFn(ctx, id)
}

func (s *stubAdminService) UpdateMiniAppStatus(ctx context.Context, id uuid.UUID, status string) error {
	if s.updateMiniAppStatusFn == nil {
		return nil
	}
	return s.updateMiniAppStatusFn(ctx, id, status)
}

func (s *stubAdminService) InstallApp(ctx context.Context, appID, userID uuid.UUID, permissions []string) (*postgres.AppInstallation, error) {
	if s.installAppFn == nil {
		return &postgres.AppInstallation{}, nil
	}
	return s.installAppFn(ctx, appID, userID, permissions)
}

func (s *stubAdminService) UninstallApp(ctx context.Context, appID, userID uuid.UUID) error {
	if s.uninstallAppFn == nil {
		return nil
	}
	return s.uninstallAppFn(ctx, appID, userID)
}

func (s *stubAdminService) GetUserInstalledApps(ctx context.Context, userID uuid.UUID) ([]postgres.MiniApp, error) {
	if s.getUserInstalledAppsFn == nil {
		return nil, nil
	}
	return s.getUserInstalledAppsFn(ctx, userID)
}

func (s *stubAdminService) CreateMiniAppSession(ctx context.Context, appID, userID uuid.UUID) (*service.MiniAppSession, error) {
	if s.createMiniAppSessionFn == nil {
		return nil, nil
	}
	return s.createMiniAppSessionFn(ctx, appID, userID)
}

func (s *stubAdminService) CreateOAuthClient(ctx context.Context, client *postgres.OAuthClient) error {
	if s.createOAuthClientFn == nil {
		return nil
	}
	return s.createOAuthClientFn(ctx, client)
}

func (s *stubAdminService) GetOAuthClientByClientID(ctx context.Context, clientID string) (*postgres.OAuthClient, error) {
	if s.getOAuthClientByIDFn == nil {
		return nil, nil
	}
	return s.getOAuthClientByIDFn(ctx, clientID)
}

func newAdminTestRouter(svc adminService) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	New(svc).RegisterRoutes(r)
	return r
}

func decodeEnvelope(t *testing.T, resp *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var body map[string]any
	if err := json.Unmarshal(resp.Body.Bytes(), &body); err != nil {
		t.Fatalf("failed to decode response body: %v", err)
	}
	return body
}

func TestInstallAppPassesGrantedPermissions(t *testing.T) {
	appID := uuid.New()
	userID := uuid.New()
	called := false

	router := newAdminTestRouter(&stubAdminService{
		installAppFn: func(ctx context.Context, gotAppID, gotUserID uuid.UUID, permissions []string) (*postgres.AppInstallation, error) {
			called = true
			if gotAppID != appID {
				t.Fatalf("unexpected app id: %s", gotAppID)
			}
			if gotUserID != userID {
				t.Fatalf("unexpected user id: %s", gotUserID)
			}
			if len(permissions) != 1 || permissions[0] != "clipboard.write" {
				t.Fatalf("unexpected permissions: %#v", permissions)
			}

			return &postgres.AppInstallation{
				AppID:              gotAppID,
				UserID:             gotUserID,
				GrantedPermissions: permissions,
				InstalledAt:        time.Now().UTC(),
			}, nil
		},
	})

	req := httptest.NewRequest(http.MethodPost, "/v1/apps/"+appID.String()+"/install", bytes.NewBufferString(`{"granted_permissions":["clipboard.write"]}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-User-Id", userID.String())
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)

	if resp.Code != http.StatusCreated {
		t.Fatalf("expected status %d, got %d", http.StatusCreated, resp.Code)
	}
	if !called {
		t.Fatal("expected installApp to be called")
	}

	body := decodeEnvelope(t, resp)
	data, ok := body["data"].(map[string]any)
	if !ok {
		t.Fatalf("expected data object, got %#v", body["data"])
	}
	perms, ok := data["granted_permissions"].([]any)
	if !ok || len(perms) != 1 || perms[0] != "clipboard.write" {
		t.Fatalf("unexpected granted_permissions payload: %#v", data["granted_permissions"])
	}
}

func TestInstallAppMapsInvalidPermissions(t *testing.T) {
	appID := uuid.New()
	userID := uuid.New()

	router := newAdminTestRouter(&stubAdminService{
		installAppFn: func(ctx context.Context, gotAppID, gotUserID uuid.UUID, permissions []string) (*postgres.AppInstallation, error) {
			return nil, serviceErr("INVALID_PERMISSIONS")
		},
	})

	req := httptest.NewRequest(http.MethodPost, "/v1/apps/"+appID.String()+"/install", bytes.NewBufferString(`{"granted_permissions":["device.camera"]}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-User-Id", userID.String())
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)

	if resp.Code != http.StatusBadRequest {
		t.Fatalf("expected status %d, got %d", http.StatusBadRequest, resp.Code)
	}

	body := decodeEnvelope(t, resp)
	errBody, ok := body["error"].(map[string]any)
	if !ok || errBody["code"] != "INVALID_PERMISSIONS" {
		t.Fatalf("unexpected error payload: %#v", body["error"])
	}
}

func TestUninstallAppSuccess(t *testing.T) {
	appID := uuid.New()
	userID := uuid.New()
	called := false

	router := newAdminTestRouter(&stubAdminService{
		uninstallAppFn: func(ctx context.Context, gotAppID, gotUserID uuid.UUID) error {
			called = true
			if gotAppID != appID {
				t.Fatalf("unexpected app id: %s", gotAppID)
			}
			if gotUserID != userID {
				t.Fatalf("unexpected user id: %s", gotUserID)
			}
			return nil
		},
	})

	req := httptest.NewRequest(http.MethodDelete, "/v1/apps/"+appID.String()+"/install", nil)
	req.Header.Set("X-User-Id", userID.String())
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d", http.StatusOK, resp.Code)
	}
	if !called {
		t.Fatal("expected uninstallApp to be called")
	}

	body := decodeEnvelope(t, resp)
	data, ok := body["data"].(map[string]any)
	if !ok || data["status"] != "uninstalled" {
		t.Fatalf("unexpected response payload: %#v", body["data"])
	}
}

func TestUninstallAppMapsInternalError(t *testing.T) {
	appID := uuid.New()
	userID := uuid.New()

	router := newAdminTestRouter(&stubAdminService{
		uninstallAppFn: func(ctx context.Context, gotAppID, gotUserID uuid.UUID) error {
			return serviceErr("db unavailable")
		},
	})

	req := httptest.NewRequest(http.MethodDelete, "/v1/apps/"+appID.String()+"/install", nil)
	req.Header.Set("X-User-Id", userID.String())
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)

	if resp.Code != http.StatusInternalServerError {
		t.Fatalf("expected status %d, got %d", http.StatusInternalServerError, resp.Code)
	}

	body := decodeEnvelope(t, resp)
	errBody, ok := body["error"].(map[string]any)
	if !ok || errBody["code"] != "INTERNAL_ERROR" {
		t.Fatalf("unexpected error payload: %#v", body["error"])
	}
}

func TestGetMiniAppSessionSuccess(t *testing.T) {
	appID := uuid.New()
	userID := uuid.New()
	called := false

	router := newAdminTestRouter(&stubAdminService{
		createMiniAppSessionFn: func(ctx context.Context, gotAppID, gotUserID uuid.UUID) (*service.MiniAppSession, error) {
			called = true
			if gotAppID != appID {
				t.Fatalf("unexpected app id: %s", gotAppID)
			}
			if gotUserID != userID {
				t.Fatalf("unexpected user id: %s", gotUserID)
			}
			return &service.MiniAppSession{
				AppID:              gotAppID.String(),
				UserID:             gotUserID.String(),
				TokenType:          "Bearer",
				AccessToken:        "session-token",
				GrantedPermissions: []string{"user.profile.read"},
			}, nil
		},
	})

	req := httptest.NewRequest(http.MethodGet, "/v1/apps/"+appID.String()+"/session", nil)
	req.Header.Set("X-User-Id", userID.String())
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d", http.StatusOK, resp.Code)
	}
	if !called {
		t.Fatal("expected CreateMiniAppSession to be called")
	}

	body := decodeEnvelope(t, resp)
	data, ok := body["data"].(map[string]any)
	if !ok || data["access_token"] != "session-token" {
		t.Fatalf("unexpected response data: %#v", body["data"])
	}
}

func TestGetMiniAppSessionMapsAppNotInstalled(t *testing.T) {
	appID := uuid.New()
	userID := uuid.New()

	router := newAdminTestRouter(&stubAdminService{
		createMiniAppSessionFn: func(ctx context.Context, gotAppID, gotUserID uuid.UUID) (*service.MiniAppSession, error) {
			return nil, serviceErr("APP_NOT_INSTALLED")
		},
	})

	req := httptest.NewRequest(http.MethodGet, "/v1/apps/"+appID.String()+"/session", nil)
	req.Header.Set("X-User-Id", userID.String())
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)

	if resp.Code != http.StatusForbidden {
		t.Fatalf("expected status %d, got %d", http.StatusForbidden, resp.Code)
	}

	body := decodeEnvelope(t, resp)
	errBody, ok := body["error"].(map[string]any)
	if !ok || errBody["code"] != "APP_NOT_INSTALLED" {
		t.Fatalf("unexpected error payload: %#v", body["error"])
	}
}

type serviceErr string

func (e serviceErr) Error() string {
	return string(e)
}
