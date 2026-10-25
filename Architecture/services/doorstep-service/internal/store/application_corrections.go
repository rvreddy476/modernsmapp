package store

import (
	"context"

	"github.com/atpost/doorstep-service/internal/model"
	"github.com/google/uuid"
)

func (s *Store) ReplaceRejectedApplicationIdentity(ctx context.Context, proID uuid.UUID, kind string, mediaID uuid.UUID) (*model.ProDocument, error) {
	if mediaID == uuid.Nil || (kind != "passport" && kind != "voter_id" && kind != "driving_license") {
		return nil, ErrInvalid
	}
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }() // no-op after Commit
	var status string
	if err := tx.QueryRow(ctx, `SELECT status FROM doorstep.professionals WHERE id=$1 FOR UPDATE`, proID).Scan(&status); err != nil {
		return nil, mapErr(err)
	}
	if status != "pending_verification" {
		return nil, &TransitionError{Status: status}
	}
	var currentID uuid.UUID
	if err := tx.QueryRow(ctx, `SELECT identity_document_id FROM doorstep.pro_applications WHERE pro_id=$1 FOR UPDATE`, proID).Scan(&currentID); err != nil {
		return nil, mapErr(err)
	}
	current, err := scanDoc(tx.QueryRow(ctx, `SELECT `+docCols+` FROM doorstep.pro_documents WHERE id=$1 AND pro_id=$2 FOR UPDATE`, currentID, proID))
	if err != nil {
		return nil, err
	}
	// A retry after an unknown response returns the already-saved replacement.
	// A new upload cannot overwrite a pending or approved proof.
	if current.Status == "pending" && current.Kind == kind && current.MediaID == mediaID.String() {
		return &current, nil
	}
	if current.Status != "rejected" {
		return nil, ErrConflict
	}
	doc, err := scanDoc(tx.QueryRow(ctx, `INSERT INTO doorstep.pro_documents(pro_id,kind,media_id,status) VALUES($1,$2,$3,'pending') RETURNING `+docCols, proID, kind, mediaID.String()))
	if err != nil {
		return nil, err
	}
	if _, err := tx.Exec(ctx, `UPDATE doorstep.pro_applications SET identity_document_id=$2 WHERE pro_id=$1`, proID, doc.ID); err != nil {
		return nil, err
	}
	if _, err := tx.Exec(ctx, `UPDATE doorstep.pro_kyc_checks SET status='expired' WHERE pro_id=$1 AND kind='manual_identity' AND status='passed'`, proID); err != nil {
		return nil, err
	}
	if _, err := tx.Exec(ctx, `UPDATE doorstep.professionals SET gender=NULL,gender_source=NULL,updated_at=NOW() WHERE id=$1 AND gender_source='admin_review'`, proID); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return &doc, nil
}
