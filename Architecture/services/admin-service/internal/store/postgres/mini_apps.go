package postgres

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// MiniApp represents a third-party app on the AtPost platform.
type MiniApp struct {
	ID           uuid.UUID `json:"id"`
	DeveloperID  uuid.UUID `json:"developer_id"`
	Name         string    `json:"name"`
	Description  string    `json:"description"`
	IconURL      string    `json:"icon_url,omitempty"`
	ManifestURL  string    `json:"manifest_url"`
	Permissions  []string  `json:"permissions"`
	Status       string    `json:"status"`
	Category     string    `json:"category,omitempty"`
	InstallCount int64     `json:"install_count"`
	CreatedAt    time.Time `json:"created_at"`
}

// AppInstallation represents a user installing a mini app.
type AppInstallation struct {
	AppID              uuid.UUID `json:"app_id"`
	UserID             uuid.UUID `json:"user_id"`
	GrantedPermissions []string  `json:"granted_permissions"`
	InstalledAt        time.Time `json:"installed_at"`
}

// OAuthClient represents a registered OAuth 2.0 client ("Login with AtPost").
type OAuthClient struct {
	ID               uuid.UUID `json:"id"`
	DeveloperID      uuid.UUID `json:"developer_id"`
	Name             string    `json:"name"`
	ClientID         string    `json:"client_id"`
	ClientSecretHash string    `json:"-"`
	RedirectURIs     []string  `json:"redirect_uris"`
	Scopes           []string  `json:"scopes"`
	IsActive         bool      `json:"is_active"`
	CreatedAt        time.Time `json:"created_at"`
}

// OAuthToken represents an issued OAuth access token.
type OAuthToken struct {
	ID               uuid.UUID `json:"id"`
	ClientID         uuid.UUID `json:"client_id"`
	UserID           uuid.UUID `json:"user_id"`
	AccessTokenHash  string    `json:"-"`
	RefreshTokenHash string    `json:"-"`
	Scopes           []string  `json:"scopes"`
	ExpiresAt        time.Time `json:"expires_at"`
	CreatedAt        time.Time `json:"created_at"`
}

// --- Mini Apps ---

// CreateMiniApp inserts a new mini app record.
func (s *Store) CreateMiniApp(ctx context.Context, app *MiniApp) error {
	app.ID = uuid.New()
	return s.db.QueryRow(ctx, `
		INSERT INTO mini_apps (id, developer_id, name, description, icon_url, manifest_url, permissions, status, category, install_count, created_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,'pending',$8,0,NOW())
		RETURNING status, install_count, created_at
	`, app.ID, app.DeveloperID, app.Name, app.Description, app.IconURL, app.ManifestURL, app.Permissions, app.Category,
	).Scan(&app.Status, &app.InstallCount, &app.CreatedAt)
}

// GetMiniApp returns a mini app by ID.
func (s *Store) GetMiniApp(ctx context.Context, id uuid.UUID) (*MiniApp, error) {
	var app MiniApp
	err := s.db.QueryRow(ctx, `
		SELECT id, developer_id, name, description, COALESCE(icon_url,''), manifest_url, permissions,
		       status, COALESCE(category,''), install_count, created_at
		FROM mini_apps WHERE id = $1
	`, id).Scan(&app.ID, &app.DeveloperID, &app.Name, &app.Description, &app.IconURL, &app.ManifestURL,
		&app.Permissions, &app.Status, &app.Category, &app.InstallCount, &app.CreatedAt)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	return &app, nil
}

// ListMiniApps returns all live mini apps, optionally filtered by category.
func (s *Store) ListMiniApps(ctx context.Context, category string, limit, offset int) ([]MiniApp, error) {
	query := `
		SELECT id, developer_id, name, description, COALESCE(icon_url,''), manifest_url, permissions,
		       status, COALESCE(category,''), install_count, created_at
		FROM mini_apps WHERE status = 'live'`
	args := []interface{}{}
	argN := 1

	if category != "" {
		query += ` AND category = $` + itoa(argN)
		args = append(args, category)
		argN++
	}
	query += ` ORDER BY install_count DESC LIMIT $` + itoa(argN) + ` OFFSET $` + itoa(argN+1)
	args = append(args, limit, offset)

	rows, err := s.db.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var apps []MiniApp
	for rows.Next() {
		var app MiniApp
		if err := rows.Scan(&app.ID, &app.DeveloperID, &app.Name, &app.Description, &app.IconURL,
			&app.ManifestURL, &app.Permissions, &app.Status, &app.Category, &app.InstallCount, &app.CreatedAt); err != nil {
			return nil, err
		}
		apps = append(apps, app)
	}
	return apps, rows.Err()
}

// UpdateMiniAppStatus updates the status of a mini app.
func (s *Store) UpdateMiniAppStatus(ctx context.Context, id uuid.UUID, status string) error {
	_, err := s.db.Exec(ctx, `UPDATE mini_apps SET status = $1 WHERE id = $2`, status, id)
	return err
}

// InstallApp creates an installation record and increments install_count.
func (s *Store) InstallApp(ctx context.Context, appID, userID uuid.UUID, grantedPermissions []string) (*AppInstallation, error) {
	inst := &AppInstallation{
		AppID:              appID,
		UserID:             userID,
		GrantedPermissions: grantedPermissions,
	}
	err := s.db.QueryRow(ctx, `
		INSERT INTO app_installations (app_id, user_id, granted_permissions, installed_at)
		VALUES ($1, $2, $3, NOW())
		ON CONFLICT (app_id, user_id) DO UPDATE
		SET granted_permissions = EXCLUDED.granted_permissions, installed_at = NOW()
		RETURNING installed_at
	`, appID, userID, grantedPermissions).Scan(&inst.InstalledAt)
	if err != nil {
		return nil, err
	}
	// Increment install count (best-effort)
	_, _ = s.db.Exec(ctx, `UPDATE mini_apps SET install_count = install_count + 1 WHERE id = $1`, appID)
	return inst, nil
}

// UninstallApp removes an installation record.
func (s *Store) UninstallApp(ctx context.Context, appID, userID uuid.UUID) error {
	_, err := s.db.Exec(ctx, `DELETE FROM app_installations WHERE app_id = $1 AND user_id = $2`, appID, userID)
	return err
}

// GetUserInstalledApps returns all apps installed by a user.
func (s *Store) GetUserInstalledApps(ctx context.Context, userID uuid.UUID) ([]MiniApp, error) {
	rows, err := s.db.Query(ctx, `
		SELECT m.id, m.developer_id, m.name, m.description, COALESCE(m.icon_url,''), m.manifest_url,
		       m.permissions, m.status, COALESCE(m.category,''), m.install_count, m.created_at
		FROM mini_apps m
		JOIN app_installations ai ON ai.app_id = m.id
		WHERE ai.user_id = $1
		ORDER BY ai.installed_at DESC
	`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var apps []MiniApp
	for rows.Next() {
		var app MiniApp
		if err := rows.Scan(&app.ID, &app.DeveloperID, &app.Name, &app.Description, &app.IconURL,
			&app.ManifestURL, &app.Permissions, &app.Status, &app.Category, &app.InstallCount, &app.CreatedAt); err != nil {
			return nil, err
		}
		apps = append(apps, app)
	}
	return apps, rows.Err()
}

// --- OAuth ---

// CreateOAuthClient registers a new OAuth client.
func (s *Store) CreateOAuthClient(ctx context.Context, client *OAuthClient) error {
	client.ID = uuid.New()
	return s.db.QueryRow(ctx, `
		INSERT INTO oauth_clients (id, developer_id, name, client_id, client_secret_hash, redirect_uris, scopes, is_active, created_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,TRUE,NOW())
		RETURNING is_active, created_at
	`, client.ID, client.DeveloperID, client.Name, client.ClientID, client.ClientSecretHash,
		client.RedirectURIs, client.Scopes,
	).Scan(&client.IsActive, &client.CreatedAt)
}

// GetOAuthClientByClientID returns an OAuth client by its client_id string.
func (s *Store) GetOAuthClientByClientID(ctx context.Context, clientID string) (*OAuthClient, error) {
	var c OAuthClient
	err := s.db.QueryRow(ctx, `
		SELECT id, developer_id, name, client_id, client_secret_hash, redirect_uris, scopes, is_active, created_at
		FROM oauth_clients WHERE client_id = $1
	`, clientID).Scan(&c.ID, &c.DeveloperID, &c.Name, &c.ClientID, &c.ClientSecretHash,
		&c.RedirectURIs, &c.Scopes, &c.IsActive, &c.CreatedAt)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	return &c, nil
}

// CreateOAuthToken stores a new OAuth token record.
func (s *Store) CreateOAuthToken(ctx context.Context, token *OAuthToken) error {
	token.ID = uuid.New()
	return s.db.QueryRow(ctx, `
		INSERT INTO oauth_tokens (id, client_id, user_id, access_token_hash, refresh_token_hash, scopes, expires_at, created_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,NOW())
		RETURNING created_at
	`, token.ID, token.ClientID, token.UserID, token.AccessTokenHash, token.RefreshTokenHash,
		token.Scopes, token.ExpiresAt,
	).Scan(&token.CreatedAt)
}

// GetOAuthToken retrieves an OAuth token by ID.
func (s *Store) GetOAuthToken(ctx context.Context, id uuid.UUID) (*OAuthToken, error) {
	var t OAuthToken
	err := s.db.QueryRow(ctx, `
		SELECT id, client_id, user_id, access_token_hash, COALESCE(refresh_token_hash,''), scopes, expires_at, created_at
		FROM oauth_tokens WHERE id = $1
	`, id).Scan(&t.ID, &t.ClientID, &t.UserID, &t.AccessTokenHash, &t.RefreshTokenHash,
		&t.Scopes, &t.ExpiresAt, &t.CreatedAt)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	return &t, nil
}

// itoa converts a positive int to its decimal string representation.
// Used for building SQL placeholder strings like "$1", "$2", etc.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	digits := make([]byte, 0, 4)
	for n > 0 {
		digits = append([]byte{byte('0' + n%10)}, digits...)
		n /= 10
	}
	return string(digits)
}
