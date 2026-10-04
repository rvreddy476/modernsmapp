package store

import (
	"context"
	"errors"
	"github.com/atpost/doorstep-service/internal/dispatch"
	"github.com/atpost/doorstep-service/internal/events"
	"github.com/atpost/doorstep-service/internal/model"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"time"
)

const reworkCols = `id,booking_id,child_booking_id,status,reason,created_at`

func scanRework(r scanner) (model.ReworkRequest, error) {
	var v model.ReworkRequest
	e := r.Scan(&v.ID, &v.BookingID, &v.ChildBookingID, &v.Status, &v.Reason, &v.CreatedAt)
	return v, mapErr(e)
}
func (s *Store) VisitReworks(ctx context.Context, id, user uuid.UUID) ([]model.ReworkRequest, error) {
	var owner uuid.UUID
	if e := s.db.QueryRow(ctx, `SELECT customer_user_id FROM doorstep.bookings WHERE id=$1`, id).Scan(&owner); e != nil {
		return nil, mapErr(e)
	}
	if owner != user {
		return nil, ErrNotFound
	}
	rows, e := s.db.Query(ctx, `SELECT `+reworkCols+` FROM doorstep.rework_requests WHERE booking_id=$1 ORDER BY created_at,id`, id)
	if e != nil {
		return nil, e
	}
	return collect(rows, func(r pgx.Rows) (model.ReworkRequest, error) { return scanRework(r) })
}

type ReworkSpec struct {
	ID, ChildID, BookingID, User, ProID uuid.UUID
	Reason                              string
	MediaIDs                            []string
	Same                                bool
	SlotStart                           *time.Time
	SlotEnd, BlockStart, BlockEnd, At   time.Time
}

func (s *Store) RequestVisitRework(ctx context.Context, in ReworkSpec, guard func(*VisitFacts) error) (*model.ReworkRequest, error) {
	var result model.ReworkRequest
	e := s.visitTransaction(ctx, in.BookingID, in.At, guard, func(tx pgx.Tx, f *VisitFacts) error {
		prev, err := scanRework(tx.QueryRow(ctx, `SELECT `+reworkCols+` FROM doorstep.rework_requests WHERE booking_id=$1 ORDER BY created_at LIMIT 1 FOR UPDATE`, in.BookingID))
		if err == nil {
			result = prev
			// One rework per original visit, including retry after child completion.
			if prev.ChildBookingID != nil || in.SlotStart == nil {
				return nil
			}
			in.ID = prev.ID
		} else if !errors.Is(err, ErrNotFound) {
			return err
		} else {
			if in.MediaIDs == nil {
				in.MediaIDs = []string{}
			}
			result = model.ReworkRequest{ID: in.ID, BookingID: in.BookingID, Status: "requested", Reason: in.Reason, CreatedAt: in.At}
			if _, err = tx.Exec(ctx, `INSERT INTO doorstep.rework_requests(id,booking_id,customer_user_id,reason,media_ids,same_professional,created_at) VALUES($1,$2,$3,$4,$5,$6,$7)`, in.ID, in.BookingID, in.User, in.Reason, in.MediaIDs, in.Same, in.At); err != nil {
				return mapErr(err)
			}
		}
		if in.SlotStart != nil {
			status := "confirmed"
			var pro *uuid.UUID
			var deadline *time.Time
			var cause *string
			if in.Same {
				pro = &in.ProID
			} else {
				status = "pro_unavailable"
				d := in.At.Add(dispatch.ChoiceWindow)
				deadline = &d
				c := "no_professional"
				cause = &c
			}
			_, err = tx.Exec(ctx, `INSERT INTO doorstep.bookings(id,customer_user_id,idempotency_key,parent_booking_id,city_code,zone_id,category_id,service_id,address_id,address_snapshot,address_sealed,locality,location,slot_start,slot_end,duration_minutes,status,gender_rule,require_female_pro,crew_size,total_paise,taxable_paise,tax_paise,confirmed_at,created_at,updated_at,reserved_pro_id,choice_deadline,unavailable_cause,pro_unavailable_at)
    SELECT $2,customer_user_id,'rework:'||$3::text,id,city_code,zone_id,category_id,service_id,address_id,address_snapshot,address_sealed,locality,location,$4,$5,duration_minutes,$6,gender_rule,require_female_pro,crew_size,0,0,0,$7,$7,$7,$8,$9,$10,CASE WHEN $6='pro_unavailable' THEN $7::timestamptz ELSE NULL END FROM doorstep.bookings WHERE id=$1`, in.BookingID, in.ChildID, in.ID, *in.SlotStart, in.SlotEnd, status, in.At, pro, deadline, cause)
			if err != nil {
				return mapErr(err)
			}
			if _, err = tx.Exec(ctx, `INSERT INTO doorstep.booking_items(booking_id,line_no,kind,ref_id,price_id,name,quantity,unit_price_paise,line_total_paise,taxable_paise,tax_paise,gst_category,tax_rate_bps,sac,unit,pro_price_id) SELECT $2,line_no,kind,ref_id,price_id,name,quantity,0,0,0,0,gst_category,tax_rate_bps,sac,unit,pro_price_id FROM doorstep.booking_items WHERE booking_id=$1`, in.BookingID, in.ChildID); err != nil {
				return mapErr(err)
			}
			if in.Same {
				if _, err = holdFirstTx(ctx, tx, in.ChildID, []uuid.UUID{in.ProID}, "booking", in.BlockStart, in.BlockEnd, nil); err != nil {
					return err
				}
			}
			if err = historyTx(ctx, tx, in.ChildID, nil, status, "customer", &in.User, nil, in.At); err != nil {
				return err
			}
			if _, err = tx.Exec(ctx, `UPDATE doorstep.rework_requests SET child_booking_id=$2,status='scheduled',slot_start=$3,same_professional=$4 WHERE id=$1`, in.ID, in.ChildID, *in.SlotStart, in.Same); err != nil {
				return mapErr(err)
			}
			result.ChildBookingID = &in.ChildID
			result.Status = "scheduled"
			core, err := bookingCoreTx(ctx, tx, in.ChildID)
			if err != nil {
				return err
			}
			if in.Same {
				err = s.enqueueBookingEvent(ctx, tx, events.BookingConfirmed, core, in.At, events.BookingConfirmedData{BookingCore: core, PaidPaise: 0})
			} else {
				err = s.enqueueBookingEvent(ctx, tx, events.BookingProUnavailable, core, in.At, events.BookingProUnavailableData{BookingCore: core, Cause: *cause, ChoiceDeadline: *deadline})
			}
			if err != nil {
				return err
			}
		}
		core, err := bookingCoreTx(ctx, tx, in.BookingID)
		if err != nil {
			return err
		}
		return s.enqueueBookingEvent(ctx, tx, events.BookingReworkRequested, core, in.At, events.BookingReworkRequestedData{BookingCore: core, ReworkID: result.ID, ChildBookingID: result.ChildBookingID})
	})
	return &result, e
}
