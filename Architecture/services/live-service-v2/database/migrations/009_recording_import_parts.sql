-- 009_recording_import_parts.sql — the replay joins every complete part (16 Nov 2026).
--
-- 008 kept every egress as its own part but imported only the longest one.
-- The import job now freezes, when it is queued, the object keys of ALL the
-- stream's complete parts in part order; live-service sends them to
-- media-service's import as `keys`, and media-service's worker joins them
-- (ffmpeg concat) into the one replay. object_key stays the longest part:
-- the single-key fallback an import without `keys` (and a pre-parts stream)
-- still uses. duration_ms is now the sum of the complete parts.
--
-- '{}' (pre-009 jobs, pre-parts streams, one-part streams queued before
-- this): the single object_key path, unchanged.
--
-- Additive and idempotent: one column with a default.

ALTER TABLE live_recording_imports
    ADD COLUMN IF NOT EXISTS object_keys TEXT[] NOT NULL DEFAULT '{}';
