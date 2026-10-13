-- Applications can be received before customer service launches in a town.
ALTER TABLE doorstep.professionals ALTER COLUMN city_code DROP NOT NULL;
ALTER TABLE doorstep.pro_documents DROP CONSTRAINT IF EXISTS pro_documents_kind_check;
ALTER TABLE doorstep.pro_documents ADD CONSTRAINT pro_documents_kind_check CHECK
 (kind IN ('police_certificate','trade_certificate','selfie','aadhaar','pan','other','passport','voter_id','driving_license','experience_certificate'));

CREATE TABLE IF NOT EXISTS doorstep.pro_applications (
 pro_id UUID PRIMARY KEY REFERENCES doorstep.professionals(id),
 first_name TEXT NOT NULL,
 last_name TEXT NOT NULL,
 declared_gender TEXT NOT NULL CHECK (declared_gender IN ('male','female','others')),
 state_code TEXT NOT NULL,
 state_name TEXT NOT NULL,
 town TEXT NOT NULL,
 primary_phone_last4 TEXT NOT NULL,
 alternate_phone_sealed BYTEA,
 alternate_phone_last4 TEXT,
 alternate_phone_verified_at TIMESTAMPTZ,
 identity_document_id UUID NOT NULL REFERENCES doorstep.pro_documents(id),
 review_consent_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
 submitted_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

ALTER TABLE doorstep.pro_applications ADD COLUMN IF NOT EXISTS review_consent_at TIMESTAMPTZ NOT NULL DEFAULT NOW();

-- Documents are immutable while a submitted application is in review. A later
-- replacement must go through a new review, never inherit identity approval.
CREATE OR REPLACE FUNCTION doorstep.application_identity_revoked() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF OLD.status='approved' AND NEW.status <> 'approved' AND EXISTS
  (SELECT 1 FROM doorstep.pro_applications a WHERE a.identity_document_id=NEW.id) THEN
  UPDATE doorstep.pro_kyc_checks SET status='expired' WHERE pro_id=NEW.pro_id AND kind='manual_identity' AND status='passed';
 END IF;
 RETURN NEW;
END $$;
DROP TRIGGER IF EXISTS trg_application_identity_revoked ON doorstep.pro_documents;
CREATE TRIGGER trg_application_identity_revoked AFTER UPDATE OF status ON doorstep.pro_documents
 FOR EACH ROW EXECUTE FUNCTION doorstep.application_identity_revoked();
