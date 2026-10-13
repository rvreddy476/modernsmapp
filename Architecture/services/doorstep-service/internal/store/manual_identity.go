package store

import (
	"context"
	"fmt"
	"github.com/atpost/doorstep-service/internal/model"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// ReviewManualIdentity binds a human decision to the current PAN and approved
// selfie while holding the professional lock. Gender never comes from the pro.
func (s *Store) ReviewManualIdentity(ctx context.Context, a Actor, id uuid.UUID, gender, reason string) (*model.Professional, error) {
	if a.UserID == uuid.Nil || a.Permission != "doorstep:pros.approve" {
		return nil, fmt.Errorf("%w: admin identity review permission required", ErrInvalid)
	}
	var out model.Professional
	err := s.adminWrite(ctx, a, "professional.identity_review", "professional",
		map[string]any{"gender": gender, "reason": reason, "method": "manual_documents"}, func(tx pgx.Tx) (string, error) {
			var status string
			var hasPAN bool
			if err := tx.QueryRow(ctx, `SELECT status, pan_sealed IS NOT NULL FROM doorstep.professionals WHERE id=$1 FOR UPDATE`, id).Scan(&status, &hasPAN); err != nil {
				return "", err
			}
			if status == "blocked" || status == "rejected" {
				return "", ErrInvalid
			}
			var selfie bool
			if err := tx.QueryRow(ctx, `SELECT COALESCE((SELECT status='approved' FROM doorstep.pro_documents WHERE pro_id=$1 AND kind='selfie' ORDER BY created_at DESC,id DESC LIMIT 1),FALSE)`, id).Scan(&selfie); err != nil {
				return "", err
			}
			var application, approvedDocument bool
			if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM doorstep.pro_applications WHERE pro_id=$1), EXISTS(SELECT 1 FROM doorstep.pro_applications a JOIN doorstep.pro_documents d ON d.id=a.identity_document_id WHERE a.pro_id=$1 AND d.status='approved')`, id).Scan(&application, &approvedDocument); err != nil {
				return "", err
			}
			if (application && !approvedDocument) || (!application && (!hasPAN || !selfie)) {
				return "", fmt.Errorf("%w: save PAN and approve the current selfie before identity review", ErrInvalid)
			}
			if _, err := tx.Exec(ctx, `UPDATE doorstep.pro_kyc_checks SET status='expired' WHERE pro_id=$1 AND kind='manual_identity' AND status='passed'`, id); err != nil {
				return "", err
			}
			if _, err := tx.Exec(ctx, `INSERT INTO doorstep.pro_kyc_checks(pro_id,kind,status,provider,verified_at) VALUES($1,'manual_identity','passed','admin_review',NOW())`, id); err != nil {
				return "", err
			}
			var value, source any
			if gender != "unknown" {
				value, source = gender, "admin_review"
			}
			var err error
			out, err = scanPro(tx.QueryRow(ctx, `UPDATE doorstep.professionals SET gender=$2,gender_source=$3,updated_at=NOW() WHERE id=$1 RETURNING `+proCols, id, value, source))
			return id.String(), err
		})
	if err != nil {
		return nil, err
	}
	return &out, nil
}
