-- Offline partner onboarding; no UIDAI assertion is manufactured.
ALTER TABLE doorstep.professionals DROP CONSTRAINT IF EXISTS professionals_gender_source_check;
ALTER TABLE doorstep.professionals ADD CONSTRAINT professionals_gender_source_check
    CHECK (gender_source IN ('digilocker','admin_review'));
ALTER TABLE doorstep.pro_kyc_checks DROP CONSTRAINT IF EXISTS pro_kyc_checks_kind_check;
ALTER TABLE doorstep.pro_kyc_checks ADD CONSTRAINT pro_kyc_checks_kind_check
    CHECK (kind IN ('digilocker_aadhaar','selfie_face_match','pan','bank','manual_identity'));

CREATE OR REPLACE FUNCTION doorstep.require_manual_identity_review() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF NEW.kind = 'manual_identity' AND NEW.status = 'passed' AND
       (TG_OP = 'INSERT' OR OLD.status IS DISTINCT FROM 'passed') THEN
        IF NOT EXISTS (SELECT 1 FROM doorstep.admin_audit_log a
            WHERE a.created_at = now() AND a.permission = 'doorstep:pros.approve'
              AND a.action = 'professional.identity_review' AND a.entity_id = NEW.pro_id::text) THEN
            RAISE EXCEPTION 'manual identity requires an audited admin review'
                USING ERRCODE = '23514', CONSTRAINT = 'ck_doorstep_manual_identity_review';
        END IF;
    END IF;
    RETURN NULL;
END $$;
DROP TRIGGER IF EXISTS trg_doorstep_manual_identity_review ON doorstep.pro_kyc_checks;
CREATE CONSTRAINT TRIGGER trg_doorstep_manual_identity_review AFTER INSERT OR UPDATE ON doorstep.pro_kyc_checks
    DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION doorstep.require_manual_identity_review();

CREATE OR REPLACE FUNCTION doorstep.pan_change_requires_identity_review() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF NEW.pan_sealed IS DISTINCT FROM OLD.pan_sealed THEN
        UPDATE doorstep.pro_kyc_checks SET status='expired'
            WHERE pro_id=NEW.id AND kind='manual_identity' AND status='passed';
        IF NEW.gender_source='admin_review' THEN
            NEW.gender:=NULL; NEW.gender_source:=NULL;
        END IF;
    END IF;
    RETURN NEW;
END $$;
DROP TRIGGER IF EXISTS trg_doorstep_pan_identity_review ON doorstep.professionals;
CREATE TRIGGER trg_doorstep_pan_identity_review BEFORE UPDATE OF pan_sealed ON doorstep.professionals
    FOR EACH ROW EXECUTE FUNCTION doorstep.pan_change_requires_identity_review();
