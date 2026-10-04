-- doorstep-service 005: professionals price their own services, the customer
-- picks a professional, same-day "as soon as possible" jobs, and the
-- pro_unavailable state (lane B1, the 4 Oct 2026 change of model).
--
-- The platform catalogue stays the menu (categories, services, options,
-- add-ons, units); every bookable price is a professional's own row in
-- doorstep.pro_service_prices, reviewed by an admin. city_prices stays as an
-- optional suggested price only: nothing is ever charged from it again.
-- Re-runnable: IF NOT EXISTS / DROP ... IF EXISTS everywhere (tested by
-- running the file twice).

-- ---------------------------------------------------------------------
-- Catalogue: the new service families and per-unit options.
--
-- Families drive GST (internal/tax). Appliance repairs stay
-- APPLIANCE_REPAIR and disinfection is PEST_CONTROL (SAC 998531 covers
-- disinfecting); makeup artists are BEAUTY_SALON (women's beauty rules).
-- CAR_CARE, HOME_STAFFING, RELOCATION, PHOTOGRAPHY, FITNESS_WELLNESS and
-- CONSTRUCTION have no shared/gst category yet: the tax lane maps them, and
-- until it does those services are hidden from customers (professionals can
-- still declare the skills and submit prices).
-- ---------------------------------------------------------------------
ALTER TABLE doorstep.categories DROP CONSTRAINT IF EXISTS categories_family_check;
ALTER TABLE doorstep.categories ADD CONSTRAINT categories_family_check CHECK (family IN (
    'HOME_CLEANING','PEST_CONTROL','APPLIANCE_REPAIR','INSTALLATION_REPAIR','PAINTING','BEAUTY_SALON',
    'CAR_CARE','HOME_STAFFING','RELOCATION','PHOTOGRAPHY','FITNESS_WELLNESS','CONSTRUCTION'));

-- An option is priced per job (the default), per hour (staffing, yoga,
-- photography) or per month (staffing engagements). quantity counts units;
-- duration_minutes is per unit (a monthly option's duration is the first
-- visit, which is what the calendar reserves).
ALTER TABLE doorstep.service_options ADD COLUMN IF NOT EXISTS unit TEXT NOT NULL DEFAULT 'per_job';
ALTER TABLE doorstep.service_options DROP CONSTRAINT IF EXISTS ck_doorstep_options_unit;
ALTER TABLE doorstep.service_options ADD CONSTRAINT ck_doorstep_options_unit CHECK (unit IN ('per_job','per_hour','per_month'));

-- ---------------------------------------------------------------------
-- Professional prices. One row per professional x option/add-on x
-- submission:
--   pending    submitted, waiting for an admin (at most one per item)
--   approved   live from effective_from until effective_to (NULL = live);
--              a newer approval closes it (effective_to = approval time)
--   rejected   an admin refused it (reason required)
--   withdrawn  the professional took it back (a pending one, or the live
--              one: then effective_to records when it stopped)
-- A changed price is a new pending row; the approved one stays live until
-- the new one is approved. GST-inclusive paise, like every customer price.
-- ---------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS doorstep.pro_service_prices (
    id             UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    pro_id         UUID NOT NULL REFERENCES doorstep.professionals(id),
    service_id     UUID NOT NULL REFERENCES doorstep.services(id),
    item_kind      TEXT NOT NULL CHECK (item_kind IN ('option','addon')),
    option_id      UUID REFERENCES doorstep.service_options(id),
    addon_id       UUID REFERENCES doorstep.addons(id),
    unit           TEXT NOT NULL CHECK (unit IN ('per_job','per_hour','per_month')),
    price_paise    BIGINT NOT NULL CHECK (price_paise > 0 AND price_paise <= 10000000),
    status         TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending','approved','rejected','withdrawn')),
    effective_from TIMESTAMPTZ,
    effective_to   TIMESTAMPTZ,
    submitted_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    reviewed_by    UUID,
    reviewed_at    TIMESTAMPTZ,
    reason         TEXT,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT ck_doorstep_pro_price_item CHECK ((item_kind = 'option' AND option_id IS NOT NULL AND addon_id IS NULL)
        OR (item_kind = 'addon' AND addon_id IS NOT NULL AND option_id IS NULL)),
    CONSTRAINT ck_doorstep_pro_price_approved CHECK (status <> 'approved'
        OR (effective_from IS NOT NULL AND reviewed_at IS NOT NULL AND reviewed_by IS NOT NULL)),
    CONSTRAINT ck_doorstep_pro_price_rejected CHECK (status <> 'rejected'
        OR (reason IS NOT NULL AND reviewed_at IS NOT NULL AND reviewed_by IS NOT NULL)),
    CONSTRAINT ck_doorstep_pro_price_period CHECK (effective_to IS NULL OR (effective_from IS NOT NULL AND effective_to >= effective_from)),
    CONSTRAINT ck_doorstep_pro_price_pending CHECK (status <> 'pending' OR (effective_from IS NULL AND effective_to IS NULL)),
    -- Live periods of one professional's price for one item never overlap.
    CONSTRAINT ex_doorstep_pro_option_price EXCLUDE USING gist (
        pro_id WITH =, option_id WITH =, tstzrange(effective_from, effective_to, '[)') WITH &&
    ) WHERE (option_id IS NOT NULL AND effective_from IS NOT NULL),
    CONSTRAINT ex_doorstep_pro_addon_price EXCLUDE USING gist (
        pro_id WITH =, addon_id WITH =, tstzrange(effective_from, effective_to, '[)') WITH &&
    ) WHERE (addon_id IS NOT NULL AND effective_from IS NOT NULL)
);
CREATE UNIQUE INDEX IF NOT EXISTS uq_doorstep_pro_price_pending
    ON doorstep.pro_service_prices (pro_id, COALESCE(option_id, addon_id)) WHERE status = 'pending';
CREATE UNIQUE INDEX IF NOT EXISTS uq_doorstep_pro_price_live
    ON doorstep.pro_service_prices (pro_id, COALESCE(option_id, addon_id)) WHERE status = 'approved' AND effective_to IS NULL;
CREATE INDEX IF NOT EXISTS idx_doorstep_pro_prices_pro ON doorstep.pro_service_prices (pro_id, service_id);
CREATE INDEX IF NOT EXISTS idx_doorstep_pro_prices_queue ON doorstep.pro_service_prices (submitted_at, id) WHERE status = 'pending';
CREATE INDEX IF NOT EXISTS idx_doorstep_pro_prices_option_live
    ON doorstep.pro_service_prices (option_id, price_paise) WHERE status = 'approved' AND option_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_doorstep_pro_prices_addon_live
    ON doorstep.pro_service_prices (addon_id, price_paise) WHERE status = 'approved' AND addon_id IS NOT NULL;

-- The item must belong to the row's service (whatever code path writes it).
CREATE OR REPLACE FUNCTION doorstep.pro_price_item_of_service() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF NEW.option_id IS NOT NULL AND NOT EXISTS (
        SELECT 1 FROM doorstep.service_options o WHERE o.id = NEW.option_id AND o.service_id = NEW.service_id) THEN
        RAISE EXCEPTION 'the option is not one of the service''s'
            USING ERRCODE = '23514', CONSTRAINT = 'ck_doorstep_pro_price_item_service';
    END IF;
    IF NEW.addon_id IS NOT NULL AND NOT EXISTS (
        SELECT 1 FROM doorstep.addons a JOIN doorstep.addon_groups g ON g.id = a.group_id
         WHERE a.id = NEW.addon_id AND g.service_id = NEW.service_id) THEN
        RAISE EXCEPTION 'the add-on is not one of the service''s'
            USING ERRCODE = '23514', CONSTRAINT = 'ck_doorstep_pro_price_item_service';
    END IF;
    RETURN NEW;
END $$;
DROP TRIGGER IF EXISTS trg_doorstep_pro_price_item_of_service ON doorstep.pro_service_prices;
CREATE TRIGGER trg_doorstep_pro_price_item_of_service
    BEFORE INSERT OR UPDATE ON doorstep.pro_service_prices
    FOR EACH ROW EXECUTE FUNCTION doorstep.pro_price_item_of_service();

-- Same-day ("as soon as possible") opt-in per professional and service.
CREATE TABLE IF NOT EXISTS doorstep.pro_service_settings (
    pro_id     UUID NOT NULL REFERENCES doorstep.professionals(id),
    service_id UUID NOT NULL REFERENCES doorstep.services(id),
    same_day   BOOLEAN NOT NULL DEFAULT FALSE,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (pro_id, service_id)
);

-- ---------------------------------------------------------------------
-- Quotes and booking lines carry the professional and the professional's
-- price row. price_id (A1) referenced city_prices; it now holds the
-- professional price id for new lines, so its foreign key goes and
-- pro_price_id carries the reference.
-- ---------------------------------------------------------------------
ALTER TABLE doorstep.quotes ADD COLUMN IF NOT EXISTS pro_id UUID REFERENCES doorstep.professionals(id);
ALTER TABLE doorstep.quote_items DROP CONSTRAINT IF EXISTS quote_items_price_id_fkey;
ALTER TABLE doorstep.quote_items ADD COLUMN IF NOT EXISTS pro_price_id UUID REFERENCES doorstep.pro_service_prices(id);
ALTER TABLE doorstep.quote_items ADD COLUMN IF NOT EXISTS unit TEXT NOT NULL DEFAULT 'per_job';
ALTER TABLE doorstep.booking_items DROP CONSTRAINT IF EXISTS booking_items_price_id_fkey;
ALTER TABLE doorstep.booking_items ADD COLUMN IF NOT EXISTS pro_price_id UUID REFERENCES doorstep.pro_service_prices(id);
ALTER TABLE doorstep.booking_items ADD COLUMN IF NOT EXISTS unit TEXT NOT NULL DEFAULT 'per_job';

-- Only an approved professional price, of the line's own professional, can
-- be quoted or booked, whatever code path writes the line.
CREATE OR REPLACE FUNCTION doorstep.line_price_must_be_approved() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE
    want_pro UUID;
BEGIN
    IF NEW.pro_price_id IS NULL THEN
        RETURN NEW;
    END IF;
    IF TG_TABLE_NAME = 'quote_items' THEN
        SELECT q.pro_id INTO want_pro FROM doorstep.quotes q WHERE q.id = NEW.quote_id;
    ELSE
        SELECT b.reserved_pro_id INTO want_pro FROM doorstep.bookings b WHERE b.id = NEW.booking_id;
    END IF;
    IF NOT EXISTS (SELECT 1 FROM doorstep.pro_service_prices pp
                    WHERE pp.id = NEW.pro_price_id AND pp.status = 'approved'
                      AND (want_pro IS NULL OR pp.pro_id = want_pro)) THEN
        RAISE EXCEPTION 'a line may only carry an approved price of its own professional'
            USING ERRCODE = '23514', CONSTRAINT = 'ck_doorstep_line_price_approved';
    END IF;
    RETURN NEW;
END $$;
DROP TRIGGER IF EXISTS trg_doorstep_quote_item_price ON doorstep.quote_items;
CREATE TRIGGER trg_doorstep_quote_item_price
    BEFORE INSERT OR UPDATE ON doorstep.quote_items
    FOR EACH ROW EXECUTE FUNCTION doorstep.line_price_must_be_approved();
DROP TRIGGER IF EXISTS trg_doorstep_booking_item_price ON doorstep.booking_items;
CREATE TRIGGER trg_doorstep_booking_item_price
    BEFORE INSERT OR UPDATE ON doorstep.booking_items
    FOR EACH ROW EXECUTE FUNCTION doorstep.line_price_must_be_approved();

-- ---------------------------------------------------------------------
-- Bookings: ASAP, and the pro_unavailable state.
--   asap                the customer asked for the professional now (an
--                       on-duty professional within range; the block runs
--                       from the booking time, the offer window is 3 min).
--   pro_unavailable     the chosen professional declined, let the offer
--                       lapse, gave the job back, was not on duty, did not
--                       turn up, or ops took the job off them. The booking
--                       keeps the payment; the customer picks another
--                       professional and a time (difference charged or
--                       refunded) or cancels for a full refund; with no
--                       choice by choice_deadline (30 min) it is cancelled
--                       with a full refund. Nobody is reassigned silently.
--   excluded_pro_ids    professionals the customer cannot pick again for
--                       this booking (they let it go, or ops excluded them).
-- ---------------------------------------------------------------------
ALTER TABLE doorstep.bookings DROP CONSTRAINT IF EXISTS bookings_status_check;
ALTER TABLE doorstep.bookings ADD CONSTRAINT bookings_status_check CHECK (status IN (
    'pending_payment','confirmed','assigned','en_route','arrived','in_progress','awaiting_extras_payment','completed',
    'cancelled','expired','customer_no_show','pro_no_show','pro_unavailable'));
ALTER TABLE doorstep.bookings ADD COLUMN IF NOT EXISTS asap BOOLEAN NOT NULL DEFAULT FALSE;
ALTER TABLE doorstep.bookings ADD COLUMN IF NOT EXISTS pro_unavailable_at TIMESTAMPTZ;
ALTER TABLE doorstep.bookings ADD COLUMN IF NOT EXISTS choice_deadline TIMESTAMPTZ;
ALTER TABLE doorstep.bookings ADD COLUMN IF NOT EXISTS unavailable_cause TEXT;
ALTER TABLE doorstep.bookings ADD COLUMN IF NOT EXISTS excluded_pro_ids UUID[] NOT NULL DEFAULT '{}';
ALTER TABLE doorstep.bookings ADD COLUMN IF NOT EXISTS pro_change_count INT NOT NULL DEFAULT 0;
ALTER TABLE doorstep.bookings DROP CONSTRAINT IF EXISTS ck_doorstep_bookings_pro_unavailable;
ALTER TABLE doorstep.bookings ADD CONSTRAINT ck_doorstep_bookings_pro_unavailable CHECK (
    (status <> 'pro_unavailable' OR (choice_deadline IS NOT NULL AND unavailable_cause IS NOT NULL AND reserved_pro_id IS NULL))
    AND (unavailable_cause IS NULL OR unavailable_cause IN
        ('declined','offer_expired','pro_cancel','not_on_duty','pro_no_show','ops_redispatch','no_professional')));
CREATE INDEX IF NOT EXISTS idx_doorstep_bookings_choice
    ON doorstep.bookings (choice_deadline) WHERE status = 'pro_unavailable';

-- ---------------------------------------------------------------------
-- Extras bills gain a kind: visit_extras (A5, approved during the visit)
-- or pro_change (the price difference when the customer picks a dearer
-- professional for a booking that lost its own). Both are paid through
-- payments-service reference doorstep_extras, key doorstep:extras:{bill}.
-- A pro_change bill nobody paid before its hold lapsed is cancelled.
-- ---------------------------------------------------------------------
ALTER TABLE doorstep.extras_bills ADD COLUMN IF NOT EXISTS kind TEXT NOT NULL DEFAULT 'visit_extras';
ALTER TABLE doorstep.extras_bills DROP CONSTRAINT IF EXISTS ck_doorstep_extras_bills_kind;
ALTER TABLE doorstep.extras_bills ADD CONSTRAINT ck_doorstep_extras_bills_kind CHECK (kind IN ('visit_extras','pro_change'));
ALTER TABLE doorstep.extras_bills DROP CONSTRAINT IF EXISTS extras_bills_status_check;
ALTER TABLE doorstep.extras_bills ADD CONSTRAINT extras_bills_status_check
    CHECK (status IN ('open','payment_pending','paid','outstanding','waived','refunded','cancelled'));

-- ---------------------------------------------------------------------
-- A customer's change of professional on a pro_unavailable booking. The
-- new professional's priced lines are kept on the row until it applies.
--   applied           the difference was 0 or negative (refunded at once),
--                     or the difference bill was paid (signed event)
--   pending_payment   a dearer professional is held (hold block) until the
--                     difference bill is paid
--   abandoned         the hold lapsed, the customer picked again, or the
--                     booking ended; a late capture of its bill is refunded
-- ---------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS doorstep.booking_pro_changes (
    id                   UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    booking_id           UUID NOT NULL REFERENCES doorstep.bookings(id),
    customer_user_id     UUID NOT NULL,
    idempotency_key      TEXT NOT NULL,
    from_pro_id          UUID REFERENCES doorstep.professionals(id),
    to_pro_id            UUID NOT NULL REFERENCES doorstep.professionals(id),
    asap                 BOOLEAN NOT NULL DEFAULT FALSE,
    slot_start           TIMESTAMPTZ NOT NULL,
    slot_end             TIMESTAMPTZ NOT NULL,
    block_start          TIMESTAMPTZ NOT NULL,
    block_end            TIMESTAMPTZ NOT NULL,
    duration_minutes     INT NOT NULL CHECK (duration_minutes > 0),
    previous_total_paise BIGINT NOT NULL CHECK (previous_total_paise >= 0),
    new_total_paise      BIGINT NOT NULL CHECK (new_total_paise >= 0),
    new_taxable_paise    BIGINT NOT NULL CHECK (new_taxable_paise >= 0),
    new_tax_paise        BIGINT NOT NULL CHECK (new_tax_paise >= 0),
    difference_paise     BIGINT NOT NULL,
    items                JSONB NOT NULL,
    status               TEXT NOT NULL CHECK (status IN ('pending_payment','applied','abandoned')),
    extras_bill_id       UUID REFERENCES doorstep.extras_bills(id),
    hold_expires_at      TIMESTAMPTZ,
    applied_at           TIMESTAMPTZ,
    abandoned_at         TIMESTAMPTZ,
    created_at           TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at           TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (booking_id, idempotency_key),
    CONSTRAINT ck_doorstep_pro_change_difference CHECK (difference_paise = new_total_paise - previous_total_paise),
    CONSTRAINT ck_doorstep_pro_change_total CHECK (new_total_paise = new_taxable_paise + new_tax_paise),
    CONSTRAINT ck_doorstep_pro_change_slot CHECK (slot_end > slot_start AND block_end > block_start),
    CONSTRAINT ck_doorstep_pro_change_payment CHECK (
        (difference_paise > 0) = (extras_bill_id IS NOT NULL)
        AND (status <> 'pending_payment' OR (difference_paise > 0 AND hold_expires_at IS NOT NULL)))
);
CREATE UNIQUE INDEX IF NOT EXISTS uq_doorstep_pro_change_pending
    ON doorstep.booking_pro_changes (booking_id) WHERE status = 'pending_payment';
CREATE INDEX IF NOT EXISTS idx_doorstep_pro_changes_booking ON doorstep.booking_pro_changes (booking_id, created_at);
CREATE INDEX IF NOT EXISTS idx_doorstep_pro_changes_hold
    ON doorstep.booking_pro_changes (hold_expires_at) WHERE status = 'pending_payment';
CREATE UNIQUE INDEX IF NOT EXISTS uq_doorstep_pro_change_bill
    ON doorstep.booking_pro_changes (extras_bill_id) WHERE extras_bill_id IS NOT NULL;

-- Payment events on extras bills (doorstep_extras) are applied once too:
-- the inbox already admits the reference type (001).
CREATE INDEX IF NOT EXISTS idx_doorstep_payments_bill ON doorstep.payments (extras_bill_id) WHERE extras_bill_id IS NOT NULL;

-- ---------------------------------------------------------------------
-- Nothing a professional submits is approved by itself (founder, 4 Oct
-- 2026: "Nothing immediately approved, admin will verify and approve it").
-- A skill becoming verified, a document approved, a selfie face match
-- passed, a professional approved, a price approved or a background check
-- clear needs an audited admin review: an admin_audit_log row written in
-- the SAME transaction (created_at = the transaction's now()) by the
-- reviewing admin where the row names one. Checked at commit (deferred), so
-- the audit row may come after the change, as adminWrite writes it. The
-- face match, a vendor's background-check verdict and DigiLocker are
-- advisory inputs to that review; DigiLocker's Aadhaar fetch stays an
-- identity check (it is not an approval of anything).
-- ---------------------------------------------------------------------
ALTER TABLE doorstep.background_checks ADD COLUMN IF NOT EXISTS provider_verdict TEXT;

CREATE OR REPLACE FUNCTION doorstep.require_admin_review() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE
    granted BOOLEAN := FALSE;
    named   BOOLEAN := FALSE; -- the row names its reviewer
    actor   UUID;
BEGIN
    IF TG_TABLE_NAME = 'pro_skills' THEN
        IF NEW.status = 'verified' THEN
            IF TG_OP = 'INSERT' THEN granted := TRUE; ELSE granted := OLD.status IS DISTINCT FROM 'verified'; END IF;
        END IF;
        named := TRUE; actor := NEW.verified_by;
    ELSIF TG_TABLE_NAME = 'pro_documents' THEN
        IF NEW.status = 'approved' THEN
            IF TG_OP = 'INSERT' THEN granted := TRUE; ELSE granted := OLD.status IS DISTINCT FROM 'approved'; END IF;
        END IF;
        named := TRUE; actor := NEW.reviewed_by;
    ELSIF TG_TABLE_NAME = 'pro_kyc_checks' THEN
        IF NEW.kind = 'selfie_face_match' AND NEW.status = 'passed' THEN
            IF TG_OP = 'INSERT' THEN granted := TRUE; ELSE granted := OLD.status IS DISTINCT FROM 'passed'; END IF;
        END IF;
    ELSIF TG_TABLE_NAME = 'professionals' THEN
        IF NEW.status = 'approved' THEN
            IF TG_OP = 'INSERT' THEN granted := TRUE; ELSE granted := OLD.status IS DISTINCT FROM 'approved'; END IF;
        END IF;
    ELSIF TG_TABLE_NAME = 'pro_service_prices' THEN
        IF NEW.status = 'approved' THEN
            IF TG_OP = 'INSERT' THEN granted := TRUE; ELSE granted := OLD.status IS DISTINCT FROM 'approved'; END IF;
        END IF;
        named := TRUE; actor := NEW.reviewed_by;
    ELSIF TG_TABLE_NAME = 'background_checks' THEN
        IF NEW.status = 'clear' THEN
            IF TG_OP = 'INSERT' THEN granted := TRUE; ELSE granted := OLD.status IS DISTINCT FROM 'clear'; END IF;
        END IF;
        named := TRUE; actor := NEW.reviewed_by;
    END IF;
    IF NOT granted THEN
        RETURN NULL;
    END IF;
    IF named AND actor IS NULL THEN
        RAISE EXCEPTION '%: an approval must name the reviewing admin', TG_TABLE_NAME
            USING ERRCODE = '23514', CONSTRAINT = 'ck_doorstep_admin_review';
    END IF;
    IF NOT EXISTS (SELECT 1 FROM doorstep.admin_audit_log a
                    WHERE a.created_at = now() AND (actor IS NULL OR a.actor_user_id = actor)) THEN
        RAISE EXCEPTION '%: only an audited admin review may approve or verify', TG_TABLE_NAME
            USING ERRCODE = '23514', CONSTRAINT = 'ck_doorstep_admin_review';
    END IF;
    RETURN NULL;
END $$;

DO $$
DECLARE t TEXT;
BEGIN
    FOREACH t IN ARRAY ARRAY['pro_skills','pro_documents','pro_kyc_checks','professionals','pro_service_prices','background_checks'] LOOP
        EXECUTE format('DROP TRIGGER IF EXISTS trg_doorstep_admin_review ON doorstep.%I', t);
        EXECUTE format('CREATE CONSTRAINT TRIGGER trg_doorstep_admin_review AFTER INSERT OR UPDATE ON doorstep.%I
                        DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION doorstep.require_admin_review()', t);
    END LOOP;
END $$;
