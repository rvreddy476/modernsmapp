package partnerwork

import (
	"context"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// BusyIfInstalled supports services sharing the configured coordination
// authority. Legacy deployments without that authority retain old behavior;
// database/readback errors never imply availability.
func BusyIfInstalled(ctx context.Context, db *pgxpool.Pool, user uuid.UUID) (bool, error) {
	var installed bool
	if err := db.QueryRow(ctx, `SELECT to_regclass('partner_work.authority') IS NOT NULL`).Scan(&installed); err != nil {
		return false, err
	}
	if !installed {
		return false, nil
	}
	var busy bool
	err := db.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM partner_work.reservations WHERE user_id=$1 AND state IN ('reserved','active'))`, user).Scan(&busy)
	return busy, err
}

// VisibleOfferJobs removes withdrawn choices from domain readbacks even before
// the domain expiry worker updates its offer rows. Legacy uncoordinated test
// stores retain their existing behavior; an installed authority fails closed.
func VisibleOfferJobs(ctx context.Context, db *pgxpool.Pool, user uuid.UUID, c Category) (map[uuid.UUID]bool, bool, error) {
	var installed bool
	if err := db.QueryRow(ctx, `SELECT to_regclass('partner_work.authority') IS NOT NULL`).Scan(&installed); err != nil {
		return nil, false, err
	}
	if !installed {
		return nil, false, nil
	}
	snapshot, err := Snapshot(ctx, db, user)
	if err != nil {
		return nil, true, err
	}
	jobs := map[uuid.UUID]bool{}
	for _, r := range snapshot.Reservations {
		if r.State == "reserved" || r.State == "active" {
			return jobs, true, nil
		}
	}
	if snapshot.Session.Category == nil || *snapshot.Session.Category != c {
		return jobs, true, nil
	}
	for _, r := range snapshot.Reservations {
		if r.Category == c && r.State == "offered" {
			jobs[r.JobID] = true
		}
	}
	return jobs, true, nil
}
