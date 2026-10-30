-- Community answer and contributor counts move out of the answer write
-- (Q&A backend audit M7, 30 Oct 2026).
--
-- Every answer used to COUNT(*) all answers of its community and UNION all
-- question and answer authors, then update the community row, inside the
-- answer's transaction: answers in one community ran one at a time and got
-- slower as it grew. A write now only stamps qa_stats_stale_at (when it is
-- not already stamped, so a busy community takes no row lock at all), and
-- service.RunCommunityStatsRefresher recounts stamped communities in the
-- background (store.RefreshStaleCommunityStats).
--
-- ADD COLUMN without a default is a catalog-only change (no rewrite, no
-- scan); it waits for the ACCESS EXCLUSIVE lock for an instant, bounded by
-- the migration's lock_timeout.
SET LOCAL lock_timeout = '5s';
ALTER TABLE qa_communities ADD COLUMN IF NOT EXISTS qa_stats_stale_at TIMESTAMPTZ;
CREATE INDEX IF NOT EXISTS idx_qa_communities_stats_stale
    ON qa_communities (qa_stats_stale_at) WHERE qa_stats_stale_at IS NOT NULL;
