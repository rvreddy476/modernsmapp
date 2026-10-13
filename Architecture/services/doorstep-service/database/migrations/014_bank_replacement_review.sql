-- Keep the approved payout account separate from a proposed replacement.
LOCK TABLE doorstep.pro_payout_accounts IN SHARE ROW EXCLUSIVE MODE;
DO $$ BEGIN
 IF NOT EXISTS (SELECT 1 FROM information_schema.columns WHERE table_schema='doorstep' AND table_name='pro_payout_accounts' AND column_name='superseded') THEN
  ALTER TABLE doorstep.pro_payout_accounts ADD COLUMN superseded BOOLEAN NOT NULL DEFAULT FALSE;
  UPDATE doorstep.pro_payout_accounts SET superseded=TRUE WHERE status='pending' AND NOT active;
 END IF;
END $$;

-- Repair only the old insertion bug: an unreviewed active submission directly
-- replaced an admin-approved account. Never approve the replacement, restore
-- a revoked proof, or change a partner's approval/duty status.
CREATE TABLE IF NOT EXISTS doorstep.bank_replacement_repairs (
 replacement_id UUID PRIMARY KEY REFERENCES doorstep.pro_payout_accounts(id),
 restored_account_id UUID NOT NULL REFERENCES doorstep.pro_payout_accounts(id),
 repaired_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
DO $$
DECLARE r RECORD;
BEGIN
 FOR r IN
  SELECT n.id replacement_id, old.id restored_id
  FROM doorstep.pro_payout_accounts n
  JOIN doorstep.professionals p ON p.id=n.pro_id AND p.status='approved'
  JOIN doorstep.pro_documents nd ON nd.id=n.proof_document_id AND nd.status='pending' AND nd.reviewed_by IS NULL
  JOIN LATERAL (
   SELECT a.* FROM doorstep.pro_payout_accounts a WHERE a.pro_id=n.pro_id AND a.created_at<n.created_at
   ORDER BY a.created_at DESC,a.id DESC LIMIT 1
  ) old ON NOT old.active AND old.status='verified'
  JOIN doorstep.pro_documents d ON d.id=old.proof_document_id AND d.pro_id=n.pro_id
    AND d.kind='bank_proof' AND d.status='approved' AND d.reviewed_by IS NOT NULL
  WHERE n.active AND n.status='pending'
 LOOP
  UPDATE doorstep.pro_payout_accounts SET active=FALSE WHERE id=r.replacement_id;
  UPDATE doorstep.pro_payout_accounts SET active=TRUE WHERE id=r.restored_id;
  INSERT INTO doorstep.bank_replacement_repairs(replacement_id,restored_account_id)
   VALUES(r.replacement_id,r.restored_id) ON CONFLICT DO NOTHING;
 END LOOP;
END $$;
CREATE UNIQUE INDEX IF NOT EXISTS uq_doorstep_bank_pending
 ON doorstep.pro_payout_accounts(pro_id) WHERE status='pending' AND NOT superseded;
