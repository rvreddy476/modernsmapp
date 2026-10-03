-- Migration 013: Doorstep (home services, lane L-E, 2026-10-04).
--
-- 1. The Doorstep professional app as a push-device install target. The
--    Doorstep customer flow lives inside Momentum; the professional app is a
--    separate install whose FCM token registers under app = 'doorstep_pro'.
--    Job offers, job changes and account notices go only to those devices,
--    and a customer's booking update never lands in the professional
--    install. Migrations 007/009 pinned the allowed apps in a CHECK
--    constraint, so the constraint is replaced — drop and re-add, both
--    idempotent, so the bootstrap can run repeatedly.
ALTER TABLE user_devices DROP CONSTRAINT IF EXISTS user_devices_app_check;
ALTER TABLE user_devices
    ADD CONSTRAINT user_devices_app_check
    CHECK (app IN ('momentum', 'feast_kitchen', 'feast_rider', 'mopedu_captain', 'doorstep_pro'));

-- 2. The doorstep preference category: the customer's booking updates
--    (confirmed, professional assigned / on the way / arrived, extras, done,
--    cancelled, refunds, reminders, chat). Both halves follow the 005 split
--    and default on: a booking update is something the customer is waiting
--    for. The professional's pushes are operational and are NOT gated by
--    this (or any Momentum) toggle.
ALTER TABLE notification_preferences ADD COLUMN IF NOT EXISTS push_doorstep  BOOLEAN NOT NULL DEFAULT TRUE;
ALTER TABLE notification_preferences ADD COLUMN IF NOT EXISTS inapp_doorstep BOOLEAN NOT NULL DEFAULT TRUE;
