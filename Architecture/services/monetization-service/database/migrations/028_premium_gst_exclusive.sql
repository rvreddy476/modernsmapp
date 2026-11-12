-- 028: Momentum Premium prices EXCLUDE GST (founder decision, 13 Nov 2026).
--
-- The configured price (PREMIUM_PRICE_*_PAISE) is the taxable value; GST at
-- 18% (shared/gst PLATFORM_DIGITAL_SUBSCRIPTION, SAC 998439, adviser-flagged)
-- is added on top. A pass stores the split it was sold with, and its
-- amount_paise is the TOTAL: the payments intent amount and the amount the
-- signed payment.succeeded event must carry.
--
-- Existing rows (none in production) keep NULL in every new column: their
-- amount_paise stays what was charged. A row that has a split must add up.

ALTER TABLE premium_periods
    ADD COLUMN IF NOT EXISTS taxable_paise   BIGINT,
    ADD COLUMN IF NOT EXISTS cgst_paise      BIGINT,
    ADD COLUMN IF NOT EXISTS sgst_paise      BIGINT,
    ADD COLUMN IF NOT EXISTS igst_paise      BIGINT,
    ADD COLUMN IF NOT EXISTS total_paise     BIGINT,
    ADD COLUMN IF NOT EXISTS gst_rate_bp     INT,
    ADD COLUMN IF NOT EXISTS sac             TEXT,
    ADD COLUMN IF NOT EXISTS tax_provisional BOOLEAN;

DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'premium_periods_gst_split') THEN
        ALTER TABLE premium_periods ADD CONSTRAINT premium_periods_gst_split CHECK (
            (taxable_paise IS NULL AND cgst_paise IS NULL AND sgst_paise IS NULL AND igst_paise IS NULL
             AND total_paise IS NULL AND gst_rate_bp IS NULL AND sac IS NULL AND tax_provisional IS NULL)
            OR
            (taxable_paise > 0 AND cgst_paise >= 0 AND sgst_paise >= 0 AND igst_paise >= 0
             AND gst_rate_bp BETWEEN 0 AND 10000 AND sac ~ '^[0-9]{6}$' AND tax_provisional IS NOT NULL
             AND (igst_paise = 0 OR (cgst_paise = 0 AND sgst_paise = 0))
             AND total_paise = taxable_paise + cgst_paise + sgst_paise + igst_paise
             AND amount_paise = total_paise)
        );
    END IF;
END $$;
