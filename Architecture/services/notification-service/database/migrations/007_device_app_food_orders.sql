-- Migration 007: which installed app a push token belongs to, and the
-- food_orders preference category (Feast, lane B5b, 2026-09-13).
--
-- user_devices.app — an FCM registration token belongs to ONE installed app.
-- Feast customers live inside Momentum; Feast Kitchen (restaurants) and Feast
-- Rider (delivery partners) are separate installs with their own tokens. A
-- push is sent only to devices registered for the app it is meant for: a
-- "new order" must never ring the owner's Momentum install, and a like must
-- never land in the Kitchen app. Every row that predates this column was
-- registered by Momentum, so the default backfills it exactly.
ALTER TABLE user_devices ADD COLUMN IF NOT EXISTS app TEXT NOT NULL DEFAULT 'momentum';

DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint WHERE conname = 'user_devices_app_check'
    ) THEN
        ALTER TABLE user_devices
            ADD CONSTRAINT user_devices_app_check
            CHECK (app IN ('momentum', 'feast_kitchen', 'feast_rider'));
    END IF;
END $$;

-- Every push path looks devices up by (user, app) now.
CREATE INDEX IF NOT EXISTS idx_user_devices_user_app
    ON user_devices (user_id, app) WHERE is_active = TRUE;

-- food_orders: customer-facing Feast order updates (confirmed, preparing, out
-- for delivery, delivered, refunds). Both halves follow the 005 split and
-- default on. Kitchen new-order and rider job-offer pushes are operational
-- and are NOT gated by this (or any Momentum) toggle.
ALTER TABLE notification_preferences ADD COLUMN IF NOT EXISTS push_food_orders  BOOLEAN NOT NULL DEFAULT TRUE;
ALTER TABLE notification_preferences ADD COLUMN IF NOT EXISTS inapp_food_orders BOOLEAN NOT NULL DEFAULT TRUE;
