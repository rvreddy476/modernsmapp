-- Commerce P0 — migration 011: address PII becomes ciphertext.
--
-- LB-24 / v1 §5.14, and the review's §5-D8 ruling.
--
-- Customer names, phone numbers and street addresses were plaintext columns.
-- Aurora storage encryption covers the disk, which protects against someone
-- carrying away a volume — it does nothing about a query, a log line, an
-- analytics export or a compromised read replica.
--
-- Two rulings shape this migration:
--
--   * Review §5.1-5.14 and §5-D8: renaming plaintext to `_legacy` and
--     leaving it for a release does NOT close the finding. The plaintext has
--     to be gone before real customer addresses are handled, so this
--     migration provisions the ciphertext columns and 013 removes the
--     plaintext once the backfill is verified.
--
--   * Review §5-D8: profile addresses and invoice/order snapshots are
--     DIFFERENT retention classes. Order snapshots may be legally required
--     for years; a profile address is not. They therefore get separate key
--     scopes so one can be shredded without destroying the other, and
--     destructive key-shred stays disabled until a CA ruling.
--
-- What stays plaintext, deliberately: postal_code, city, state. Delivery
-- serviceability and the interstate/intrastate GST determination both need
-- them in the query path, and none of the three identifies a person on its
-- own.

-- ─── Ciphertext columns ──────────────────────────────────────────────
--
-- Envelope encryption: a KMS-held data key encrypts the value, and
-- `pii_key_version` records which key generation produced it so a rotation
-- can decrypt old rows while new writes use the new key.

ALTER TABLE customer_addresses
    ADD COLUMN IF NOT EXISTS contact_name_enc   BYTEA,
    ADD COLUMN IF NOT EXISTS phone_enc          BYTEA,
    ADD COLUMN IF NOT EXISTS address_line_1_enc BYTEA,
    ADD COLUMN IF NOT EXISTS address_line_2_enc BYTEA,
    ADD COLUMN IF NOT EXISTS landmark_enc       BYTEA,
    ADD COLUMN IF NOT EXISTS pii_key_version    INT,
    -- Scope drives the retention policy and the key that protects the row.
    ADD COLUMN IF NOT EXISTS pii_scope TEXT NOT NULL DEFAULT 'profile'
        CHECK (pii_scope IN ('profile','order_snapshot'));

-- A deterministic, salted hash for exact-match lookup ("do I already have
-- this address?") without decrypting anything. It is NOT a substitute for
-- the ciphertext and is never displayed.
ALTER TABLE customer_addresses
    ADD COLUMN IF NOT EXISTS lookup_hash TEXT;

CREATE INDEX IF NOT EXISTS idx_customer_addresses_lookup
    ON customer_addresses (user_id, lookup_hash) WHERE lookup_hash IS NOT NULL;

-- ─── Seller pickup addresses ─────────────────────────────────────────
--
-- Same treatment: a seller's pickup address and phone identify a person just
-- as much as a buyer's do, and v1 §4.4 only looked at the customer table.

ALTER TABLE seller_addresses
    ADD COLUMN IF NOT EXISTS contact_name_enc   BYTEA,
    ADD COLUMN IF NOT EXISTS phone_enc          BYTEA,
    ADD COLUMN IF NOT EXISTS address_line_1_enc BYTEA,
    ADD COLUMN IF NOT EXISTS address_line_2_enc BYTEA,
    ADD COLUMN IF NOT EXISTS pii_key_version    INT;

-- ─── Order snapshot ciphertext ───────────────────────────────────────
--
-- The order's address snapshot is the legally interesting copy, so it is
-- encrypted separately from the profile row it was taken from and under the
-- order-snapshot key scope.

ALTER TABLE orders
    ADD COLUMN IF NOT EXISTS delivery_address_snapshot_enc BYTEA,
    ADD COLUMN IF NOT EXISTS snapshot_key_version          INT;

-- ─── Backfill progress ───────────────────────────────────────────────
--
-- Encryption cannot happen in SQL: the data key lives in KMS and only the
-- application can call it. The backfill therefore runs as an application
-- job, and this table is how the deployment gate knows whether it finished
-- — the plaintext drop in 013 refuses to run until this says complete.

CREATE TABLE IF NOT EXISTS pii_backfill_progress (
    table_name    TEXT PRIMARY KEY,
    total_rows    BIGINT NOT NULL DEFAULT 0,
    encrypted_rows BIGINT NOT NULL DEFAULT 0,
    last_id       UUID,
    completed_at  TIMESTAMPTZ,
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

INSERT INTO pii_backfill_progress (table_name) VALUES
    ('customer_addresses'), ('seller_addresses'), ('orders')
ON CONFLICT DO NOTHING;

-- ─── Retention ───────────────────────────────────────────────────────
--
-- Recorded, not enforced. Review §5-D8: do NOT key-shred invoice history on
-- an unapproved 90-day assumption — destroying records we may be required to
-- keep is worse than retaining encrypted ones. The purge job reads this
-- table and refuses to act on any row whose policy is not `approved`.

CREATE TABLE IF NOT EXISTS pii_retention_policy (
    scope           TEXT PRIMARY KEY,
    retain_days     INT,
    shred_enabled   BOOLEAN NOT NULL DEFAULT FALSE,
    approval_status TEXT NOT NULL DEFAULT 'pending_legal_review'
        CHECK (approval_status IN ('pending_legal_review','approved')),
    notes           TEXT,
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

INSERT INTO pii_retention_policy (scope, retain_days, shred_enabled, approval_status, notes) VALUES
    ('profile', 90, FALSE, 'pending_legal_review',
     'Proposed: 90 days decryptable after last use, then key-shred. Awaiting founder/CA ruling (D8).'),
    ('order_snapshot', NULL, FALSE, 'pending_legal_review',
     'GST and consumer-dispute retention may exceed any product default. Shred stays DISABLED until legal rules; encrypted retention is the safe state.')
ON CONFLICT DO NOTHING;
