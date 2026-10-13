package store

import (
	"context"
	"github.com/atpost/doorstep-service/internal/model"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"strconv"
)

type BankSubmission struct {
	Holder, Last4, IFSC, BankName, BranchName, ProofKind string
	ProofMediaID                                         uuid.UUID
	Sealed                                               []byte
	KeyVersion                                           uint32
}

// The owner is resolved by the service, never accepted from a request body.
// Do not select or decrypt account_number_sealed for this history view.
func (s *Store) payoutAccountHistory(ctx context.Context, proID uuid.UUID) ([]model.PayoutAccountRecord, error) {
	rows, err := s.db.Query(ctx, `SELECT a.id,a.account_holder,a.account_last4,a.ifsc,a.bank_name,a.status,a.active,a.created_at,d.reviewed_at,d.reason,a.superseded,a.proof_document_id,a.proof_kind
	 FROM doorstep.pro_payout_accounts a LEFT JOIN doorstep.pro_documents d ON d.id=a.proof_document_id
	 WHERE a.pro_id=$1 ORDER BY a.created_at DESC,a.id DESC LIMIT 50`, proID)
	if err != nil {
		return nil, err
	}
	return collect(rows, func(r pgx.Rows) (model.PayoutAccountRecord, error) {
		var v model.PayoutAccountRecord
		err := r.Scan(&v.ID, &v.AccountHolder, &v.AccountLast4, &v.IFSC, &v.BankName, &v.Status, &v.Active, &v.SubmittedAt, &v.ReviewedAt, &v.Reason, &v.Superseded, &v.ProofDocumentID, &v.ProofKind)
		return v, err
	})
}

// Callers lock the professional first; the proof and account are one write.
func insertBank(ctx context.Context, tx pgx.Tx, proID uuid.UUID, in BankSubmission) (*model.PayoutAccount, error) {
	if len(in.Sealed) == 0 || in.ProofMediaID == uuid.Nil {
		return nil, ErrInvalid
	}
	var approved bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM doorstep.pro_payout_accounts a JOIN doorstep.pro_documents d ON d.id=a.proof_document_id
	 WHERE a.pro_id=$1 AND a.active AND a.status='verified' AND d.status='approved' AND d.reviewed_by IS NOT NULL)`, proID).Scan(&approved); err != nil {
		return nil, err
	}
	if _, err := tx.Exec(ctx, `UPDATE doorstep.pro_payout_accounts SET active=FALSE,superseded=TRUE WHERE pro_id=$1 AND status='pending' AND NOT superseded`, proID); err != nil {
		return nil, err
	}
	if _, err := tx.Exec(ctx, `UPDATE doorstep.pro_payout_accounts SET active=FALSE WHERE pro_id=$1 AND active AND NOT $2`, proID, approved); err != nil {
		return nil, err
	}
	docID := uuid.New()
	if _, err := tx.Exec(ctx, `INSERT INTO doorstep.pro_documents(id,pro_id,kind,media_id,status) VALUES($1,$2,'bank_proof',$3,'pending')`, docID, proID, in.ProofMediaID.String()); err != nil {
		return nil, err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO doorstep.pro_payout_accounts(pro_id,account_holder,account_number_sealed,account_last4,ifsc,key_version,bank_name,branch_name,proof_kind,proof_document_id,active)
 VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)`, proID, in.Holder, in.Sealed, in.Last4, in.IFSC, "v"+strconv.FormatUint(uint64(in.KeyVersion), 10), in.BankName, in.BranchName, in.ProofKind, docID, !approved); err != nil {
		return nil, err
	}
	return &model.PayoutAccount{AccountHolder: in.Holder, AccountLast4: in.Last4, IFSC: in.IFSC, Status: "pending", BankName: in.BankName, BranchName: in.BranchName, ProofKind: in.ProofKind, ProofDocumentID: &docID}, nil
}

func (s *Store) SetPayoutAccount(ctx context.Context, proID uuid.UUID, in BankSubmission) (*model.PayoutAccount, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	var status string
	if err = tx.QueryRow(ctx, `SELECT status FROM doorstep.professionals WHERE id=$1 FOR UPDATE`, proID).Scan(&status); err != nil {
		return nil, mapErr(err)
	}
	if status == "blocked" || status == "rejected" {
		return nil, ErrInvalid
	}
	out, err := insertBank(ctx, tx, proID, in)
	if err != nil {
		return nil, err
	}
	return out, mapErr(tx.Commit(ctx))
}
