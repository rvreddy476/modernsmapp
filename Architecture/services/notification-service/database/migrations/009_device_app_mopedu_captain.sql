-- Migration 009: the Mopedu Captain app as a push-device install target
-- (2026-09-18).
--
-- The Mopedu customer flow lives inside Momentum; the captain (driver) app is
-- a separate install whose FCM token registers under app = 'mopedu_captain'.
-- Ride offers and payment notices go only to those devices, and a customer's
-- ride update never lands in the captain install. Migration 007 pinned the
-- allowed apps in a CHECK constraint, so the constraint is replaced — drop
-- and re-add, both idempotent, so the bootstrap can run repeatedly.
ALTER TABLE user_devices DROP CONSTRAINT IF EXISTS user_devices_app_check;
ALTER TABLE user_devices
    ADD CONSTRAINT user_devices_app_check
    CHECK (app IN ('momentum', 'feast_kitchen', 'feast_rider', 'mopedu_captain'));
