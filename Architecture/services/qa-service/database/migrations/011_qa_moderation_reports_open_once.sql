-- One open report per (reporter, target_type, target_id) (31 Oct 2026).
-- Without it one user could report the same answer up to the daily report
-- limit and fill the moderators' queue with copies. "Open" is the queue:
-- pending or reviewed (AdminDecideReport decides only those; AdminStats
-- counts only those). Once a report is resolved or dismissed the reporter
-- may report the same target again, so the index is partial.
-- store.CreateReport inserts ON CONFLICT DO NOTHING against this index, with
-- the same predicate (store.openReportPredicateSQL), and answers a repeat
-- with the open report it already has.
--
-- A plain build in this migration, not database/concurrent_indexes.go: the
-- insert's ON CONFLICT clause needs the index to exist before the first
-- report is written (it is an error without it), and the concurrent builds
-- run in the background after boot. moderation_reports is small (one row
-- per user report, capped per reporter per day), so the build is brief;
-- lock_timeout bounds the wait for the lock.
--
-- The table lock (SHARE ROW EXCLUSIVE: reads go on, writes wait) is taken
-- first so no duplicate can be inserted between the fold below and the index
-- build, which would fail the build and the boot.
SET LOCAL lock_timeout = '5s';

LOCK TABLE moderation_reports IN SHARE ROW EXCLUSIVE MODE;

-- Earlier duplicates: the oldest open report of each triple stays open; the
-- later open copies are closed as dismissed (by nobody: reviewed_by stays
-- NULL), not deleted, so the reporter's history and any moderation action
-- that cites them survive.
UPDATE moderation_reports dup
SET status = 'dismissed', resolved_at = now()
WHERE dup.status IN ('pending', 'reviewed')
  AND EXISTS (
    SELECT 1 FROM moderation_reports keep
    WHERE keep.reporter_id = dup.reporter_id
      AND keep.target_type = dup.target_type
      AND keep.target_id = dup.target_id
      AND keep.status IN ('pending', 'reviewed')
      AND (keep.created_at, keep.id) < (dup.created_at, dup.id));

CREATE UNIQUE INDEX IF NOT EXISTS uq_moderation_reports_open_once
    ON moderation_reports (reporter_id, target_type, target_id)
    WHERE status IN ('pending', 'reviewed');
