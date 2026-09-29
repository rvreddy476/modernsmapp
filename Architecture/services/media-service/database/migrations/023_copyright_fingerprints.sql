-- Copyright Match phase 1: fingerprint jobs, fingerprints, the anchor index,
-- pairs, the SEPARATE pair outbox, and shadow observations (plan section
-- 6.1, 2026-09-29). Everything here is derived data: it can be truncated
-- and rebuilt by the backfill command. media_event_outbox and its relay are
-- untouched (P-15).
--
-- No table here has a foreign key to media_assets, on purpose: an asset
-- purge must be able to write an invalidation for a pair before deleting
-- the fingerprint, and a deletion must never erase an unpublished
-- invalidation (the same reasoning as migration 013). The purge paths
-- delete these rows explicitly. Every table with a media_asset_id column is
-- listed in reclaim_policy.go's DerivedMediaTables.

-- One row per (asset, generation, algorithm). Enqueued in the same
-- transaction as the transcode completion, and by reprocess and the backfill
-- command. Claimed with FOR UPDATE SKIP LOCKED and a claim token; the final
-- write needs a matching token AND the generation fence
-- (media_generation = ready_generation = job generation, status 'ready').
CREATE TABLE IF NOT EXISTS media_fingerprint_jobs (
    media_asset_id   UUID     NOT NULL,
    media_generation BIGINT   NOT NULL,
    algo_version     SMALLINT NOT NULL,
    priority         SMALLINT NOT NULL DEFAULT 0,   -- 0 new upload, 10 backfill
    status           TEXT     NOT NULL DEFAULT 'queued'
                     CHECK (status IN ('queued','claimed','done','superseded','failed','skipped')),
    skip_reason      TEXT,                          -- required when status = 'skipped'
    attempts         INT      NOT NULL DEFAULT 0,
    claim_token      UUID,
    heartbeat_at     TIMESTAMPTZ,
    not_before       TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    progress_ms      INT      NOT NULL DEFAULT 0,
    truncation       JSONB,
    last_error       TEXT,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (media_asset_id, media_generation, algo_version),
    CHECK (status <> 'skipped' OR skip_reason IS NOT NULL)
);
CREATE INDEX IF NOT EXISTS media_fingerprint_jobs_ready
    ON media_fingerprint_jobs (priority, not_before)
    WHERE status = 'queued';
-- Lease reclaim: a claimed job whose heartbeat went stale.
CREATE INDEX IF NOT EXISTS media_fingerprint_jobs_claimed
    ON media_fingerprint_jobs (heartbeat_at)
    WHERE status = 'claimed';

-- The whole 2 fps frame sequence, flat and static frames included, as
-- [t_ms int32 LE | hash uint64 LE | flags uint8]* (13 bytes a frame).
-- input_ref is a rung or variant name, never a URL; input_etags is audit.
CREATE TABLE IF NOT EXISTS copyright_fingerprints (
    media_asset_id   UUID     NOT NULL,
    media_generation BIGINT   NOT NULL,
    algo_version     SMALLINT NOT NULL,
    input_kind       TEXT     NOT NULL CHECK (input_kind IN ('hls_rung','mp4_variant','original')),
    input_ref        TEXT     NOT NULL,
    input_etags      JSONB,
    duration_ms      INT      NOT NULL,
    informative_ms   INT      NOT NULL,
    frames           BYTEA    NOT NULL,
    anchor_spacing_s SMALLINT NOT NULL,
    dur_bucket       SMALLINT NOT NULL,
    superseded_at    TIMESTAMPTZ,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (media_asset_id, media_generation, algo_version)
);

-- The multi-index: four 16-bit bands of every anchor hash, keyed by
-- algorithm, duration bucket, band and band value. The primary key IS the
-- covering lookup index. Each posting carries the full hash so a hit can be
-- filtered to Hamming ≤ τ_hit without a second read.
CREATE TABLE IF NOT EXISTS copyright_anchor_postings (
    algo_version     SMALLINT NOT NULL,
    dur_bucket       SMALLINT NOT NULL,
    band_no          SMALLINT NOT NULL CHECK (band_no BETWEEN 0 AND 3),
    band_value       INT      NOT NULL CHECK (band_value BETWEEN 0 AND 65535),
    media_asset_id   UUID     NOT NULL,
    media_generation BIGINT   NOT NULL,
    anchor_ms        INT      NOT NULL,
    hash             BIGINT   NOT NULL,
    PRIMARY KEY (algo_version, dur_bucket, band_no, band_value, media_asset_id, media_generation, anchor_ms)
);
-- The stale-postings sweeper and the purge delete by asset.
CREATE INDEX IF NOT EXISTS copyright_anchor_postings_by_asset
    ON copyright_anchor_postings (media_asset_id, media_generation);

-- Hot keys, refreshed nightly (phase 2); the lookup also caps every key
-- at LIMIT 257 and drops a key that hits the cap entirely.
CREATE TABLE IF NOT EXISTS copyright_band_hot (
    algo_version SMALLINT NOT NULL,
    dur_bucket   SMALLINT NOT NULL,
    band_no      SMALLINT NOT NULL,
    band_value   INT      NOT NULL,
    postings     BIGINT   NOT NULL,
    refreshed_at TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (algo_version, dur_bucket, band_no, band_value)
);

-- Pairs: media facts only. media_lo < media_hi in byte order. A revision
-- bumps only when class, score, status or direction changes, and every
-- revision writes exactly one outbox row in the same transaction.
CREATE TABLE IF NOT EXISTS copyright_pairs (
    pair_id       UUID PRIMARY KEY,
    media_lo      UUID     NOT NULL,
    gen_lo        BIGINT   NOT NULL,
    media_hi      UUID     NOT NULL,
    gen_hi        BIGINT   NOT NULL,
    algo_version  SMALLINT NOT NULL,
    pair_revision BIGINT   NOT NULL DEFAULT 1,
    status        TEXT     NOT NULL CHECK (status IN ('active','invalidated')),
    invalidated_reason TEXT CHECK (invalidated_reason IN ('reprocessed','deleted','algo_retired')),
    direction     TEXT     NOT NULL CHECK (direction IN ('lo_earlier','hi_earlier','contemporaneous','ambiguous_legacy')),
    class         TEXT     NOT NULL CHECK (class IN ('full_or_near_full','contains','partial','short_clip_candidate','insufficient_evidence')),
    ref_coverage  REAL,
    copy_coverage REAL,
    matched_s     REAL,
    matched_informative_s REAL,
    median_hamming REAL,
    p90_hamming    REAL,
    diversity      SMALLINT,
    computed_at    TIMESTAMPTZ NOT NULL,
    UNIQUE (media_lo, gen_lo, media_hi, gen_hi, algo_version),
    CHECK (media_lo < media_hi),
    CHECK (status <> 'invalidated' OR invalidated_reason IS NOT NULL)
);
CREATE INDEX IF NOT EXISTS copyright_pairs_by_lo ON copyright_pairs (media_lo);
CREATE INDEX IF NOT EXISTS copyright_pairs_by_hi ON copyright_pairs (media_hi);

-- The pair outbox. SEPARATE from media_event_outbox: that table's
-- UNIQUE (media_asset_id, event_type) is load-bearing for three writers and
-- four readers, and its relay stops on the first failure. This one is keyed
-- by (pair_id, pair_revision), claimed with SKIP LOCKED under a lease, and
-- its relay continues past a failed row. Not purged: it carries no PII.
CREATE TABLE IF NOT EXISTS copyright_pair_outbox (
    event_id        UUID PRIMARY KEY,
    pair_id         UUID     NOT NULL,
    pair_revision   BIGINT   NOT NULL,
    event_type      TEXT     NOT NULL CHECK (event_type IN ('media.copyright_pair.upserted','media.copyright_pair.invalidated')),
    payload         JSONB    NOT NULL,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    next_attempt_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    published_at    TIMESTAMPTZ,
    attempts        INT      NOT NULL DEFAULT 0,
    last_error      TEXT,
    UNIQUE (pair_id, pair_revision)
);
CREATE INDEX IF NOT EXISTS copyright_pair_outbox_due
    ON copyright_pair_outbox (next_attempt_at)
    WHERE published_at IS NULL;

-- Shadow observations: every verified candidate that is NOT
-- full_or_near_full (contains, partial, short_clip_candidate,
-- insufficient_evidence), and truncations. Never relayed, never surfaced;
-- they are what the E1–E3 evaluation reads. One row per (asset, generation,
-- algo, other asset, other generation), latest computation wins.
CREATE TABLE IF NOT EXISTS copyright_match_observations (
    media_asset_id    UUID     NOT NULL,
    media_generation  BIGINT   NOT NULL,
    algo_version      SMALLINT NOT NULL,
    other_media_id    UUID     NOT NULL,
    other_generation  BIGINT   NOT NULL,
    class             TEXT     NOT NULL CHECK (class IN ('full_or_near_full','contains','partial','short_clip_candidate','insufficient_evidence')),
    ref_coverage      REAL,
    copy_coverage     REAL,
    matched_s         REAL,
    matched_informative_s REAL,
    median_hamming    REAL,
    p90_hamming       REAL,
    diversity         SMALLINT,
    min_band_distance SMALLINT,
    truncated         BOOLEAN  NOT NULL DEFAULT FALSE,
    computed_at       TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (media_asset_id, media_generation, algo_version, other_media_id, other_generation)
);
