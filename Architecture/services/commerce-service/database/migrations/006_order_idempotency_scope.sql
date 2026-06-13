-- Align checkout idempotency with service semantics: keys are unique per
-- customer, not globally across all customers.

ALTER TABLE orders DROP CONSTRAINT IF EXISTS orders_idempotency_key_key;
DROP INDEX IF EXISTS orders_idempotency_key_key;

CREATE UNIQUE INDEX IF NOT EXISTS idx_orders_customer_idempotency_key
    ON orders(customer_user_id, idempotency_key)
    WHERE idempotency_key IS NOT NULL;
