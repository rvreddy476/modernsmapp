package postgres

import (
	"context"
	"errors"
	"time"

	"github.com/atpost/food-service/internal/orderstate"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Audit 2026-10-30 L4: a rider who goes offline mid-job can finish it, and a
// rider who goes silent before pickup loses the job back to dispatch.

// riderOnJobSQL is the presence worker's on_job rule (rider_presence.go) for
// the partner aliased dp: they hold a live assignment on an order that is
// with a rider.
const riderOnJobSQL = `EXISTS (
	SELECT 1 FROM food.delivery_assignments da
	JOIN food.orders o ON o.id = da.order_id
	WHERE da.delivery_partner_id = dp.id
	  AND da.status NOT IN ('CREATED', 'DELIVERED', 'FAILED', 'CANCELLED', 'REJECTED')
	  AND o.status IN ('DELIVERY_ASSIGNED', 'PICKED_UP', 'OUT_FOR_DELIVERY'))`

// riderMayWorkHeldJob is the status rule for acting on an assignment the
// rider already holds (step buttons, delivery-code verify). OFFLINE is
// availability, not standing: it stops new offers, not the job in hand.
// Every caller also scopes the assignment to the partner.
func riderMayWorkHeldJob(status string) bool {
	return status == "ACTIVE" || status == "OFFLINE"
}

// SilentRiderJobTimeout: an ASSIGNED or ACCEPTED job whose rider has sent no
// location ping (and taken no step) for this long goes back to dispatch.
const SilentRiderJobTimeout = 10 * time.Minute

// ReleaseSilentRiderJobs returns to dispatch, one transaction each, the
// pre-pickup jobs (assignment ASSIGNED or ACCEPTED, order DELIVERY_ASSIGNED)
// whose rider has been silent for `silence`: no location ping and no
// assignment or accept since then. The release is the rider's own release
// (assignment back to CREATED with codes and batch links cleared, a REJECTED
// history snapshot for the rider, order DELIVERY_ASSIGNED -> DELIVERY_ASSIGNING
// by the system), so the existing dispatch worker offers it again. Food that
// was picked up is never taken back. Safe on every replica: each job is
// re-checked under the order lock, skipped when another replica holds it.
func (s *Store) ReleaseSilentRiderJobs(ctx context.Context, silence time.Duration, limit int) ([]uuid.UUID, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	rows, err := s.db.Query(ctx, `
		SELECT da.id FROM food.delivery_assignments da
		JOIN food.orders o ON o.id = da.order_id
		WHERE da.status IN ('ASSIGNED', 'ACCEPTED') AND da.delivery_partner_id IS NOT NULL
		  AND o.status = 'DELIVERY_ASSIGNED'
		  AND `+silentRiderSQL+`
		ORDER BY da.assigned_at NULLS FIRST
		LIMIT $2
	`, silence.Seconds(), limit)
	if err != nil {
		return nil, err
	}
	var candidates []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		candidates = append(candidates, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	var released []uuid.UUID
	for _, id := range candidates {
		ok, err := s.releaseSilentRiderJob(ctx, id, silence)
		if err != nil {
			return released, err
		}
		if ok {
			released = append(released, id)
		}
	}
	return released, nil
}

// silentRiderSQL holds for assignment da when its rider's last sign of life
// (latest ping, the assignment, the accept) is older than $1 seconds.
const silentRiderSQL = `GREATEST(
		COALESCE(da.accepted_at, '-infinity'::timestamptz),
		COALESCE(da.assigned_at, '-infinity'::timestamptz),
		COALESCE((SELECT l.recorded_at FROM food.delivery_partner_locations l
		          WHERE l.delivery_partner_id = da.delivery_partner_id
		          ORDER BY l.recorded_at DESC LIMIT 1), '-infinity'::timestamptz)
	) < NOW() - make_interval(secs => $1::float8)`

func (s *Store) releaseSilentRiderJob(ctx context.Context, assignmentID uuid.UUID, silence time.Duration) (bool, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback(ctx) }() // no-op after Commit
	// Order first (the lock order of accept and cancel), skipping one another
	// replica or a live request holds.
	var orderID uuid.UUID
	err = tx.QueryRow(ctx, `
		SELECT o.id FROM food.orders o
		JOIN food.delivery_assignments da ON da.order_id = o.id
		WHERE da.id = $1 AND o.status = 'DELIVERY_ASSIGNED'
		FOR UPDATE OF o SKIP LOCKED
	`, assignmentID).Scan(&orderID)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	var partnerID uuid.UUID
	err = tx.QueryRow(ctx, `
		SELECT da.delivery_partner_id FROM food.delivery_assignments da
		WHERE da.id = $2 AND da.status IN ('ASSIGNED', 'ACCEPTED') AND da.delivery_partner_id IS NOT NULL
		  AND `+silentRiderSQL+`
		FOR UPDATE OF da
	`, silence.Seconds(), assignmentID).Scan(&partnerID)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil // the rider spoke, stepped or released meanwhile
	}
	if err != nil {
		return false, err
	}
	_, snapshot, err := s.releasedAssignmentSnapshotTx(ctx, tx, assignmentID)
	if err != nil {
		return false, err
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO food.delivery_tracking_events (
			assignment_id, delivery_partner_id, status, latitude, longitude, note, snapshot
		)
		SELECT $1, $2, 'REJECTED', current_latitude, current_longitude, 'released: rider silent',
			$3::jsonb || (SELECT jsonb_build_object('assigned_at', assigned_at, 'accepted_at', accepted_at)
			              FROM food.delivery_assignments WHERE id = $1)
		FROM food.delivery_partners WHERE id = $2
	`, assignmentID, partnerID, snapshot); err != nil {
		return false, err
	}
	if _, err := tx.Exec(ctx, `
		UPDATE food.delivery_assignments
		SET delivery_partner_id = NULL, status = 'CREATED',
			assigned_at = NULL, accepted_at = NULL,
			pickup_code = NULL, delivery_code = NULL,
			batch_id = NULL, batch_sequence = NULL
		WHERE id = $1
	`, assignmentID); err != nil {
		return false, err
	}
	if err := transitionOrderTx(ctx, tx, OrderTransition{
		OrderID: orderID, From: orderstate.DeliveryAssigned, To: orderstate.DeliveryAssigning,
		Actor: orderstate.ActorSystem, Reason: "delivery partner silent; job returned to dispatch",
	}); err != nil {
		return false, err
	}
	return true, tx.Commit(ctx)
}
