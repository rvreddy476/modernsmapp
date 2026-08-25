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

-- usr.user_settings is OWNED HERE, and the three columns above are only the
-- legacy subset. identity user-service selects the FULL privacy matrix on
-- every settings read (its store's `userSettingsColumns`), so a database
-- carrying only the legacy columns lets that service boot and then answer
-- `GET /v1/users/{id}/settings` with a 500 -- at which point graph-service
-- falls back to strict privacy defaults and direct messaging looks randomly
-- unavailable to everyone.
--
-- These columns were added by hand to one developer volume during the Slice B
-- chat capture. A hand-edited volume is not a deployment, so the DDL lives
-- here, next to the CREATE TABLE that owns the table. Same pattern and the
-- same reason as the profile.profiles ALTER above: additive and idempotent,
-- so the bootstrap does not depend on a migration having run first.
--
-- Defaults are the spec 5.2/5.4 posture for a NEW account, not the fail-safe
-- posture graph-service uses when a fetch fails. Restrictive where the
-- setting gates contact (who_can_message, who_can_call, who_can_add_to_groups)
-- and permissive only where it does not (who_can_see_profile_photo).
--
-- who_can_send_connection_request defaults to 'everyone' rather than graph's
-- fail-safe 'friends_of_friends_or_contacts': connection requests are Slice A's
-- surface and were proven against 'everyone'. Tightening it is a product
-- decision, not a schema repair, and does not belong in a chat pass.
ALTER TABLE usr.user_settings
    ADD COLUMN IF NOT EXISTS who_can_message                   TEXT    NOT NULL DEFAULT 'connections_only',
    ADD COLUMN IF NOT EXISTS who_can_send_connection_request   TEXT    NOT NULL DEFAULT 'everyone',
    ADD COLUMN IF NOT EXISTS who_can_call                      TEXT    NOT NULL DEFAULT 'connections_only',
    ADD COLUMN IF NOT EXISTS who_can_add_to_groups             TEXT    NOT NULL DEFAULT 'connections_only',
    ADD COLUMN IF NOT EXISTS who_can_see_online_status         TEXT    NOT NULL DEFAULT 'connections_only',
    ADD COLUMN IF NOT EXISTS who_can_see_read_receipts         TEXT    NOT NULL DEFAULT 'connections_only',
    ADD COLUMN IF NOT EXISTS who_can_see_last_seen             TEXT    NOT NULL DEFAULT 'connections_only',
    ADD COLUMN IF NOT EXISTS who_can_see_profile_photo         TEXT    NOT NULL DEFAULT 'everyone',
    ADD COLUMN IF NOT EXISTS allow_phone_discovery             BOOLEAN NOT NULL DEFAULT FALSE,
    ADD COLUMN IF NOT EXISTS allow_contact_sync_match          BOOLEAN NOT NULL DEFAULT FALSE,
    ADD COLUMN IF NOT EXISTS discoverable_by_phone_to_contacts BOOLEAN NOT NULL DEFAULT FALSE,
    ADD COLUMN IF NOT EXISTS strict_privacy_mode               BOOLEAN NOT NULL DEFAULT FALSE,
    ADD COLUMN IF NOT EXISTS block_unknown_calls               BOOLEAN NOT NULL DEFAULT FALSE,
    ADD COLUMN IF NOT EXISTS auto_filter_abusive_content       BOOLEAN NOT NULL DEFAULT TRUE,
    ADD COLUMN IF NOT EXISTS under_18_mode                     BOOLEAN NOT NULL DEFAULT FALSE,
    ADD COLUMN IF NOT EXISTS tc_close_friends_posts            BOOLEAN NOT NULL DEFAULT FALSE,
    ADD COLUMN IF NOT EXISTS tc_location_pings                 BOOLEAN NOT NULL DEFAULT FALSE,
    ADD COLUMN IF NOT EXISTS tc_after_hours_posts              BOOLEAN NOT NULL DEFAULT FALSE,
    ADD COLUMN IF NOT EXISTS tc_audio_room_invite              BOOLEAN NOT NULL DEFAULT FALSE,
    ADD COLUMN IF NOT EXISTS privacy_version                   INT     NOT NULL DEFAULT 1,
    -- Production chat pass (chat directive §3.2). chat_availability 'paused'
    -- is STRONGER than who_can_message='no_one': it stops new conversations,
    -- requests, invites and user messages in both directions while keeping
    -- history readable. send_typing_indicators gates the actor's own typing
    -- broadcasts. show_message_preview gates message text in push
    -- notifications (defaults on for plaintext chat; E2EE conversations
    -- default off client-side regardless of this value).
    ADD COLUMN IF NOT EXISTS chat_availability                 TEXT    NOT NULL DEFAULT 'enabled',
    ADD COLUMN IF NOT EXISTS send_typing_indicators            BOOLEAN NOT NULL DEFAULT TRUE,
    ADD COLUMN IF NOT EXISTS show_message_preview              BOOLEAN NOT NULL DEFAULT TRUE;

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


-- ---------------------------------------------------------------------------
-- Added 2026-08-16. These two tables previously existed ONLY in
-- identity-platform/database/migrations/ and were applied to the local
-- database by hand, so every FRESH environment came up without them and
-- registration broke on the first signup.
--
-- The boot-time schema precondition check (identity-shared/store/schemaguard)
-- found this by refusing to start against an empty database. Both blocks below
-- are copied from migrations 016 and 017 and are idempotent, so re-applying
-- them against a database that already has them is a no-op.
-- ---------------------------------------------------------------------------

-- 016 — durable verification-email delivery jobs
--
-- WHY
--
-- Registration used to commit the account and THEN send the verification
-- email, outside the transaction:
--
--     tx.Commit()
--     if err := RequestEmailVerification(...); err != nil { return err }
--
-- A mail-provider blip therefore produced a committed account whose owner saw
-- "registration failed", with nothing anywhere that would ever retry the send.
-- The account existed, the address was claimed, and the only route forward was
-- for the user to guess that signing in would hand them a fresh verification
-- token.
--
-- This table makes the send a durable unit of work enqueued INSIDE the
-- registration transaction. Either the account and its pending email both
-- exist, or neither does. A relay drains the queue with backoff.
--
-- The job deliberately carries NO code. auth.otp_codes stores only a hash, so
-- a code cannot be recovered for a retry anyway — and a queue row holding a
-- live credential would be a secret at rest for no benefit. The relay calls
-- the normal issue-and-send path, which mints a fresh code.

CREATE TABLE IF NOT EXISTS auth.email_delivery_jobs (
    id              BIGSERIAL PRIMARY KEY,
    user_id         UUID        NOT NULL REFERENCES auth.users(user_id) ON DELETE CASCADE,
    -- 'email_verify' today. A column rather than a constant so password-reset
    -- and step-up mail can share the queue without another migration.
    purpose         TEXT        NOT NULL,
    attempts        INT         NOT NULL DEFAULT 0,
    next_attempt_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    sent_at         TIMESTAMPTZ,
    last_error      TEXT
);

-- PARTIAL index: the relay only ever asks for unsent, due work. Indexing the
-- whole table would grow without bound as sent rows accumulate, while the
-- working set stays small.
CREATE INDEX IF NOT EXISTS idx_email_delivery_jobs_due
    ON auth.email_delivery_jobs (next_attempt_at)
    WHERE sent_at IS NULL;

-- Supports the "does this user already have work queued" check and the
-- per-user cleanup path.
CREATE INDEX IF NOT EXISTS idx_email_delivery_jobs_user
    ON auth.email_delivery_jobs (user_id)
    WHERE sent_at IS NULL;

COMMENT ON TABLE auth.email_delivery_jobs IS
    'Durable queue for security emails enqueued transactionally with the domain write. Carries no credential; the relay re-issues.';

-- 017 — idempotency keys for client-retryable writes
--
-- WHY
--
-- POST /v1/auth/register has no idempotency key, so a client that times out
-- mid-write cannot safely retry: it cannot distinguish "not created" from
-- "created, response lost". Today that is masked by the unique constraint on
-- email — but a collision is not idempotency. It returns USER_EXISTS to a user
-- who never saw a successful registration, which reads as "someone already
-- took my address".
--
-- With this table a repeat of the SAME request returns the ORIGINAL response.
--
-- request_hash exists to catch key reuse with different content. Silently
-- returning the first response for a different body would be worse than any
-- error: the caller would believe a request succeeded that was never
-- processed.
--
-- The stored response is a completed HTTP result, not domain state. It expires
-- (see expires_at) because an idempotency key is a retry window, not an audit
-- log — the account row is the durable record.

CREATE TABLE IF NOT EXISTS auth.idempotency_keys (
    -- Scope is (endpoint, key): the same key on a different endpoint is a
    -- different operation, and callers should not have to coordinate a global
    -- namespace.
    endpoint      TEXT        NOT NULL,
    idempotency_key TEXT      NOT NULL,
    -- SHA-256 of the canonical request body. Same key + different hash is a
    -- client bug and must be reported, never silently replayed.
    request_hash  TEXT        NOT NULL,
    status_code   INT         NOT NULL,
    response_body BYTEA       NOT NULL,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    expires_at    TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (endpoint, idempotency_key)
);

-- Supports expiry sweeps without scanning the table.
CREATE INDEX IF NOT EXISTS idx_idempotency_keys_expiry
    ON auth.idempotency_keys (expires_at);

COMMENT ON TABLE auth.idempotency_keys IS
    'Short-lived record of completed responses so a client retry replays the original result instead of duplicating the write.';
