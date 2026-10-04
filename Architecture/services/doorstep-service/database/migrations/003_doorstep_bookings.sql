-- doorstep-service 003: bookings, holds and payments (lane A3).
--
-- Adds what 001 left to A3: sealed customer address lines, the professional
-- a booking reserved, a "needs attention" flag for money that did not match,
-- and the indexes the payment consumer, the refund worker and the slot
-- search read. Re-runnable: IF NOT EXISTS / DROP ... IF EXISTS everywhere
-- (tested by running the file twice).

-- ---------------------------------------------------------------------
-- Customer addresses: the street lines (line1, line2, landmark) are sealed
-- with shared/pii (scope doorstep.customer_address) as one JSON blob. Only
-- the locality, pincode, city, zone and the point stay readable: the
-- locality is what a professional sees before accepting, the point is what
-- serviceability and dispatch need. The plaintext columns stay for the
-- schema's shape and are never written by the service (NULL).
-- ---------------------------------------------------------------------
ALTER TABLE doorstep.customer_addresses ALTER COLUMN line1 DROP NOT NULL;
ALTER TABLE doorstep.customer_addresses ADD COLUMN IF NOT EXISTS lines_sealed BYTEA;
ALTER TABLE doorstep.customer_addresses ADD COLUMN IF NOT EXISTS lines_key_version INT;
ALTER TABLE doorstep.customer_addresses DROP CONSTRAINT IF EXISTS ck_doorstep_address_lines;
ALTER TABLE doorstep.customer_addresses ADD CONSTRAINT ck_doorstep_address_lines
    CHECK (line1 IS NOT NULL OR lines_sealed IS NOT NULL);

-- ---------------------------------------------------------------------
-- Bookings.
--   reserved_pro_id   the professional whose calendar holds the slot (the
--                     hold, then the booking block). A4 offers the job to
--                     this professional first.
--   address_sealed    the address lines at booking time, sealed like the
--                     address (address_snapshot keeps only the readable
--                     parts: label, locality, city, pincode, point, zone).
--   needs_attention   money did not match (a capture for another amount,
--                     payer or intent, or a refund payments could not
--                     make): the booking is never confirmed from it and ops
--                     must look. attention_reason says why.
-- ---------------------------------------------------------------------
ALTER TABLE doorstep.bookings ADD COLUMN IF NOT EXISTS reserved_pro_id UUID REFERENCES doorstep.professionals(id);
ALTER TABLE doorstep.bookings ADD COLUMN IF NOT EXISTS address_sealed BYTEA;
ALTER TABLE doorstep.bookings ADD COLUMN IF NOT EXISTS needs_attention BOOLEAN NOT NULL DEFAULT FALSE;
ALTER TABLE doorstep.bookings ADD COLUMN IF NOT EXISTS attention_reason TEXT;
CREATE INDEX IF NOT EXISTS idx_doorstep_bookings_reserved_pro ON doorstep.bookings (reserved_pro_id, slot_start) WHERE reserved_pro_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_doorstep_bookings_attention ON doorstep.bookings (updated_at) WHERE needs_attention;
CREATE INDEX IF NOT EXISTS idx_doorstep_bookings_created ON doorstep.bookings (created_at DESC, id DESC);

-- A booking holds at most one active calendar block (hold or booking) per
-- professional; with single-professional bookings (crew off) that is one.
CREATE UNIQUE INDEX IF NOT EXISTS uq_doorstep_calendar_booking_active
    ON doorstep.pro_calendar_blocks (booking_id, pro_id) WHERE active AND booking_id IS NOT NULL;

-- Payment inbox: what the decision found (mismatch detail, late capture).
ALTER TABLE doorstep.payment_inbox ADD COLUMN IF NOT EXISTS detail TEXT;

-- Payments: one row per (booking, reference) intent key; the payments-
-- service intent id is unique once known.
CREATE UNIQUE INDEX IF NOT EXISTS uq_doorstep_payments_intent ON doorstep.payments (payments_intent_id) WHERE payments_intent_id IS NOT NULL;

-- Refunds: payment.refunded / payment.refund_failed name the command.
CREATE INDEX IF NOT EXISTS idx_doorstep_refunds_command ON doorstep.refunds (payments_refund_id) WHERE payments_refund_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_doorstep_refunds_booking ON doorstep.refunds (booking_id, created_at);
-- The worker resubmits only refunds payments never accepted.
DROP INDEX IF EXISTS doorstep.idx_doorstep_refunds_retry;
CREATE INDEX IF NOT EXISTS idx_doorstep_refunds_unsubmitted ON doorstep.refunds (next_attempt_at) WHERE status = 'requested';
