-- payments-service migration 016 — application `memberships` (creator memberships)
--
-- monetization-service takes paid creator memberships through payments-service
-- as the application `memberships`, with one reference type (shared/servicetoken
-- RefCreatorMembership):
--   creator_membership  one intent per paid membership period: the first
--                       join, each renewal and each upgrade difference
--                       (reference_id = monetization's membership_payments.id).
-- Refunds go against the same reference. Money is applied only from the signed
-- payment.succeeded event. The caller is monetization-service, allowed only
-- `memberships` (SERVICE_CALLER_MONETIZATION_SERVICE_APPLICATIONS) and only
-- creator_membership (SERVICE_CALLER_MONETIZATION_SERVICE_REFTYPES).
--
--   1. Seed the registry row: "Memberships", active, the same merchant name the
--      Razorpay sheet shows today ("Momentum Merchant"), upi and card. No cash.
--      ON CONFLICT DO NOTHING: a row an operator already created or changed
--      through PUT /v1/payments/internal/applications/memberships is left alone.
--   2. Extend the legacy mapping (payments.legacy_application_for, from 010,
--      extended by 011, 012, 013 and 015) so owner monetization-service, or
--      reference type creator_membership, maps to `memberships`. The backfill
--      and gated 998 use it. No payment row predates this (monetization-service
--      has never opened an intent), so the backfill is not re-run here.
--
-- Re-runnable: both steps are idempotent.

INSERT INTO payments.applications (key, display_name, status, merchant_display_name, enabled_methods)
VALUES ('memberships', 'Memberships', 'active', 'Momentum Merchant', ARRAY['upi','card'])
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
               WHEN 'monetization-service' THEN 'memberships'
               ELSE CASE reference_type
                        WHEN 'order'               THEN 'mstore'
                        WHEN 'food_order'          THEN 'feast'
                        WHEN 'dating_premium'      THEN 'dating'
                        WHEN 'mopedu_ride'         THEN 'mopedu'
                        WHEN 'mopedu_subscription' THEN 'mopedu'
                        WHEN 'doorstep_booking'    THEN 'doorstep'
                        WHEN 'doorstep_extras'     THEN 'doorstep'
                        WHEN 'creator_membership'  THEN 'memberships'
                    END
           END
$$;
