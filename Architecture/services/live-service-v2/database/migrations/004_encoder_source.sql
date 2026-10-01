-- 004_encoder_source.sql — go live from streaming software or a camera
-- (1 Oct 2026).
--
-- A stream's `source` says where the host's media comes from: 'device' (the
-- browser/phone camera, a LiveKit publisher token — every row before this
-- migration) or 'encoder' (OBS, a hardware encoder or a camera, through a
-- LiveKit RTMP ingress).
--
-- ingress_id is the LiveKit ingress currently issued for the stream (NULL =
-- none). encoder_identity is the LiveKit participant identity that ingress
-- publishes as ("encoder_<stream id>"); it stays after the ingress is deleted
-- so a late webhook from it is still recognised as the host's media and never
-- counted as a viewer.
--
-- The stream key is NOT stored: it is read back from LiveKit when the host
-- asks for it. The ingress lives until the stream ends (a failed start keeps
-- it for the next attempt).
--
-- Additive and idempotent; existing rows are 'device'.

ALTER TABLE live_streams
    ADD COLUMN IF NOT EXISTS source           TEXT NOT NULL DEFAULT 'device',
    ADD COLUMN IF NOT EXISTS ingress_id       TEXT,
    ADD COLUMN IF NOT EXISTS encoder_identity TEXT;

ALTER TABLE live_streams DROP CONSTRAINT IF EXISTS live_streams_source_check;
ALTER TABLE live_streams ADD CONSTRAINT live_streams_source_check
    CHECK (source IN ('device','encoder'));

-- The sweeper retries deleting the ingress of an ended stream.
CREATE INDEX IF NOT EXISTS idx_live_streams_ingress
    ON live_streams (status) WHERE ingress_id IS NOT NULL;
