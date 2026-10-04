package store

import (
	"context"
	"time"

	"github.com/atpost/doorstep-service/internal/dispatch"
	"github.com/atpost/doorstep-service/internal/events"
	"github.com/atpost/doorstep-service/internal/model"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

type IncidentResult struct {
	Incident        *model.Incident
	RefundIDs       []uuid.UUID
	ChangedBookings []uuid.UUID
}

// Sweep other open jobs after suspension. Each job takes the normal B1
// transaction and choice window; none is silently handed to someone else.
func (s *Store) SuspendedVisitBookings(ctx context.Context, limit int) ([]uuid.UUID, error) {
	rows, e := s.db.Query(ctx, `SELECT b.id FROM doorstep.bookings b JOIN doorstep.professionals p ON p.id=b.reserved_pro_id WHERE p.status IN ('suspended','blocked','rejected') AND b.status IN ('confirmed','assigned','en_route','arrived','in_progress','awaiting_extras_payment') ORDER BY b.id LIMIT $1`, limit)
	if e != nil {
		return nil, e
	}
	return collectIDs(rows)
}

func (s *Store) RaiseVisitIncident(ctx context.Context, id, u uuid.UUID, kind, action string, in model.SOSInput, at time.Time, guard func(*VisitFacts) error) (*IncidentResult, error) {
	result := &IncidentResult{Incident: &model.Incident{ID: uuid.New(), BookingID: &id, RaisedByKind: kind, Kind: action, Severity: "critical", Status: "open", Description: in.Note, CreatedAt: at}}
	e := s.visitTransaction(ctx, id, at, guard, func(tx pgx.Tx, f *VisitFacts) error {
		var count int
		if e := tx.QueryRow(ctx, `SELECT count(*) FROM doorstep.incidents WHERE booking_id=$1 AND raised_by_user_id=$2 AND created_at>$3`, id, u, at.Add(-time.Minute)).Scan(&count); e != nil {
			return e
		}
		if count >= 3 {
			return ErrConflict
		}
		auto := kind == "customer" && f.Family == "BEAUTY_SALON" && f.Pro != nil
		result.Incident.ProAutoSuspended = auto
		if _, e := tx.Exec(ctx, `INSERT INTO doorstep.incidents(id,booking_id,pro_id,raised_by_kind,raised_by_user_id,kind,severity,description,lat,lng,pro_auto_suspended,created_at,updated_at) VALUES($1,$2,$3,$4,$5,$6,'critical',$7,$8,$9,$10,$11,$11)`, result.Incident.ID, id, f.Pro.ProID, kind, u, action, in.Note, in.Lat, in.Lng, auto, at); e != nil {
			return e
		}
		if auto {
			if _, e := tx.Exec(ctx, `UPDATE doorstep.professionals SET status='suspended',incident_suspended=true,on_duty=false,updated_at=$2 WHERE id=$1 AND status='approved'`, f.Pro.ProID, at); e != nil {
				return e
			}
			if e := s.enqueueProEvent(ctx, tx, events.ProStatusChanged, f.Pro.UserID, events.ProStatusChangedData{ProID: f.Pro.ProID, ProUserID: f.Pro.UserID, FromStatus: f.Pro.AccountStatus, ToStatus: "suspended"}); e != nil {
				return e
			}
			if e := s.enqueueRoleForStatusTx(ctx, tx, f.Pro.UserID, "suspended", "safety incident"); e != nil {
				return e
			}
		}
		if auto || action == "unsafe_exit" {
			cause := events.UnavailableProUnsafeExit
			if auto {
				cause = events.UnavailableProSuspended
			}
			refunds, e := resetVisitTx(ctx, tx, id, at)
			if e != nil {
				return e
			}
			result.RefundIDs = refunds
			if e = releaseBlocksTx(ctx, tx, id, at, cause); e != nil {
				return e
			}
			if _, e = tx.Exec(ctx, `UPDATE doorstep.booking_assignments SET status='released',release_cause=$2,updated_at=$3 WHERE booking_id=$1 AND status IN ('offered','accepted')`, id, cause, at); e != nil {
				return e
			}
			deadline := at.Add(dispatch.ChoiceWindow)
			if _, e = tx.Exec(ctx, `UPDATE doorstep.bookings SET status='pro_unavailable',reserved_pro_id=NULL,assigned_at=NULL,en_route_at=NULL,arrived_at=NULL,pro_unavailable_at=$2,choice_deadline=$3,unavailable_cause=$4,excluded_pro_ids=ARRAY(SELECT DISTINCT x FROM unnest(excluded_pro_ids||ARRAY[$5]::uuid[])x),version=version+1,updated_at=$2 WHERE id=$1`, id, at, deadline, cause, f.Pro.ProID); e != nil {
				return e
			}
			from := f.Status
			reason := "visit stopped for safety"
			if e = historyTx(ctx, tx, id, &from, "pro_unavailable", kind, &u, &reason, at); e != nil {
				return e
			}
			core, e := bookingCoreTx(ctx, tx, id)
			if e != nil {
				return e
			}
			if e = s.enqueueBookingEvent(ctx, tx, events.BookingProUnavailable, core, at, events.BookingProUnavailableData{BookingCore: core, Cause: cause, ChoiceDeadline: deadline}); e != nil {
				return e
			}
			result.ChangedBookings = append(result.ChangedBookings, id)
		}
		if _, e := tx.Exec(ctx, `UPDATE doorstep.share_tokens SET revoked_at=$2 WHERE booking_id=$1 AND revoked_at IS NULL`, id, at); e != nil {
			return e
		}
		proUser := f.Pro.UserID
		data := events.IncidentRaisedData{IncidentID: result.Incident.ID, BookingID: &id, CustomerUserID: &f.Customer, ProUserID: &proUser, Kind: action, Severity: "critical", RaisedByKind: kind, ProAutoSuspended: auto}
		key, payload, e := events.Incident(data, at)
		if e != nil {
			return e
		}
		return s.events.Enqueue(ctx, tx, events.IncidentRaised, key, payload)
	})
	return result, e
}
