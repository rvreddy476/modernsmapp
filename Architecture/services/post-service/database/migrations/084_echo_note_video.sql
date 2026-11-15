-- Migration 084: short video in Echo notes.
--
-- A note may carry one video on its own (no images or GIF with it). The
-- length allowed depends on who posted it, and is fixed when the note is
-- written: 140 s without Premium (replies), 10 minutes with Premium.
-- media-service usually learns the length only after transcoding, so the
-- limit is stored on the note: a video that turns out longer stays visible
-- to its author alone (service/notes_video.go). NULL means no video.
--
-- Idempotent.

ALTER TABLE posts ADD COLUMN IF NOT EXISTS note_video_max_ms INT;
