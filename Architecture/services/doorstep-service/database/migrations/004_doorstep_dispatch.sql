-- doorstep-service 004: offers, dispatch and presence (lane A4).
--
-- Adds the bookkeeping dispatch and its workers need on top of A3's
-- bookings: when dispatch last ran, whether it ran out of professionals,
-- whether ops were alerted, whether the professional is late, and a rescue
-- window for a job reassigned at the last minute. Professionals gain their
-- latest location fix (latest only, no history) and a no-show counter.
-- Re-runnable: IF NOT EXISTS / DROP ... IF EXISTS everywhere (tested by
-- running the file twice).

-- ---------------------------------------------------------------------
-- Professionals: presence. on_duty / on_duty_since and
-- doorstep.pro_duty_sessions already exist (001). last_point is the latest
-- fix only (overwritten, never a trail); Redis GEO mirrors it per city for
-- the live map. The stale-GPS worker takes a professional off duty when
-- last_fix_at is older than five minutes.
-- ---------------------------------------------------------------------
ALTER TABLE doorstep.professionals ADD COLUMN IF NOT EXISTS last_point geography(Point, 4326);
ALTER TABLE doorstep.professionals ADD COLUMN IF NOT EXISTS last_fix_at TIMESTAMPTZ;
ALTER TABLE doorstep.professionals ADD COLUMN IF NOT EXISTS last_fix_accuracy_m REAL;
ALTER TABLE doorstep.professionals ADD COLUMN IF NOT EXISTS no_show_count INT NOT NULL DEFAULT 0;
ALTER TABLE doorstep.professionals DROP CONSTRAINT IF EXISTS ck_doorstep_pros_no_show_count;
ALTER TABLE doorstep.professionals ADD CONSTRAINT ck_doorstep_pros_no_show_count CHECK (no_show_count >= 0);
CREATE INDEX IF NOT EXISTS idx_doorstep_pros_on_duty ON doorstep.professionals (last_fix_at) WHERE on_duty;

-- ---------------------------------------------------------------------
-- Bookings: dispatch bookkeeping.
--   dispatch_attempted_at  the last time dispatch ran for the booking (the
--                          retry worker waits between attempts).
--   dispatch_exhausted_at  dispatch found nobody left; ops were alerted
--                          once. Cleared when an offer goes out.
--   unassigned_alerted_at  the T-2 h "still unassigned" alert went out.
--   pro_late_at            slot + 15 min and the professional had not
--                          arrived: the customer was told and may cancel
--                          free of charge.
--   rescue_until           a last-minute reassignment (no-show, or a job
--                          given back inside 45 min of the slot): if nobody
--                          accepts by then, the booking ends with a full
--                          refund (rescue_cause decides the final status).
-- ---------------------------------------------------------------------
ALTER TABLE doorstep.bookings ADD COLUMN IF NOT EXISTS dispatch_attempted_at TIMESTAMPTZ;
ALTER TABLE doorstep.bookings ADD COLUMN IF NOT EXISTS dispatch_exhausted_at TIMESTAMPTZ;
ALTER TABLE doorstep.bookings ADD COLUMN IF NOT EXISTS unassigned_alerted_at TIMESTAMPTZ;
ALTER TABLE doorstep.bookings ADD COLUMN IF NOT EXISTS pro_late_at TIMESTAMPTZ;
ALTER TABLE doorstep.bookings ADD COLUMN IF NOT EXISTS rescue_until TIMESTAMPTZ;
ALTER TABLE doorstep.bookings ADD COLUMN IF NOT EXISTS rescue_cause TEXT;
ALTER TABLE doorstep.bookings DROP CONSTRAINT IF EXISTS ck_doorstep_bookings_rescue;
ALTER TABLE doorstep.bookings ADD CONSTRAINT ck_doorstep_bookings_rescue CHECK (
    (rescue_until IS NULL) = (rescue_cause IS NULL)
    AND (rescue_cause IS NULL OR rescue_cause IN ('pro_no_show', 'pro_cancel', 'not_on_duty', 'ops_redispatch')));
-- The workers scan live bookings by slot.
CREATE INDEX IF NOT EXISTS idx_doorstep_bookings_dispatch
    ON doorstep.bookings (slot_start) WHERE status IN ('confirmed', 'assigned', 'en_route');
CREATE INDEX IF NOT EXISTS idx_doorstep_bookings_rescue
    ON doorstep.bookings (rescue_until) WHERE rescue_until IS NOT NULL;

-- ---------------------------------------------------------------------
-- Assignments: a professional's open offers, and the jobs list.
-- ---------------------------------------------------------------------
CREATE INDEX IF NOT EXISTS idx_doorstep_assignments_pro_open
    ON doorstep.booking_assignments (pro_id, offer_expires_at) WHERE status = 'offered';
CREATE INDEX IF NOT EXISTS idx_doorstep_assignments_pro_jobs
    ON doorstep.booking_assignments (pro_id, booking_id) WHERE status IN ('accepted', 'completed');
CREATE INDEX IF NOT EXISTS idx_doorstep_assignments_booking ON doorstep.booking_assignments (booking_id, created_at);
