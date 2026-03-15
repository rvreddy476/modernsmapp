-- Data export requests: users can request their own data export
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
CREATE INDEX IF NOT EXISTS idx_export_requests_user ON data_export_requests(user_id, requested_at DESC);

-- Mini Apps: third-party apps built on the AtPost platform
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

-- OAuth clients: "Login with AtPost"
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
