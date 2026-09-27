-- Alternate audio tracks for short videos (2026-09-27).
--
-- One row per (video asset, language). The uploaded or generated audio is
-- stored under the VIDEO asset's own key prefix (user/<uid>/<mid>/dub/<lang>/…)
-- so it inherits the video's delivery authorization and its purge, and the
-- muxed MP4 outputs are ordinary media_variants rows named dub_<lang>_<rung>,
-- so /serve/:variant and the batch variants map carry them unchanged.
--
-- Processing is the DB-polled job pattern media_caption_jobs uses: claimed
-- with FOR UPDATE SKIP LOCKED, stale claims reclaimed, terminal writes fenced
-- by claim_token.
CREATE TABLE IF NOT EXISTS media_audio_tracks (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    media_asset_id  UUID NOT NULL REFERENCES media_assets(id) ON DELETE CASCADE,
    language        TEXT NOT NULL,
    label           TEXT NOT NULL,
    source          TEXT NOT NULL CHECK (source IN ('uploaded', 'generated')),
    source_language TEXT,
    source_key      TEXT,
    status          TEXT NOT NULL DEFAULT 'pending'
                    CHECK (status IN ('pending', 'processing', 'ready', 'failed')),
    attempts        INT  NOT NULL DEFAULT 0,
    last_error      TEXT,
    claimed_at      TIMESTAMPTZ,
    claim_token     TEXT,
    rungs           TEXT[] NOT NULL DEFAULT '{}',
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (media_asset_id, language)
);

CREATE INDEX IF NOT EXISTS idx_media_audio_tracks_status_claimed
    ON media_audio_tracks (status, claimed_at);
