-- payments-service migration 013 — reference type `mopedu_subscription`
--
-- rider-service also takes a captain's plan period through payments-service
-- as the application `mopedu`, with reference type `mopedu_subscription`
-- (shared/servicetoken RefMopeduSubscription): one intent per plan period,
-- paid by the captain to Mopedu. The caller is still rider-service, allowed
-- only `mopedu` (SERVICE_CALLER_RIDER_SERVICE_APPLICATIONS); its REFTYPES now
-- name both mopedu_ride and mopedu_subscription.
--
--   1. No registry change: `mopedu` was seeded by 012 and is reused.
--   2. Extend the legacy mapping (payments.legacy_application_for, from 010,
--      extended by 011 and 012) so reference type mopedu_subscription maps to
--      `mopedu`. Owner rider-service already mapped to `mopedu` by 012. The
--      backfill and gated 998 use it. No payment row predates this (no captain
--      plan has been paid through payments), so the backfill is not re-run.
--
-- Re-runnable: CREATE OR REPLACE is idempotent.

CREATE OR REPLACE FUNCTION payments.legacy_application_for(owner_domain TEXT, reference_type TEXT)
RETURNS TEXT
LANGUAGE sql IMMUTABLE AS $$
    SELECT CASE owner_domain
               WHEN 'commerce-service' THEN 'mstore'
               WHEN 'food-service'     THEN 'feast'
               WHEN 'dating-service'   THEN 'dating'
               WHEN 'rider-service'    THEN 'mopedu'
               ELSE CASE reference_type
                        WHEN 'order'               THEN 'mstore'
                        WHEN 'food_order'          THEN 'feast'
                        WHEN 'dating_premium'      THEN 'dating'
                        WHEN 'mopedu_ride'         THEN 'mopedu'
                        WHEN 'mopedu_subscription' THEN 'mopedu'
                    END
           END
$$;
