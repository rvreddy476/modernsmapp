-- Commerce P0 — migration 013: give a fulfilment job a stable identity.
--
-- M-8 / LB-24. The paid-order job is now created INSIDE the transaction that
-- marks the order paid, so a process exit between the two can no longer
-- leave a paid order that nobody fulfils.
--
-- Making it transactional exposes the other half of the problem: without an
-- identity, "create the job" is not idempotent. A retried payment event, or
-- a reconciler resolving the same capture the webhook already resolved,
-- would enqueue a second job — and this job books a courier shipment, so a
-- duplicate costs a second AWB and a second charge.
--
-- `fulfillment_jobs` is keyed (kind, payload JSONB), which has no natural
-- uniqueness. This adds it: one live job per (kind, order).
--
-- Scoped to pending/processing so a legitimately re-run job after a
-- completed one — a re-shipment, say — is still possible.

CREATE UNIQUE INDEX IF NOT EXISTS idx_fulfillment_jobs_one_live_per_order
    ON fulfillment_jobs (kind, (payload ->> 'order_id'))
    WHERE status IN ('pending', 'processing');

-- Read path for "does this order already have a live job", used by the
-- reconciler and by the ops queue view.
CREATE INDEX IF NOT EXISTS idx_fulfillment_jobs_order
    ON fulfillment_jobs ((payload ->> 'order_id'));
