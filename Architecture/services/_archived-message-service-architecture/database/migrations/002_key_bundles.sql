-- 002_key_bundles.sql: E2E encryption key bundle storage
-- Server stores only public keys; private keys never leave client devices.
CREATE TABLE IF NOT EXISTS chat.key_bundles (
    user_id           UUID PRIMARY KEY,
    identity_key      BYTEA NOT NULL,
    signed_pre_key    BYTEA NOT NULL,
    pre_key_signature BYTEA,
    one_time_pre_keys BYTEA[] DEFAULT '{}',
    updated_at        TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

COMMENT ON TABLE chat.key_bundles IS
    'Public key bundles for E2E encrypted messaging (Signal Protocol X3DH).
     Server is a blind intermediary; private keys never stored here.';
