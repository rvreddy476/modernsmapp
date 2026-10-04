-- doorstep-service 002: professional onboarding (lane A2).
--
-- Adds what 001 left to A2: per-skill certificate rules, trade certificates,
-- the DigiLocker PKCE state, the day-off calendar link and a database guard
-- that no background check from an uploaded document is clear unless its
-- police certificate was approved. Re-runnable: IF NOT EXISTS / DROP ... IF
-- EXISTS everywhere (tested by running the file twice).

-- ---------------------------------------------------------------------
-- Skills: does verification need a trade certificate?
--
-- TRUE (the default, fail closed): the skill is verified only when an admin
-- approves an uploaded trade certificate for it (electrician, AC, appliance,
-- RO, pest control in the Hyderabad seed). FALSE: no certificate exists for
-- the trade in practice (cleaning, painting, carpentry, plumbing, salon), so
-- declaring the skill verifies it; the admin can still revoke it, the
-- professional is still approved by a human, and the category gender rule
-- and background check still apply.
-- ---------------------------------------------------------------------
ALTER TABLE doorstep.skills ADD COLUMN IF NOT EXISTS requires_certificate BOOLEAN NOT NULL DEFAULT TRUE;

-- ---------------------------------------------------------------------
-- Trade certificates are professional documents bound to one skill.
-- Selfies are documents too, so the admin console can view one (bytes,
-- audited) and decide a selfie the face match left pending: approved when
-- the match passed, pending when it was below the threshold or could not
-- run, rejected when it failed or a newer selfie superseded it.
-- ---------------------------------------------------------------------
ALTER TABLE doorstep.pro_documents ADD COLUMN IF NOT EXISTS skill_code TEXT REFERENCES doorstep.skills(code);
ALTER TABLE doorstep.pro_documents DROP CONSTRAINT IF EXISTS pro_documents_kind_check;
ALTER TABLE doorstep.pro_documents ADD CONSTRAINT pro_documents_kind_check
    CHECK (kind IN ('police_certificate','trade_certificate','selfie','aadhaar','pan','other'));
CREATE UNIQUE INDEX IF NOT EXISTS uq_doorstep_pro_docs_pending_selfie
    ON doorstep.pro_documents (pro_id) WHERE kind = 'selfie' AND status = 'pending';
ALTER TABLE doorstep.pro_documents DROP CONSTRAINT IF EXISTS ck_doorstep_pro_documents_skill;
ALTER TABLE doorstep.pro_documents ADD CONSTRAINT ck_doorstep_pro_documents_skill
    CHECK ((kind = 'trade_certificate') = (skill_code IS NOT NULL));
-- One certificate under review at a time (police per professional, trade per
-- professional and skill).
CREATE UNIQUE INDEX IF NOT EXISTS uq_doorstep_pro_docs_pending_police
    ON doorstep.pro_documents (pro_id) WHERE kind = 'police_certificate' AND status = 'pending';
CREATE UNIQUE INDEX IF NOT EXISTS uq_doorstep_pro_docs_pending_trade
    ON doorstep.pro_documents (pro_id, skill_code) WHERE kind = 'trade_certificate' AND status = 'pending';

-- ---------------------------------------------------------------------
-- DigiLocker OAuth with PKCE (copied from food-service). Only a SHA-256 of
-- the state is stored and the code_verifier is sealed (shared/pii scope
-- doorstep.digilocker_verifier). The callback consumes a state exactly once
-- with a conditional UPDATE bound to the professional's user.
-- ---------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS doorstep.digilocker_auth_states (
    state_hash           TEXT PRIMARY KEY CHECK (state_hash ~ '^[0-9a-f]{64}$'),
    pro_id               UUID NOT NULL REFERENCES doorstep.professionals(id) ON DELETE CASCADE,
    code_verifier_sealed BYTEA NOT NULL,
    expires_at           TIMESTAMPTZ NOT NULL,
    consumed_at          TIMESTAMPTZ,
    created_at           TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_doorstep_digilocker_states_pro ON doorstep.digilocker_auth_states (pro_id, expires_at);

-- ---------------------------------------------------------------------
-- A day off is also a day_off block on the calendar, so the exclusion
-- constraint refuses a day off over an accepted job and slot search never
-- offers the day.
-- ---------------------------------------------------------------------
ALTER TABLE doorstep.pro_days_off ADD COLUMN IF NOT EXISTS calendar_block_id UUID REFERENCES doorstep.pro_calendar_blocks(id);

-- ---------------------------------------------------------------------
-- No background check from an uploaded document is clear unless its own
-- police certificate (same professional) is approved. This holds whatever
-- code path writes the row: the admin review is the only way to approve the
-- certificate, so no admin path can clear a check without a document.
-- ---------------------------------------------------------------------
CREATE OR REPLACE FUNCTION doorstep.background_check_clear_needs_document() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF NEW.status = 'clear' AND NEW.source = 'uploaded_document' AND NOT EXISTS (
        SELECT 1 FROM doorstep.pro_documents d
         WHERE d.id = NEW.document_id
           AND d.pro_id = NEW.pro_id
           AND d.kind = 'police_certificate'
           AND d.status = 'approved') THEN
        RAISE EXCEPTION 'a background check from an uploaded document is clear only when its police certificate is approved'
            USING ERRCODE = '23514', CONSTRAINT = 'ck_doorstep_bg_clear_needs_document';
    END IF;
    RETURN NEW;
END $$;
DROP TRIGGER IF EXISTS trg_doorstep_bg_clear_needs_document ON doorstep.background_checks;
CREATE TRIGGER trg_doorstep_bg_clear_needs_document
    BEFORE INSERT OR UPDATE ON doorstep.background_checks
    FOR EACH ROW EXECUTE FUNCTION doorstep.background_check_clear_needs_document();

-- Admin queues.
CREATE INDEX IF NOT EXISTS idx_doorstep_pros_created ON doorstep.professionals (created_at DESC, id DESC);
