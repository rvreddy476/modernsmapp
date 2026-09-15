-- payments-service migration 011 — application `dating` (Momentum Dating Premium)
--
-- dating-service takes Premium through payments-service as the application
-- `dating`, with reference type `dating_premium` (shared/servicetoken
-- RefDatingPremium): one-off passes (30 / 90 / 365 days) and Boost, not
-- subscriptions. The caller is dating-service, allowed only `dating`
-- (SERVICE_CALLER_DATING_SERVICE_APPLICATIONS).
--
--   1. Seed the registry row: "Momentum Dating", active, the same merchant name
--      the Razorpay sheet shows today ("Momentum Merchant"), upi and card.
--      ON CONFLICT DO NOTHING: a row an operator already created or changed
--      through PUT /v1/payments/internal/applications/dating is left alone.
--   2. Extend the legacy mapping (payments.legacy_application_for, from 010) so
--      owner dating-service, or reference type dating_premium, maps to `dating`.
--      The backfill and gated 998 use it. No payment row predates this (dating
--      called Razorpay directly), so the backfill is not re-run here.
--
-- Re-runnable: both steps are idempotent.

INSERT INTO payments.applications (key, display_name, status, merchant_display_name, enabled_methods)
VALUES ('dating', 'Momentum Dating', 'active', 'Momentum Merchant', ARRAY['upi','card'])
ON CONFLICT (key) DO NOTHING;

CREATE OR REPLACE FUNCTION payments.legacy_application_for(owner_domain TEXT, reference_type TEXT)
RETURNS TEXT
LANGUAGE sql IMMUTABLE AS $$
    SELECT CASE owner_domain
               WHEN 'commerce-service' THEN 'mstore'
               WHEN 'food-service'     THEN 'feast'
               WHEN 'dating-service'   THEN 'dating'
               ELSE CASE reference_type
                        WHEN 'order'          THEN 'mstore'
                        WHEN 'food_order'     THEN 'feast'
                        WHEN 'dating_premium' THEN 'dating'
                    END
           END
$$;
