-- Commerce P0 — migration 012: the fence.
--
-- A5 (prepaid only), LB-11 (returns), D2 (single seller), and §4's fenced
-- surface list.
--
-- The review's §7 ruling: "Fencing is sufficient only if there is a
-- production-default-deny route allowlist, gateway reachability test,
-- disabled worker/consumer entry points, and safe handling/quarantine of
-- legacy queued messages. Hiding controls in Android is not a fence."
--
-- Routes and workers are fenced in Go. This migration is the part the
-- application cannot bypass even by accident: the DATABASE refuses to record
-- a fenced state. If a queued job from before the cutover replays, or a code
-- path is missed, the write fails rather than quietly succeeding.

-- ─── A5: prepaid only ────────────────────────────────────────────────
--
-- COD is not merely hidden. Its checkout path was the one that deducted
-- stock immediately, which is where the worst inventory corruption lived,
-- and its operational half (failed-delivery restock, cash remittance,
-- reconciliation) was never built. Until a founder scope change adds those
-- controls, the database will not accept a COD order.

-- B11 — the NARROWED CHECK moved to gated/998_contract_triggers_and_fences.sql.
--
-- Dropping the existing payment_method constraint and installing one that no
-- longer admits 'cod' is a CONTRACT operation. An old replica still creating
-- COD orders — which its code does — would have its INSERT refused the moment
-- this landed, mid-rollout, for a customer. That is a deploy-induced outage,
-- not a defect in either image, and it is exactly what "expand-only boot
-- migrations" is supposed to prevent.
--
-- The quarantine table below stays: it is additive, and capturing the legacy
-- COD rows is useful whether or not the constraint has landed yet.

-- Legacy COD orders are quarantined rather than deleted: they are real
-- history, and the fence is about what can be CREATED from now on.
CREATE TABLE IF NOT EXISTS fenced_legacy_orders AS
SELECT id, order_number, payment_method, status, created_at
  FROM orders
 WHERE payment_method = 'cod';

-- ─── LB-11: returns ──────────────────────────────────────────────────
--
-- `CreateReturnRequest` persisted caller-supplied order_id, order_item_id,
-- customer_user_id and seller_id with no relational check, so a caller could
-- attach a return to someone else's order and move that order to
-- `return_requested` (review M-3). Returns are out of the P0 loop, so the
-- cheap and safe answer is that no new return can be created at all.

CREATE OR REPLACE FUNCTION refuse_fenced_return() RETURNS TRIGGER AS $$
BEGIN
    RAISE EXCEPTION
        'returns are fenced in Commerce P0 (LB-11): the return loop is not part of the launch scope '
        'and its creation path did not verify order ownership'
        USING ERRCODE = 'insufficient_privilege';
END;
$$ LANGUAGE plpgsql;

-- B11: the trigger ATTACHMENT moved to gated/998. The function definition
-- above is expand-only and stays.

-- ─── Fenced P2 surfaces ──────────────────────────────────────────────
--
-- Built, unreachable, and kept. Each of these is a write path that is out of
-- the launch loop; the trigger makes "we forgot to unregister a route" a
-- failed insert rather than a live feature.

CREATE OR REPLACE FUNCTION refuse_fenced_surface() RETURNS TRIGGER AS $$
BEGIN
    RAISE EXCEPTION
        'table % is fenced in Commerce P0: this surface is outside the launch loop', TG_TABLE_NAME
        USING ERRCODE = 'insufficient_privilege';
END;
$$ LANGUAGE plpgsql;

-- B11: the twelve `trg_fence_*` attachments and the reviews fence moved to
-- gated/998_contract_triggers_and_fences.sql. Each refuses INSERTs outright
-- on a table an old replica may still be writing to, so attaching them at
-- boot breaks the old image mid-rollout.
--
-- Reviews stay READABLE either way — the backend already enforces verified
-- purchase and the product page shows them — and no new review can be written
-- once the gated step lands (D-9). Read paths are untouched throughout.
--
-- NOTE ON THE ROLLOUT WINDOW. Between the boot migrations and the gated step,
-- the DATABASE fence is not installed. The APPLICATION fence still is:
-- FenceMiddleware (internal/http/handler_p0.go) answers 404 for every fenced
-- prefix and for the legacy money routes, ahead of routing, and those
-- surfaces are unregistered besides. The database trigger is the third layer,
-- and it lands when the fleet is drained.

-- ─── Fence inventory, for the proof ──────────────────────────────────
--
-- The acceptance criteria require every fenced surface to be proven
-- unreachable by test rather than by inspection. This view is what that test
-- enumerates, so a surface added later without a fence shows up as a
-- difference rather than being silently omitted.

CREATE OR REPLACE VIEW fenced_surfaces AS
SELECT c.relname AS table_name, t.tgname AS trigger_name
  FROM pg_trigger t
  JOIN pg_class c ON c.oid = t.tgrelid
 WHERE NOT t.tgisinternal
   AND t.tgname LIKE 'trg_fence%';
