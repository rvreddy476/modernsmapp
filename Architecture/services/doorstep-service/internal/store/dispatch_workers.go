package store

import (
	"context"
	"time"

	"github.com/atpost/doorstep-service/internal/dispatch"
	"github.com/atpost/doorstep-service/internal/events"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Dispatch workers (A4): each query finds the rows one rule applies to;
// the service applies the rule through Redispatch, CancelBooking or
// EndProNoShow, which lock the booking and re-check it, so two replicas
// finding the same row cannot both act on it.

func scanAssignmentRefs(rows pgx.Rows) ([]AssignmentRef, error) {
	return collect(rows, func(r pgx.Rows) (AssignmentRef, error) {
		var a AssignmentRef
		err := r.Scan(&a.ID, &a.BookingID, &a.ProID, &a.ProUserID, &a.Status, &a.ExpiresAt)
		a.ExpiresAt = a.ExpiresAt.UTC()
		return a, err
	})
}

const assignmentRefCols = `a.id, a.booking_id, a.pro_id, p.user_id, a.status, a.offer_expires_at`

// DueOfferExpiries lists open offers past their expiry, oldest first.
func (s *Store) DueOfferExpiries(ctx context.Context, now time.Time, limit int) ([]AssignmentRef, error) {
	rows, err := s.db.Query(ctx, `SELECT `+assignmentRefCols+`
		FROM doorstep.booking_assignments a JOIN doorstep.professionals p ON p.id = a.pro_id
		WHERE a.status = 'offered' AND a.offer_expires_at <= $1 ORDER BY a.offer_expires_at LIMIT $2`, now, limit)
	if err != nil {
		return nil, err
	}
	return scanAssignmentRefs(rows)
}

// BookingsAwaitingOffer lists confirmed bookings with no open offer and no
// accepted professional (a confirm whose offer never went out, or one
// dispatch found nobody for) that are due another attempt: never more often
// than dispatch.RetryEvery, and only while the slot is beyond the
// unassigned-cancel deadline (or a rescue is running).
func (s *Store) BookingsAwaitingOffer(ctx context.Context, now time.Time, limit int) ([]uuid.UUID, error) {
	rows, err := s.db.Query(ctx, `
		SELECT b.id FROM doorstep.bookings b
		WHERE b.status = 'confirmed'
		  AND (b.dispatch_attempted_at IS NULL OR b.dispatch_attempted_at <= $1::timestamptz - make_interval(secs => $2))
		  AND (b.rescue_until IS NOT NULL OR b.slot_start > $1::timestamptz + make_interval(secs => $3))
		  AND NOT EXISTS (SELECT 1 FROM doorstep.booking_assignments a WHERE a.booking_id = b.id AND a.status IN ('offered', 'accepted'))
		ORDER BY b.slot_start LIMIT $4`, now, dispatch.RetryEvery.Seconds(), dispatch.CancelBefore.Seconds(), limit)
	if err != nil {
		return nil, err
	}
	return collectIDs(rows)
}

func collectIDs(rows pgx.Rows) ([]uuid.UUID, error) {
	return collect(rows, func(r pgx.Rows) (uuid.UUID, error) {
		var id uuid.UUID
		return id, r.Scan(&id)
	})
}

// AlertUnassigned raises the T-2 h alert once per booking: confirmed, no
// accepted professional, slot within two hours (and not a rescue, which
// has its own deadline). The flag and doorstep.booking.unassigned_alert
// commit together. Returns the alerted bookings.
func (s *Store) AlertUnassigned(ctx context.Context, now time.Time, limit int) ([]events.BookingCore, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	rows, err := tx.Query(ctx, `SELECT id FROM doorstep.bookings
		WHERE status = 'confirmed' AND unassigned_alerted_at IS NULL AND rescue_until IS NULL
		  AND slot_start <= $1::timestamptz + make_interval(secs => $2)
		ORDER BY slot_start LIMIT $3 FOR UPDATE SKIP LOCKED`, now, dispatch.AlertBefore.Seconds(), limit)
	if err != nil {
		return nil, err
	}
	ids, err := collectIDs(rows)
	if err != nil {
		return nil, err
	}
	out := make([]events.BookingCore, 0, len(ids))
	for _, id := range ids {
		if _, err := tx.Exec(ctx, `UPDATE doorstep.bookings SET unassigned_alerted_at = $2 WHERE id = $1`, id, now); err != nil {
			return nil, err
		}
		core, err := bookingCoreTx(ctx, tx, id)
		if err != nil {
			return nil, err
		}
		if err := s.enqueueBookingEvent(ctx, tx, events.BookingUnassignedAlert, core, now, events.BookingUnassignedAlertData{
			BookingCore: core, MinutesToSlot: minutesTo(now, core.SlotStart), Reason: events.AlertTMinus2h}); err != nil {
			return nil, err
		}
		out = append(out, core)
	}
	return out, mapErr(tx.Commit(ctx))
}

// UnassignedPastCancel lists confirmed bookings (not a rescue) whose slot
// is inside the unassigned-cancel deadline: nobody accepted in time.
func (s *Store) UnassignedPastCancel(ctx context.Context, now time.Time, limit int) ([]uuid.UUID, error) {
	rows, err := s.db.Query(ctx, `SELECT id FROM doorstep.bookings
		WHERE status = 'confirmed' AND rescue_until IS NULL AND slot_start <= $1::timestamptz + make_interval(secs => $2)
		ORDER BY slot_start LIMIT $3`, now, dispatch.CancelBefore.Seconds(), limit)
	if err != nil {
		return nil, err
	}
	return collectIDs(rows)
}

// AssignedNotOnDuty lists accepted jobs whose professional is not on duty
// by the duty deadline (slot - 90 min), skipping a professional who
// accepted within the last dispatch.DutyGrace.
func (s *Store) AssignedNotOnDuty(ctx context.Context, now time.Time, limit int) ([]AssignmentRef, error) {
	rows, err := s.db.Query(ctx, `SELECT `+assignmentRefCols+`
		FROM doorstep.bookings b
		JOIN doorstep.booking_assignments a ON a.booking_id = b.id AND a.role = 'lead' AND a.status = 'accepted'
		JOIN doorstep.professionals p ON p.id = a.pro_id
		WHERE b.status = 'assigned' AND NOT p.on_duty
		  AND b.slot_start <= $1::timestamptz + make_interval(secs => $2) AND b.slot_start > $1
		  AND a.responded_at <= $1::timestamptz - make_interval(secs => $3)
		ORDER BY b.slot_start LIMIT $4`, now, dispatch.DutyBefore.Seconds(), dispatch.DutyGrace.Seconds(), limit)
	if err != nil {
		return nil, err
	}
	return scanAssignmentRefs(rows)
}

// MarkLate flags jobs whose professional has not arrived 15 minutes after
// the slot: pro_late_at (the customer may now cancel free) and
// doorstep.booking.pro_late, once per booking. Returns the flagged ones.
func (s *Store) MarkLate(ctx context.Context, now time.Time, limit int) ([]events.BookingCore, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	rows, err := tx.Query(ctx, `SELECT id FROM doorstep.bookings
		WHERE status IN ('assigned', 'en_route') AND pro_late_at IS NULL
		  AND slot_start <= $1::timestamptz - make_interval(secs => $2)
		ORDER BY slot_start LIMIT $3 FOR UPDATE SKIP LOCKED`, now, dispatch.LateAfter.Seconds(), limit)
	if err != nil {
		return nil, err
	}
	ids, err := collectIDs(rows)
	if err != nil {
		return nil, err
	}
	out := make([]events.BookingCore, 0, len(ids))
	for _, id := range ids {
		if _, err := tx.Exec(ctx, `UPDATE doorstep.bookings SET pro_late_at = $2, updated_at = $2 WHERE id = $1`, id, now); err != nil {
			return nil, err
		}
		core, err := bookingCoreTx(ctx, tx, id)
		if err != nil {
			return nil, err
		}
		if err := s.enqueueBookingEvent(ctx, tx, events.BookingProLate, core, now, events.BookingProLateData{
			BookingCore: core, MinutesLate: -minutesTo(now, core.SlotStart), FreeCancel: true}); err != nil {
			return nil, err
		}
		out = append(out, core)
	}
	return out, mapErr(tx.Commit(ctx))
}

// NoShows lists accepted jobs whose professional has not arrived 30
// minutes after the slot.
func (s *Store) NoShows(ctx context.Context, now time.Time, limit int) ([]AssignmentRef, error) {
	rows, err := s.db.Query(ctx, `SELECT `+assignmentRefCols+`
		FROM doorstep.bookings b
		JOIN doorstep.booking_assignments a ON a.booking_id = b.id AND a.role = 'lead' AND a.status = 'accepted'
		JOIN doorstep.professionals p ON p.id = a.pro_id
		WHERE b.status IN ('assigned', 'en_route') AND b.slot_start <= $1::timestamptz - make_interval(secs => $2)
		ORDER BY b.slot_start LIMIT $3`, now, dispatch.NoShowAfter.Seconds(), limit)
	if err != nil {
		return nil, err
	}
	return scanAssignmentRefs(rows)
}

// RescueRef is a confirmed booking whose rescue deadline passed.
type RescueRef struct {
	BookingID uuid.UUID
	Cause     string
}

// RescuesExpired lists rescues nobody accepted in time.
func (s *Store) RescuesExpired(ctx context.Context, now time.Time, limit int) ([]RescueRef, error) {
	rows, err := s.db.Query(ctx, `SELECT id, rescue_cause FROM doorstep.bookings
		WHERE status = 'confirmed' AND rescue_until <= $1 ORDER BY rescue_until LIMIT $2`, now, limit)
	if err != nil {
		return nil, err
	}
	return collect(rows, func(r pgx.Rows) (RescueRef, error) {
		var x RescueRef
		return x, r.Scan(&x.BookingID, &x.Cause)
	})
}

// ProRef names a professional for presence.
type ProRef struct {
	ProID    uuid.UUID
	UserID   uuid.UUID
	CityCode string
}

// StaleOnDuty lists on-duty professionals whose last fix (or, with none,
// going on duty) is older than dispatch.StaleFix.
func (s *Store) StaleOnDuty(ctx context.Context, now time.Time, limit int) ([]ProRef, error) {
	rows, err := s.db.Query(ctx, `SELECT id, user_id, city_code FROM doorstep.professionals
		WHERE on_duty AND COALESCE(last_fix_at, on_duty_since, updated_at) <= $1::timestamptz - make_interval(secs => $2)
		ORDER BY id LIMIT $3`, now, dispatch.StaleFix.Seconds(), limit)
	if err != nil {
		return nil, err
	}
	return collect(rows, func(r pgx.Rows) (ProRef, error) {
		var x ProRef
		return x, r.Scan(&x.ProID, &x.UserID, &x.CityCode)
	})
}
