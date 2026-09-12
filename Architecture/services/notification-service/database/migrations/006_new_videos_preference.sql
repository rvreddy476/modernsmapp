-- Migration 006: the new_videos preference, and render inputs on fan-out jobs.
--
-- Tube launch (2026-09-12). Every subscriber is told about a new upload by
-- default; this pair of toggles is the account-wide opt-out (the per-channel
-- bell lives in post-service). Both halves follow the 005 split: inapp_ gates
-- the inbox row and realtime event, push_ gates the device push. Default TRUE
-- on both so a subscriber who never opened settings hears about every upload.
ALTER TABLE notification_preferences ADD COLUMN IF NOT EXISTS push_new_videos  BOOLEAN NOT NULL DEFAULT TRUE;
ALTER TABLE notification_preferences ADD COLUMN IF NOT EXISTS inapp_new_videos BOOLEAN NOT NULL DEFAULT TRUE;

-- The push reads "{channel} uploaded: {title}". Both strings are captured at
-- enqueue from the PostCreated event so delivery never looks anything up per
-- recipient. Empty (not NULL) for jobs enqueued from older producers: the
-- worker resolves the channel name once per job and falls back to neutral
-- copy for the title.
ALTER TABLE subscriber_fanout_jobs ADD COLUMN IF NOT EXISTS title        TEXT NOT NULL DEFAULT '';
ALTER TABLE subscriber_fanout_jobs ADD COLUMN IF NOT EXISTS channel_name TEXT NOT NULL DEFAULT '';
