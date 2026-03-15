-- Platform Extras: Mini Apps, OAuth, and cross-service references
-- These tables live in the main app database for cross-service accessibility.

-- Mini Apps
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

-- OAuth clients (for "Login with AtPost")
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
CREATE INDEX IF NOT EXISTS idx_oauth_tokens_user ON oauth_tokens(user_id, client_id);
