-- 023: every accrual names the binary that wrote it.
-- Plan Phase 5C; audit M-15 (deployed-revision stamping).
--
-- rule_version (migration 019) says which FORMULA priced a day. build_sha
-- says which BUILD of monetization-service ran that formula, so a row can
-- be traced to an exact commit when source and image are found to
-- disagree (the audit found an analytics image that predated a money
-- rule; nothing on any row said so). Set from internal/buildinfo, which is
-- stamped at link time; a binary built outside the release path writes
-- 'unknown', which is itself the signal.
--
-- Nullable: rows from before this migration have no build to name and
-- must not be given one.
ALTER TABLE creator_fund_earnings
    ADD COLUMN IF NOT EXISTS build_sha TEXT;
