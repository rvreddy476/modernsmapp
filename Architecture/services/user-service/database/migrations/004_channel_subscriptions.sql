-- Channel subscriptions: tracks which channels each user follows
CREATE TABLE IF NOT EXISTS channel_subscriptions (
    channel_id    UUID NOT NULL REFERENCES channels(id) ON DELETE CASCADE,
    user_id       UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    notify_on     TEXT NOT NULL DEFAULT 'all' CHECK (notify_on IN ('all','highlights','none')),
    subscribed_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (channel_id, user_id)
);
CREATE INDEX IF NOT EXISTS idx_channel_subs_user ON channel_subscriptions(user_id);

-- Trigger to keep channels.subscriber_count in sync
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
