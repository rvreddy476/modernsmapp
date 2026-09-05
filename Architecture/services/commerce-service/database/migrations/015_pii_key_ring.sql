-- The PII key ring: durable wrapped data keys, per scope and version.
--
-- WHY THIS EXISTS. `pii.KeyProvider.DataKey(scope, version)` is asked to
-- return "the same 32 bytes you gave me for version N", and the ciphertext
-- envelope records only that integer N (internal/pii/envelope.go: the wire
-- format is version(4 BE) | nonce(12) | ciphertext+tag).
--
-- KMS cannot satisfy that from a number. `GenerateDataKey` returns a plaintext
-- key AND an opaque ciphertext blob, and the ONLY way to get that same
-- plaintext back later is to hand the same blob to `Decrypt`. A provider that
-- called GenerateDataKey again for version 2 would get different bytes, and
-- every row written under the old version 2 would be permanently
-- undecryptable.
--
-- So the blob has to be stored, keyed by exactly what the ciphertext records:
-- (scope, version). That is this table. Losing it loses the data — it is
-- backup-critical in the same way the encrypted columns are.
--
-- WHAT IS NOT STORED HERE: any plaintext key material. Every row is a KMS
-- ciphertext that is useless without the CMK and the matching encryption
-- context. A dump of this table decrypts nothing.
--
-- EXPAND-ONLY. A new table with no constraint on any existing one, so an old
-- replica is unaffected and this belongs in the boot set rather than behind
-- the drained-fleet gate.

CREATE TABLE IF NOT EXISTS pii_key_ring (
    -- Matches pii.Scope. Retention classes are separate keys so a profile
    -- shred cannot destroy an order snapshot that GST rules require us to
    -- keep (review §5-D8).
    scope           TEXT   NOT NULL,

    -- The integer the ciphertext envelope carries. Starts at 1; version 0 is
    -- reserved by the KeyProvider contract to mean "whatever is current".
    version         INT    NOT NULL CHECK (version > 0),

    -- The KMS ciphertext blob from GenerateDataKey. Never a plaintext key.
    wrapped_key     BYTEA  NOT NULL CHECK (octet_length(wrapped_key) > 0),

    -- Which CMK wrapped it. Recorded because a key policy change or a
    -- re-key must be able to tell which rows were wrapped by which CMK, and
    -- because Decrypt against the wrong CMK fails in a way that is otherwise
    -- hard to diagnose.
    kms_key_id      TEXT   NOT NULL,

    -- The encryption context this blob was created under. KMS requires the
    -- SAME context at Decrypt, so it is part of the record rather than
    -- reconstructed from config that may have drifted.
    encryption_context JSONB NOT NULL DEFAULT '{}'::jsonb,

    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    -- Set when a newer version takes over. The row is NEVER deleted: old
    -- rows still decrypt with it, and dropping it is equivalent to shredding
    -- every value written while it was current.
    retired_at      TIMESTAMPTZ,

    PRIMARY KEY (scope, version)
);

-- At most one live version per scope.
--
-- A partial unique index rather than application logic: two pods racing to
-- create the first key for a scope must not both succeed, because each would
-- write rows under a key the other cannot read. The loser retries and finds
-- the winner's version.
CREATE UNIQUE INDEX IF NOT EXISTS uq_pii_key_ring_active
    ON pii_key_ring (scope) WHERE retired_at IS NULL;

COMMENT ON TABLE pii_key_ring IS
    'Commerce P0 LB-24: KMS-wrapped data keys, keyed by the (scope, version) the '
    'ciphertext envelope records. BACKUP-CRITICAL — losing a row makes every value '
    'written under that version permanently undecryptable. Contains no plaintext key '
    'material; every wrapped_key is useless without the CMK and its encryption context.';
