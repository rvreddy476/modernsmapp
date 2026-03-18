CREATE TABLE IF NOT EXISTS creator_tax_profiles (
    user_id UUID PRIMARY KEY,
    pan_encrypted TEXT,
    gstin TEXT,
    tax_residency TEXT NOT NULL DEFAULT 'IN',
    tds_exempt BOOLEAN NOT NULL DEFAULT false,
    verified_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS tds_ledger (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    creator_id UUID NOT NULL,
    financial_year TEXT NOT NULL,
    gross_amount_paise BIGINT NOT NULL,
    tds_amount_paise BIGINT NOT NULL,
    section TEXT NOT NULL DEFAULT '194-O',
    reference_id UUID,
    deducted_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_tds_creator_fy ON tds_ledger(creator_id, financial_year);

CREATE TABLE IF NOT EXISTS gst_ledger (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    transaction_id UUID,
    taxable_amount_paise BIGINT NOT NULL,
    gst_rate_bps INT NOT NULL DEFAULT 1800,
    cgst_paise BIGINT NOT NULL DEFAULT 0,
    sgst_paise BIGINT NOT NULL DEFAULT 0,
    igst_paise BIGINT NOT NULL DEFAULT 0,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS invoices (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID NOT NULL,
    invoice_number SERIAL,
    invoice_type TEXT NOT NULL CHECK (invoice_type IN ('subscription','payout','donation','service_fee')),
    amount_paise BIGINT NOT NULL,
    tax_paise BIGINT NOT NULL DEFAULT 0,
    total_paise BIGINT NOT NULL,
    pdf_media_id UUID,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_inv_user ON invoices(user_id, created_at DESC);
