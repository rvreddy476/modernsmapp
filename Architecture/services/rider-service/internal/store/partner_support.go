package store

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/atpost/shared/outbox"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func (s *Store) ListPartnerRides(ctx context.Context, user uuid.UUID, limit int) ([]Ride, error) {
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	rows, err := s.db.Query(ctx, `SELECT `+rideSelectColumns+` FROM rider_rides WHERE partner_id IN (SELECT id FROM rider_partners WHERE user_id=$1) ORDER BY created_at DESC,id DESC LIMIT $2`, user, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []Ride{}
	for rows.Next() {
		r, err := scanRide(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, *r)
	}
	return result, rows.Err()
}

func (s *Store) CreatePartnerSupport(ctx context.Context, user, ride uuid.UUID, category, description string) (*Complaint, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	var customer, partner uuid.UUID
	err = tx.QueryRow(ctx, `SELECT r.customer_user_id,p.id FROM rider_rides r JOIN rider_partners p ON p.id=r.partner_id WHERE r.id=$1 AND p.user_id=$2 FOR SHARE OF r,p`, ride, user).Scan(&customer, &partner)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrRideNotFound
	}
	if err != nil {
		return nil, err
	}
	c, err := scanComplaint(tx.QueryRow(ctx, `INSERT INTO rider_complaints(ride_id,customer_id,partner_id,category,description) VALUES($1,$2,$3,$4,$5) RETURNING id,ride_id,customer_id,partner_id,category,description,status,resolution_note,resolved_by,resolved_at,created_at`, ride, customer, partner, category, "[Partner support] "+description))
	if err != nil {
		return nil, err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO rider_complaint_partner_reporters(complaint_id,partner_user_id) VALUES($1,$2)`, c.ID, user); err != nil {
		return nil, err
	}
	b, err := json.Marshal(map[string]any{"complaint_id": c.ID, "ride_id": ride, "partner_user_id": user, "status": c.Status, "occurred_at": c.CreatedAt})
	if err != nil {
		return nil, err
	}
	if err = outbox.NewQueuer("rider").Enqueue(ctx, tx, "rider.partner.support.raised", "rider.partner."+user.String()+".support", b); err != nil {
		return nil, err
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, err
	}
	return c, nil
}

func (s *Store) PartnerComplaintReporter(ctx context.Context, id uuid.UUID) (*uuid.UUID, error) {
	var u uuid.UUID
	err := s.db.QueryRow(ctx, `SELECT partner_user_id FROM rider_complaint_partner_reporters WHERE complaint_id=$1`, id).Scan(&u)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	return &u, err
}
