-- Migration 018: the memberships preference category (paid memberships,
-- phase 1, 2026-10-23).
--
-- monetization-service publishes the membership life cycle on
-- monetization.events (membership.started, .renewed, .renewal_due,
-- .payment_failed, .ended). Six notices come from it: the creator hears
-- about a new member (membership_new_member) and the member hears that they
-- joined, renewed, are due to renew, that a payment failed, or that the
-- membership ended (membership_joined, membership_renewed,
-- membership_renewal_due, membership_payment_failed, membership_ended). One
-- pair of toggles covers all of them, split as in 005: inapp_ gates the inbox
-- row and realtime event, push_ the device push. Default TRUE on both:
-- renewal is by reminder (no auto-debit), so a member who never hears the
-- reminder silently loses access they paid for.
ALTER TABLE notification_preferences ADD COLUMN IF NOT EXISTS push_memberships  BOOLEAN NOT NULL DEFAULT TRUE;
ALTER TABLE notification_preferences ADD COLUMN IF NOT EXISTS inapp_memberships BOOLEAN NOT NULL DEFAULT TRUE;
