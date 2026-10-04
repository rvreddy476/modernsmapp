package store

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/atpost/doorstep-service/internal/dispatch"
	"github.com/atpost/doorstep-service/internal/model"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// A professional's working day (A4): duty, the latest location fix, offers
// and jobs. Reads that name a booking always go through the professional's
// own assignment, so a professional never reads someone else's job.

// ErrNotOnDuty: a location fix from a professional who is off duty.
var ErrNotOnDuty = errors.New("store: professional is not on duty")

// Fix is one location fix.
type Fix struct {
	Lat, Lng  float64
	AccuracyM *float64
}

// ProDuty is the professional's duty state.
func (s *Store) ProDuty(ctx context.Context, proID uuid.UUID) (*model.DutyState, error) {
	var d model.DutyState
	if err := s.db.QueryRow(ctx, `SELECT on_duty, on_duty_since FROM doorstep.professionals WHERE id = $1`, proID).
		Scan(&d.OnDuty, &d.Since); err != nil {
		return nil, mapErr(err)
	}
	if d.Since != nil {
		t := d.Since.UTC()
		d.Since = &t
	}
	return &d, nil
}

// SetDuty turns duty on or off (idempotent: changed is false when it
// already was) with its duty session, and records a fix given with it.
func (s *Store) SetDuty(ctx context.Context, proID uuid.UUID, on bool, fix *Fix, at time.Time) (state *model.DutyState, changed bool, err error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, false, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var cur bool
	if err := tx.QueryRow(ctx, `SELECT on_duty FROM doorstep.professionals WHERE id = $1 FOR UPDATE`, proID).Scan(&cur); err != nil {
		return nil, false, mapErr(err)
	}
	if cur != on {
		changed = true
		if on {
			if _, err := tx.Exec(ctx, `UPDATE doorstep.professionals SET on_duty = TRUE, on_duty_since = $2, updated_at = $2 WHERE id = $1`,
				proID, at); err != nil {
				return nil, false, err
			}
			if _, err := tx.Exec(ctx, `INSERT INTO doorstep.pro_duty_sessions (pro_id, started_at) VALUES ($1, $2)
				ON CONFLICT DO NOTHING`, proID, at); err != nil {
				return nil, false, mapErr(err)
			}
		} else {
			if _, err := tx.Exec(ctx, `UPDATE doorstep.professionals SET on_duty = FALSE, on_duty_since = NULL, updated_at = $2 WHERE id = $1`,
				proID, at); err != nil {
				return nil, false, err
			}
			if _, err := tx.Exec(ctx, `UPDATE doorstep.pro_duty_sessions SET ended_at = GREATEST($2, started_at)
				WHERE pro_id = $1 AND ended_at IS NULL`, proID, at); err != nil {
				return nil, false, err
			}
		}
	}
	if on && fix != nil {
		if err := recordFixTx(ctx, tx, proID, *fix, at); err != nil {
			return nil, false, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, false, mapErr(err)
	}
	st, err := s.ProDuty(ctx, proID)
	return st, changed, err
}

func recordFixTx(ctx context.Context, q pgx.Tx, proID uuid.UUID, f Fix, at time.Time) error {
	_, err := q.Exec(ctx, `UPDATE doorstep.professionals SET last_point = ST_SetSRID(ST_MakePoint($3, $2), 4326)::geography,
		last_fix_at = $4, last_fix_accuracy_m = $5 WHERE id = $1`, proID, f.Lat, f.Lng, at, f.AccuracyM)
	return err
}

// RecordFix stores the latest fix of an on-duty professional (the previous
// one is overwritten; no trail is kept). ErrNotOnDuty when off duty.
func (s *Store) RecordFix(ctx context.Context, proID uuid.UUID, f Fix, at time.Time) error {
	tag, err := s.db.Exec(ctx, `UPDATE doorstep.professionals SET last_point = ST_SetSRID(ST_MakePoint($3, $2), 4326)::geography,
		last_fix_at = $4, last_fix_accuracy_m = $5 WHERE id = $1 AND on_duty`, proID, f.Lat, f.Lng, at, f.AccuracyM)
	if err != nil {
		return mapErr(err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotOnDuty
	}
	return nil
}

// TravellingTo lists the bookings the professional is travelling to
// (en_route), with the job's point, for live location frames.
func (s *Store) TravellingTo(ctx context.Context, proID uuid.UUID) ([]JobPoint, error) {
	rows, err := s.db.Query(ctx, `SELECT b.id, ST_Y(b.location::geometry), ST_X(b.location::geometry)
		FROM doorstep.booking_assignments a JOIN doorstep.bookings b ON b.id = a.booking_id
		WHERE a.pro_id = $1 AND a.status = 'accepted' AND b.status = 'en_route'`, proID)
	if err != nil {
		return nil, err
	}
	return collect(rows, func(r pgx.Rows) (JobPoint, error) {
		var j JobPoint
		return j, r.Scan(&j.BookingID, &j.Lat, &j.Lng)
	})
}

// JobPoint is a booking and where it is.
type JobPoint struct {
	BookingID uuid.UUID
	Lat, Lng  float64
}

// ProActiveBookingIDs lists the bookings the professional has accepted that
// are still happening (their realtime topics).
func (s *Store) ProActiveBookingIDs(ctx context.Context, proID uuid.UUID) ([]uuid.UUID, error) {
	rows, err := s.db.Query(ctx, `SELECT b.id FROM doorstep.booking_assignments a JOIN doorstep.bookings b ON b.id = a.booking_id
		WHERE a.pro_id = $1 AND a.status = 'accepted'
		  AND b.status IN ('assigned', 'en_route', 'arrived', 'in_progress', 'awaiting_extras_payment')
		ORDER BY b.slot_start, b.id`, proID)
	if err != nil {
		return nil, err
	}
	return collectIDs(rows)
}

// ---------------------------------------------------------------- offers

// offerCols reads an offer: the distance is from the professional's
// previous job that day (else home) to the job, like the matcher's; the
// earning estimate uses the city's (or the category's) commission.
const offerCols = `a.id, a.booking_id, a.status, s.name, c.slug, b.locality,
	COALESCE(ST_Distance(COALESCE(
	    (SELECT b2.location FROM doorstep.pro_calendar_blocks k JOIN doorstep.bookings b2 ON b2.id = k.booking_id
	      WHERE k.pro_id = a.pro_id AND k.active AND k.kind IN ('hold', 'booking') AND k.booking_id <> b.id
	        AND lower(k.during) < b.slot_start
	        AND (lower(k.during) AT TIME ZONE 'Asia/Kolkata')::date = (b.slot_start AT TIME ZONE 'Asia/Kolkata')::date
	      ORDER BY lower(k.during) DESC LIMIT 1),
	    p.home_point), b.location), 0)::float8,
	b.slot_start, b.slot_end, b.taxable_paise,
	COALESCE((SELECT r.commission_bps FROM doorstep.commission_rules r
	           WHERE r.city_code = b.city_code AND r.active AND (r.category_id = b.category_id OR r.category_id IS NULL)
	             AND r.effective_from <= $2 AND (r.effective_to IS NULL OR r.effective_to > $2)
	           ORDER BY r.category_id IS NULL, r.effective_from DESC LIMIT 1), 0),
	a.offer_expires_at`

const offerFrom = `FROM doorstep.booking_assignments a
	JOIN doorstep.bookings b ON b.id = a.booking_id
	JOIN doorstep.services s ON s.id = b.service_id
	JOIN doorstep.categories c ON c.id = b.category_id
	JOIN doorstep.professionals p ON p.id = a.pro_id`

func scanOffer(r pgx.Row, now time.Time) (model.Offer, error) {
	var o model.Offer
	var status string
	var dist float64
	var taxable int64
	var bps int
	err := r.Scan(&o.ID, &o.BookingID, &status, &o.ServiceName, &o.CategorySlug, &o.Locality, &dist, &o.SlotStart, &o.SlotEnd,
		&taxable, &bps, &o.ExpiresAt)
	if err != nil {
		return o, err
	}
	o.SlotStart, o.SlotEnd, o.ExpiresAt = o.SlotStart.UTC(), o.SlotEnd.UTC(), o.ExpiresAt.UTC()
	o.DistanceM = int(dist + 0.5)
	o.EarningEstimatePaise = dispatch.EarningEstimate(taxable, bps)
	o.Status = dispatch.OfferStatus(status, o.ExpiresAt, now)
	return o, nil
}

// ProOffers lists the professional's open offers, soonest expiry first.
func (s *Store) ProOffers(ctx context.Context, proID uuid.UUID, now time.Time) ([]model.Offer, error) {
	rows, err := s.db.Query(ctx, `SELECT `+offerCols+` `+offerFrom+`
		WHERE a.pro_id = $1 AND a.status = 'offered' AND a.offer_expires_at > $2
		ORDER BY a.offer_expires_at, a.id`, proID, now)
	if err != nil {
		return nil, err
	}
	return collect(rows, func(r pgx.Rows) (model.Offer, error) { return scanOffer(r, now) })
}

// ProOffer reads one of the professional's offers in any state.
func (s *Store) ProOffer(ctx context.Context, proID, offerID uuid.UUID, now time.Time) (*model.Offer, error) {
	o, err := scanOffer(s.db.QueryRow(ctx, `SELECT `+offerCols+` `+offerFrom+` WHERE a.pro_id = $1 AND a.id = $3`, proID, now, offerID), now)
	if err != nil {
		return nil, mapErr(err)
	}
	return &o, nil
}

// OfferRef finds one of the professional's assignments by id.
func (s *Store) OfferRef(ctx context.Context, proID, offerID uuid.UUID) (*AssignmentRef, error) {
	var a AssignmentRef
	err := s.db.QueryRow(ctx, `SELECT `+assignmentRefCols+`
		FROM doorstep.booking_assignments a JOIN doorstep.professionals p ON p.id = a.pro_id
		WHERE a.id = $1 AND a.pro_id = $2`, offerID, proID).Scan(&a.ID, &a.BookingID, &a.ProID, &a.ProUserID, &a.Status, &a.ExpiresAt)
	if err != nil {
		return nil, mapErr(err)
	}
	a.ExpiresAt = a.ExpiresAt.UTC()
	return &a, nil
}

// ---------------------------------------------------------------- jobs

// ProJobRecord is a booking as one professional sees it.
type ProJobRecord struct {
	Booking          *BookingRecord
	AssignmentID     uuid.UUID
	AssignmentStatus string
	Family           string
	MinBefore        int
	MinAfter         int
	CommissionBPS    int
	ArrivedAt        *time.Time
	FinishedAt       *time.Time
	CompletedAt      *time.Time
	Uploaded         model.PhotoCounts
}

// ProJobRecord reads a booking through the professional's own assignment
// (offered, accepted or completed); anything else is ErrNotFound.
func (s *Store) ProJobRecord(ctx context.Context, proID, bookingID uuid.UUID, now time.Time) (*ProJobRecord, error) {
	r := &ProJobRecord{}
	err := s.db.QueryRow(ctx, `
		SELECT a.id, a.status, c.family, s.min_before_photos, s.min_after_photos, b.arrived_at, b.finished_at, b.completed_at,
		       COALESCE((SELECT r.commission_bps FROM doorstep.commission_rules r
		                  WHERE r.city_code = b.city_code AND r.active AND (r.category_id = b.category_id OR r.category_id IS NULL)
		                    AND r.effective_from <= $3 AND (r.effective_to IS NULL OR r.effective_to > $3)
		                  ORDER BY r.category_id IS NULL, r.effective_from DESC LIMIT 1), 0),
		       (SELECT count(*) FROM doorstep.booking_photos ph WHERE ph.booking_id = b.id AND ph.phase = 'before'),
		       (SELECT count(*) FROM doorstep.booking_photos ph WHERE ph.booking_id = b.id AND ph.phase = 'after'),
		       (SELECT count(*) FROM doorstep.booking_photos ph WHERE ph.booking_id = b.id AND ph.phase = 'kit_seal')
		FROM doorstep.booking_assignments a
		JOIN doorstep.bookings b ON b.id = a.booking_id
		JOIN doorstep.services s ON s.id = b.service_id
		JOIN doorstep.categories c ON c.id = b.category_id
		WHERE a.booking_id = $1 AND a.pro_id = $2 AND a.status IN ('offered', 'accepted', 'completed')
		ORDER BY a.created_at DESC LIMIT 1`, bookingID, proID, now).Scan(&r.AssignmentID, &r.AssignmentStatus, &r.Family,
		&r.MinBefore, &r.MinAfter, &r.ArrivedAt, &r.FinishedAt, &r.CompletedAt, &r.CommissionBPS,
		&r.Uploaded.Before, &r.Uploaded.After, &r.Uploaded.KitSeal)
	if err != nil {
		return nil, mapErr(err)
	}
	for _, t := range []**time.Time{&r.ArrivedAt, &r.FinishedAt, &r.CompletedAt} {
		if *t != nil {
			u := (**t).UTC()
			*t = &u
		}
	}
	if r.Booking, err = s.BookingRecord(ctx, bookingID, nil); err != nil {
		return nil, err
	}
	return r, nil
}

// Professional job list modes.
const (
	JobsUpcoming = "upcoming"
	JobsActive   = "active"
	JobsPast     = "past"
	JobsAll      = "all"
)

// ProJobIDs pages the bookings the professional accepted: upcoming
// (assigned, soonest first), active (on the way to finished), past
// (finished or ended, latest first) or all (latest first); date narrows to
// one IST day. limit+1 rows come back when there is a next page.
func (s *Store) ProJobIDs(ctx context.Context, proID uuid.UUID, mode, date string, after *BookingCursor, limit int) ([]BookingCursor, error) {
	where := `a.pro_id = $1 AND a.role = 'lead' AND a.status IN ('accepted', 'completed')`
	order, cmp := `b.slot_start DESC, b.id DESC`, `<`
	switch mode {
	case JobsUpcoming:
		where += ` AND b.status = 'assigned'`
		order, cmp = `b.slot_start, b.id`, `>`
	case JobsActive:
		where += ` AND b.status IN ('en_route', 'arrived', 'in_progress', 'awaiting_extras_payment')`
		order, cmp = `b.slot_start, b.id`, `>`
	case JobsPast:
		where += ` AND b.status NOT IN ('assigned', 'en_route', 'arrived', 'in_progress', 'awaiting_extras_payment')`
	}
	args := []any{proID, limit + 1}
	if date != "" {
		args = append(args, date)
		where += ` AND (b.slot_start AT TIME ZONE 'Asia/Kolkata')::date = $3::date`
	} else {
		args = append(args, nil)
		where += ` AND $3::text IS NULL`
	}
	if after != nil {
		where += ` AND (b.slot_start, b.id) ` + cmp + ` ($4, $5)`
		args = append(args, after.SlotStart, after.ID)
	}
	rows, err := s.db.Query(ctx, `SELECT b.slot_start, b.id FROM doorstep.booking_assignments a
		JOIN doorstep.bookings b ON b.id = a.booking_id WHERE `+where+` ORDER BY `+order+` LIMIT $2`, args...)
	if err != nil {
		return nil, err
	}
	return collect(rows, func(r pgx.Rows) (BookingCursor, error) {
		var c BookingCursor
		err := r.Scan(&c.SlotStart, &c.ID)
		c.SlotStart = c.SlotStart.UTC()
		return c, err
	})
}

// ---------------------------------------------------------------- profile reads

// ProArea is the saved service area: zones, home (null until saved) and
// radius.
func (s *Store) ProArea(ctx context.Context, proID uuid.UUID) (*model.ProArea, error) {
	a := &model.ProArea{}
	err := s.db.QueryRow(ctx, `SELECT ST_Y(home_point::geometry), ST_X(home_point::geometry), service_radius_m
		FROM doorstep.professionals WHERE id = $1`, proID).Scan(&a.HomeLat, &a.HomeLng, &a.RadiusM)
	if err != nil {
		return nil, mapErr(err)
	}
	if a.ZoneIDs, err = s.ProZoneIDs(ctx, proID); err != nil {
		return nil, err
	}
	return a, nil
}

// CityZones lists a city's active zones (a professional picks theirs).
func (s *Store) CityZones(ctx context.Context, city string) ([]model.ProZone, error) {
	rows, err := s.db.Query(ctx, `SELECT z.id, z.city_code, z.name, z.slug, ST_AsGeoJSON(z.boundary, 6)::text
		FROM doorstep.zones z JOIN doorstep.cities c ON c.code = z.city_code
		WHERE z.city_code = $1 AND z.active AND c.active ORDER BY z.name, z.id`, city)
	if err != nil {
		return nil, err
	}
	return collect(rows, func(r pgx.Rows) (model.ProZone, error) {
		var z model.ProZone
		var boundary string
		err := r.Scan(&z.ID, &z.CityCode, &z.Name, &z.Slug, &boundary)
		z.Boundary = json.RawMessage(boundary)
		return z, err
	})
}
