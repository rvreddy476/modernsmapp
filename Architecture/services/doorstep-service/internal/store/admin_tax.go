package store

import (
	"context"
	"github.com/atpost/doorstep-service/internal/model"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"time"
)

func (s *Store) TaxRegistration(ctx context.Context, id uuid.UUID) (*model.TaxRegistration, error) {
	v := &model.TaxRegistration{ProID: id}
	err := s.db.QueryRow(ctx, `SELECT gstin,updated_at FROM doorstep.professionals WHERE id=$1`, id).Scan(&v.GSTIN, &v.UpdatedAt)
	return v, mapErr(err)
}
func (s *Store) SetTaxRegistration(ctx context.Context, a Actor, id uuid.UUID, gstin *string, reason string, at time.Time) (*model.TaxRegistration, error) {
	out := &model.TaxRegistration{ProID: id}
	err := s.adminWrite(ctx, a, "professional.tax_registration", "professional", map[string]any{"registered": gstin != nil, "reason": reason}, func(tx pgx.Tx) (string, error) {
		if err := requireRow(ctx, tx, `SELECT 1 FROM doorstep.professionals WHERE id=$1 FOR UPDATE`, id); err != nil {
			return "", err
		}
		var active bool
		err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM doorstep.bookings b WHERE (b.reserved_pro_id=$1 OR EXISTS(SELECT 1 FROM doorstep.booking_assignments a WHERE a.booking_id=b.id AND a.pro_id=$1 AND a.status='accepted')) AND b.status IN ('pending_payment','confirmed','assigned','en_route','arrived','in_progress','awaiting_extras_payment'))`, id).Scan(&active)
		if err != nil {
			return "", err
		}
		if active {
			return "", ErrConflict
		}
		// The record may not change the treatment of an active, already quoted visit.
		err = tx.QueryRow(ctx, `UPDATE doorstep.professionals SET gstin=$2,updated_at=$3 WHERE id=$1 RETURNING gstin,updated_at`, id, gstin, at).Scan(&out.GSTIN, &out.UpdatedAt)
		return id.String(), err
	})
	return out, err
}
