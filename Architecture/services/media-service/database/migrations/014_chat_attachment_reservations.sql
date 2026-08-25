-- Atomically pin canonical media while chat delivery is being completed.
-- ON DELETE RESTRICT is deliberate and load-bearing.
CREATE TABLE IF NOT EXISTS media_chat_attachment_reservations (
    reference_id UUID PRIMARY KEY,
    media_id UUID NOT NULL REFERENCES media_assets(id) ON DELETE RESTRICT,
    uploader_id UUID NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_media_chat_attachment_media
    ON media_chat_attachment_reservations(media_id);
