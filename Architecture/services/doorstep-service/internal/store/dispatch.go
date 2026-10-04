package store

import (
	"context"
	"errors"
	"time"

	"github.com/atpost/doorstep-service/internal/dispatch"
	"github.com/atpost/doorstep-service/internal/events"
	"github.com/atpost/doorstep-service/internal/slots"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Dispatch (A4): offers to professionals and every reassignment. One
// transaction (Redispatch) closes the professional's assignment, sends the
// booking back to confirmed when it had been assigned, releases the old
// calendar block and inserts the next professional's (the exclusion
// constraint decides, candidate by candidate) and opens the new offer — so a
// booking never holds two professionals and never loses its block between
// "release one" and "hold the next". Lock order everywhere: the booking row,
// then its assignment rows (the same order as CancelBooking).

// Dispatch sentinels.
var (
	// ErrStale: the booking changed since dispatch read it (status, version
	// or the assignment being closed); the caller re-reads and retries or
	// gives up.
	ErrStale = errors.New("store: the booking changed under dispatch")
	// ErrOfferNotFound: no such offer for this professional.
	ErrOfferNotFound = errors.New("store: offer not found")
	// ErrOfferExpired: the offer lapsed.
	ErrOfferExpired = errors.New("store: offer expired")
	// ErrOfferTaken: the offer was declined, withdrawn or superseded, or the
	// booking is no longer waiting for this professional.
	ErrOfferTaken = errors.New("store: offer no longer open")
)

// IneligibleError: the professional may not take this job (re-checked at
// accept). Reason is one of not_approved, suspended, gender, background.
type IneligibleError struct{ Reason string }

func (e *IneligibleError) Error() string { return "store: professional not eligible: " + e.Reason }

// AssignmentRef is one assignment row.
type AssignmentRef struct {
	ID        uuid.UUID
	BookingID uuid.UUID
	ProID     uuid.UUID
	ProUserID uuid.UUID
	Status    string
	ExpiresAt time.Time
}

// DispatchFacts is what dispatch needs to know about one booking.
type DispatchFacts struct {
	BookingID     uuid.UUID
	Customer      uuid.UUID
	Status        string
	Version       int
	CityCode      string
	ZoneID        uuid.UUID
	CategoryID    uuid.UUID
	CategorySlug  string
	Skill         string
	GenderRule    string
	RequireFemale bool
	Lat, Lng      float64
	Locality      string
	SlotStart     time.Time
	SlotEnd       time.Time
	Duration      int
	BufferMinutes int
	ReservedProID *uuid.UUID
	Windows       dispatch.Windows
	RescueUntil   *time.Time
	RescueCause   *string
	Exhausted     bool
	// Asap: a same-day job (B1): its offer lapses after 3 minutes.
	Asap bool
	// Live is the open offer or the accepted assignment, if any.
	Live *AssignmentRef
	// Excluded are the professionals who already had this booking and let
	// it go (declined, expired, gave it back, did not turn up, released).
	Excluded []uuid.UUID
}

// Request is the slot request the hard filters apply.
func (f *DispatchFacts) Request() slots.Request {
	return slots.Request{DurationMinutes: f.Duration, BufferMinutes: f.BufferMinutes, GenderRule: f.GenderRule,
		RequireFemale: f.RequireFemale}
}

// DispatchFacts reads one booking for dispatch.
func (s *Store) DispatchFacts(ctx context.Context, id uuid.UUID) (*DispatchFacts, error) {
	f := &DispatchFacts{}
	var exhausted *time.Time
	err := s.db.QueryRow(ctx, `
		SELECT b.id, b.customer_user_id, b.status, b.version, b.city_code, b.zone_id, b.category_id, c.slug, s.required_skill,
		       b.gender_rule, b.require_female_pro, ST_Y(b.location::geometry), ST_X(b.location::geometry), b.locality,
		       b.slot_start, b.slot_end, b.duration_minutes, z.travel_buffer_minutes, b.reserved_pro_id,
		       ci.offer_window_far_minutes, ci.offer_window_near_minutes, ci.offer_far_threshold_minutes,
		       b.rescue_until, b.rescue_cause, b.dispatch_exhausted_at, b.asap
		FROM doorstep.bookings b
		JOIN doorstep.categories c ON c.id = b.category_id
		JOIN doorstep.services s ON s.id = b.service_id
		JOIN doorstep.zones z ON z.id = b.zone_id
		JOIN doorstep.cities ci ON ci.code = b.city_code
		WHERE b.id = $1`, id).Scan(&f.BookingID, &f.Customer, &f.Status, &f.Version, &f.CityCode, &f.ZoneID, &f.CategoryID,
		&f.CategorySlug, &f.Skill, &f.GenderRule, &f.RequireFemale, &f.Lat, &f.Lng, &f.Locality, &f.SlotStart, &f.SlotEnd,
		&f.Duration, &f.BufferMinutes, &f.ReservedProID, &f.Windows.FarMinutes, &f.Windows.NearMinutes,
		&f.Windows.FarThresholdMinutes, &f.RescueUntil, &f.RescueCause, &exhausted, &f.Asap)
	if err != nil {
		return nil, mapErr(err)
	}
	f.SlotStart, f.SlotEnd, f.Exhausted = f.SlotStart.UTC(), f.SlotEnd.UTC(), exhausted != nil
	live, err := liveAssignmentTx(ctx, s.db, id)
	if err != nil {
		return nil, err
	}
	f.Live = live
	rows, err := s.db.Query(ctx, `SELECT DISTINCT pro_id FROM doorstep.booking_assignments
		WHERE booking_id = $1 AND status IN ('declined', 'expired', 'released', 'no_show', 'cancelled')`, id)
	if err != nil {
		return nil, err
	}
	f.Excluded, err = collect(rows, func(r pgx.Rows) (uuid.UUID, error) {
		var p uuid.UUID
		return p, r.Scan(&p)
	})
	return f, err
}

func liveAssignmentTx(ctx context.Context, q querier, bookingID uuid.UUID) (*AssignmentRef, error) {
	var a AssignmentRef
	err := q.QueryRow(ctx, `
		SELECT a.id, a.booking_id, a.pro_id, p.user_id, a.status, a.offer_expires_at
		FROM doorstep.booking_assignments a JOIN doorstep.professionals p ON p.id = a.pro_id
		WHERE a.booking_id = $1 AND a.role = 'lead' AND a.status IN ('offered', 'accepted')
		ORDER BY a.created_at DESC LIMIT 1`, bookingID).Scan(&a.ID, &a.BookingID, &a.ProID, &a.ProUserID, &a.Status, &a.ExpiresAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	a.ExpiresAt = a.ExpiresAt.UTC()
	return &a, nil
}

// CloseAssignment is the assignment a redispatch closes first.
type CloseAssignment struct {
	ID    uuid.UUID
	ProID uuid.UUID
	// From are the statuses it may be closed from (offered for a decline or
	// an expiry; accepted for a job given back, a no-show, ops).
	From []string
	// To is declined, expired, released, no_show or cancelled.
	To            string
	Cause         string
	DeclineReason *string
	// PenaltyPaise is charged as a negative earning line (0: none).
	PenaltyPaise int64
	// CountCancel / CountNoShow move the professional's counters.
	CountCancel bool
	CountNoShow bool
}

// Redispatch is one dispatch run on a booking.
type Redispatch struct {
	BookingID uuid.UUID
	// From are the booking statuses this run applies from.
	From []string
	// Version > 0: the booking must still be at this version.
	Version int
	Close   *CloseAssignment
	// Cause (asyncapi BookingReassigned.cause) when an assigned booking
	// goes back to confirmed.
	Cause     string
	ActorKind string // history actor of the back-to-confirmed step
	ActorID   *uuid.UUID
	Reason    string
	// Candidates are the ranked professionals; the reserved professional
	// first when they are still free (then their block is kept).
	Candidates []uuid.UUID
	// The slot: when MoveSlot, the booking moves to [Start, End) (a
	// last-minute rescue).
	Start, End, BlockEnd time.Time
	MoveSlot             bool
	OfferExpiresAt       time.Time
	// Rescue: the booking must be accepted by RescueUntil or it ends with a
	// full refund (RescueCause decides how).
	RescueUntil *time.Time
	RescueCause string
	Audit       *Actor
	At          time.Time
}

// RedispatchResult is what a run did.
type RedispatchResult struct {
	// NoOp: a live offer or an accepted professional already existed.
	NoOp bool
	// The new offer (nil when nobody could be held).
	OfferID   *uuid.UUID
	ProID     *uuid.UUID
	ProUserID *uuid.UUID
	ExpiresAt time.Time
	// Exhausted: nobody left; the booking stays confirmed without a
	// professional (ops alerted the first time).
	Exhausted bool
	// Rescue: the booking carries a rescue deadline after this run.
	Rescue bool
	// Closed is the assignment closed first, if any, and its owner.
	ClosedOfferID     *uuid.UUID
	ClosedProUserID   *uuid.UUID
	ClosedOutcome     string
	PreviousProUserID *uuid.UUID // set when an assigned booking went back to confirmed
	Status            string
	Customer          uuid.UUID
}

// Redispatch runs one dispatch on a booking in one transaction (see the
// package comment above). ErrStale when the booking or the assignment moved
// on.
func (s *Store) Redispatch(ctx context.Context, in Redispatch) (*RedispatchResult, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	at := in.At
	var b struct {
		status, locality string
		customer         uuid.UUID
		version          int
		reserved         *uuid.UUID
		exhausted        *time.Time
		rescueUntil      *time.Time
	}
	err = tx.QueryRow(ctx, `SELECT status, version, customer_user_id, reserved_pro_id, locality, dispatch_exhausted_at, rescue_until
		FROM doorstep.bookings WHERE id = $1 FOR UPDATE`, in.BookingID).Scan(&b.status, &b.version, &b.customer, &b.reserved,
		&b.locality, &b.exhausted, &b.rescueUntil)
	if err != nil {
		return nil, mapErr(err)
	}
	if !contains(in.From, b.status) || (in.Version > 0 && b.version != in.Version) {
		return nil, ErrStale
	}
	res := &RedispatchResult{Customer: b.customer, Status: b.status}

	if in.Close != nil {
		var status string
		var proUser uuid.UUID
		err := tx.QueryRow(ctx, `SELECT a.status, p.user_id FROM doorstep.booking_assignments a
			JOIN doorstep.professionals p ON p.id = a.pro_id
			WHERE a.id = $1 AND a.booking_id = $2 AND a.pro_id = $3 FOR UPDATE OF a`, in.Close.ID, in.BookingID, in.Close.ProID).
			Scan(&status, &proUser)
		if errors.Is(err, pgx.ErrNoRows) || (err == nil && !contains(in.Close.From, status)) {
			return nil, ErrStale
		}
		if err != nil {
			return nil, err
		}
		if _, err := tx.Exec(ctx, `UPDATE doorstep.booking_assignments SET status = $2, responded_at = COALESCE(responded_at, $3),
			decline_reason = COALESCE($4, decline_reason), release_cause = NULLIF($5, ''), updated_at = $3 WHERE id = $1`,
			in.Close.ID, in.Close.To, at, in.Close.DeclineReason, in.Close.Cause); err != nil {
			return nil, err
		}
		if err := penaltyTx(ctx, tx, in.Close, in.BookingID, at); err != nil {
			return nil, err
		}
		id, u := in.Close.ID, proUser
		res.ClosedOfferID, res.ClosedProUserID = &id, &u
		if status == "offered" {
			res.ClosedOutcome = offerOutcome(in.Close.To)
			if err := s.enqueueOfferEvent(ctx, tx, events.ProOfferClosed, in.BookingID, b.customer, proUser, at,
				events.ProOfferClosedData{OfferID: id, BookingID: in.BookingID, ProUserID: proUser, CustomerUserID: b.customer,
					Outcome: res.ClosedOutcome}); err != nil {
				return nil, err
			}
		}
	}

	live, err := liveAssignmentTx(ctx, tx, in.BookingID)
	if err != nil {
		return nil, err
	}
	if live != nil {
		res.NoOp = true
		return res, mapErr(tx.Commit(ctx))
	}

	// An assigned (or travelling) booking whose professional is gone goes
	// back to confirmed, and the previous professional is named.
	if b.status != "confirmed" {
		if in.Close == nil || res.ClosedProUserID == nil {
			return nil, ErrStale
		}
		if _, err := tx.Exec(ctx, `UPDATE doorstep.bookings SET status = 'confirmed', assigned_at = NULL, en_route_at = NULL,
			arrived_at = NULL, version = version + 1, updated_at = $2 WHERE id = $1`, in.BookingID, at); err != nil {
			return nil, err
		}
		from, reason := b.status, in.Reason
		if err := historyTx(ctx, tx, in.BookingID, &from, "confirmed", in.ActorKind, in.ActorID, &reason, at); err != nil {
			return nil, err
		}
		core, err := bookingCoreTx(ctx, tx, in.BookingID)
		if err != nil {
			return nil, err
		}
		prev := *res.ClosedProUserID
		if err := s.enqueueBookingEvent(ctx, tx, events.BookingReassigned, core, at, events.BookingReassignedData{
			BookingCore: core, PreviousProUserID: prev, Cause: in.Cause}); err != nil {
			return nil, err
		}
		res.PreviousProUserID, res.Status = &prev, "confirmed"
	}
	if in.RescueUntil != nil {
		if _, err := tx.Exec(ctx, `UPDATE doorstep.bookings SET rescue_until = $2, rescue_cause = $3 WHERE id = $1`,
			in.BookingID, *in.RescueUntil, in.RescueCause); err != nil {
			return nil, mapErr(err)
		}
		b.rescueUntil = in.RescueUntil
	}
	res.Rescue = b.rescueUntil != nil
	if in.MoveSlot {
		if _, err := tx.Exec(ctx, `UPDATE doorstep.bookings SET slot_start = $2, slot_end = $3, version = version + 1, updated_at = $4
			WHERE id = $1`, in.BookingID, in.Start, in.End, at); err != nil {
			return nil, mapErr(err)
		}
	}

	// Hold the next professional: keep the reserved one's block when they
	// are still the first choice for the same slot, else release and hold.
	var pro uuid.UUID
	var blockID uuid.UUID
	kept := false
	if !in.MoveSlot && b.reserved != nil && len(in.Candidates) > 0 && in.Candidates[0] == *b.reserved {
		// Any active booking block of theirs (an ASAP block starts at the
		// booking time, not the slot).
		err := tx.QueryRow(ctx, `SELECT id FROM doorstep.pro_calendar_blocks WHERE booking_id = $1 AND pro_id = $2 AND active
			AND kind = 'booking' LIMIT 1`, in.BookingID, *b.reserved).Scan(&blockID)
		switch {
		case err == nil:
			pro, kept = *b.reserved, true
		case !errors.Is(err, pgx.ErrNoRows):
			return nil, err
		}
	}
	if !kept {
		if err := releaseBlocksTx(ctx, tx, in.BookingID, at, "redispatch"); err != nil {
			return nil, err
		}
		pro, err = holdFirstTx(ctx, tx, in.BookingID, in.Candidates, "booking", in.Start, in.BlockEnd, nil)
		switch {
		case errors.Is(err, ErrSlotTaken):
			return s.exhaustTx(ctx, tx, in, res, b.exhausted == nil, at)
		case err != nil:
			return nil, err
		}
		if err := tx.QueryRow(ctx, `SELECT id FROM doorstep.pro_calendar_blocks WHERE booking_id = $1 AND pro_id = $2 AND active`,
			in.BookingID, pro).Scan(&blockID); err != nil {
			return nil, err
		}
	}
	var proUser uuid.UUID
	if err := tx.QueryRow(ctx, `UPDATE doorstep.professionals SET offers_received = offers_received + 1, updated_at = $2
		WHERE id = $1 RETURNING user_id`, pro, at).Scan(&proUser); err != nil {
		return nil, mapErr(err)
	}
	if _, err := tx.Exec(ctx, `UPDATE doorstep.bookings SET reserved_pro_id = $2, dispatch_attempted_at = $3, dispatch_exhausted_at = NULL,
		updated_at = $3 WHERE id = $1`, in.BookingID, pro, at); err != nil {
		return nil, err
	}
	offerID := uuid.New()
	if _, err := tx.Exec(ctx, `
		INSERT INTO doorstep.booking_assignments (id, booking_id, pro_id, role, status, attempt, offered_at, offer_expires_at,
		                                          calendar_block_id, created_at, updated_at)
		VALUES ($1, $2, $3, 'lead', 'offered',
		        (SELECT count(*) + 1 FROM doorstep.booking_assignments WHERE booking_id = $2), $4, $5, $6, $4, $4)`,
		offerID, in.BookingID, pro, at, in.OfferExpiresAt, blockID); err != nil {
		return nil, mapErr(err)
	}
	if err := s.enqueueOfferEvent(ctx, tx, events.ProOfferCreated, in.BookingID, b.customer, proUser, at,
		events.ProOfferCreatedData{OfferID: offerID, BookingID: in.BookingID, ProUserID: proUser, CustomerUserID: b.customer,
			ExpiresAt: in.OfferExpiresAt.UTC(), Locality: b.locality}); err != nil {
		return nil, err
	}
	if in.Audit != nil {
		if err := auditTx(ctx, tx, *in.Audit, "booking.redispatch", "booking", in.BookingID.String(), map[string]any{
			"from_status": b.status, "reason": in.Reason, "excluded": in.Close != nil}); err != nil {
			return nil, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, mapErr(err)
	}
	res.OfferID, res.ProID, res.ProUserID, res.ExpiresAt = &offerID, &pro, &proUser, in.OfferExpiresAt.UTC()
	return res, nil
}

// exhaustTx: nobody could be held. The booking keeps its status
// (confirmed) without a professional or a block; ops are alerted the first
// time it happens. The run's other effects (the closed assignment, back to
// confirmed) commit with it.
func (s *Store) exhaustTx(ctx context.Context, tx pgx.Tx, in Redispatch, res *RedispatchResult, firstTime bool, at time.Time) (*RedispatchResult, error) {
	if err := releaseBlocksTx(ctx, tx, in.BookingID, at, "redispatch_exhausted"); err != nil {
		return nil, err
	}
	if _, err := tx.Exec(ctx, `UPDATE doorstep.bookings SET reserved_pro_id = NULL, dispatch_attempted_at = $2,
		dispatch_exhausted_at = COALESCE(dispatch_exhausted_at, $2), updated_at = $2 WHERE id = $1`, in.BookingID, at); err != nil {
		return nil, err
	}
	if firstTime {
		core, err := bookingCoreTx(ctx, tx, in.BookingID)
		if err != nil {
			return nil, err
		}
		if err := s.enqueueBookingEvent(ctx, tx, events.BookingUnassignedAlert, core, at, events.BookingUnassignedAlertData{
			BookingCore: core, MinutesToSlot: minutesTo(at, core.SlotStart), Reason: events.AlertNoProfessionalLeft}); err != nil {
			return nil, err
		}
	}
	if in.Audit != nil {
		if err := auditTx(ctx, tx, *in.Audit, "booking.redispatch", "booking", in.BookingID.String(), map[string]any{
			"reason": in.Reason, "outcome": "no_professional_left"}); err != nil {
			return nil, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, mapErr(err)
	}
	res.Exhausted = true
	return res, nil
}

func minutesTo(now, t time.Time) int { return int(t.Sub(now) / time.Minute) }

func contains(set []string, v string) bool {
	for _, s := range set {
		if s == v {
			return true
		}
	}
	return false
}

func offerOutcome(to string) string {
	switch to {
	case "declined":
		return "declined"
	case "expired":
		return "expired"
	case "accepted":
		return "accepted"
	}
	return "withdrawn"
}

func (s *Store) enqueueOfferEvent(ctx context.Context, tx pgx.Tx, eventType string, bookingID, customer, proUser uuid.UUID, at time.Time, data any) error {
	key, payload, err := events.ProOffer(eventType, bookingID, customer, proUser, at, data)
	if err != nil {
		return err
	}
	return s.events.Enqueue(ctx, tx, eventType, key, payload)
}

// penaltyTx moves the professional's counters and writes the penalty line
// (once per booking and professional).
func penaltyTx(ctx context.Context, tx pgx.Tx, c *CloseAssignment, bookingID uuid.UUID, at time.Time) error {
	if c.CountCancel || c.CountNoShow {
		if _, err := tx.Exec(ctx, `UPDATE doorstep.professionals SET
			cancellations_count = cancellations_count + CASE WHEN $2 THEN 1 ELSE 0 END,
			no_show_count = no_show_count + CASE WHEN $3 THEN 1 ELSE 0 END, updated_at = $4 WHERE id = $1`,
			c.ProID, c.CountCancel, c.CountNoShow, at); err != nil {
			return err
		}
	}
	if c.PenaltyPaise > 0 {
		if _, err := tx.Exec(ctx, `INSERT INTO doorstep.earning_lines (pro_id, booking_id, kind, amount_paise, created_at)
			VALUES ($1, $2, 'penalty', $3, $4) ON CONFLICT (booking_id, pro_id, kind) DO NOTHING`,
			c.ProID, bookingID, -c.PenaltyPaise, at); err != nil {
			return err
		}
	}
	return nil
}

// ---------------------------------------------------------------- accept

// AcceptInput is a professional accepting an offer.
type AcceptInput struct {
	OfferID uuid.UUID
	ProID   uuid.UUID
	At      time.Time
}

// AcceptResult is the accepted booking.
type AcceptResult struct {
	BookingID uuid.UUID
	Customer  uuid.UUID
	// Already: the offer had been accepted by this professional before
	// (an idempotent replay).
	Already bool
}

// AcceptOffer accepts an offer (rider AcceptOffer's shape): the booking row
// is locked first, then the offer; only the offered professional may accept;
// an expired, declined, withdrawn or superseded offer is refused; the
// professional is re-checked (approved, not suspended, gender rule,
// background check valid on the slot date); the booking moves confirmed ->
// assigned with a version compare, the offer becomes accepted and the
// professional's acceptance counter moves — one transaction, with
// doorstep.booking.assigned and doorstep.pro.offer_closed (accepted).
func (s *Store) AcceptOffer(ctx context.Context, in AcceptInput) (*AcceptResult, error) {
	var bookingID uuid.UUID
	err := s.db.QueryRow(ctx, `SELECT booking_id FROM doorstep.booking_assignments WHERE id = $1 AND pro_id = $2`,
		in.OfferID, in.ProID).Scan(&bookingID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrOfferNotFound
	}
	if err != nil {
		return nil, err
	}
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var bStatus, genderRule string
	var version int
	var customer uuid.UUID
	var reserved *uuid.UUID
	var requireFemale bool
	var slotStart time.Time
	if err := tx.QueryRow(ctx, `SELECT status, version, customer_user_id, reserved_pro_id, gender_rule, require_female_pro, slot_start
		FROM doorstep.bookings WHERE id = $1 FOR UPDATE`, bookingID).Scan(&bStatus, &version, &customer, &reserved, &genderRule,
		&requireFemale, &slotStart); err != nil {
		return nil, mapErr(err)
	}
	var aStatus string
	var expires time.Time
	if err := tx.QueryRow(ctx, `SELECT status, offer_expires_at FROM doorstep.booking_assignments
		WHERE id = $1 AND pro_id = $2 FOR UPDATE`, in.OfferID, in.ProID).Scan(&aStatus, &expires); err != nil {
		return nil, mapErr(err)
	}
	res := &AcceptResult{BookingID: bookingID, Customer: customer}
	switch aStatus {
	case "accepted":
		res.Already = true
		return res, mapErr(tx.Commit(ctx))
	case "expired":
		return nil, ErrOfferExpired
	case "offered":
	default:
		return nil, ErrOfferTaken
	}
	if !in.At.Before(expires) {
		return nil, ErrOfferExpired
	}
	if bStatus != "confirmed" || reserved == nil || *reserved != in.ProID {
		return nil, ErrOfferTaken
	}
	var hasBlock bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM doorstep.pro_calendar_blocks WHERE booking_id = $1 AND pro_id = $2 AND active)`,
		bookingID, in.ProID).Scan(&hasBlock); err != nil {
		return nil, err
	}
	if !hasBlock {
		return nil, ErrOfferTaken
	}
	// Re-check the professional on the row that will be written.
	var pStatus, gender, name string
	var suspended, bgClear bool
	if err := tx.QueryRow(ctx, `
		SELECT p.status, p.incident_suspended, COALESCE(p.gender, ''), p.display_name,
		       EXISTS (SELECT 1 FROM doorstep.background_checks c WHERE c.pro_id = p.id AND c.status = 'clear'
		                 AND c.valid_from <= ($2::timestamptz AT TIME ZONE 'Asia/Kolkata')::date
		                 AND c.valid_until > ($2::timestamptz AT TIME ZONE 'Asia/Kolkata')::date)
		FROM doorstep.professionals p WHERE p.id = $1 FOR SHARE`, in.ProID, slotStart).Scan(&pStatus, &suspended, &gender, &name,
		&bgClear); err != nil {
		return nil, mapErr(err)
	}
	switch {
	case pStatus != "approved":
		return nil, &IneligibleError{Reason: "not_approved"}
	case suspended:
		return nil, &IneligibleError{Reason: "suspended"}
	case !slots.GenderAllowed(genderRule, requireFemale, gender):
		return nil, &IneligibleError{Reason: "gender"}
	case !bgClear:
		return nil, &IneligibleError{Reason: "background"}
	}
	tag, err := tx.Exec(ctx, `UPDATE doorstep.bookings SET status = 'assigned', assigned_at = $3, version = version + 1,
		dispatch_exhausted_at = NULL, rescue_until = NULL, rescue_cause = NULL, updated_at = $3
		WHERE id = $1 AND version = $2 AND status = 'confirmed'`, bookingID, version, in.At)
	if err != nil {
		return nil, err
	}
	if tag.RowsAffected() == 0 {
		return nil, ErrOfferTaken
	}
	if _, err := tx.Exec(ctx, `UPDATE doorstep.booking_assignments SET status = 'accepted', responded_at = $2, updated_at = $2
		WHERE id = $1`, in.OfferID, in.At); err != nil {
		return nil, err
	}
	var proUser uuid.UUID
	if err := tx.QueryRow(ctx, `UPDATE doorstep.professionals SET offers_accepted = offers_accepted + 1, updated_at = $2
		WHERE id = $1 RETURNING user_id`, in.ProID, in.At).Scan(&proUser); err != nil {
		return nil, err
	}
	from := "confirmed"
	if err := historyTx(ctx, tx, bookingID, &from, "assigned", "pro", &proUser, nil, in.At); err != nil {
		return nil, err
	}
	core, err := bookingCoreTx(ctx, tx, bookingID)
	if err != nil {
		return nil, err
	}
	if err := s.enqueueBookingEvent(ctx, tx, events.BookingAssigned, core, in.At, events.BookingAssignedData{
		BookingCore: core, ProFirstName: firstName(name), AssignmentID: in.OfferID}); err != nil {
		return nil, err
	}
	if err := s.enqueueOfferEvent(ctx, tx, events.ProOfferClosed, bookingID, customer, proUser, in.At, events.ProOfferClosedData{
		OfferID: in.OfferID, BookingID: bookingID, ProUserID: proUser, CustomerUserID: customer, Outcome: "accepted"}); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, mapErr(err)
	}
	return res, nil
}

// ---------------------------------------------------------------- terminal

// EndProNoShow ends a booking nobody could serve after a professional did
// not turn up (or a rescue found no one): status pro_no_show, every block
// released, live assignments closed (close, when given, becomes no_show
// with its penalty), a full refund of what is refundable and
// doorstep.booking.no_show (party pro) — one transaction. It returns the
// refund to submit.
func (s *Store) EndProNoShow(ctx context.Context, id uuid.UUID, close *CloseAssignment, from []string, reason, refundCause, refundKey string,
	at time.Time) ([]uuid.UUID, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	b, err := lockBookingTx(ctx, tx, id, nil)
	if err != nil {
		return nil, err
	}
	if !contains(from, b.Status) {
		return nil, ErrStale
	}
	core, err := bookingCoreTx(ctx, tx, id)
	if err != nil {
		return nil, err
	}
	if close != nil {
		tag, err := tx.Exec(ctx, `UPDATE doorstep.booking_assignments SET status = $3, release_cause = $4, updated_at = $5
			WHERE id = $1 AND booking_id = $2 AND status = ANY($6)`, close.ID, id, close.To, close.Cause, at, close.From)
		if err != nil {
			return nil, err
		}
		if tag.RowsAffected() == 0 {
			return nil, ErrStale
		}
		if err := penaltyTx(ctx, tx, close, id, at); err != nil {
			return nil, err
		}
	}
	if _, err := tx.Exec(ctx, `UPDATE doorstep.booking_assignments SET status = 'cancelled', release_cause = 'pro_no_show', updated_at = $2
		WHERE booking_id = $1 AND status IN ('offered', 'accepted')`, id, at); err != nil {
		return nil, err
	}
	if err := releaseBlocksTx(ctx, tx, id, at, "pro_no_show"); err != nil {
		return nil, err
	}
	if _, err := tx.Exec(ctx, `UPDATE doorstep.bookings SET status = 'pro_no_show', cancelled_by_kind = 'system', cancel_reason = $3,
		cancelled_at = $2, rescue_until = NULL, rescue_cause = NULL, version = version + 1, updated_at = $2 WHERE id = $1`,
		id, at, reason); err != nil {
		return nil, err
	}
	fromStatus := b.Status
	if err := historyTx(ctx, tx, id, &fromStatus, "pro_no_show", "system", nil, &reason, at); err != nil {
		return nil, err
	}
	var refundIDs []uuid.UUID
	if b.Refundable > 0 && b.PaymentID != nil {
		if refundIDs, err = refundAcrossTx(ctx, tx, id, b.Refundable, refundCause, refundKey, nil, at); err != nil {
			return nil, err
		}
	}
	core.Status = "pro_no_show"
	if err := s.enqueueBookingEvent(ctx, tx, events.BookingNoShow, core, at, events.BookingNoShowData{
		BookingCore: core, Party: "pro"}); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, mapErr(err)
	}
	return refundIDs, nil
}

// Contains reports whether v is in set.
func Contains(set []string, v string) bool { return contains(set, v) }
