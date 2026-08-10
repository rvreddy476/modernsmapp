-- Module 1 fixes-v1.
--
-- Codex P0-2: media_subtitles stays the ONE canonical caption/transcript
-- content store (deviation approved in principle), but three things were
-- broken:
--   a) the service wrote source='auto', which the CHECK rejects
--      (allowed: auto_generated | manual | translated);
--   b) content_url was written empty and the transcript text was never
--      persisted anywhere, so CaptionStatus.Text could never populate;
--   c) there was no durable request state, so status was inferred and a
--      failure was forgotten on the next GET.
--
-- Fix: add a `content` column for inline transcript text (URL stays for
-- rendered .vtt files when we produce them), and a separate
-- media_caption_jobs table holding ONLY request lifecycle state — no
-- transcript content, so nothing is duplicated.
ALTER TABLE media_subtitles
    ADD COLUMN IF NOT EXISTS content TEXT,
    ADD COLUMN IF NOT EXISTS edited_by_owner BOOLEAN NOT NULL DEFAULT FALSE,
    ADD COLUMN IF NOT EXISTS updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW();

-- content_url was NOT NULL with no default; inline transcripts have no
-- URL until a .vtt is rendered.
ALTER TABLE media_subtitles ALTER COLUMN content_url SET DEFAULT '';

-- Durable caption request lifecycle (status only — content lives in
-- media_subtitles). "unavailable" is computed at read time when no real
-- backend is configured and is never persisted as a success.
CREATE TABLE IF NOT EXISTS media_caption_jobs (
    media_id     UUID PRIMARY KEY REFERENCES media_assets(id) ON DELETE CASCADE,
    language     TEXT NOT NULL DEFAULT '',
    status       TEXT NOT NULL DEFAULT 'pending'
                 CHECK (status IN ('pending','running','completed','failed')),
    attempts     INT NOT NULL DEFAULT 0,
    last_error   TEXT,
    claimed_at   TIMESTAMPTZ,
    -- fixes-v2 / Codex P1-4: fences complete/fail/release so a stale
    -- worker cannot finalize a job another worker has reclaimed.
    claim_token  UUID,
    requested_by UUID,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_caption_jobs_claimable
    ON media_caption_jobs (created_at)
    WHERE status IN ('pending','running');

-- Codex P1-7: decorative is a first-class, persisted state — distinct
-- from "no description yet" — so screen readers can correctly SKIP the
-- image on every surface that references this canonical asset.
ALTER TABLE media_assets
    ADD COLUMN IF NOT EXISTS alt_decorative BOOLEAN NOT NULL DEFAULT FALSE;

-- fixes-v2 / Codex P2-1: enforce the mutual exclusion at the canonical
-- layer, not only in application code. A decorative asset must carry no
-- description — otherwise screen readers get contradictory instructions.
ALTER TABLE media_assets DROP CONSTRAINT IF EXISTS media_assets_alt_decorative_check;
ALTER TABLE media_assets ADD CONSTRAINT media_assets_alt_decorative_check
    CHECK (NOT (alt_decorative AND COALESCE(alt_text, '') <> ''));
