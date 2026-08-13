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
    -- UH6: cascade so the GDPR grace-period hard purge of auth.users
    -- doesn't strand session rows that block the DELETE.
    user_id UUID NOT NULL REFERENCES auth.users(user_id) ON DELETE CASCADE,
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

-- RBAC: server-side roles. The signed access token's `scopes` claim is resolved
-- from this table (UNION the SUPERADMIN/ADMIN/MODERATOR_USER_IDS env allowlists,
-- which remain as the bootstrap path for the first superadmin). Replaces the
-- previous model where clients declared their own privileges via X-Scopes.
CREATE TABLE IF NOT EXISTS auth.user_roles (
    user_id    UUID NOT NULL,
    role       TEXT NOT NULL CHECK (role IN ('superadmin','admin','moderator')),
    granted_by UUID,
    granted_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (user_id, role)
);
CREATE INDEX IF NOT EXISTS idx_user_roles_user_id ON auth.user_roles(user_id);

-- Immutable audit trail of privileged actions (role grants/revokes, etc.).
-- Append-only: revokes delete the user_roles row, so this is the durable record
-- of who did what to whom, including denied attempts. Industry-best / SOC2.
CREATE TABLE IF NOT EXISTS auth.admin_audit (
    id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    actor_id   UUID NOT NULL,
    action     TEXT NOT NULL,
    target_id  UUID,
    detail     TEXT,
    allowed    BOOLEAN NOT NULL DEFAULT TRUE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_admin_audit_actor ON auth.admin_audit(actor_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_admin_audit_target ON auth.admin_audit(target_id, created_at DESC);

-- WebAuthn / passkey credentials. One row per registered authenticator. The
-- public key + sign_count are used to verify assertions at login; credential_id
-- is the authenticator's handle (unique). Phishing-resistant second factor /
-- passwordless. Verification logic lives behind the `webauthn` build tag.
CREATE TABLE IF NOT EXISTS auth.webauthn_credentials (
    id               UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id          UUID NOT NULL,
    credential_id    BYTEA NOT NULL UNIQUE,
    public_key       BYTEA NOT NULL,
    attestation_type TEXT NOT NULL DEFAULT '',
    aaguid           BYTEA,
    sign_count       BIGINT NOT NULL DEFAULT 0,
    transports       TEXT[] NOT NULL DEFAULT '{}',
    clone_warning    BOOLEAN NOT NULL DEFAULT FALSE,
    name             TEXT NOT NULL DEFAULT 'passkey',
    created_at       TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    last_used_at     TIMESTAMPTZ
);
CREATE INDEX IF NOT EXISTS idx_webauthn_creds_user ON auth.webauthn_credentials(user_id);

CREATE TABLE IF NOT EXISTS usr.users (
    id UUID PRIMARY KEY REFERENCES auth.users(user_id) ON DELETE CASCADE,
    status TEXT NOT NULL DEFAULT 'active',
    is_verified BOOLEAN NOT NULL DEFAULT FALSE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS usr.user_settings (
    user_id UUID PRIMARY KEY REFERENCES usr.users(id) ON DELETE CASCADE,
    account_visibility TEXT NOT NULL DEFAULT 'public',
    allow_messages_from TEXT NOT NULL DEFAULT 'everyone',
    allow_comments_from TEXT NOT NULL DEFAULT 'everyone',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS profile.profiles (
    user_id UUID PRIMARY KEY REFERENCES auth.users(user_id) ON DELETE CASCADE,
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

-- profile-service's profiles.go selects 30+ columns that weren't in the
-- original CREATE TABLE. Adding them inline so the bootstrap doesn't
-- rely on migrations running first.
ALTER TABLE profile.profiles
    ADD COLUMN IF NOT EXISTS preferred_name      TEXT,
    ADD COLUMN IF NOT EXISTS pronouns            TEXT,
    ADD COLUMN IF NOT EXISTS is_verified         BOOLEAN NOT NULL DEFAULT FALSE,
    ADD COLUMN IF NOT EXISTS verification_level  TEXT    NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS status_text         TEXT    NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS status_emoji        TEXT    NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS status_expires_at   TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS profile_theme_color TEXT    NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS intro_media_url     TEXT    NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS intro_media_type    TEXT    NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS cta_label           TEXT    NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS cta_url             TEXT    NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS member_since_badge  BOOLEAN NOT NULL DEFAULT FALSE,
    ADD COLUMN IF NOT EXISTS timezone            TEXT    NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS follower_count      INT     NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS following_count     INT     NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS friend_count        INT     NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS post_count          INT     NOT NULL DEFAULT 0;

-- profile-service inbox dedup table — referenced by inbox_events
-- consumer dedup check. Schema matches consumer.go's queries
-- (composite key on consumer_name + event_id so multiple services
-- could share the table later).
CREATE TABLE IF NOT EXISTS profile.inbox_events (
    consumer_name TEXT NOT NULL,
    event_id      TEXT NOT NULL,
    processed_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (consumer_name, event_id)
);
CREATE INDEX IF NOT EXISTS idx_profile_inbox_events_processed_at
    ON profile.inbox_events(processed_at);

-- TOTP secret encryption at rest. New writes go to
-- two_factor_secret_encrypted (AES-256-GCM, nonce-prefixed) — legacy
-- plaintext two_factor_secret stays during the cutover so old rows
-- still verify. The reader prefers the encrypted column when set.
-- See identity-shared/crypto/secret_box.go for the cipher.
ALTER TABLE auth.users
    ADD COLUMN IF NOT EXISTS two_factor_secret_encrypted BYTEA;

-- A13: login anomaly audit trail. Each row is one detection event —
-- new IP, new device, impossible travel, etc. Industry-standard audit
-- log so ops can review patterns + the user can see "where you've
-- signed in from" in security settings. resolved_at flips when the
-- user confirms the login was theirs (acknowledged in-app).
CREATE TABLE IF NOT EXISTS auth.login_anomalies (
    id             UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id        UUID NOT NULL REFERENCES auth.users(user_id) ON DELETE CASCADE,
    anomaly_type   TEXT NOT NULL
                   CHECK (anomaly_type IN ('new_ip','new_device','new_country','impossible_travel','many_failed','password_reset_used','session_revoked')),
    ip             TEXT,
    user_agent     TEXT,
    device_id      TEXT,
    country_code   TEXT,
    metadata       JSONB NOT NULL DEFAULT '{}'::jsonb,
    risk_score     SMALLINT NOT NULL DEFAULT 0
                   CHECK (risk_score BETWEEN 0 AND 100),
    challenged     BOOLEAN NOT NULL DEFAULT FALSE,
    acknowledged_at TIMESTAMPTZ,
    occurred_at    TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_login_anomalies_user_time ON auth.login_anomalies(user_id, occurred_at DESC);
CREATE INDEX IF NOT EXISTS idx_login_anomalies_unacked ON auth.login_anomalies(user_id, occurred_at DESC)
    WHERE acknowledged_at IS NULL;

-- A15: refresh-token IP/UA fingerprint bind. Refresh tokens stolen via
-- XSS / device theft are the #1 silent-takeover vector once the
-- access token has expired. We persist the IP/UA at session creation
-- (already in auth.sessions) and on each refresh we check that the
-- caller's IP isn't impossible-travel + UA hasn't drastically changed.
-- The fingerprint columns already exist on auth.sessions — we just need
-- a `family_id` to track sibling rotations + an `anomaly_flagged`
-- bit so a flagged refresh can short-circuit.
ALTER TABLE auth.sessions
    ADD COLUMN IF NOT EXISTS family_id UUID,
    ADD COLUMN IF NOT EXISTS anomaly_flagged BOOLEAN NOT NULL DEFAULT FALSE,
    ADD COLUMN IF NOT EXISTS last_refresh_at TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS last_refresh_ip TEXT;
-- Backfill family_id = session_id for existing rows so each pre-A15
-- session is its own "family" — first rotation forks new IDs.
UPDATE auth.sessions SET family_id = session_id WHERE family_id IS NULL;
CREATE INDEX IF NOT EXISTS idx_sessions_family ON auth.sessions(family_id) WHERE family_id IS NOT NULL;
-- Module 3 M3-P0-3 / LB-5 — consent record and pending activation.
--
-- WHAT WAS MISSING
--
-- Registration validated an accepted terms VERSION in memory and then threw it
-- away. `auth.users` has consent_terms / consent_privacy / consent_age
-- booleans, and registration never wrote them — so after the fact there was no
-- way to answer "did this user accept anything, and which text?" for any
-- account. Under the DPDP Act the answer to that question is the lawful basis
-- for processing, and a boolean with no version cannot answer it either.
--
-- Registration also created the account ACTIVE and returned access and refresh
-- tokens immediately, without sending any verification challenge. So an
-- address could be registered without its owner ever being contacted: someone
-- else's email became a working account, and the real owner never learned.

-- Versioned consent record. One row per acceptance, never updated — a consent
-- history is evidence, and evidence that can be edited in place is not
-- evidence. A user who re-accepts a newer version gets a second row.
CREATE TABLE IF NOT EXISTS auth.registration_consents (
    id             UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id        UUID        NOT NULL REFERENCES auth.users(user_id) ON DELETE CASCADE,
    terms_version  TEXT        NOT NULL,
    -- What the user actually agreed to, recorded separately so a future
    -- change to one does not retroactively imply the others.
    accepted_terms   BOOLEAN   NOT NULL DEFAULT FALSE,
    accepted_privacy BOOLEAN   NOT NULL DEFAULT FALSE,
    -- The 18+ self-declaration. Stored alongside the date of birth that
    -- justified it, so an audit does not have to re-derive the age from a
    -- profile row that may have been edited since.
    declared_dob   DATE,
    accepted_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    -- Provenance for a disputed acceptance.
    ip             TEXT,
    user_agent     TEXT
);

CREATE INDEX IF NOT EXISTS idx_registration_consents_user
    ON auth.registration_consents (user_id, accepted_at DESC);

-- Pending activation.
--
-- `account_status` already exists and defaults to 'active'. Registration now
-- writes 'pending_verification' instead, and successful one-time email
-- verification promotes it to 'active'. No session is issued in between.
--
-- The CHECK is deliberately NOT added: account_status is a free-text column
-- with existing values across environments, and a constraint added here would
-- fail the migration on any row this module did not create. The allowed set is
-- enforced in code (internal/store) where it can be reasoned about.
CREATE INDEX IF NOT EXISTS idx_users_pending_verification
    ON auth.users (created_at)
    WHERE account_status = 'pending_verification';

-- Module 3 CLB-3 — verification transactions.
--
-- Registration creates a PENDING account and issues no session, so the user
-- has no credential the auth middleware would accept — which is why
-- /verify-email and /resend-verification cannot live behind it. This table
-- holds the short-lived, opaque, single-purpose credential that authorises
-- exactly those two calls and nothing else.
--
-- Only the SHA-256 digest is stored: a database dump does not yield usable
-- credentials. See internal/store/verification_transaction.go for why a fast
-- digest is correct here and bcrypt is not.
CREATE TABLE IF NOT EXISTS auth.verification_transactions (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id     UUID NOT NULL,
    purpose     VARCHAR(32) NOT NULL,
    token_hash  TEXT NOT NULL UNIQUE,
    expires_at  TIMESTAMPTZ NOT NULL,
    consumed_at TIMESTAMPTZ,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_verification_tx_user
    ON auth.verification_transactions (user_id, purpose);

CREATE INDEX IF NOT EXISTS idx_verification_tx_expiry
    ON auth.verification_transactions (expires_at);
