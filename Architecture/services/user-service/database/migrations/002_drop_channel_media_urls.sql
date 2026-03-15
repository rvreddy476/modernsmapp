-- 002_channel_media_ids.sql
-- Replace text URL columns (icon_url, banner_url) with proper UUID media references.
-- These reference media_assets in the media service.
ALTER TABLE channels ADD COLUMN IF NOT EXISTS avatar_media_id UUID;
ALTER TABLE channels ADD COLUMN IF NOT EXISTS banner_media_id UUID;

-- Drop legacy text URL columns (replaced by UUID references above)
ALTER TABLE channels DROP COLUMN IF EXISTS icon_url;
ALTER TABLE channels DROP COLUMN IF EXISTS banner_url;
