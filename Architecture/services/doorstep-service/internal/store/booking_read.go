package store

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/atpost/doorstep-service/internal/model"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// BookingRecord is a booking as stored, with the wire booking assembled
// except what the service decides: the address lines (sealed here), the
// OTPs and can_cancel / can_reschedule.
type BookingRecord struct {
	Booking         model.Booking
	CustomerUserID  uuid.UUID
	CategoryID      uuid.UUID
	GenderRule      string
	RequiredSkill   string
	ReservedProID   *uuid.UUID
	AddressSealed   []byte
	RescheduleCount int
	NeedsAttention  bool
	AttentionReason *string
	// ProLateAt: the professional was late (A4); free cancellation.
	ProLateAt     *time.Time
	BufferMinutes int
	Lat, Lng      float64
	// B1: the professionals the customer may not pick again, and the
	// pending change of professional (if any; its payment intent is the
	// service's to attach).
	ExcludedProIDs  []uuid.UUID
	PendingChangeID *uuid.UUID
	CityState       string
	// History is the full status history (actor kinds and reasons are for
	// the admin view; the customer gets StatusSteps).
	History []model.HistoryEntry
}

// BookingRecord reads one booking. customer narrows it to that customer's
// (ErrNotFound for anyone else's); nil is the admin read.
func (s *Store) BookingRecord(ctx context.Context, id uuid.UUID, customer *uuid.UUID) (*BookingRecord, error) {
	r := &BookingRecord{}
	b := &r.Booking
	var snap []byte
	err := s.db.QueryRow(ctx, `
		SELECT b.id, b.customer_user_id, b.status, b.service_id, s.name, c.slug, c.id, b.city_code, b.zone_id,
		       b.slot_start, b.slot_end, b.duration_minutes, b.require_female_pro, b.gender_rule, s.required_skill,
		       b.total_paise, b.taxable_paise, b.tax_paise, b.paid_paise, b.refunded_paise, b.cancellation_fee_paise,
		       b.extras_total_paise, b.hold_expires_at, b.address_snapshot, b.address_sealed, b.parent_booking_id,
		       b.reschedule_count, b.reserved_pro_id, b.needs_attention, b.attention_reason, b.pro_late_at,
		       ST_Y(b.location::geometry), ST_X(b.location::geometry), z.travel_buffer_minutes, b.created_at, b.updated_at,
		       COALESCE((SELECT sum(o.amount_paise) FROM doorstep.outstanding o WHERE o.booking_id = b.id AND o.status = 'open'), 0)::bigint,
		       b.asap, b.choice_deadline, b.unavailable_cause, b.excluded_pro_ids, ci.state_code,
		       (SELECT pc.id FROM doorstep.booking_pro_changes pc WHERE pc.booking_id = b.id AND pc.status = 'pending_payment')
		FROM doorstep.bookings b
		JOIN doorstep.services s ON s.id = b.service_id
		JOIN doorstep.categories c ON c.id = b.category_id
		JOIN doorstep.zones z ON z.id = b.zone_id
		JOIN doorstep.cities ci ON ci.code = b.city_code
		WHERE b.id = $1 AND ($2::uuid IS NULL OR b.customer_user_id = $2)`, id, customer).Scan(
		&b.ID, &r.CustomerUserID, &b.Status, &b.ServiceID, &b.ServiceName, &b.CategorySlug, &r.CategoryID, &b.CityCode, &b.ZoneID,
		&b.SlotStart, &b.SlotEnd, &b.DurationMinutes, &b.RequireFemalePro, &r.GenderRule, &r.RequiredSkill,
		&b.TotalPaise, &b.TaxablePaise, &b.TaxPaise, &b.PaidPaise, &b.RefundedPaise, &b.CancellationFeePaise,
		&b.ExtrasTotalPaise, &b.HoldExpiresAt, &snap, &r.AddressSealed, &b.ParentBookingID,
		&r.RescheduleCount, &r.ReservedProID, &r.NeedsAttention, &r.AttentionReason, &r.ProLateAt,
		&r.Lat, &r.Lng, &r.BufferMinutes, &b.CreatedAt, &b.UpdatedAt, &b.OutstandingPaise,
		&b.Asap, &b.ChoiceDeadline, &b.UnavailableCause, &r.ExcludedProIDs, &r.CityState, &r.PendingChangeID)
	if err != nil {
		return nil, mapErr(err)
	}
	b.SlotStart, b.SlotEnd, b.CreatedAt, b.UpdatedAt = b.SlotStart.UTC(), b.SlotEnd.UTC(), b.CreatedAt.UTC(), b.UpdatedAt.UTC()
	utcPtr(&b.HoldExpiresAt)
	utcPtr(&b.ChoiceDeadline)
	if r.ExcludedProIDs == nil {
		r.ExcludedProIDs = []uuid.UUID{}
	}
	var a AddressSnapshot
	if err := json.Unmarshal(snap, &a); err != nil {
		return nil, fmt.Errorf("booking %s address snapshot: %w", id, err)
	}
	b.Address = model.Address{ID: a.AddressID, Label: a.Label, Locality: a.Locality, CityCode: a.CityCode, Pincode: a.Pincode,
		Lat: a.Lat, Lng: a.Lng, ZoneID: a.ZoneID, CreatedAt: a.CreatedAt.UTC()}

	rows, err := s.db.Query(ctx, `
		SELECT kind, ref_id, COALESCE(price_id, '00000000-0000-0000-0000-000000000000'::uuid), name, unit, quantity, unit_price_paise,
		       line_total_paise, taxable_paise, tax_paise, tax_rate_bps, gst_category, sac
		FROM doorstep.booking_items WHERE booking_id = $1 ORDER BY line_no`, id)
	if err != nil {
		return nil, err
	}
	b.Items, err = collect(rows, func(r pgx.Rows) (model.QuoteLine, error) {
		var l model.QuoteLine
		err := r.Scan(&l.Kind, &l.RefID, &l.PriceID, &l.Name, &l.Unit, &l.Quantity, &l.UnitPricePaise, &l.LineTotalPaise,
			&l.TaxablePaise, &l.TaxPaise, &l.TaxRateBPS, &l.GSTCategory, &l.SAC)
		return l, err
	})
	if err != nil {
		return nil, err
	}

	rows, err = s.db.Query(ctx, `SELECT from_status, to_status, actor_kind, reason, created_at
		FROM doorstep.booking_status_history WHERE booking_id = $1 ORDER BY id`, id)
	if err != nil {
		return nil, err
	}
	r.History, err = collect(rows, func(r pgx.Rows) (model.HistoryEntry, error) {
		var h model.HistoryEntry
		err := r.Scan(&h.FromStatus, &h.ToStatus, &h.ActorKind, &h.Reason, &h.CreatedAt)
		h.CreatedAt = h.CreatedAt.UTC()
		return h, err
	})
	if err != nil {
		return nil, err
	}
	b.StatusHistory = make([]model.StatusStep, 0, len(r.History))
	for _, h := range r.History {
		b.StatusHistory = append(b.StatusHistory, model.StatusStep{FromStatus: h.FromStatus, ToStatus: h.ToStatus, CreatedAt: h.CreatedAt})
	}

	if b.Photos, err = s.bookingPhotos(ctx, id); err != nil {
		return nil, err
	}

	// The professional: the one who accepted, else the one the customer
	// picked (B1: the customer chose them, so the card is theirs to see):
	// first name, photo, rating; never a phone.
	var name string
	var photo *string
	var sum int64
	var count, jobs int
	err = s.db.QueryRow(ctx, `
		SELECT p.display_name, p.photo_media_id, p.rating_sum, p.rating_count, p.jobs_completed
		FROM doorstep.professionals p
		WHERE p.id = COALESCE((SELECT a.pro_id FROM doorstep.booking_assignments a
		                        WHERE a.booking_id = $1 AND a.role = 'lead' AND a.status IN ('accepted', 'completed')
		                        ORDER BY a.updated_at DESC LIMIT 1),
		                       (SELECT b.reserved_pro_id FROM doorstep.bookings b WHERE b.id = $1))`, id).Scan(&name, &photo, &sum, &count, &jobs)
	switch mapped := mapErr(err); {
	case mapped == ErrNotFound:
	case err != nil:
		return nil, err
	default:
		pro := &model.BookingProfessional{FirstName: firstName(name), PhotoMediaID: photo, JobsCompleted: jobs}
		if count > 0 {
			avg := float64(sum) / float64(count)
			avg = float64(int(avg*10+0.5)) / 10
			pro.RatingAvg = &avg
		}
		b.Professional = pro
	}
	return r, nil
}

func firstName(display string) string {
	f := strings.Fields(display)
	if len(f) == 0 {
		return ""
	}
	return f[0]
}

func (s *Store) bookingPhotos(ctx context.Context, id uuid.UUID) ([]model.Photo, error) {
	rows, err := s.db.Query(ctx, `SELECT id, booking_id, phase, media_id, created_at FROM doorstep.booking_photos
		WHERE booking_id = $1 ORDER BY created_at, id`, id)
	if err != nil {
		return nil, err
	}
	return collect(rows, func(r pgx.Rows) (model.Photo, error) {
		var p model.Photo
		err := r.Scan(&p.ID, &p.BookingID, &p.Phase, &p.MediaID, &p.CreatedAt)
		p.CreatedAt = p.CreatedAt.UTC()
		return p, err
	})
}

// BookingCursor is a keyset position (slot_start, id).
type BookingCursor struct {
	SlotStart time.Time
	ID        uuid.UUID
}

// Customer list modes.
const (
	ListUpcoming = "upcoming"
	ListPast     = "past"
	ListAll      = "all"
)

// ActiveStatuses are the statuses of a booking that is still happening.
var ActiveStatuses = []string{"pending_payment", "confirmed", "assigned", "en_route", "arrived", "in_progress", "awaiting_extras_payment"}

const summaryCols = `b.id, b.status, s.name, c.slug, b.slot_start, b.slot_end, b.total_paise, b.created_at`

func scanSummary(r pgx.Rows) (model.BookingSummary, error) {
	var b model.BookingSummary
	err := r.Scan(&b.ID, &b.Status, &b.ServiceName, &b.CategorySlug, &b.SlotStart, &b.SlotEnd, &b.TotalPaise, &b.CreatedAt)
	b.SlotStart, b.SlotEnd, b.CreatedAt = b.SlotStart.UTC(), b.SlotEnd.UTC(), b.CreatedAt.UTC()
	return b, err
}

// CustomerBookings pages a customer's bookings: upcoming (active, soonest
// first), past (finished, latest first) or all (latest first). limit+1 rows
// come back when there is a next page.
func (s *Store) CustomerBookings(ctx context.Context, customer uuid.UUID, mode string, after *BookingCursor, limit int) ([]model.BookingSummary, error) {
	where := `b.customer_user_id = $1`
	order := `b.slot_start DESC, b.id DESC`
	cmp := `<`
	switch mode {
	case ListUpcoming:
		where += ` AND b.status = ANY($2)`
		order, cmp = `b.slot_start, b.id`, `>`
	case ListPast:
		where += ` AND NOT (b.status = ANY($2))`
	default:
		where += ` AND ($2::text[] IS NOT NULL)`
	}
	args := []any{customer, ActiveStatuses, limit + 1}
	if after != nil {
		where += ` AND (b.slot_start, b.id) ` + cmp + ` ($4, $5)`
		args = append(args, after.SlotStart, after.ID)
	}
	rows, err := s.db.Query(ctx, `SELECT `+summaryCols+`
		FROM doorstep.bookings b JOIN doorstep.services s ON s.id = b.service_id JOIN doorstep.categories c ON c.id = b.category_id
		WHERE `+where+` ORDER BY `+order+` LIMIT $3`, args...)
	if err != nil {
		return nil, err
	}
	return collect(rows, scanSummary)
}

// BookingMoney is a booking's payment rows and refunds.
func (s *Store) BookingMoney(ctx context.Context, bookingID uuid.UUID) ([]PaymentRow, []model.Refund, error) {
	rows, err := s.db.Query(ctx, `SELECT `+paymentCols+` FROM doorstep.payments WHERE booking_id = $1 ORDER BY created_at, id`, bookingID)
	if err != nil {
		return nil, nil, err
	}
	pays, err := collect(rows, func(r pgx.Rows) (PaymentRow, error) { return scanPayment(r) })
	if err != nil {
		return nil, nil, err
	}
	rows, err = s.db.Query(ctx, `SELECT id, payment_id, cause, amount_paise, status, created_at FROM doorstep.refunds
		WHERE booking_id = $1 ORDER BY created_at, id`, bookingID)
	if err != nil {
		return nil, nil, err
	}
	refunds, err := collect(rows, scanRefund)
	return pays, refunds, err
}

func scanRefund(r pgx.Rows) (model.Refund, error) {
	var f model.Refund
	err := r.Scan(&f.ID, &f.PaymentID, &f.Cause, &f.AmountPaise, &f.Status, &f.CreatedAt)
	f.CreatedAt = f.CreatedAt.UTC()
	return f, err
}
