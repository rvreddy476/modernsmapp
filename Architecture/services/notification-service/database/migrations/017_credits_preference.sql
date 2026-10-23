-- Migration 017: the credits preference category (video credits, 2026-10-23).
--
-- post-service now tags VChat accounts in a long video's cast & crew, and a
-- tag waits for the tagged person's answer before the video shows on their
-- profile. Two notices come from it: credit_request (to the tagged person,
-- "{uploader} credited you in a video") and credit_accepted (to the uploader,
-- "{name} accepted your credit"). One pair of toggles covers both, split as in
-- 005: inapp_ gates the inbox row and realtime event, push_ the device push.
-- Default TRUE on both: a credit request is something the person must answer.
ALTER TABLE notification_preferences ADD COLUMN IF NOT EXISTS push_credits  BOOLEAN NOT NULL DEFAULT TRUE;
ALTER TABLE notification_preferences ADD COLUMN IF NOT EXISTS inapp_credits BOOLEAN NOT NULL DEFAULT TRUE;
