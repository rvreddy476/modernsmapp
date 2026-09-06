-- 014: pay creators on the QUALITY of the views, not only the count.
--
-- Until now a settlement was views x rpm / 1000 and nothing else. The
-- analytics service computes a content quality score (CQS) in [0,1] per
-- content item per day, and it was read by nobody. This migration adds
-- the storage for a *band-limited* quality multiplier and records, on
-- every settled row, exactly how the multiplier changed the payment so
-- a creator can be shown why they were paid what they were.
--
-- Deliberately a BAND, not a raw multiply:
--   * CQS near zero is common for content with few impressions, and a
--     raw multiply would pay a creator nothing for views that really
--     happened. The floor guarantees genuine views always pay.
--   * CQS is noisy at low impression counts, so the multiplier is also
--     shrunk toward neutral until a content item has enough impressions
--     for its score to mean anything (applied in code, recorded here).
--   * The ceiling caps the upside so a single viral-engagement day
--     cannot mint an unbounded payout.

CREATE TABLE IF NOT EXISTS monetization_quality_bands (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    content_type    TEXT NOT NULL CHECK (content_type IN ('long_video','flick')),
    region_code     TEXT NOT NULL DEFAULT 'IN',
    -- Multiplier bounds in basis points: 10000 bps = 1.0x (no change).
    floor_bps       BIGINT NOT NULL CHECK (floor_bps >= 0),
    ceiling_bps     BIGINT NOT NULL CHECK (ceiling_bps >= 0),
    -- The CQS at which the multiplier is exactly 1.0x. Content scoring
    -- at the pivot is paid the plain views x RPM amount.
    pivot_cqs       DOUBLE PRECISION NOT NULL DEFAULT 0.35
        CHECK (pivot_cqs > 0 AND pivot_cqs < 1),
    -- Bayesian shrink strength: the impression count at which the
    -- measured CQS is trusted halfway. Below it, the score is pulled
    -- toward the pivot (i.e. toward paying the plain amount).
    confidence_impressions BIGINT NOT NULL DEFAULT 1000
        CHECK (confidence_impressions >= 0),
    enabled         BOOLEAN NOT NULL DEFAULT TRUE,
    effective_from  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    effective_to    TIMESTAMPTZ,
    notes           TEXT,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    created_by      UUID,
    CHECK (ceiling_bps >= floor_bps)
);

CREATE INDEX IF NOT EXISTS idx_quality_bands_lookup
    ON monetization_quality_bands (content_type, region_code, effective_from DESC);

-- Launch baseline: 0.85x floor, 1.25x ceiling, neutral at CQS 0.35.
-- A creator whose content scores zero still keeps 85% of the plain
-- views x RPM amount; the best-scoring content earns 25% more.
INSERT INTO monetization_quality_bands
    (content_type, region_code, floor_bps, ceiling_bps, pivot_cqs, confidence_impressions, notes)
SELECT 'long_video', 'IN', 8500, 12500, 0.35, 1000,
       'launch baseline: 0.85x-1.25x quality band, neutral at CQS 0.35'
WHERE NOT EXISTS (
    SELECT 1 FROM monetization_quality_bands
    WHERE content_type = 'long_video' AND region_code = 'IN' AND effective_to IS NULL
);

INSERT INTO monetization_quality_bands
    (content_type, region_code, floor_bps, ceiling_bps, pivot_cqs, confidence_impressions, notes)
SELECT 'flick', 'IN', 8500, 12500, 0.35, 1000,
       'launch baseline: 0.85x-1.25x quality band, neutral at CQS 0.35'
WHERE NOT EXISTS (
    SELECT 1 FROM monetization_quality_bands
    WHERE content_type = 'flick' AND region_code = 'IN' AND effective_to IS NULL
);

-- Per-settlement audit trail. base_gross_paise is the historical
-- views x RPM number; gross_paise stays the amount actually paid, so
-- nothing downstream (wallet, ledger, dashboard totals) changes meaning.
-- When the band is disabled these columns record a 1.0x multiplier and
-- base_gross_paise == gross_paise, i.e. the old behaviour, visibly.
ALTER TABLE creator_fund_earnings
    ADD COLUMN IF NOT EXISTS base_gross_paise BIGINT NOT NULL DEFAULT 0;
ALTER TABLE creator_fund_earnings
    ADD COLUMN IF NOT EXISTS quality_cqs DOUBLE PRECISION NOT NULL DEFAULT 0;
ALTER TABLE creator_fund_earnings
    ADD COLUMN IF NOT EXISTS quality_effective_cqs DOUBLE PRECISION NOT NULL DEFAULT 0;
ALTER TABLE creator_fund_earnings
    ADD COLUMN IF NOT EXISTS quality_impressions BIGINT NOT NULL DEFAULT 0;
ALTER TABLE creator_fund_earnings
    ADD COLUMN IF NOT EXISTS quality_multiplier_bps BIGINT NOT NULL DEFAULT 10000;

-- Backfill rows settled before the multiplier existed: they were paid
-- the plain amount, which is a 1.0x multiplier by definition.
UPDATE creator_fund_earnings
SET base_gross_paise = gross_paise
WHERE base_gross_paise = 0 AND gross_paise <> 0;
