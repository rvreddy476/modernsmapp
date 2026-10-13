-- New bookings can use one administrator-maintained price and match before
-- payment. Existing quotes/bookings retain their customer-selected flow.
ALTER TABLE doorstep.quotes ADD COLUMN IF NOT EXISTS assignment_mode TEXT NOT NULL DEFAULT 'customer_selected'
    CHECK (assignment_mode IN ('customer_selected', 'automatic'));
ALTER TABLE doorstep.bookings ADD COLUMN IF NOT EXISTS assignment_mode TEXT NOT NULL DEFAULT 'customer_selected'
    CHECK (assignment_mode IN ('customer_selected', 'automatic'));
ALTER TABLE doorstep.bookings ADD COLUMN IF NOT EXISTS matching_deadline TIMESTAMPTZ;
ALTER TABLE doorstep.bookings DROP CONSTRAINT IF EXISTS bookings_status_check;
ALTER TABLE doorstep.bookings ADD CONSTRAINT bookings_status_check CHECK (status IN (
    'matching','pending_payment','confirmed','assigned','en_route','arrived','in_progress','awaiting_extras_payment','completed',
    'cancelled','expired','customer_no_show','pro_no_show','pro_unavailable'));
CREATE INDEX IF NOT EXISTS idx_doorstep_matching ON doorstep.bookings (matching_deadline) WHERE status = 'matching';
-- Multiple eligible partners may receive the same offer, but exactly one wins.
DROP INDEX IF EXISTS doorstep.uq_doorstep_assignment_active_lead;
CREATE UNIQUE INDEX IF NOT EXISTS uq_doorstep_assignment_active_lead
    ON doorstep.booking_assignments (booking_id) WHERE role = 'lead' AND status = 'accepted';
