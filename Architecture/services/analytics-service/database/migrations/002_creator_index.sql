-- 002_creator_index.sql: Index for creator analytics queries
CREATE INDEX IF NOT EXISTS idx_events_user_created
    ON analytics.events_raw(user_id, received_at DESC);

CREATE INDEX IF NOT EXISTS idx_content_daily_summary_creator_day
    ON analytics.content_daily_summary(creator_id, day_bucket DESC);
