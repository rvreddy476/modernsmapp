package store

import (
	"context"
	"time"

	"github.com/atpost/doorstep-service/internal/model"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Admin booking reads (A3 builds what the console's booking pages need; A6
// adds incidents, tickets, ratings, settlements).

// AdminBookingFilter narrows the admin list.
type AdminBookingFilter struct {
	Status string
	City   string
	// Date is an IST date ("2006-01-02") the slot starts on.
	Date  string
	After *BookingCursor
	Limit int
}

// AdminBookings pages bookings latest slot first. limit+1 rows come back
// when there is a next page. Rows carry no address.
func (s *Store) AdminBookings(ctx context.Context, f AdminBookingFilter) ([]model.BookingSummary, error) {
	args := []any{f.Limit + 1, f.Status, f.City, f.Date}
	where := `($2 = '' OR b.status = $2) AND ($3 = '' OR b.city_code = $3)
		AND ($4 = '' OR (b.slot_start AT TIME ZONE 'Asia/Kolkata')::date = NULLIF($4, '')::date)`
	if f.After != nil {
		where += ` AND (b.slot_start, b.id) < ($5, $6)`
		args = append(args, f.After.SlotStart, f.After.ID)
	}
	rows, err := s.db.Query(ctx, `SELECT `+summaryCols+`
		FROM doorstep.bookings b JOIN doorstep.services s ON s.id = b.service_id JOIN doorstep.categories c ON c.id = b.category_id
		WHERE `+where+` ORDER BY b.slot_start DESC, b.id DESC LIMIT $1`, args...)
	if err != nil {
		return nil, err
	}
	return collect(rows, scanSummary)
}

// BookingAssignments lists a booking's offers and assignments.
func (s *Store) BookingAssignments(ctx context.Context, id uuid.UUID) ([]model.AssignmentView, error) {
	rows, err := s.db.Query(ctx, `SELECT id, pro_id, role, status, offered_at, offer_expires_at, responded_at
		FROM doorstep.booking_assignments WHERE booking_id = $1 ORDER BY offered_at, id`, id)
	if err != nil {
		return nil, err
	}
	return collect(rows, func(r pgx.Rows) (model.AssignmentView, error) {
		var a model.AssignmentView
		err := r.Scan(&a.ID, &a.ProID, &a.Role, &a.Status, &a.OfferedAt, &a.OfferExpiresAt, &a.RespondedAt)
		a.OfferedAt, a.OfferExpiresAt = a.OfferedAt.UTC(), a.OfferExpiresAt.UTC()
		if a.RespondedAt != nil {
			t := a.RespondedAt.UTC()
			a.RespondedAt = &t
		}
		return a, err
	})
}

// BookingExtras lists a booking's extras.
func (s *Store) BookingExtras(ctx context.Context, id uuid.UUID) ([]model.Extra, error) {
	rows, err := s.db.Query(ctx, `SELECT id, booking_id, kind, rate_card_id, addon_id, name, quantity, unit_price_paise, total_paise,
		status, evidence_media_id, created_at FROM doorstep.booking_extras WHERE booking_id = $1 ORDER BY created_at, id`, id)
	if err != nil {
		return nil, err
	}
	return collect(rows, func(r pgx.Rows) (model.Extra, error) {
		var e model.Extra
		err := r.Scan(&e.ID, &e.BookingID, &e.Kind, &e.RateCardID, &e.AddonID, &e.Name, &e.Quantity, &e.UnitPricePaise,
			&e.TotalPaise, &e.Status, &e.EvidenceMediaID, &e.CreatedAt)
		e.CreatedAt = e.CreatedAt.UTC()
		return e, err
	})
}

// AdminStats are the dashboard counts; "today" is the IST day of now.
func (s *Store) AdminStats(ctx context.Context, now time.Time) (*model.AdminStats, error) {
	var st model.AdminStats
	err := s.db.QueryRow(ctx, `
		WITH day AS (SELECT (($1::timestamptz AT TIME ZONE 'Asia/Kolkata')::date) AS d)
		SELECT
		  (SELECT count(*) FROM doorstep.bookings b, day WHERE (b.slot_start AT TIME ZONE 'Asia/Kolkata')::date = day.d
		     AND b.status NOT IN ('expired', 'pending_payment'))::int,
		  (SELECT count(*) FROM doorstep.bookings WHERE status IN ('en_route', 'arrived', 'in_progress', 'awaiting_extras_payment'))::int,
		  (SELECT count(*) FROM doorstep.bookings WHERE status = 'confirmed' AND slot_start <= $1::timestamptz + INTERVAL '2 hours')::int,
		  (SELECT count(*) FROM doorstep.bookings WHERE needs_attention)::int,
		  (SELECT count(*) FROM doorstep.professionals WHERE status = 'approved')::int,
		  (SELECT count(*) FROM doorstep.professionals WHERE status = 'pending_verification')::int,
		  (SELECT count(*) FROM doorstep.pro_documents WHERE status = 'pending')::int,
		  (SELECT count(*) FROM doorstep.incidents WHERE status <> 'resolved')::int,
		  (SELECT COALESCE(sum(p.amount_paise), 0) FROM doorstep.payments p, day
		     WHERE p.status IN ('succeeded', 'partially_refunded', 'refunded')
		       AND (p.captured_at AT TIME ZONE 'Asia/Kolkata')::date = day.d)::bigint,
		  (SELECT COALESCE(sum(r.amount_paise), 0) FROM doorstep.refunds r, day
		     WHERE r.status = 'succeeded' AND (r.updated_at AT TIME ZONE 'Asia/Kolkata')::date = day.d)::bigint,
		  (SELECT COALESCE(sum(amount_paise), 0) FROM doorstep.outstanding WHERE status = 'open')::bigint`, now).Scan(
		&st.BookingsToday, &st.BookingsInProgress, &st.UnassignedWithin2h, &st.BookingsNeedingAttention, &st.ProsApproved,
		&st.ProsPendingVerification, &st.DocumentsPending, &st.IncidentsOpen, &st.GMVTodayPaise, &st.RefundsTodayPaise,
		&st.OutstandingPaise)
	if err != nil {
		return nil, err
	}
	return &st, nil
}
