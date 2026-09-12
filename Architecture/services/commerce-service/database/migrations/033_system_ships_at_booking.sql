-- Commerce P0 — migration 033: the fulfilment worker may mark an order shipped.
--
-- Migration 010's matrix admits `packed -> shipped` for a seller and nothing
-- for "system". But the durable fulfilment job (M-8) books the courier the
-- moment payment lands, before any seller has touched the order, and then
-- flips the order to `shipped` as actor "system". On a gated database the
-- trigger refused that write and the error was discarded, so a booked
-- shipment sat behind an order that still read `confirmed`.
--
-- Two rows, both for the worker:
--
--   confirmed -> shipped   the worker booked before the seller packed; there
--                          was no packing step in the app to record
--   packed    -> shipped   the seller packed first and the (retried or
--                          delayed) worker booked afterwards
--
-- A seller booking a shipment still goes confirmed -> packed -> shipped
-- under their own rows; nothing here widens what a seller may do.
--
-- Expand-only: adding a permitted pair cannot reject any writer.

INSERT INTO order_status_transitions (from_status, to_status, actor_type) VALUES
    ('confirmed','shipped','system'),
    ('packed','shipped','system')
ON CONFLICT DO NOTHING;
