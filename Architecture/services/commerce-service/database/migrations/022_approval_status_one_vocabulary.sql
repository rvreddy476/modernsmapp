-- 022 — one vocabulary for "this product may be sold" (the EXPAND half).
--
-- `ApproveProductByAdmin` wrote approval_status='live'. Both gates that decide
-- whether a product may be SOLD require 'approved':
--
--     ProductSaleEligibility  (add-to-cart)      accessors_p0.go
--     lockAndPriceLines       (checkout, locked) checkout.go
--
-- Neither has ever accepted 'live'. The browse queries, however, accepted
-- `approval_status IN ('approved','live')`. So an approved product was VISIBLE
-- and unbuyable at the same time: it appeared in the public catalogue, and
-- add-to-cart answered "a product in the cart is no longer available". Every
-- product that ever went through the moderation queue was in that state.
--
-- ─── WHY THIS FILE ONLY CONVERTS, AND DOES NOT TIGHTEN ──────────────────
--
-- Removing 'live' from the CHECK constraint is a CONTRACT operation. During a
-- rolling deploy the old image is still running `ApproveProductByAdmin` as it
-- was, so a narrowed constraint would reject the old pods' approvals for as
-- long as the rollout takes. That is the failure mode the expand-only rule
-- exists to prevent, and it is why the tightening lives in
-- `database/gated/1001_approval_status_tighten.sql` instead.
--
-- What remains here is a pure widening of what can be sold: rows an admin
-- genuinely approved become sellable, which is what the approval meant. No
-- product gains an approval it was not given, nothing is rejected that was
-- previously accepted, and an old pod that writes 'live' after this runs is
-- still writing a value its own CHECK permits — it simply produces a product
-- that this same conversion will pick up on the next deploy, exactly as it
-- did before.
--
-- `status` is untouched. The two columns answer different questions: `status`
-- ('active' plus published_at) is what makes a listing public, and
-- `approval_status` records the review outcome. 'live' was a second,
-- disagreeing spelling of the first, stored in the column that means the
-- second.

BEGIN;

UPDATE products
   SET approval_status = 'approved',
       updated_at      = NOW()
 WHERE approval_status = 'live';

COMMIT;
