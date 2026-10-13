ALTER TABLE doorstep.pro_documents DROP CONSTRAINT IF EXISTS pro_documents_kind_check;
ALTER TABLE doorstep.pro_documents ADD CONSTRAINT pro_documents_kind_check CHECK
 (kind IN ('police_certificate','trade_certificate','selfie','aadhaar','pan','other','passport','voter_id','driving_license','experience_certificate','bank_proof'));

ALTER TABLE doorstep.pro_payout_accounts ADD COLUMN IF NOT EXISTS bank_name TEXT NOT NULL DEFAULT '';
ALTER TABLE doorstep.pro_payout_accounts ADD COLUMN IF NOT EXISTS branch_name TEXT NOT NULL DEFAULT '';
ALTER TABLE doorstep.pro_payout_accounts ADD COLUMN IF NOT EXISTS proof_kind TEXT NOT NULL DEFAULT '';
ALTER TABLE doorstep.pro_payout_accounts ADD COLUMN IF NOT EXISTS proof_document_id UUID REFERENCES doorstep.pro_documents(id);
CREATE UNIQUE INDEX IF NOT EXISTS uq_doorstep_bank_proof ON doorstep.pro_payout_accounts(proof_document_id) WHERE proof_document_id IS NOT NULL;
ALTER TABLE doorstep.pro_payout_accounts DROP CONSTRAINT IF EXISTS ck_doorstep_bank_proof_kind;
ALTER TABLE doorstep.pro_payout_accounts ADD CONSTRAINT ck_doorstep_bank_proof_kind CHECK
 (proof_kind IN ('','cancelled_cheque','bank_statement','passbook'));

-- Existing accounts are not retrospectively declared verified. Readiness also
-- checks the linked, approved proof; a saved account alone never enables work.
CREATE OR REPLACE FUNCTION doorstep.require_reviewed_bank_proof() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF NEW.proof_document_id IS NOT NULL AND NOT EXISTS (
  SELECT 1 FROM doorstep.pro_documents d WHERE d.id=NEW.proof_document_id AND d.pro_id=NEW.pro_id AND d.kind='bank_proof'
 ) THEN
  RAISE EXCEPTION 'bank proof must belong to this professional' USING ERRCODE='23514';
 END IF;
 IF NEW.active AND NEW.status='verified' AND NOT EXISTS (
  SELECT 1 FROM doorstep.pro_documents d WHERE d.id=NEW.proof_document_id AND d.pro_id=NEW.pro_id
   AND d.kind='bank_proof' AND d.status='approved' AND d.reviewed_by IS NOT NULL
 ) THEN
  RAISE EXCEPTION 'bank account requires reviewed proof' USING ERRCODE='23514';
 END IF;
 RETURN NEW;
END $$;
DROP TRIGGER IF EXISTS trg_doorstep_bank_proof ON doorstep.pro_payout_accounts;
CREATE TRIGGER trg_doorstep_bank_proof BEFORE INSERT OR UPDATE ON doorstep.pro_payout_accounts
 FOR EACH ROW EXECUTE FUNCTION doorstep.require_reviewed_bank_proof();
