-- payments-service migration 019 — application `premium` (Momentum Premium)
--
-- monetization-service sells Momentum Premium passes (30 and 365 days; short-
-- posts design 3.1, founder decision 11 Nov 2026) through payments-service as
-- the application `premium`, with one reference type (shared/servicetoken
-- RefPlatformPremium):
--   platform_premium  one intent per pass (reference_id = monetization's
--                     premium_periods.id).
-- A pass is granted only from the signed payment.succeeded event. The caller
-- is monetization-service, which must be allowed `premium` in
-- SERVICE_CALLER_MONETIZATION_SERVICE_APPLICATIONS (alongside memberships)
-- and platform_premium in SERVICE_CALLER_MONETIZATION_SERVICE_REFTYPES.
--
--   1. Seed the registry row: "Momentum Premium", active, merchant name
--      "Momentum Merchant", upi and card. No cash.
--      ON CONFLICT DO NOTHING: a row an operator already created or changed
--      through PUT /v1/payments/internal/applications/premium is left alone.
--   2. Extend the legacy mapping (payments.legacy_application_for, last
--      replaced by 016). monetization-service now owns two applications, so
--      its owner branch is decided by reference type: platform_premium maps
--      to `premium`, everything else it owns stays `memberships`. No payment
--      row predates this (Premium has never taken money), so the backfill is
--      not re-run here.
--
-- Re-runnable: both steps are idempotent.

INSERT INTO payments.applications (key, display_name, status, merchant_display_name, enabled_methods)
VALUES ('premium', 'Momentum Premium', 'active', 'Momentum Merchant', ARRAY['upi','card'])
ON CONFLICT (key) DO NOTHING;

CREATE OR REPLACE FUNCTION payments.legacy_application_for(owner_domain TEXT, reference_type TEXT)
RETURNS TEXT
LANGUAGE sql IMMUTABLE AS $$
    SELECT CASE owner_domain
               WHEN 'commerce-service'     THEN 'mstore'
               WHEN 'food-service'         THEN 'feast'
               WHEN 'dating-service'       THEN 'dating'
               WHEN 'rider-service'        THEN 'mopedu'
               WHEN 'doorstep-service'     THEN 'doorstep'
               WHEN 'monetization-service' THEN CASE reference_type
                                                    WHEN 'platform_premium' THEN 'premium'
                                                    ELSE 'memberships'
                                                END
               ELSE CASE reference_type
                        WHEN 'order'               THEN 'mstore'
                        WHEN 'food_order'          THEN 'feast'
                        WHEN 'dating_premium'      THEN 'dating'
                        WHEN 'mopedu_ride'         THEN 'mopedu'
                        WHEN 'mopedu_subscription' THEN 'mopedu'
                        WHEN 'doorstep_booking'    THEN 'doorstep'
                        WHEN 'doorstep_extras'     THEN 'doorstep'
                        WHEN 'creator_membership'  THEN 'memberships'
                        WHEN 'platform_premium'    THEN 'premium'
                    END
           END
$$;
