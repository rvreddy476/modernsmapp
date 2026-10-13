-- Optional private introduction evidence; never an approval requirement.
ALTER TABLE doorstep.pro_documents DROP CONSTRAINT IF EXISTS pro_documents_kind_check;
ALTER TABLE doorstep.pro_documents ADD CONSTRAINT pro_documents_kind_check CHECK
 (kind IN ('police_certificate','trade_certificate','selfie','aadhaar','pan','other','passport','voter_id','driving_license','experience_certificate','bank_proof','introduction_video'));
