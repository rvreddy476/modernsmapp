# Module: admin-service

_Auto-extracted from source (DDL + route registrations). Verbatim — the source of truth is the code._

## HTTP routes
```
DELETE /:id/install
DELETE /users/:userId/suspend
GET /audit-log
GET /authorize
GET /dashboard
GET /data-export/:id
GET /:id
GET /:id/session
GET /installed
GET /products/queue
GET /reports
GET /sellers/queue
GET /sellers/:sellerId
GET /suspensions
PATCH /:id/status
POST /clients
POST /data-export
POST /:id/install
POST /products/:productId/approve
POST /products/:productId/reject
POST /sellers/:sellerId/approve
POST /sellers/:sellerId/reject
POST /sellers/:sellerId/request-changes
POST /sellers/:sellerId/suspend
POST /takedown
POST /token
POST /users/:userId/suspend
GROUP /v1/admin
GROUP /v1/admin/commerce
GROUP /v1/apps
GROUP /v1/oauth
```

## Database schema (CREATE TABLE — full column DDL)
```sql
CREATE TABLE IF NOT EXISTS admin.audit_log (
    id UUID PRIMARY KEY,
    admin_actor TEXT NOT NULL,
    action TEXT NOT NULL,
    entity_type TEXT NOT NULL,
    entity_id TEXT NOT NULL,
    payload JSONB,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS admin.suspensions (
    user_id UUID PRIMARY KEY,
    until TIMESTAMPTZ NOT NULL,
    reason TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS data_export_requests (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id         UUID NOT NULL,
    status          TEXT NOT NULL DEFAULT 'queued'
        CHECK (status IN ('queued','processing','ready','downloaded','expired')),
    download_url    TEXT,
    file_size_bytes BIGINT,
    requested_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    completed_at    TIMESTAMPTZ,
    expires_at      TIMESTAMPTZ
);

CREATE TABLE IF NOT EXISTS mini_apps (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    developer_id    UUID NOT NULL,
    name            TEXT NOT NULL,
    description     TEXT NOT NULL,
    icon_url        TEXT,
    manifest_url    TEXT NOT NULL,
    permissions     TEXT[] NOT NULL DEFAULT '{}',
    status          TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending','approved','live','suspended')),
    category        TEXT CHECK (category IN ('games','booking','learning','shopping','tools','entertainment')),
    install_count   BIGINT NOT NULL DEFAULT 0,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS app_installations (
    app_id              UUID NOT NULL REFERENCES mini_apps(id) ON DELETE CASCADE,
    user_id             UUID NOT NULL,
    granted_permissions TEXT[] NOT NULL DEFAULT '{}',
    installed_at        TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (app_id, user_id)
);

CREATE TABLE IF NOT EXISTS oauth_clients (
    id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    developer_id        UUID NOT NULL,
    name                TEXT NOT NULL,
    client_id           TEXT NOT NULL UNIQUE,
    client_secret_hash  TEXT NOT NULL,
    redirect_uris       TEXT[] NOT NULL DEFAULT '{}',
    scopes              TEXT[] NOT NULL DEFAULT '{}',
    is_active           BOOLEAN NOT NULL DEFAULT TRUE,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS oauth_tokens (
    id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    client_id           UUID NOT NULL REFERENCES oauth_clients(id),
    user_id             UUID NOT NULL,
    access_token_hash   TEXT NOT NULL,
    refresh_token_hash  TEXT,
    scopes              TEXT[] NOT NULL DEFAULT '{}',
    expires_at          TIMESTAMPTZ NOT NULL,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

```

## API types (request/response Go structs with JSON tags)
```go
type adminCommerceActionReq struct {
	Reason  string `json:"reason"`
	Notes   string `json:"notes"`
	Changes string `json:"changes"`
}

type TakedownRequest struct {
	EntityType string `json:"entity_type" binding:"required,oneof=post comment user message"`
	EntityID   string `json:"entity_id" binding:"required"`
	Reason     string `json:"reason" binding:"required"`
}

type SuspendRequest struct {
	Until  time.Time `json:"until" binding:"required"`
	Reason string    `json:"reason" binding:"required"`
}

type CreateMiniAppRequest struct {
	Name        string   `json:"name" binding:"required"`
	Description string   `json:"description" binding:"required"`
	IconURL     string   `json:"icon_url"`
	ManifestURL string   `json:"manifest_url" binding:"required"`
	Permissions []string `json:"permissions"`
	Category    string   `json:"category"`
}

type UpdateAppStatusRequest struct {
	Status string `json:"status" binding:"required"`
}

type InstallAppRequest struct {
	GrantedPermissions []string `json:"granted_permissions"`
}

type CreateOAuthClientRequest struct {
	Name         string   `json:"name" binding:"required"`
	ClientID     string   `json:"client_id" binding:"required"`
	ClientSecret string   `json:"client_secret" binding:"required"`
	RedirectURIs []string `json:"redirect_uris"`
	Scopes       []string `json:"scopes"`
}
```
