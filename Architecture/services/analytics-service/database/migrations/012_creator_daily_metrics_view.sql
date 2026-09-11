-- 012: the analytics -> monetization contract, given a name.
-- Plan Phase 5C; audit M-15.
--
-- ============================================================================
--  analytics.v_creator_daily_metrics_v1 is a VERSIONED CONTRACT.
--
--  monetization-service prices creator-fund days from this view and from
--  nothing else in this schema. Until this migration it read
--  content_daily_summary directly, so a column rename in analytics would
--  have broken settlement with no warning and no owner. The view is the
--  boundary: analytics owns it, monetization names it, and its column list
--  is checked at monetization start-up.
--
--  Rules:
--    * Never change the column list, the column types, or the row
--      semantics of _v1. A different shape is a NEW view,
--      v_creator_daily_metrics_v2, created beside this one; _v1 stays until
--      monetization has moved and says so.
--    * Renaming or reshaping content_daily_summary / content_ownership is
--      allowed as long as this view still resolves; CREATE OR REPLACE it
--      in the same migration to keep the contract whole.
--    * No new grant: monetization connects with the same role it always
--      did (one shared DSN), so the view inherits that role's access.
--
--  Columns (all read by monetization-service; see
--  internal/store/postgres/creator_fund.go and store.go there):
--    creator_id, content_id, day_bucket, content_type  identity of the row
--    views_display                                     the paid unit
--    watch_time_total_ms, impressions                  audit + quality inputs
--    content_quality_score                             the payout multiplier input
--    view_score_total                                  eligibility evaluator input
--    eligibility_state, eligibility_effective_from     dated eligibility, from
--                                                      the ownership projection
--                                                      (migration 011); NULL
--                                                      when no ownership row
--                                                      exists for the content
--    updated_at                                        folded into the accrual's
--                                                      input_revision, so a
--                                                      rewritten day is
--                                                      detectable
--
--  The eligibility columns are a LEFT JOIN. The rollup already applies
--  eligibility when it decides whether a summary row exists at all
--  (migration 011), so a row here is one the rollup let through; the state
--  is exposed so the consumer can see why, and a summary row with no
--  ownership row is still returned rather than silently dropped from a
--  creator's statement.
-- ============================================================================

CREATE OR REPLACE VIEW analytics.v_creator_daily_metrics_v1 AS
SELECT
    s.creator_id,
    s.content_id,
    s.day_bucket,
    s.content_type,
    s.views_display,
    s.watch_time_total_ms,
    s.impressions,
    s.content_quality_score,
    s.view_score_total,
    o.eligibility_state,
    o.eligibility_effective_from,
    s.updated_at
FROM analytics.content_daily_summary AS s
LEFT JOIN analytics.content_ownership AS o
       ON o.content_id = s.content_id;

COMMENT ON VIEW analytics.v_creator_daily_metrics_v1 IS
    'Versioned contract read by monetization-service (plan 5C, M-15). Do not alter; a new shape is v_creator_daily_metrics_v2.';
