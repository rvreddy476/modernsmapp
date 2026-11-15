-- 008_recording_parts.sql — one recording part per egress (15 Nov 2026).
--
-- A streaming-software (encoder) live whose encoder drops long enough for
-- LiveKit to close the room reconnects into a NEW room instance under the
-- same name. The room-composite egress of the old instance ends with it, and
-- nothing started another, so everything after the reconnect was missing
-- from the saved video.
--
-- Every egress is now one row here. Part 1 writes recordings/<stream>.mp4
-- (the key every earlier recording used); part n > 1 writes
-- recordings/<stream>_part<n>.mp4. A new part starts when the stream is live
-- and no part is starting or recording. The media import (one asset per
-- stream, media-service source_ref = the stream id) is queued only once the
-- stream has ended and no part is still recording, from the longest complete
-- part (service/recording_parts.go explains why it does not join them).
--
-- state:  starting  (reserved, StartEgress in flight; abandoned after 2 min)
--         recording (LiveKit egress id known)
--         complete  (egress_ended EGRESS_COMPLETE: the file exists)
--         failed    (start failed, egress failed, or the stream ended first)
--
-- Streams with no row here (recorded before this migration) keep the old
-- path: live_streams.egress_id and an import queued on egress_ended.
--
-- Additive and idempotent: a new table, no change to a live table.

CREATE TABLE IF NOT EXISTS live_recording_parts (
    stream_id     UUID NOT NULL REFERENCES live_streams(id) ON DELETE CASCADE,
    part          INT  NOT NULL CHECK (part >= 1),
    object_key    TEXT NOT NULL,
    egress_id     TEXT,
    state         TEXT NOT NULL DEFAULT 'starting'
                  CHECK (state IN ('starting','recording','complete','failed')),
    recording_url TEXT,
    duration_ms   BIGINT NOT NULL DEFAULT 0,
    last_error    TEXT,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    ended_at      TIMESTAMPTZ,
    PRIMARY KEY (stream_id, part)
);

-- An egress id names one part.
CREATE UNIQUE INDEX IF NOT EXISTS uq_live_recording_parts_egress
    ON live_recording_parts (egress_id) WHERE egress_id IS NOT NULL;

-- The sweeper's backstop: complete parts of recent streams with no import.
CREATE INDEX IF NOT EXISTS idx_live_recording_parts_complete
    ON live_recording_parts (created_at) WHERE state = 'complete';
