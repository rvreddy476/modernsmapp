-- Hidden chats (contract pinned 24 Oct 2026). Additive and idempotent;
-- database/setup.sql carries the same statements (applied on every boot by
-- BootstrapSchema).
--
-- A member hides one conversation FOR THEMSELVES. That is a NEW per-member
-- flag, `locked`: it is not `hidden` (which already means "deleted for me")
-- and not the 'archived' label. Every list a member sees excludes their
-- locked conversations; reading or writing one needs an open token. The
-- other members are unaffected.
ALTER TABLE chat.conversation_members
    ADD COLUMN IF NOT EXISTS locked    BOOLEAN NOT NULL DEFAULT FALSE,
    ADD COLUMN IF NOT EXISTS locked_at TIMESTAMPTZ;
CREATE INDEX IF NOT EXISTS idx_conversation_members_locked
    ON chat.conversation_members(user_id) WHERE locked;

-- The account's two secrets, set once per account and shared by every device.
-- Only Argon2id PHC strings are ever stored; the CHECK makes a raw secret
-- impossible to write even by a buggy code path.
CREATE TABLE IF NOT EXISTS chat.hidden_chat_settings (
    user_id            UUID PRIMARY KEY,
    secret_key_hash    TEXT NOT NULL,
    chat_password_hash TEXT NOT NULL,
    -- strict: a reset after sign-in wipes (clear-for-me) every locked
    -- conversation instead of keeping it behind the new secrets.
    strict             BOOLEAN NOT NULL DEFAULT FALSE,
    -- shortcut: the web key combination that opens the key prompt (display
    -- preference only; NULL = the client default Ctrl+Shift+H).
    shortcut           TEXT,
    created_at         TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at         TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT hidden_chat_settings_hashes_only CHECK (
        secret_key_hash LIKE '$argon2id$%' AND chat_password_hash LIKE '$argon2id$%'
    ),
    CONSTRAINT hidden_chat_settings_shortcut_len CHECK (shortcut IS NULL OR length(shortcut) <= 64)
);

-- Wrong-attempt counters per account and secret. Kept in Postgres (not only
-- Redis) so a restart or a Redis flush cannot reset a lockout. 5 failures
-- lock the secret; each lockout raises lockout_level (15 min, 1 h, 24 h).
CREATE TABLE IF NOT EXISTS chat.hidden_chat_attempts (
    user_id         UUID NOT NULL,
    secret_kind     TEXT NOT NULL CHECK (secret_kind IN ('secret_key', 'chat_password')),
    failures        INT NOT NULL DEFAULT 0,
    lockout_level   INT NOT NULL DEFAULT 0,
    locked_until    TIMESTAMPTZ,
    last_failure_at TIMESTAMPTZ,
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (user_id, secret_kind)
);

-- Reveal / open session tokens. The client holds 32 random bytes; only their
-- SHA-256 is stored, bound to the user and the device (the access token's
-- session id). expires_at slides forward 15 minutes on every valid use.
CREATE TABLE IF NOT EXISTS chat.hidden_chat_tokens (
    token_hash   BYTEA PRIMARY KEY,
    user_id      UUID NOT NULL,
    device_key   TEXT NOT NULL,
    kind         TEXT NOT NULL CHECK (kind IN ('reveal', 'open')),
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    expires_at   TIMESTAMPTZ NOT NULL,
    last_used_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_hidden_chat_tokens_user ON chat.hidden_chat_tokens(user_id);
