-- payments-service migration 012 — application `mopedu` (Mopedu ride-hailing)
--
-- rider-service takes ride fares through payments-service as the application
-- `mopedu`, with reference type `mopedu_ride` (shared/servicetoken
-- RefMopeduRide): one intent per completed ride, refunds for no-show and
-- dispute resolutions. The caller is rider-service, allowed only `mopedu`
-- (SERVICE_CALLER_RIDER_SERVICE_APPLICATIONS).
--
--   1. Seed the registry row: "Mopedu", active, the same merchant name the
--      Razorpay sheet shows today ("Momentum Merchant"), upi and card.
--      ON CONFLICT DO NOTHING: a row an operator already created or changed
--      through PUT /v1/payments/internal/applications/mopedu is left alone.
--   2. Extend the legacy mapping (payments.legacy_application_for, from 010,
--      extended by 011) so owner rider-service, or reference type mopedu_ride,
--      maps to `mopedu`. The backfill and gated 998 use it. No payment row
--      predates this (rider-service has never taken money), so the backfill
--      is not re-run here.
--
-- Re-runnable: both steps are idempotent.

INSERT INTO payments.applications (key, display_name, status, merchant_display_name, enabled_methods)
VALUES ('mopedu', 'Mopedu', 'active', 'Momentum Merchant', ARRAY['upi','card'])
ON CONFLICT (key) DO NOTHING;

CREATE OR REPLACE FUNCTION payments.legacy_application_for(owner_domain TEXT, reference_type TEXT)
RETURNS TEXT
LANGUAGE sql IMMUTABLE AS $$
    SELECT CASE owner_domain
               WHEN 'commerce-service' THEN 'mstore'
               WHEN 'food-service'     THEN 'feast'
               WHEN 'dating-service'   THEN 'dating'
               WHEN 'rider-service'    THEN 'mopedu'
               ELSE CASE reference_type
                        WHEN 'order'          THEN 'mstore'
                        WHEN 'food_order'     THEN 'feast'
                        WHEN 'dating_premium' THEN 'dating'
                        WHEN 'mopedu_ride'    THEN 'mopedu'
                    END
           END
$$;
