-- 003_hls.sql: Add HLS adaptive streaming support
ALTER TABLE media_assets ADD COLUMN IF NOT EXISTS hls_master_key TEXT;

COMMENT ON COLUMN media_assets.hls_master_key IS
    'Blob storage key for HLS master playlist (master.m3u8). NULL means HLS not generated.';
