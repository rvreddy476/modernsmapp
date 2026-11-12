-- 029: GST tax invoices and credit notes for Momentum Premium (13 Nov 2026).
--
-- A tax invoice is written in the SAME transaction that marks a pass paid from
-- the signed payment.succeeded event; a credit note in the SAME transaction as
-- the full refund that revokes it. One of each per pass at most.
--
-- Numbers (CGST Rules 46(b) and 53): one series per kind, consecutive within
-- an Indian financial year (IST), at most 16 characters:
--   MP/26-27/000001   tax invoices
--   MPC/26-27/000001  credit notes
-- taken from premium_invoice_counters in the issuing transaction, so a
-- rollback takes the number back and the series has no gaps.
--
-- Amounts are integer paise copied from the split stored on the pass
-- (migration 028). A credit note is the exact negative of its invoice. The
-- supplier and buyer are snapshotted: a later change of configuration or
-- display name never rewrites an issued document. No e-mail or phone here.

-- The buyer's display name, read best effort at checkout, printed on the invoice.
ALTER TABLE premium_periods ADD COLUMN IF NOT EXISTS buyer_name TEXT;

CREATE TABLE IF NOT EXISTS premium_invoice_counters (
    series     TEXT        NOT NULL CHECK (series IN ('MP', 'MPC')),
    fy         TEXT        NOT NULL CHECK (fy ~ '^[0-9]{2}-[0-9]{2}$'),
    last_seq   BIGINT      NOT NULL CHECK (last_seq > 0),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (series, fy)
);

CREATE TABLE IF NOT EXISTS premium_invoices (
    id                  UUID        PRIMARY KEY,
    kind                TEXT        NOT NULL CHECK (kind IN ('invoice', 'credit_note')),
    series              TEXT        NOT NULL,
    fy                  TEXT        NOT NULL,
    seq                 BIGINT      NOT NULL CHECK (seq > 0),
    number              TEXT        NOT NULL UNIQUE CHECK (length(number) <= 16 AND number ~ '^[A-Z]+/[0-9]{2}-[0-9]{2}/[0-9]{6,}$'),
    user_id             UUID        NOT NULL,
    pass_id             UUID        NOT NULL REFERENCES premium_periods(id),
    issued_at           TIMESTAMPTZ NOT NULL,
    invoice_date        DATE        NOT NULL,
    supplier_legal_name TEXT        NOT NULL DEFAULT '',
    supplier_address    TEXT        NOT NULL DEFAULT '',
    supplier_gstin      TEXT        NOT NULL DEFAULT '',
    supplier_state_code TEXT        NOT NULL DEFAULT '',
    supplier_state_name TEXT        NOT NULL DEFAULT '',
    buyer_name          TEXT        NOT NULL DEFAULT '',
    pos_state_code      TEXT        NOT NULL DEFAULT '',
    pos_state_name      TEXT        NOT NULL DEFAULT '',
    description         TEXT        NOT NULL,
    sac                 TEXT        NOT NULL CHECK (sac ~ '^[0-9]{6}$'),
    plan_code           TEXT        NOT NULL,
    duration_days       INT         NOT NULL CHECK (duration_days > 0),
    gst_rate_bp         INT         NOT NULL CHECK (gst_rate_bp BETWEEN 0 AND 10000),
    taxable_paise       BIGINT      NOT NULL,
    cgst_paise          BIGINT      NOT NULL,
    sgst_paise          BIGINT      NOT NULL,
    igst_paise          BIGINT      NOT NULL,
    total_paise         BIGINT      NOT NULL,
    currency            TEXT        NOT NULL DEFAULT 'INR',
    -- Internal only: the rate awaits the adviser. Never on the document.
    tax_provisional     BOOLEAN     NOT NULL,
    original_invoice_id UUID        REFERENCES premium_invoices(id),
    original_number     TEXT,
    original_date       DATE,
    -- The row is its own outbox: premium.invoice.issued /
    -- premium.credit_note.issued is published after commit and retried by
    -- the premium worker until published_at is set.
    published_at        TIMESTAMPTZ,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (series, fy, seq),
    UNIQUE (pass_id, kind),
    CHECK ((kind = 'invoice' AND series = 'MP') OR (kind = 'credit_note' AND series = 'MPC')),
    CHECK (total_paise = taxable_paise + cgst_paise + sgst_paise + igst_paise),
    CHECK (igst_paise = 0 OR (cgst_paise = 0 AND sgst_paise = 0)),
    CHECK (
        (kind = 'invoice' AND taxable_paise > 0 AND cgst_paise >= 0 AND sgst_paise >= 0 AND igst_paise >= 0
         AND original_invoice_id IS NULL AND original_number IS NULL AND original_date IS NULL)
        OR
        (kind = 'credit_note' AND taxable_paise < 0 AND cgst_paise <= 0 AND sgst_paise <= 0 AND igst_paise <= 0
         AND original_invoice_id IS NOT NULL AND original_number IS NOT NULL AND original_date IS NOT NULL)
    )
);

CREATE INDEX IF NOT EXISTS idx_premium_invoices_user ON premium_invoices (user_id, issued_at DESC);
CREATE INDEX IF NOT EXISTS idx_premium_invoices_date ON premium_invoices (invoice_date, kind, seq);
CREATE INDEX IF NOT EXISTS idx_premium_invoices_unpublished ON premium_invoices (issued_at) WHERE published_at IS NULL;
