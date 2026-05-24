CREATE EXTENSION IF NOT EXISTS pgcrypto;

CREATE SCHEMA IF NOT EXISTS auth;
CREATE SCHEMA IF NOT EXISTS usr;
CREATE SCHEMA IF NOT EXISTS profile;

CREATE TABLE IF NOT EXISTS auth.users (
    user_id UUID PRIMARY KEY,
    phone TEXT UNIQUE,
    email TEXT UNIQUE,
    password_hash TEXT,
    email_verified BOOLEAN NOT NULL DEFAULT FALSE,
    phone_verified BOOLEAN NOT NULL DEFAULT FALSE,
    two_factor_enabled BOOLEAN NOT NULL DEFAULT FALSE,
    two_factor_secret TEXT,
    account_type TEXT NOT NULL DEFAULT 'personal',
    account_status TEXT NOT NULL DEFAULT 'active',
    login_provider TEXT,
    recovery_email TEXT,
    recovery_phone TEXT,
    age_verification TEXT NOT NULL DEFAULT 'unverified',
    consent_terms BOOLEAN NOT NULL DEFAULT FALSE,
    consent_privacy BOOLEAN NOT NULL DEFAULT FALSE,
    consent_age BOOLEAN NOT NULL DEFAULT FALSE,
    deletion_requested_at TIMESTAMPTZ,
    scheduled_purge_date TIMESTAMPTZ,
    last_login_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT users_identity_check CHECK (phone IS NOT NULL OR email IS NOT NULL)
);
CREATE INDEX IF NOT EXISTS idx_users_pending_deletion ON auth.users(scheduled_purge_date) WHERE account_status = 'pending_deletion';
CREATE INDEX IF NOT EXISTS idx_users_login_provider ON auth.users(login_provider) WHERE login_provider IS NOT NULL;

CREATE TABLE IF NOT EXISTS auth.otp_codes (
    id UUID PRIMARY KEY,
    phone TEXT NOT NULL,
    otp_hash TEXT NOT NULL,
    purpose TEXT NOT NULL,
    expires_at TIMESTAMPTZ NOT NULL,
    attempts INT NOT NULL DEFAULT 0,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_otp_phone_purpose ON auth.otp_codes(phone, purpose);

CREATE TABLE IF NOT EXISTS auth.sessions (
    session_id UUID PRIMARY KEY,
    user_id UUID NOT NULL REFERENCES auth.users(user_id),
    refresh_token_hash TEXT NOT NULL,
    device_id TEXT,
    platform TEXT,
    ip TEXT,
    user_agent TEXT,
    is_active BOOLEAN NOT NULL DEFAULT TRUE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    expires_at TIMESTAMPTZ NOT NULL,
    revoked_at TIMESTAMPTZ
);
CREATE INDEX IF NOT EXISTS idx_sessions_user_created ON auth.sessions(user_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_sessions_user_active ON auth.sessions(user_id, is_active) WHERE is_active = TRUE;

CREATE TABLE IF NOT EXISTS auth.trusted_devices (
    id UUID PRIMARY KEY,
    user_id UUID NOT NULL REFERENCES auth.users(user_id) ON DELETE CASCADE,
    device_fingerprint TEXT NOT NULL,
    device_name TEXT,
    last_used_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    trusted_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_trusted_devices_user ON auth.trusted_devices(user_id);
CREATE UNIQUE INDEX IF NOT EXISTS idx_trusted_devices_user_fingerprint ON auth.trusted_devices(user_id, device_fingerprint);

CREATE TABLE IF NOT EXISTS auth.outbox_events (
    id BIGSERIAL PRIMARY KEY,
    event_type TEXT NOT NULL,
    partition_key TEXT NOT NULL DEFAULT '',
    payload JSONB NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    published_at TIMESTAMPTZ
);
CREATE INDEX IF NOT EXISTS idx_auth_outbox_unpublished ON auth.outbox_events(id) WHERE published_at IS NULL;

CREATE TABLE IF NOT EXISTS auth.recovery_codes (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID NOT NULL REFERENCES auth.users(user_id) ON DELETE CASCADE,
    code_hash TEXT NOT NULL,
    used_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_recovery_codes_user_id ON auth.recovery_codes(user_id);

CREATE TABLE IF NOT EXISTS usr.users (
    id UUID PRIMARY KEY REFERENCES auth.users(user_id),
    status TEXT NOT NULL DEFAULT 'active',
    is_verified BOOLEAN NOT NULL DEFAULT FALSE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS usr.user_settings (
    user_id UUID PRIMARY KEY REFERENCES usr.users(id),
    account_visibility TEXT NOT NULL DEFAULT 'public',
    allow_messages_from TEXT NOT NULL DEFAULT 'everyone',
    allow_comments_from TEXT NOT NULL DEFAULT 'everyone',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS profile.profiles (
    user_id UUID PRIMARY KEY REFERENCES auth.users(user_id),
    username TEXT,
    display_name TEXT NOT NULL,
    first_name TEXT DEFAULT '',
    last_name TEXT DEFAULT '',
    bio TEXT DEFAULT '',
    dob DATE,
    gender TEXT DEFAULT '',
    avatar_media_id UUID,
    cover_media_id UUID,
    category TEXT DEFAULT 'personal',
    profession TEXT DEFAULT '',
    website TEXT DEFAULT '',
    location TEXT DEFAULT '',
    badge_flags INT NOT NULL DEFAULT 0,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_profiles_display_name ON profile.profiles(display_name);
CREATE UNIQUE INDEX IF NOT EXISTS idx_profiles_username ON profile.profiles(username) WHERE username IS NOT NULL;

-- TOTP secret encryption at rest. New writes go to
-- two_factor_secret_encrypted (AES-256-GCM, nonce-prefixed); legacy
-- plaintext two_factor_secret stays during the cutover so old rows
-- still verify. The reader prefers the encrypted column when set.
-- See identity-shared/crypto/secret_box.go for the cipher.
ALTER TABLE auth.users
    ADD COLUMN IF NOT EXISTS two_factor_secret_encrypted BYTEA;
