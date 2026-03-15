package service

import (
	"context"
	"fmt"

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
	return s.store.InstallApp(ctx, appID, userID, permissions)
}

// UninstallApp removes a user's installation of a mini app.
func (s *Service) UninstallApp(ctx context.Context, appID, userID uuid.UUID) error {
	return s.store.UninstallApp(ctx, appID, userID)
}

// GetUserInstalledApps returns all apps installed by a user.
func (s *Service) GetUserInstalledApps(ctx context.Context, userID uuid.UUID) ([]postgres.MiniApp, error) {
	return s.store.GetUserInstalledApps(ctx, userID)
}

// CreateOAuthClient registers a new OAuth client for a developer.
func (s *Service) CreateOAuthClient(ctx context.Context, client *postgres.OAuthClient) error {
	return s.store.CreateOAuthClient(ctx, client)
}

// GetOAuthClientByClientID returns an OAuth client by its client_id.
func (s *Service) GetOAuthClientByClientID(ctx context.Context, clientID string) (*postgres.OAuthClient, error) {
	return s.store.GetOAuthClientByClientID(ctx, clientID)
}
