-- 005_member_stats_index.sql: Index for top contributors query on group_member_stats
BEGIN;

CREATE INDEX IF NOT EXISTS idx_gms_top_contributors
    ON group_member_stats(group_id, post_count DESC);

COMMIT;
