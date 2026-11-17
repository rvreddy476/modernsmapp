-- 034_upload_quota.sql: per-user video upload budget (long-video audit lv-3).
--
-- media_upload_usage is the durable ledger: one row per confirmed video,
-- written by AdmitVideoUpload in the transaction that checked the budget
-- (under a per-user advisory lock). No foreign key on purpose: deleting a
-- video does not refund its bytes or minutes, so delete-and-re-upload cannot
-- reset the budget. duration_ms NULL means the length was not readable from
-- the header (non-MP4); is_long marks a video over the reel length or of
-- unknown length, which counts toward the in-flight long-video cap while its
-- asset is 'uploaded' or 'processing'.
--
-- media_upload_quota_overrides is how an account's limits are raised (or
-- lowered) without a deploy: a NULL column keeps the deployment default
-- (MEDIA_UPLOAD_DAILY_VIDEO_BYTES, MEDIA_UPLOAD_DAILY_VIDEO_SECONDS,
-- MEDIA_UPLOAD_MAX_INFLIGHT_LONG). Until an admin route exists, a grant is
-- an audited SQL change, for example:
--
--   INSERT INTO media_upload_quota_overrides
--          (user_id, daily_video_bytes, daily_video_seconds, max_inflight_long, reason, granted_by)
--   VALUES ('<user uuid>', 107374182400, 86400, 4, 'verified studio, ticket #', '<admin>')
--   ON CONFLICT (user_id) DO UPDATE SET daily_video_bytes = EXCLUDED.daily_video_bytes,
--          daily_video_seconds = EXCLUDED.daily_video_seconds,
--          max_inflight_long = EXCLUDED.max_inflight_long,
--          reason = EXCLUDED.reason, granted_by = EXCLUDED.granted_by, updated_at = NOW();
--
-- Rollback: both tables are new and only read by the code that ships with
-- them; the previous image ignores them, so it runs unchanged on this schema.

CREATE TABLE IF NOT EXISTS media_upload_usage (
    media_id     UUID        PRIMARY KEY,
    uploader_id  UUID        NOT NULL,
    bytes        BIGINT      NOT NULL CHECK (bytes >= 0),
    duration_ms  BIGINT      CHECK (duration_ms IS NULL OR duration_ms > 0),
    is_long      BOOLEAN     NOT NULL,
    charged_at   TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_media_upload_usage_uploader
    ON media_upload_usage (uploader_id, charged_at DESC);

CREATE TABLE IF NOT EXISTS media_upload_quota_overrides (
    user_id             UUID        PRIMARY KEY,
    daily_video_bytes   BIGINT      CHECK (daily_video_bytes IS NULL OR daily_video_bytes >= 0),
    daily_video_seconds BIGINT      CHECK (daily_video_seconds IS NULL OR daily_video_seconds >= 0),
    max_inflight_long   INT         CHECK (max_inflight_long IS NULL OR max_inflight_long >= 0),
    reason              TEXT        NOT NULL,
    granted_by          TEXT        NOT NULL,
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
