-- payments-service migration 015 — application `doorstep` (Doorstep home services)
--
-- doorstep-service takes home-service bookings through payments-service as
-- the application `doorstep`, with two reference types (shared/servicetoken
-- RefDoorstepBooking / RefDoorstepExtras):
--   doorstep_booking  one intent per booking, paid in full at checkout
--                     (reference_id = the booking id);
--   doorstep_extras   one intent per extras bill approved during the visit
--                     (reference_id = the extras bill id).
-- Refunds (cancellation, late capture, no-show, rework) go against either.
-- The caller is doorstep-service, allowed only `doorstep`
-- (SERVICE_CALLER_DOORSTEP_SERVICE_APPLICATIONS) and only those two
-- reference types (SERVICE_CALLER_DOORSTEP_SERVICE_REFTYPES).
--
--   1. Seed the registry row: "Doorstep", active, the same merchant name the
--      Razorpay sheet shows today ("Momentum Merchant"), upi and card. No
--      cash: the plan takes no cash, and the method list is the only place
--      payments-service would accept one.
--      ON CONFLICT DO NOTHING: a row an operator already created or changed
--      through PUT /v1/payments/internal/applications/doorstep is left alone.
--   2. Extend the legacy mapping (payments.legacy_application_for, from 010,
--      extended by 011, 012 and 013; 014 did not touch it) so owner
--      doorstep-service, or reference type doorstep_booking / doorstep_extras,
--      maps to `doorstep`. The backfill and gated 998 use it. No payment row
--      predates this (doorstep-service has never taken money), so the
--      backfill is not re-run here.
--
-- Re-runnable: both steps are idempotent.

INSERT INTO payments.applications (key, display_name, status, merchant_display_name, enabled_methods)
VALUES ('doorstep', 'Doorstep', 'active', 'Momentum Merchant', ARRAY['upi','card'])
ON CONFLICT (key) DO NOTHING;

CREATE OR REPLACE FUNCTION payments.legacy_application_for(owner_domain TEXT, reference_type TEXT)
RETURNS TEXT
LANGUAGE sql IMMUTABLE AS $$
    SELECT CASE owner_domain
               WHEN 'commerce-service' THEN 'mstore'
               WHEN 'food-service'     THEN 'feast'
               WHEN 'dating-service'   THEN 'dating'
               WHEN 'rider-service'    THEN 'mopedu'
               WHEN 'doorstep-service' THEN 'doorstep'
               ELSE CASE reference_type
                        WHEN 'order'               THEN 'mstore'
                        WHEN 'food_order'          THEN 'feast'
                        WHEN 'dating_premium'      THEN 'dating'
                        WHEN 'mopedu_ride'         THEN 'mopedu'
                        WHEN 'mopedu_subscription' THEN 'mopedu'
                        WHEN 'doorstep_booking'    THEN 'doorstep'
                        WHEN 'doorstep_extras'     THEN 'doorstep'
                    END
           END
$$;
