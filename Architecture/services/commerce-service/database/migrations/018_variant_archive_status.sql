-- 018 — a variant can actually be archived.
--
-- `Store.ArchiveVariant` writes `status = 'archived'`, and the base CHECK
-- permits only `active`, `inactive`, `out_of_stock`. So `DELETE
-- /v1/commerce/variants/:variantId` — the only way a seller can retire a
-- product line — has never worked: it fails on the constraint and surfaces as
-- a 500. `PATCH` can be handed the same value and fails identically.
--
-- Archiving is a soft delete on purpose. Orders and carts hold foreign keys to
-- the variant, and a hard DELETE would either be refused or cascade into
-- purchase history. `archived` keeps the row resolvable for every order that
-- already references it while removing it from sale: `ProductSaleEligibility`
-- requires `status = 'active'`, so an archived variant cannot be added to a
-- cart or checked out.
--
-- EXPAND-ONLY. This WIDENS the permitted set — every value the old constraint
-- accepted, the new one accepts. An old binary running against the new schema
-- is unaffected because it never writes 'archived'; a new binary running
-- against the old schema is what is broken today. Widening is therefore safe
-- in the boot set, and the marker below records why.

ALTER TABLE product_variants
    DROP CONSTRAINT IF EXISTS product_variants_status_check;  -- expand-only: replaced by a strict superset below

ALTER TABLE product_variants
    ADD CONSTRAINT product_variants_status_check              -- expand-only: adds 'archived' to the existing three
    CHECK (status IN ('active','inactive','out_of_stock','archived')) NOT VALID;

-- NOT VALID is deliberate and sufficient. A NOT VALID CHECK is enforced for
-- every NEW row immediately — which is all `archived` needs — while skipping
-- the full-table scan that would hold a lock during a rollout.
--
-- Marking the EXISTING rows verified is a separate, contract-shaped step and
-- lives in database/gated/999_validate_and_tighten.sql, where the boot set's
-- expand-only rule sends every VALIDATE.

COMMENT ON CONSTRAINT product_variants_status_check ON product_variants IS
    'Variant lifecycle. `archived` is the soft delete DELETE /variants/:id performs: orders and '
    'carts keep foreign keys to the row, so it must stay resolvable, while ProductSaleEligibility '
    'requires status = ''active'' and therefore refuses it for new sales.';
