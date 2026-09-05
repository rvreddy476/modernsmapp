-- 020 — two column defaults that their own CHECK constraint rejects.
--
-- `products.approval_status` was declared in setup.sql as
--
--     approval_status TEXT NOT NULL DEFAULT 'pending'
--         CHECK (approval_status IN ('pending','approved','rejected','flagged'))
--
-- and migration 001 then replaced the allow-list with the real workflow
-- states — 'draft','submitted','under_review','approved','rejected','live',
-- 'hidden','archived' — without touching the default. 'pending' is no longer
-- one of them. `products.return_policy_type` has the same shape: it defaults
-- to 'standard', a value its CHECK has never accepted.
--
-- The effect is a column default that cannot be used. Any INSERT that omits
-- the column is rejected outright:
--
--     INSERT INTO products (id,seller_id,title,slug) VALUES (...);
--     ERROR:  new row violates check constraint "products_approval_status_check"
--
-- Nothing in the live create path hits this today — `CreateProduct` names both
-- columns and the service supplies 'draft' and '7_days'. That is exactly what
-- makes it a landmine rather than an outage: it is invisible until someone
-- writes an insert that trusts the schema's own stated defaults, or a seller
-- sends the documented-looking value 'standard' and gets a 500 from a CHECK.
--
-- ─── WHY THESE VALUES ───────────────────────────────────────────────────
--
-- 'draft' is what a newly created product is: authored, not yet submitted for
-- review. It is the value `CreateProduct` already passes, so the default now
-- agrees with the only writer instead of contradicting it.
--
-- '7_days' likewise matches `coalesceStr(in.ReturnPolicyType, "7_days")` in
-- the service. Picking 'no_return' instead would make an omitted field
-- silently remove the customer's return rights, which is not a default any
-- schema should hand out.
--
-- ─── EXPAND-ONLY ────────────────────────────────────────────────────────
--
-- ALTER COLUMN ... SET DEFAULT is a catalogue update. No table rewrite, no
-- validation scan, no lock beyond the brief ACCESS EXCLUSIVE needed to update
-- pg_attrdef, and no existing row changes value. Rolling back to the previous
-- image is safe: that image also never relies on either default.

ALTER TABLE products ALTER COLUMN approval_status   SET DEFAULT 'draft';
ALTER TABLE products ALTER COLUMN return_policy_type SET DEFAULT '7_days';

COMMENT ON COLUMN products.approval_status IS
    'Review workflow state. Default realigned in migration 020: it was ''pending'', which '
    'migration 001 removed from the CHECK allow-list, so any insert omitting this column failed.';

COMMENT ON COLUMN products.return_policy_type IS
    'Return window. Default realigned in migration 020: it was ''standard'', a value the CHECK '
    'has never accepted.';
