-- doorstep-service 006: the visit (lane A5).
--
-- 001 already holds the visit tables (extras, bills, photos, OTPs,
-- outstanding, rework, ratings, incidents, share tokens, trusted contacts,
-- messages, tickets, earning lines, settlements). This adds what A5's rules
-- need on top, and database guards that hold whatever code path writes:
--
--   * a booking moves arrived -> in_progress only with its start OTP
--     verified and the accepted professional's minimum before photos (plus
--     a sealed-kit photo in a salon); it is completed only with its end OTP
--     verified, the minimum after photos, and no visit-extras bill still
--     waiting for payment (paid or outstanding only);
--   * an add-on extra carries an approved price of the extra's own
--     professional (B1: professionals price their own add-ons);
--   * the new pro_unavailable causes: the professional left feeling unsafe,
--     or was suspended after a salon customer's incident.
--
-- Re-runnable: IF NOT EXISTS / DROP ... IF EXISTS everywhere (tested by
-- running the file twice).

-- ---------------------------------------------------------------------
-- pro_unavailable causes (A5): pro_unsafe_exit (the professional's
-- "unsafe, leaving" exit; no penalty) and pro_suspended (a salon customer's
-- incident suspended the professional; every open job of theirs goes back
-- to its customer).
-- ---------------------------------------------------------------------
ALTER TABLE doorstep.bookings DROP CONSTRAINT IF EXISTS ck_doorstep_bookings_pro_unavailable;
ALTER TABLE doorstep.bookings ADD CONSTRAINT ck_doorstep_bookings_pro_unavailable CHECK (
    (status <> 'pro_unavailable' OR (choice_deadline IS NOT NULL AND unavailable_cause IS NOT NULL AND reserved_pro_id IS NULL))
    AND (unavailable_cause IS NULL OR unavailable_cause IN
        ('declined','offer_expired','pro_cancel','not_on_duty','pro_no_show','ops_redispatch','no_professional',
         'pro_unsafe_exit','pro_suspended')));

-- The invoice snapshot written at completion (A5) is never empty.
ALTER TABLE doorstep.bookings DROP CONSTRAINT IF EXISTS ck_doorstep_bookings_completed;
ALTER TABLE doorstep.bookings ADD CONSTRAINT ck_doorstep_bookings_completed CHECK (
    status <> 'completed' OR (completed_at IS NOT NULL AND finished_at IS NOT NULL AND started_at IS NOT NULL));

-- ---------------------------------------------------------------------
-- Earning lines (settlement compute; payouts stay OFF): the service, the
-- extras labour, parts reimbursed at what the customer paid, the
-- compensation for a customer's late cancel or no-show, the commission and
-- penalties (negative). One line per kind per booking and professional.
-- ---------------------------------------------------------------------
ALTER TABLE doorstep.earning_lines DROP CONSTRAINT IF EXISTS earning_lines_kind_check;
ALTER TABLE doorstep.earning_lines ADD CONSTRAINT earning_lines_kind_check CHECK (kind IN
    ('job','extras','parts','cancel_compensation','incentive','penalty','adjustment','commission'));
ALTER TABLE doorstep.earning_lines DROP CONSTRAINT IF EXISTS ck_doorstep_earning_sign;
ALTER TABLE doorstep.earning_lines ADD CONSTRAINT ck_doorstep_earning_sign CHECK (
    (kind NOT IN ('commission','penalty') OR amount_paise <= 0)
    AND (kind NOT IN ('job','extras','parts','cancel_compensation') OR amount_paise >= 0));
CREATE INDEX IF NOT EXISTS idx_doorstep_earnings_unsettled ON doorstep.earning_lines (created_at) WHERE settlement_id IS NULL;

-- ---------------------------------------------------------------------
-- Professionals: a GSTIN when the professional is registered (read at
-- completion: a registered professional's invoice is <FAMILY>_REGISTERED,
-- an unregistered one's <FAMILY>_VIA_ECO; salon is registered-only). Set by
-- an admin (A6); NULL = unregistered.
-- ---------------------------------------------------------------------
ALTER TABLE doorstep.professionals ADD COLUMN IF NOT EXISTS gstin TEXT;
ALTER TABLE doorstep.professionals DROP CONSTRAINT IF EXISTS ck_doorstep_pros_gstin;
ALTER TABLE doorstep.professionals ADD CONSTRAINT ck_doorstep_pros_gstin CHECK (gstin IS NULL OR gstin ~ '^[0-9]{2}[A-Z0-9]{13}$');

-- ---------------------------------------------------------------------
-- Extras: the unit and whether the item is a part (reimbursed, never
-- commissioned) are copied from the rate card; an add-on extra carries the
-- professional's approved add-on price row.
-- ---------------------------------------------------------------------
ALTER TABLE doorstep.booking_extras ADD COLUMN IF NOT EXISTS unit TEXT NOT NULL DEFAULT 'per_item';
ALTER TABLE doorstep.booking_extras ADD COLUMN IF NOT EXISTS is_part BOOLEAN NOT NULL DEFAULT FALSE;
ALTER TABLE doorstep.booking_extras ADD COLUMN IF NOT EXISTS pro_price_id UUID REFERENCES doorstep.pro_service_prices(id);
ALTER TABLE doorstep.booking_extras ADD COLUMN IF NOT EXISTS taxable_paise BIGINT NOT NULL DEFAULT 0;
ALTER TABLE doorstep.booking_extras ADD COLUMN IF NOT EXISTS tax_paise BIGINT NOT NULL DEFAULT 0;
ALTER TABLE doorstep.booking_extras DROP CONSTRAINT IF EXISTS ck_doorstep_extras_addon_price;
ALTER TABLE doorstep.booking_extras ADD CONSTRAINT ck_doorstep_extras_addon_price CHECK (kind <> 'addon' OR pro_price_id IS NOT NULL);
CREATE INDEX IF NOT EXISTS idx_doorstep_extras_bill ON doorstep.booking_extras (bill_id) WHERE bill_id IS NOT NULL;

CREATE OR REPLACE FUNCTION doorstep.extra_price_must_be_approved() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF NEW.kind = 'addon' AND NOT EXISTS (
        SELECT 1 FROM doorstep.pro_service_prices pp
         WHERE pp.id = NEW.pro_price_id AND pp.status = 'approved' AND pp.pro_id = NEW.pro_id AND pp.addon_id = NEW.addon_id) THEN
        RAISE EXCEPTION 'an add-on extra may only carry an approved add-on price of its own professional'
            USING ERRCODE = '23514', CONSTRAINT = 'ck_doorstep_extra_price_approved';
    END IF;
    RETURN NEW;
END $$;
DROP TRIGGER IF EXISTS trg_doorstep_extra_price ON doorstep.booking_extras;
CREATE TRIGGER trg_doorstep_extra_price
    BEFORE INSERT ON doorstep.booking_extras
    FOR EACH ROW EXECUTE FUNCTION doorstep.extra_price_must_be_approved();

-- Bills: when the grace period ends (finish + 15 min) and which bills are
-- unpaid.
CREATE INDEX IF NOT EXISTS idx_doorstep_extras_bills_due ON doorstep.extras_bills (due_at)
    WHERE kind = 'visit_extras' AND status = 'payment_pending';

-- ---------------------------------------------------------------------
-- Chat: each message belongs to the conversation of one professional, so a
-- professional who takes over a booking never reads the previous one's.
-- ---------------------------------------------------------------------
ALTER TABLE doorstep.messages ADD COLUMN IF NOT EXISTS pro_id UUID REFERENCES doorstep.professionals(id);

-- Rework: the slot asked for and whether the customer wants the same
-- professional (the default).
ALTER TABLE doorstep.rework_requests ADD COLUMN IF NOT EXISTS slot_start TIMESTAMPTZ;
ALTER TABLE doorstep.rework_requests ADD COLUMN IF NOT EXISTS same_professional BOOLEAN NOT NULL DEFAULT TRUE;
CREATE INDEX IF NOT EXISTS idx_doorstep_rework_booking ON doorstep.rework_requests (booking_id, created_at);
CREATE INDEX IF NOT EXISTS idx_doorstep_rework_child ON doorstep.rework_requests (child_booking_id) WHERE child_booking_id IS NOT NULL;

-- Safety and support lookups.
CREATE INDEX IF NOT EXISTS idx_doorstep_incidents_booking ON doorstep.incidents (booking_id, created_at);
CREATE INDEX IF NOT EXISTS idx_doorstep_incidents_pro ON doorstep.incidents (pro_id, created_at) WHERE pro_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_doorstep_share_booking ON doorstep.share_tokens (booking_id) WHERE revoked_at IS NULL;
CREATE INDEX IF NOT EXISTS idx_doorstep_ratings_booking ON doorstep.ratings (booking_id);

-- ---------------------------------------------------------------------
-- The visit gate, whatever code path moves the status (fail closed):
--   arrived -> in_progress   start OTP verified; the accepted professional's
--                            before photos >= the service minimum; a salon
--                            job also has a sealed-kit photo
--   -> completed             end OTP verified; after photos >= the minimum;
--                            no visit-extras bill open or payment_pending
-- Photos count only the accepted professional's (a professional who takes
-- over takes their own).
-- ---------------------------------------------------------------------
CREATE OR REPLACE FUNCTION doorstep.visit_gate() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE
    pro        UUID;
    min_before INT;
    min_after  INT;
    fam        TEXT;
BEGIN
    IF NEW.status = OLD.status OR NEW.status NOT IN ('in_progress', 'completed') THEN
        RETURN NEW;
    END IF;
    IF NEW.status = 'in_progress' AND OLD.status <> 'arrived' THEN
        RAISE EXCEPTION 'a visit starts only from arrived'
            USING ERRCODE = '23514', CONSTRAINT = 'ck_doorstep_visit_gate';
    END IF;
    SELECT a.pro_id INTO pro FROM doorstep.booking_assignments a
     WHERE a.booking_id = NEW.id AND a.role = 'lead' AND a.status = 'accepted' LIMIT 1;
    SELECT s.min_before_photos, s.min_after_photos, c.family INTO min_before, min_after, fam
      FROM doorstep.services s JOIN doorstep.categories c ON c.id = s.category_id WHERE s.id = NEW.service_id;
    IF pro IS NULL THEN
        RAISE EXCEPTION 'a visit moves only with an accepted professional'
            USING ERRCODE = '23514', CONSTRAINT = 'ck_doorstep_visit_gate';
    END IF;
    IF NEW.status = 'in_progress' THEN
        IF NOT EXISTS (SELECT 1 FROM doorstep.booking_otps o WHERE o.booking_id = NEW.id AND o.kind = 'start' AND o.verified_at IS NOT NULL)
           OR (SELECT count(*) FROM doorstep.booking_photos p WHERE p.booking_id = NEW.id AND p.pro_id = pro AND p.phase = 'before') < min_before
           OR (fam = 'BEAUTY_SALON' AND NOT EXISTS (SELECT 1 FROM doorstep.booking_photos p
                WHERE p.booking_id = NEW.id AND p.pro_id = pro AND p.phase = 'kit_seal')) THEN
            RAISE EXCEPTION 'a visit starts only with the start OTP and the before photos'
                USING ERRCODE = '23514', CONSTRAINT = 'ck_doorstep_visit_gate';
        END IF;
    ELSE
        IF NOT EXISTS (SELECT 1 FROM doorstep.booking_otps o WHERE o.booking_id = NEW.id AND o.kind = 'end' AND o.verified_at IS NOT NULL)
           OR (SELECT count(*) FROM doorstep.booking_photos p WHERE p.booking_id = NEW.id AND p.pro_id = pro AND p.phase = 'after') < min_after
           OR EXISTS (SELECT 1 FROM doorstep.extras_bills eb WHERE eb.booking_id = NEW.id AND eb.kind = 'visit_extras'
                       AND eb.status IN ('open', 'payment_pending')) THEN
            RAISE EXCEPTION 'a visit completes only with the end OTP, the after photos and the extras paid or outstanding'
                USING ERRCODE = '23514', CONSTRAINT = 'ck_doorstep_visit_gate';
        END IF;
    END IF;
    RETURN NEW;
END $$;
DROP TRIGGER IF EXISTS trg_doorstep_visit_gate ON doorstep.bookings;
CREATE TRIGGER trg_doorstep_visit_gate
    BEFORE UPDATE OF status ON doorstep.bookings
    FOR EACH ROW EXECUTE FUNCTION doorstep.visit_gate();
