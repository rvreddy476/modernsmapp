-- The author's kind is the kind (founder, 2026-09-04/05):
-- a reel is what the author posted as a reel; a video is what the author
-- posted as a video. Only a plain "post" that happens to carry a video is
-- classified from the measured duration + orientation.
--
-- `content_type_explicit` records whether `content_type` was the author's
-- choice (flick / long_video sent by the Reel or Tube composer) or the
-- server's measurement. The MediaTranscodeConsumer, which learns the real
-- duration + dimensions after the post exists, only reclassifies rows where
-- it is FALSE. Without this column it cannot tell an explicit long_video
-- from a "post" that defaulted to long_video while transcode was pending.
--
-- DEFAULT FALSE so every existing row keeps today's behaviour: a flick is
-- already never downgraded (consumer rule), and a pre-existing long_video
-- still follows the measurement, as it always has.

ALTER TABLE posts ADD COLUMN IF NOT EXISTS content_type_explicit BOOLEAN NOT NULL DEFAULT FALSE;
