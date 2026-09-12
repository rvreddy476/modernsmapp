-- Migration 046: Tube channel subscriptions move to post-service (2026-09-12).
--
-- Founder decisions: Subscribe on a Tube channel is follow + notify behind one
-- button, and unsubscribing removes both. Every subscriber is notified by
-- default (notify_on 'all'); the per-channel bell opts out ('none'). The old
-- 'highlights' tier never shipped a definition, so it collapses into 'none'.
--
-- The table already exists on every live database: user-service migration
-- 004 created it (with a users FK and a trigger that keeps
-- channels.subscriber_count in step). post-service owns the channels the rows
-- point at, so the writes and the fan-out reads move here. This file is written
-- to be safe in either boot order:
--
--   * user-service first: the table exists with the three-way CHECK. The
--     CREATE no-ops, the UPDATE folds 'highlights' away, the CHECK is swapped.
--   * post-service first (fresh database): the table is created here. The
--     users FK is added only when the shared users table already exists,
--     because on a fresh database user-service has not created it yet and a
--     hard REFERENCES would abort the whole migration. user-service's own
--     CREATE TABLE IF NOT EXISTS then no-ops against ours.
--
-- Migration 004 stays untouched: it is user-service's, and rewriting history
-- there would fork the schema_migrations ledger of a deployed service.
CREATE TABLE IF NOT EXISTS channel_subscriptions (
    channel_id    UUID NOT NULL REFERENCES channels(id) ON DELETE CASCADE,
    user_id       UUID NOT NULL,
    notify_on     TEXT NOT NULL DEFAULT 'all',
    subscribed_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (channel_id, user_id)
);
CREATE INDEX IF NOT EXISTS idx_channel_subs_user ON channel_subscriptions(user_id);

-- The users FK from migration 004, only where users exists and only once.
DO $$
BEGIN
    IF to_regclass('public.users') IS NOT NULL
       AND NOT EXISTS (
           SELECT 1 FROM pg_constraint
           WHERE conname = 'channel_subscriptions_user_id_fkey'
             AND conrelid = 'channel_subscriptions'::regclass) THEN
        ALTER TABLE channel_subscriptions
            ADD CONSTRAINT channel_subscriptions_user_id_fkey
            FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE;
    END IF;
END $$;

-- 'highlights' was never given a meaning; it becomes the bell-off state so
-- no subscriber is left in a tier the fan-out does not read.
UPDATE channel_subscriptions SET notify_on = 'none' WHERE notify_on = 'highlights';

ALTER TABLE channel_subscriptions DROP CONSTRAINT IF EXISTS channel_subscriptions_notify_on_check;
ALTER TABLE channel_subscriptions
    ADD CONSTRAINT channel_subscriptions_notify_on_check CHECK (notify_on IN ('all', 'none'));

-- GET /v1/channels/subscriptions pages newest-first by (subscribed_at,
-- channel_id) per user; this index serves that keyset directly.
CREATE INDEX IF NOT EXISTS idx_channel_subs_user_recent
    ON channel_subscriptions (user_id, subscribed_at DESC, channel_id);

-- channels.subscriber_count is maintained by trigger, so a read of the
-- channel never pays a COUNT(*) over the subscription table. Recreated here
-- idempotently: the function body is byte-for-byte what user-service
-- installs, so whichever service runs last leaves the same trigger.
CREATE OR REPLACE FUNCTION update_channel_subscriber_count()
RETURNS TRIGGER AS $$
BEGIN
  IF TG_OP = 'INSERT' THEN
    UPDATE channels SET subscriber_count = subscriber_count + 1 WHERE id = NEW.channel_id;
  ELSIF TG_OP = 'DELETE' THEN
    UPDATE channels SET subscriber_count = GREATEST(0, subscriber_count - 1) WHERE id = OLD.channel_id;
  END IF;
  RETURN NULL;
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS trg_channel_subscriber_count ON channel_subscriptions;
CREATE TRIGGER trg_channel_subscriber_count
AFTER INSERT OR DELETE ON channel_subscriptions
FOR EACH ROW EXECUTE FUNCTION update_channel_subscriber_count();
