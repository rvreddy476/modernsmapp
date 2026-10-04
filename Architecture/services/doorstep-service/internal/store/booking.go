package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/atpost/doorstep-service/internal/cancelrules"
	"github.com/atpost/doorstep-service/internal/events"
	"github.com/atpost/doorstep-service/internal/model"
	"github.com/atpost/doorstep-service/internal/slots"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// Bookings (A3): calendar-derived slots, holds, bookings and their money.
// The professional's calendar is doorstep.pro_calendar_blocks with
// EXCLUDE USING gist (pro_id WITH =, during WITH &&) WHERE active: a hold is
// inserted inside the booking's transaction and the constraint decides every
// race — a loser tries the next candidate, and with none left the whole
// booking rolls back (ErrSlotTaken).

// Booking sentinels.
var (
	ErrSlotTaken = errors.New("store: no candidate professional could be held for the slot")
	ErrQuoteGone = errors.New("store: the quote is no longer open")
	ErrReplay    = errors.New("store: idempotency key already used")
	// ErrPriceGone: a line's professional price is no longer approved and
	// live (withdrawn, superseded or the professional changed) (B1).
	ErrPriceGone = errors.New("store: a professional price is no longer bookable")
)

// WithClock pins the store's clock (payment events and workers); nil keeps
// time.Now.
func (s *Store) WithClock(now func() time.Time) *Store {
	s.now = now
	return s
}

func (s *Store) clock() time.Time {
	if s.now != nil {
		return s.now().UTC().Truncate(time.Microsecond)
	}
	return time.Now().UTC().Truncate(time.Microsecond)
}

// QuoteFacts is a quote with what booking it needs.
type QuoteFacts struct {
	Quote         model.Quote
	Lat, Lng      float64
	CategoryID    uuid.UUID
	CategorySlug  string
	GenderRule    string
	ServiceName   string
	RequiredSkill string
	// Bookable: the service and its category are still active.
	Bookable bool
}

// QuoteFacts loads one of the customer's quotes for slots and booking.
func (s *Store) QuoteFacts(ctx context.Context, customer, quoteID uuid.UUID) (*QuoteFacts, error) {
	q, err := s.Quote(ctx, quoteID, customer)
	if err != nil {
		return nil, err
	}
	f := &QuoteFacts{Quote: *q}
	err = s.db.QueryRow(ctx, `
		SELECT ST_Y(q.location::geometry), ST_X(q.location::geometry), c.id, c.slug, c.gender_rule, s.name, s.required_skill,
		       (s.active AND c.active)
		FROM doorstep.quotes q
		JOIN doorstep.services s ON s.id = q.service_id
		JOIN doorstep.categories c ON c.id = s.category_id
		WHERE q.id = $1`, quoteID).Scan(&f.Lat, &f.Lng, &f.CategoryID, &f.CategorySlug, &f.GenderRule, &f.ServiceName,
		&f.RequiredSkill, &f.Bookable)
	if err != nil {
		return nil, mapErr(err)
	}
	return f, nil
}

// ZoneFacts is a zone's city, travel buffer and whether it (and its city)
// is active.
func (s *Store) ZoneFacts(ctx context.Context, zoneID uuid.UUID) (city string, bufferMinutes int, active bool, err error) {
	err = s.db.QueryRow(ctx, `SELECT z.city_code, z.travel_buffer_minutes, (z.active AND c.active)
		FROM doorstep.zones z JOIN doorstep.cities c ON c.code = z.city_code WHERE z.id = $1`, zoneID).
		Scan(&city, &bufferMinutes, &active)
	return city, bufferMinutes, active, mapErr(err)
}

// SlotConfig is the category's active slot config, else the city default.
func (s *Store) SlotConfig(ctx context.Context, city string, categoryID uuid.UUID) (slots.Config, error) {
	var c slots.Config
	err := s.db.QueryRow(ctx, `
		SELECT (EXTRACT(HOUR FROM open_time) * 60 + EXTRACT(MINUTE FROM open_time))::int,
		       (EXTRACT(HOUR FROM close_time) * 60 + EXTRACT(MINUTE FROM close_time))::int,
		       slot_step_minutes, min_lead_minutes, horizon_days, hold_minutes
		FROM doorstep.slot_configs
		WHERE city_code = $1 AND active AND (category_id = $2 OR category_id IS NULL)
		ORDER BY category_id IS NULL, created_at DESC
		LIMIT 1`, city, categoryID).Scan(&c.OpenMinute, &c.CloseMinute, &c.StepMinutes, &c.LeadMinutes, &c.HorizonDays, &c.HoldMinutes)
	return c, mapErr(err)
}

// CandidateQuery selects the professionals a slot search considers.
type CandidateQuery struct {
	City     string
	ZoneID   uuid.UUID
	Lat, Lng float64
	Skill    string
	// From/To bound the blocks and job counts loaded (the horizon).
	From, To time.Time
	// ExcludeBooking leaves a booking's own blocks out (reschedule).
	ExcludeBooking *uuid.UUID
	// ServiceID reads each professional's same-day opt-in for the service
	// (B1); uuid.Nil reads none.
	ServiceID uuid.UUID
	// ProIDs, when set, loads only these professionals (a booking on the
	// professional the customer picked).
	ProIDs []uuid.UUID
}

// SlotCandidates loads every approved professional of the city with the
// facts the hard filters read (internal/slots applies them: status,
// incident, skill, gender, zone, radius, background check, hours, calendar,
// daily cap). Ordered by id.
func (s *Store) SlotCandidates(ctx context.Context, q CandidateQuery) ([]slots.Pro, error) {
	rows, err := s.db.Query(ctx, `
		WITH pt AS (SELECT ST_SetSRID(ST_MakePoint($4, $3), 4326)::geography AS g)
		SELECT p.id, p.user_id, p.status, p.incident_suspended, COALESCE(p.gender, ''),
		       EXISTS (SELECT 1 FROM doorstep.pro_skills ps WHERE ps.pro_id = p.id AND ps.skill_code = $5 AND ps.status = 'verified'),
		       EXISTS (SELECT 1 FROM doorstep.pro_zones z WHERE z.pro_id = p.id AND z.zone_id = $2),
		       (p.home_point IS NOT NULL AND ST_DWithin(p.home_point, pt.g, p.service_radius_m)),
		       COALESCE(ST_Distance(p.home_point, pt.g), 0)::float8,
		       p.max_jobs_per_day, p.rating_sum, p.rating_count, p.offers_received, p.offers_accepted,
		       p.cancellations_count + p.no_show_count, p.jobs_completed,
		       p.home_point IS NOT NULL, COALESCE(ST_Y(p.home_point::geometry), 0)::float8, COALESCE(ST_X(p.home_point::geometry), 0)::float8,
		       p.display_name, p.photo_media_id, p.on_duty, p.last_fix_at, p.last_point IS NOT NULL,
		       COALESCE(ST_Distance(p.last_point, pt.g), 0)::float8, p.service_radius_m,
		       COALESCE((SELECT st.same_day FROM doorstep.pro_service_settings st WHERE st.pro_id = p.id AND st.service_id = $6), FALSE)
		FROM doorstep.professionals p, pt
		WHERE p.city_code = $1 AND p.status = 'approved' AND ($7::uuid[] IS NULL OR p.id = ANY($7))
		ORDER BY p.id`, q.City, q.ZoneID, q.Lat, q.Lng, q.Skill, q.ServiceID, proIDs(q.ProIDs))
	if err != nil {
		return nil, err
	}
	pros, err := collect(rows, func(r pgx.Rows) (slots.Pro, error) {
		var p slots.Pro
		err := r.Scan(&p.ID, &p.UserID, &p.Status, &p.IncidentSuspended, &p.Gender, &p.SkillVerified, &p.InZone,
			&p.WithinRadius, &p.DistanceM, &p.MaxJobsPerDay, &p.RatingSum, &p.RatingCount, &p.OffersReceived,
			&p.OffersAccepted, &p.Cancellations, &p.JobsCompleted, &p.HasHome, &p.HomeLat, &p.HomeLng,
			&p.DisplayName, &p.PhotoMediaID, &p.OnDuty, &p.LastFixAt, &p.HasLive, &p.LiveDistanceM, &p.ServiceRadiusM, &p.SameDay)
		if p.LastFixAt != nil {
			t := p.LastFixAt.UTC()
			p.LastFixAt = &t
		}
		p.JobsByDay = map[string]int{}
		return p, err
	})
	if err != nil || len(pros) == 0 {
		return pros, err
	}
	ids := make([]uuid.UUID, len(pros))
	idx := make(map[uuid.UUID]int, len(pros))
	for i, p := range pros {
		ids[i], idx[p.ID] = p.ID, i
	}

	hours, err := s.db.Query(ctx, `
		SELECT pro_id, weekday, (EXTRACT(HOUR FROM start_time) * 60 + EXTRACT(MINUTE FROM start_time))::int,
		       (EXTRACT(HOUR FROM end_time) * 60 + EXTRACT(MINUTE FROM end_time))::int
		FROM doorstep.pro_weekly_hours WHERE pro_id = ANY($1)`, ids)
	if err != nil {
		return nil, err
	}
	for hours.Next() {
		var id uuid.UUID
		var w slots.Window
		if err := hours.Scan(&id, &w.Weekday, &w.Start, &w.End); err != nil {
			hours.Close()
			return nil, err
		}
		pros[idx[id]].Hours = append(pros[idx[id]].Hours, w)
	}
	hours.Close()
	if err := hours.Err(); err != nil {
		return nil, err
	}

	bg, err := s.db.Query(ctx, `
		SELECT pro_id, valid_from, valid_until FROM doorstep.background_checks
		WHERE pro_id = ANY($1) AND status = 'clear' AND valid_from IS NOT NULL AND valid_until IS NOT NULL`, ids)
	if err != nil {
		return nil, err
	}
	for bg.Next() {
		var id uuid.UUID
		var from, until time.Time
		if err := bg.Scan(&id, &from, &until); err != nil {
			bg.Close()
			return nil, err
		}
		pros[idx[id]].BackgroundClear = append(pros[idx[id]].BackgroundClear, slots.DateRange{
			From: istDate(from), Until: istDate(until)})
	}
	bg.Close()
	if err := bg.Err(); err != nil {
		return nil, err
	}

	blocks, err := s.db.Query(ctx, `
		SELECT k.pro_id, lower(k.during), upper(k.during), k.kind,
		       to_char(lower(k.during) AT TIME ZONE 'Asia/Kolkata', 'YYYY-MM-DD'),
		       b.id IS NOT NULL, COALESCE(ST_Y(b.location::geometry), 0)::float8, COALESCE(ST_X(b.location::geometry), 0)::float8
		FROM doorstep.pro_calendar_blocks k
		LEFT JOIN doorstep.bookings b ON b.id = k.booking_id
		WHERE k.pro_id = ANY($1) AND k.active AND k.during && tstzrange($2, $3, '[)')
		  AND ($4::uuid IS NULL OR k.booking_id IS DISTINCT FROM $4)`,
		ids, q.From.AddDate(0, 0, -7), q.To, q.ExcludeBooking)
	if err != nil {
		return nil, err
	}
	for blocks.Next() {
		var id uuid.UUID
		var iv slots.Interval
		var kind, day string
		var located bool
		var lat, lng float64
		if err := blocks.Scan(&id, &iv.Start, &iv.End, &kind, &day, &located, &lat, &lng); err != nil {
			blocks.Close()
			return nil, err
		}
		p := &pros[idx[id]]
		p.Blocks = append(p.Blocks, iv)
		if kind == "hold" || kind == "booking" {
			p.JobsByDay[day]++
			if located {
				p.Jobs = append(p.Jobs, slots.JobAt{Start: iv.Start, Lat: lat, Lng: lng})
			}
		}
	}
	blocks.Close()
	return pros, blocks.Err()
}

// istDate reads a DATE column as that date at IST midnight.
func istDate(d time.Time) time.Time {
	return time.Date(d.Year(), d.Month(), d.Day(), 0, 0, 0, 0, slots.IST)
}

// OutstandingDue is the customer's unpaid extras (open outstanding rows).
func (s *Store) OutstandingDue(ctx context.Context, customer uuid.UUID) (int64, []uuid.UUID, error) {
	var total int64
	var bills []uuid.UUID
	err := s.db.QueryRow(ctx, `SELECT COALESCE(sum(amount_paise), 0)::bigint, COALESCE(array_agg(extras_bill_id ORDER BY created_at), '{}')
		FROM doorstep.outstanding WHERE customer_user_id = $1 AND status = 'open'`, customer).Scan(&total, &bills)
	return total, bills, err
}

// AddressSnapshot is the readable part of the address a booking keeps
// (address_snapshot). The street lines are in bookings.address_sealed.
type AddressSnapshot struct {
	AddressID uuid.UUID `json:"address_id"`
	Label     string    `json:"label"`
	Locality  string    `json:"locality"`
	CityCode  string    `json:"city_code"`
	Pincode   string    `json:"pincode"`
	Lat       float64   `json:"lat"`
	Lng       float64   `json:"lng"`
	ZoneID    uuid.UUID `json:"zone_id"`
	CreatedAt time.Time `json:"created_at"`
}

// NewBooking is a booking to create with its hold.
type NewBooking struct {
	ID             uuid.UUID
	Customer       uuid.UUID
	IdempotencyKey string
	QuoteID        uuid.UUID
	CityCode       string
	ZoneID         uuid.UUID
	CategoryID     uuid.UUID
	CategorySlug   string
	ServiceID      uuid.UUID
	Address        AddressSnapshot
	AddressSealed  []byte
	SlotStart      time.Time
	SlotEnd        time.Time
	BlockStart     time.Time // the slot start; an ASAP job blocks from the booking time
	BlockEnd       time.Time // slot end + the zone's travel buffer
	Asap           bool
	Duration       int
	GenderRule     string
	RequireFemale  bool
	Notes          *string
	TotalPaise     int64
	TaxablePaise   int64
	TaxPaise       int64
	Items          []model.QuoteLine
	HoldExpiresAt  time.Time
	// ProID is the professional the customer picked (the quote's): the hold
	// goes on them and nobody else; the exclusion constraint decides.
	ProID     uuid.UUID
	PaymentID uuid.UUID
	IntentKey string
	At        time.Time
}

// CreateBooking inserts the booking (pending_payment) on the professional
// the customer picked, consumes the quote, checks every line still carries
// that professional's approved, live price, holds the professional (the
// exclusion constraint decides), opens the payment row and enqueues
// doorstep.booking.created — one transaction. ErrReplay: the customer
// already used the idempotency key; ErrQuoteGone: the quote was consumed or
// expired meanwhile; ErrPriceGone: a price is no longer approved and live;
// ErrSlotTaken: the professional's calendar refused the hold.
func (s *Store) CreateBooking(ctx context.Context, nb NewBooking) (uuid.UUID, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return uuid.Nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	snap, err := json.Marshal(nb.Address)
	if err != nil {
		return uuid.Nil, err
	}
	tag, err := tx.Exec(ctx, `
		INSERT INTO doorstep.bookings (id, customer_user_id, idempotency_key, quote_id, city_code, zone_id, category_id, service_id,
		    address_id, address_snapshot, address_sealed, locality, location, slot_start, slot_end, duration_minutes, status,
		    gender_rule, require_female_pro, notes, total_paise, taxable_paise, tax_paise, hold_expires_at, created_at, updated_at,
		    reserved_pro_id, asap)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, ST_SetSRID(ST_MakePoint($14, $13), 4326)::geography,
		    $15, $16, $17, 'pending_payment', $18, $19, $20, $21, $22, $23, $24, $25, $25, $26, $27)
		ON CONFLICT (customer_user_id, idempotency_key) DO NOTHING`,
		nb.ID, nb.Customer, nb.IdempotencyKey, nb.QuoteID, nb.CityCode, nb.ZoneID, nb.CategoryID, nb.ServiceID,
		nb.Address.AddressID, snap, nb.AddressSealed, nb.Address.Locality, nb.Address.Lat, nb.Address.Lng,
		nb.SlotStart, nb.SlotEnd, nb.Duration, nb.GenderRule, nb.RequireFemale, nb.Notes, nb.TotalPaise, nb.TaxablePaise,
		nb.TaxPaise, nb.HoldExpiresAt, nb.At, nb.ProID, nb.Asap)
	if err != nil {
		return uuid.Nil, mapErr(err)
	}
	if tag.RowsAffected() == 0 {
		return uuid.Nil, ErrReplay
	}
	tag, err = tx.Exec(ctx, `UPDATE doorstep.quotes SET status = 'consumed', consumed_at = $3
		WHERE id = $1 AND customer_user_id = $2 AND status = 'open' AND expires_at > $3`, nb.QuoteID, nb.Customer, nb.At)
	if err != nil {
		return uuid.Nil, err
	}
	if tag.RowsAffected() == 0 {
		return uuid.Nil, ErrQuoteGone
	}
	if err := insertItemsTx(ctx, tx, nb.ID, nb.Items); err != nil {
		return uuid.Nil, err
	}
	if err := requireLivePricesTx(ctx, tx, nb.ID, nb.ProID, nb.At); err != nil {
		return uuid.Nil, err
	}
	if err := historyTx(ctx, tx, nb.ID, nil, "pending_payment", "customer", &nb.Customer, nil, nb.At); err != nil {
		return uuid.Nil, err
	}
	// The hold goes on the picked professional only: nobody else is tried.
	pro, err := holdFirstTx(ctx, tx, nb.ID, []uuid.UUID{nb.ProID}, "hold", nb.BlockStart, nb.BlockEnd, &nb.HoldExpiresAt)
	if err != nil {
		return uuid.Nil, err
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO doorstep.payments (id, booking_id, reference_type, reference_id, intent_key, amount_paise, status, created_at, updated_at)
		VALUES ($1, $2, 'doorstep_booking', $2, $3, $4, 'created', $5, $5)`,
		nb.PaymentID, nb.ID, nb.IntentKey, nb.TotalPaise, nb.At); err != nil {
		return uuid.Nil, mapErr(err)
	}
	core := events.BookingCore{BookingID: nb.ID, CustomerUserID: nb.Customer, Status: "pending_payment", CityCode: nb.CityCode,
		CategorySlug: nb.CategorySlug, ServiceID: nb.ServiceID, SlotStart: nb.SlotStart.UTC(), SlotEnd: nb.SlotEnd.UTC()}
	if err := s.enqueueBookingEvent(ctx, tx, events.BookingCreated, core, nb.At,
		events.BookingCreatedData{BookingCore: core, TotalPaise: nb.TotalPaise, HoldExpiresAt: nb.HoldExpiresAt.UTC()}); err != nil {
		return uuid.Nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return uuid.Nil, mapErr(err)
	}
	return pro, nil
}

// holdFirstTx inserts a calendar block for the first candidate the
// exclusion constraint admits, each attempt in its own savepoint. kind is
// hold (with expiresAt) or booking. ErrSlotTaken when every candidate lost.
func holdFirstTx(ctx context.Context, tx pgx.Tx, bookingID uuid.UUID, candidates []uuid.UUID, kind string,
	start, end time.Time, expiresAt *time.Time) (uuid.UUID, error) {
	for _, pro := range candidates {
		sp, err := tx.Begin(ctx)
		if err != nil {
			return uuid.Nil, err
		}
		_, err = sp.Exec(ctx, `INSERT INTO doorstep.pro_calendar_blocks (pro_id, kind, booking_id, during, expires_at)
			VALUES ($1, $2, $3, tstzrange($4, $5, '[)'), $6)`, pro, kind, bookingID, start, end, expiresAt)
		var pg *pgconn.PgError
		if errors.As(err, &pg) && pg.Code == "23P01" {
			if rbErr := sp.Rollback(ctx); rbErr != nil {
				return uuid.Nil, rbErr
			}
			continue // another booking holds this professional: next candidate
		}
		if err != nil {
			_ = sp.Rollback(ctx)
			return uuid.Nil, mapErr(err)
		}
		if err := sp.Commit(ctx); err != nil {
			return uuid.Nil, err
		}
		return pro, nil
	}
	return uuid.Nil, ErrSlotTaken
}

// historyTx records one status change.
func historyTx(ctx context.Context, tx pgx.Tx, bookingID uuid.UUID, from *string, to, actorKind string, actorID *uuid.UUID, reason *string, at time.Time) error {
	_, err := tx.Exec(ctx, `INSERT INTO doorstep.booking_status_history (booking_id, from_status, to_status, actor_kind, actor_id, reason, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7)`, bookingID, from, to, actorKind, actorID, reason, at)
	return err
}

func (s *Store) enqueueBookingEvent(ctx context.Context, tx pgx.Tx, eventType string, core events.BookingCore, at time.Time, data any) error {
	key, payload, err := events.Booking(eventType, core, at, data)
	if err != nil {
		return err
	}
	return s.events.Enqueue(ctx, tx, eventType, key, payload)
}

// bookingCoreTx reads a booking's event core after a change; pro_user_id is
// the accepted professional, if any.
func bookingCoreTx(ctx context.Context, q querier, id uuid.UUID) (events.BookingCore, error) {
	var c events.BookingCore
	err := q.QueryRow(ctx, `
		SELECT b.id, b.customer_user_id, b.status, b.city_code, c.slug, b.service_id, b.slot_start, b.slot_end, b.parent_booking_id,
		       (SELECT p.user_id FROM doorstep.booking_assignments a JOIN doorstep.professionals p ON p.id = a.pro_id
		         WHERE a.booking_id = b.id AND a.role = 'lead' AND a.status IN ('accepted', 'completed')
		         ORDER BY a.updated_at DESC LIMIT 1)
		FROM doorstep.bookings b JOIN doorstep.categories c ON c.id = b.category_id WHERE b.id = $1`, id).Scan(
		&c.BookingID, &c.CustomerUserID, &c.Status, &c.CityCode, &c.CategorySlug, &c.ServiceID, &c.SlotStart, &c.SlotEnd,
		&c.ParentBookingID, &c.ProUserID)
	c.SlotStart, c.SlotEnd = c.SlotStart.UTC(), c.SlotEnd.UTC()
	return c, mapErr(err)
}

// BookingKeyed is a booking found by its idempotency key.
type BookingKeyed struct {
	ID        uuid.UUID
	QuoteID   uuid.UUID
	AddressID uuid.UUID
	SlotStart time.Time
	Asap      bool
}

// BookingByKey finds the customer's booking made with an idempotency key.
func (s *Store) BookingByKey(ctx context.Context, customer uuid.UUID, key string) (*BookingKeyed, error) {
	var b BookingKeyed
	var quote, addr *uuid.UUID
	err := s.db.QueryRow(ctx, `SELECT id, quote_id, address_id, slot_start, asap FROM doorstep.bookings
		WHERE customer_user_id = $1 AND idempotency_key = $2`, customer, key).Scan(&b.ID, &quote, &addr, &b.SlotStart, &b.Asap)
	if err != nil {
		return nil, mapErr(err)
	}
	if quote != nil {
		b.QuoteID = *quote
	}
	if addr != nil {
		b.AddressID = *addr
	}
	return &b, nil
}

// PaymentRow is a doorstep.payments row.
type PaymentRow struct {
	ID            uuid.UUID
	BookingID     uuid.UUID
	ReferenceType string
	ReferenceID   uuid.UUID
	IntentKey     string
	IntentID      *string
	AmountPaise   int64
	Status        string
	RefundedPaise int64
	Checkout      map[string]string
	CreatedAt     time.Time
}

const paymentCols = `id, booking_id, reference_type, reference_id, intent_key, payments_intent_id, amount_paise, status,
	refunded_paise, checkout, created_at`

func scanPayment(r pgx.Row) (PaymentRow, error) {
	var p PaymentRow
	var checkout []byte
	err := r.Scan(&p.ID, &p.BookingID, &p.ReferenceType, &p.ReferenceID, &p.IntentKey, &p.IntentID, &p.AmountPaise,
		&p.Status, &p.RefundedPaise, &checkout, &p.CreatedAt)
	if err != nil {
		return p, err
	}
	p.CreatedAt = p.CreatedAt.UTC()
	p.Checkout = map[string]string{}
	if len(checkout) > 0 {
		if err := json.Unmarshal(checkout, &p.Checkout); err != nil {
			return p, fmt.Errorf("payment %s checkout: %w", p.ID, err)
		}
	}
	return p, nil
}

// BookingPayment is the booking's doorstep_booking payment row.
func (s *Store) BookingPayment(ctx context.Context, bookingID uuid.UUID) (*PaymentRow, error) {
	p, err := scanPayment(s.db.QueryRow(ctx, `SELECT `+paymentCols+` FROM doorstep.payments
		WHERE booking_id = $1 AND reference_type = 'doorstep_booking'`, bookingID))
	if err != nil {
		return nil, mapErr(err)
	}
	return &p, nil
}

// AttachIntent records the payments-service intent and its client session
// on the payment row; created becomes pending. The intent id never changes
// once stored (the intent key is idempotent at payments-service).
func (s *Store) AttachIntent(ctx context.Context, paymentID uuid.UUID, intentID string, checkout map[string]string, at time.Time) error {
	raw, err := json.Marshal(checkout)
	if err != nil {
		return err
	}
	tag, err := s.db.Exec(ctx, `
		UPDATE doorstep.payments
		   SET payments_intent_id = COALESCE(payments_intent_id, $2), checkout = $3,
		       status = CASE WHEN status = 'created' THEN 'pending' ELSE status END, updated_at = $4
		 WHERE id = $1 AND (payments_intent_id IS NULL OR payments_intent_id = $2)`, paymentID, intentID, raw, at)
	if err != nil {
		return mapErr(err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("%w: payment %s already carries another intent", ErrConflict, paymentID)
	}
	return nil
}

// CancellationRules lists a city's active cancellation rules.
func (s *Store) CancellationRules(ctx context.Context, city string) ([]cancelrules.Rule, error) {
	rows, err := s.db.Query(ctx, `SELECT id, category_id, stage, minutes_before_lt, fee_paise, allowed, sort_order
		FROM doorstep.cancellation_rules WHERE city_code = $1 AND active ORDER BY sort_order, id`, city)
	if err != nil {
		return nil, err
	}
	return collect(rows, func(r pgx.Rows) (cancelrules.Rule, error) {
		var x cancelrules.Rule
		err := r.Scan(&x.ID, &x.CategoryID, &x.Stage, &x.MinutesBeforeLT, &x.FeePaise, &x.Allowed, &x.SortOrder)
		return x, err
	})
}

// proIDs is nil (every professional) for an empty filter.
func proIDs(ids []uuid.UUID) []uuid.UUID {
	if len(ids) == 0 {
		return nil
	}
	return ids
}

// insertItemsTx writes a booking's priced lines (the professional's price
// row in price_id and pro_price_id; the database refuses a line whose price
// is not an approved row of the booking's reserved professional).
func insertItemsTx(ctx context.Context, tx pgx.Tx, bookingID uuid.UUID, items []model.QuoteLine) error {
	for i, l := range items {
		if _, err := tx.Exec(ctx, `
			INSERT INTO doorstep.booking_items (booking_id, line_no, kind, ref_id, price_id, pro_price_id, name, unit, quantity,
			                                    unit_price_paise, line_total_paise, taxable_paise, tax_paise, tax_rate_bps, gst_category, sac)
			VALUES ($1, $2, $3, $4, $5, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15)`,
			bookingID, i+1, l.Kind, l.RefID, l.PriceID, l.Name, l.Unit, l.Quantity, l.UnitPricePaise, l.LineTotalPaise,
			l.TaxablePaise, l.TaxPaise, l.TaxRateBPS, l.GSTCategory, l.SAC); err != nil {
			var pg *pgconn.PgError
			if errors.As(err, &pg) && pg.ConstraintName == "ck_doorstep_line_price_approved" {
				return ErrPriceGone
			}
			return mapErr(err)
		}
	}
	return nil
}

// requireLivePricesTx: every line of the booking carries an approved price
// of pro that is live at `at` (else ErrPriceGone). Only approved prices are
// bookable (B1).
func requireLivePricesTx(ctx context.Context, tx pgx.Tx, bookingID, pro uuid.UUID, at time.Time) error {
	var lines, live int
	if err := tx.QueryRow(ctx, `
		SELECT count(*), count(*) FILTER (WHERE EXISTS (
		    SELECT 1 FROM doorstep.pro_service_prices pp WHERE pp.id = bi.pro_price_id AND pp.pro_id = $2 AND pp.status = 'approved'
		       AND pp.effective_from <= $3 AND (pp.effective_to IS NULL OR pp.effective_to > $3)))
		FROM doorstep.booking_items bi WHERE bi.booking_id = $1`, bookingID, pro, at).Scan(&lines, &live); err != nil {
		return err
	}
	if lines == 0 || live != lines {
		return ErrPriceGone
	}
	return nil
}
