package service

import (
	"context"
	"fmt"
	"strings"

	"github.com/atpost/admin-service/internal/store/postgres"
	"github.com/google/uuid"
)

// CreateMiniApp registers a new mini app by a developer.
func (s *Service) CreateMiniApp(ctx context.Context, app *postgres.MiniApp) error {
	return s.store.CreateMiniApp(ctx, app)
}

// GetMiniApp returns a mini app by ID.
func (s *Service) GetMiniApp(ctx context.Context, id uuid.UUID) (*postgres.MiniApp, error) {
	return s.store.GetMiniApp(ctx, id)
}

// ListMiniApps returns live mini apps, optionally filtered by category.
func (s *Service) ListMiniApps(ctx context.Context, category string, limit, offset int) ([]postgres.MiniApp, error) {
	return s.store.ListMiniApps(ctx, category, limit, offset)
}

// UpdateMiniAppStatus updates the review status of a mini app (admin only).
func (s *Service) UpdateMiniAppStatus(ctx context.Context, id uuid.UUID, status string) error {
	return s.store.UpdateMiniAppStatus(ctx, id, status)
}

// InstallApp installs a mini app for a user.
func (s *Service) InstallApp(ctx context.Context, appID, userID uuid.UUID, permissions []string) (*postgres.AppInstallation, error) {
	app, err := s.store.GetMiniApp(ctx, appID)
	if err != nil {
		return nil, err
	}
	if app == nil || app.Status != "live" {
		return nil, fmt.Errorf("APP_NOT_AVAILABLE")
	}

	grantedPermissions, err := normalizeGrantedPermissions(app.Permissions, permissions)
	if err != nil {
		return nil, err
	}

	return s.store.InstallApp(ctx, appID, userID, grantedPermissions)
}

// UninstallApp removes a user's installation of a mini app.
func (s *Service) UninstallApp(ctx context.Context, appID, userID uuid.UUID) error {
	return s.store.UninstallApp(ctx, appID, userID)
}

// GetUserInstalledApps returns all apps installed by a user.
func (s *Service) GetUserInstalledApps(ctx context.Context, userID uuid.UUID) ([]postgres.MiniApp, error) {
	return s.store.GetUserInstalledApps(ctx, userID)
}

type MiniAppSession struct {
	AppID              string   `json:"app_id"`
	UserID             string   `json:"user_id"`
	TokenType          string   `json:"token_type"`
	AccessToken        string   `json:"access_token"`
	ExpiresAt          string   `json:"expires_at"`
	ExpiresIn          int64    `json:"expires_in"`
	Issuer             string   `json:"issuer"`
	Audience           string   `json:"audience"`
	GrantedPermissions []string `json:"granted_permissions"`
}

func (s *Service) CreateMiniAppSession(ctx context.Context, appID, userID uuid.UUID) (*MiniAppSession, error) {
	if s.miniAppSessionAuth == nil {
		return nil, fmt.Errorf("SESSION_UNAVAILABLE")
	}

	app, err := s.store.GetInstalledMiniApp(ctx, appID, userID)
	if err != nil {
		return nil, err
	}
	if app == nil {
		return nil, fmt.Errorf("APP_NOT_INSTALLED")
	}
	if app.Status != "live" {
		return nil, fmt.Errorf("APP_NOT_AVAILABLE")
	}

	return s.miniAppSessionAuth.IssueMiniAppSession(ctx, app.ID, userID, app.GrantedPermissions)
}

func normalizeGrantedPermissions(requested, granted []string) ([]string, error) {
	allowed := make(map[string]struct{}, len(requested))
	for _, permission := range requested {
		value := strings.TrimSpace(permission)
		if value == "" {
			continue
		}
		allowed[value] = struct{}{}
	}

	normalized := make([]string, 0, len(granted))
	seen := make(map[string]struct{}, len(granted))
	for _, permission := range granted {
		value := strings.TrimSpace(permission)
		if value == "" {
			continue
		}
		if _, ok := allowed[value]; !ok {
			return nil, fmt.Errorf("INVALID_PERMISSIONS")
		}
		if _, duplicate := seen[value]; duplicate {
			continue
		}
		seen[value] = struct{}{}
		normalized = append(normalized, value)
	}

	return normalized, nil
}

// CreateOAuthClient registers a new OAuth client for a developer.
func (s *Service) CreateOAuthClient(ctx context.Context, client *postgres.OAuthClient) error {
	return s.store.CreateOAuthClient(ctx, client)
}

// GetOAuthClientByClientID returns an OAuth client by its client_id.
func (s *Service) GetOAuthClientByClientID(ctx context.Context, clientID string) (*postgres.OAuthClient, error) {
	return s.store.GetOAuthClientByClientID(ctx, clientID)
}
