-- Commerce — migration 039: the admin audit log learns the in-place invoice
-- correction (cmd/reissue-invoices).
--
-- Every invoice issued between the P0 checkout and the fix of 1 Oct 2026
-- (31dc9eac) went out at ₹0: IssueInvoice read the NUMERIC rupee columns the
-- P0 checkout leaves at 0.00. invoices.order_id is UNIQUE and IssueInvoice
-- returns an existing row, so those invoices are never re-issued on their
-- own. cmd/reissue-invoices corrects them IN PLACE — same number, financial
-- year, sequence and date; totals and document rebuilt from the order's
-- stored paise — and records each correction here.
--
-- ─── THE ACTOR ──────────────────────────────────────────────────────────
--
-- No human performs the correction; an operator runs a tool. actor_user_id
-- is NOT NULL and refuses the nil uuid (036), so the tool writes one fixed
-- id, 0888221b-0db5-4fcc-81e0-67a27eb880b5, which no user holds
-- (postgres.InvoiceReissueActorID), with reason 'system:invoice_reissue'.
-- The two are bound both ways by chk_admin_audit_invoice_reissue_actor:
-- that id can write nothing but an invoice_reissue row, and an
-- invoice_reissue row can come from nothing but that id. A system id that
-- could sign any action would be the "plausible-looking id" 036 forbids.
--
-- Additive and ungated: the old values are all still allowed, and an old
-- replica never writes the new ones.

ALTER TABLE commerce_admin_audit_log DROP CONSTRAINT IF EXISTS chk_admin_audit_action;
ALTER TABLE commerce_admin_audit_log ADD CONSTRAINT chk_admin_audit_action
    CHECK (action IN ('cod_remittance_settle','seller_kyc_verify',
                      'banner_create','banner_update','banner_delete',
                      'coupon_create','coupon_update',
                      'invoice_reissue'));

ALTER TABLE commerce_admin_audit_log DROP CONSTRAINT IF EXISTS chk_admin_audit_target_type;
ALTER TABLE commerce_admin_audit_log ADD CONSTRAINT chk_admin_audit_target_type
    CHECK (target_type IN ('cod_remittance','seller','banner','coupon','invoice'));

ALTER TABLE commerce_admin_audit_log DROP CONSTRAINT IF EXISTS chk_admin_audit_invoice_reissue_actor;
ALTER TABLE commerce_admin_audit_log ADD CONSTRAINT chk_admin_audit_invoice_reissue_actor
    CHECK (
        (actor_user_id = '0888221b-0db5-4fcc-81e0-67a27eb880b5'::uuid)
        = (action = 'invoice_reissue'
           AND target_type = 'invoice'
           AND reason IS NOT DISTINCT FROM 'system:invoice_reissue')
    );
