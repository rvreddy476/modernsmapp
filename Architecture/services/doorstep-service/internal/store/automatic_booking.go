package store

import (
	"context"
	"errors"
	"sort"
	"time"

	"github.com/atpost/doorstep-service/internal/events"
	"github.com/atpost/doorstep-service/internal/payments"
	"github.com/atpost/doorstep-service/internal/slots"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func assignmentMode(mode string) string {
	if mode == "automatic" {
		return mode
	}
	return "customer_selected"
}

func requireAdminPricesTx(ctx context.Context, tx pgx.Tx, booking uuid.UUID, at time.Time) error {
	var total, live int
	err := tx.QueryRow(ctx, `SELECT count(*), count(*) FILTER (WHERE EXISTS (
  SELECT 1 FROM doorstep.city_prices p JOIN doorstep.bookings b ON b.id=i.booking_id
  WHERE p.id=i.price_id AND p.city_code=b.city_code AND p.item_kind=i.kind
    AND COALESCE(p.option_id,p.addon_id)=i.ref_id AND p.price_paise=i.unit_price_paise
    AND p.effective_from <= $2 AND (p.effective_to IS NULL OR p.effective_to > $2)))
  FROM doorstep.booking_items i WHERE booking_id=$1`, booking, at).Scan(&total, &live)
	if err != nil {
		return err
	}
	if total == 0 || total != live {
		return ErrPriceGone
	}
	return nil
}

// automaticRow is always locked before an offer or professional calendar.
type automaticRow struct {
	id, customer, service, zone                 uuid.UUID
	mode, status, city, skill, gender, locality string
	lat, lng                                    float64
	start, end                                  time.Time
	deadline                                    *time.Time
	duration, buffer                            int
	female                                      bool
	paid                                        bool
	excluded                                    []uuid.UUID
}

func lockAutomatic(ctx context.Context, tx pgx.Tx, id uuid.UUID) (*automaticRow, error) {
	var b automaticRow
	err := tx.QueryRow(ctx, `SELECT b.id,b.customer_user_id,b.service_id,b.zone_id,b.assignment_mode,b.status,b.city_code,
 s.required_skill,b.gender_rule,b.locality,ST_Y(b.location::geometry),ST_X(b.location::geometry),b.slot_start,b.slot_end,
 b.matching_deadline,b.duration_minutes,z.travel_buffer_minutes,b.require_female_pro,
 (b.paid_paise >= b.total_paise AND b.refunded_paise=0),b.excluded_pro_ids
 FROM doorstep.bookings b JOIN doorstep.services s ON s.id=b.service_id JOIN doorstep.zones z ON z.id=b.zone_id
 WHERE b.id=$1 FOR UPDATE OF b`, id).Scan(&b.id, &b.customer, &b.service, &b.zone, &b.mode, &b.status, &b.city, &b.skill, &b.gender,
		&b.locality, &b.lat, &b.lng, &b.start, &b.end, &b.deadline, &b.duration, &b.buffer, &b.female, &b.paid, &b.excluded)
	return &b, mapErr(err)
}

func (b *automaticRow) request() slots.Request {
	return slots.Request{DurationMinutes: b.duration, BufferMinutes: b.buffer, GenderRule: b.gender, RequireFemale: b.female}
}

func (b *automaticRow) query(ids []uuid.UUID) CandidateQuery {
	return CandidateQuery{City: b.city, ZoneID: b.zone, Lat: b.lat, Lng: b.lng, Skill: b.skill, ServiceID: b.service,
		From: b.start.AddDate(0, 0, -7), To: b.start.AddDate(0, 0, 7), ProIDs: ids, ExcludeBooking: &b.id}
}

// AutomaticQueue is separate from payment-first legacy dispatch. Every
// matching booking is retried by the worker, including after a process restart.
func (s *Store) AutomaticQueue(ctx context.Context, limit int) ([]uuid.UUID, error) {
	rows, err := s.db.Query(ctx, `SELECT id FROM doorstep.bookings WHERE assignment_mode='automatic' AND status='matching'
 ORDER BY matching_deadline,id LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	return collect(rows, func(r pgx.Rows) (uuid.UUID, error) {
		var id uuid.UUID
		err := r.Scan(&id)
		return id, err
	})
}

// AutomaticOfferWindow is a server-owned deadline, never reset by app opens.
const AutomaticOfferWindow = 20 * time.Second

// MatchAutomatic offers to ONE professional at a time, lowest workload first.
// Offers never reserve calendars: acceptance rechecks availability under locks.
func (s *Store) MatchAutomatic(ctx context.Context, id uuid.UUID, at time.Time) error {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }() // no-op after Commit
	b, err := lockAutomatic(ctx, tx, id)
	if err != nil {
		return err
	}
	if b.mode != "automatic" || b.status != "matching" {
		return nil
	}
	if b.deadline == nil || !at.Before(*b.deadline) {
		if b.paid {
			// CancelBooking owns its transaction. Re-check the matching status
			// after releasing this lock so a concurrent acceptance wins safely.
			_ = tx.Rollback(ctx)
			_, err := s.CancelBooking(ctx, id, nil, nil, at, func(current *LockedBooking) (*CancelDecision, error) {
				if current.Status != "matching" || (current.MatchingDeadline != nil && at.Before(*current.MatchingDeadline)) {
					return nil, ErrStale
				}
				return &CancelDecision{ActorKind: "system", Reason: "No replacement professional was available. A full refund has been requested.", RefundPaise: current.Refundable,
					RefundCause: payments.CauseUnassigned, RefundKey: payments.RefundKey(id, payments.CauseUnassigned)}, nil
			})
			if errors.Is(err, ErrStale) {
				return nil
			}
			return err
		}
		if _, err = tx.Exec(ctx, `UPDATE doorstep.bookings SET status='expired',matching_deadline=NULL,version=version+1,updated_at=$2 WHERE id=$1`, id, at); err != nil {
			return err
		}
		if err = s.closeAutomaticOffers(ctx, tx, b, uuid.Nil, at, "expired"); err != nil {
			return err
		}
		from, reason := "matching", "no professional accepted before the request expired"
		if err = historyTx(ctx, tx, id, &from, "expired", "system", nil, &reason, at); err != nil {
			return err
		}
		core, e := bookingCoreTx(ctx, tx, id)
		if e != nil {
			return e
		}
		if err = s.enqueueBookingEvent(ctx, tx, events.BookingExpired, core, at, core); err != nil {
			return err
		}
		return tx.Commit(ctx)
	}
	// Close only elapsed offers; declines and expiries are excluded on retries.
	rows, err := tx.Query(ctx, `UPDATE doorstep.booking_assignments a SET status='expired',responded_at=$2,updated_at=$2
 FROM doorstep.professionals p WHERE a.booking_id=$1 AND a.status='offered' AND a.offer_expires_at <= $2 AND p.id=a.pro_id
 RETURNING a.id,p.user_id`, id, at)
	if err != nil {
		return err
	}
	type expiredOwner struct{ offer, user uuid.UUID }
	expired, err := collect(rows, func(r pgx.Rows) (expiredOwner, error) {
		var o expiredOwner
		err := r.Scan(&o.offer, &o.user)
		return o, err
	})
	if err != nil {
		return err
	}
	for _, o := range expired {
		if err = s.enqueueOfferEvent(ctx, tx, events.ProOfferClosed, id, b.customer, o.user, at, events.ProOfferClosedData{
			OfferID: o.offer, BookingID: id, ProUserID: o.user, CustomerUserID: b.customer, Outcome: "expired"}); err != nil {
			return err
		}
	}
	var live bool
	if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM doorstep.booking_assignments WHERE booking_id=$1 AND status='offered')`, id).Scan(&live); err != nil {
		return err
	}
	if live {
		return tx.Commit(ctx)
	}
	rows, err = tx.Query(ctx, `SELECT pro_id FROM doorstep.booking_assignments WHERE booking_id=$1 AND status<>'cancelled'`, id)
	if err != nil {
		return err
	}
	tried, err := collect(rows, func(r pgx.Rows) (uuid.UUID, error) {
		var p uuid.UUID
		err := r.Scan(&p)
		return p, err
	})
	if err != nil {
		return err
	}
	excluded := map[uuid.UUID]bool{}
	for _, p := range b.excluded {
		excluded[p] = true
	}
	for _, p := range tried {
		excluded[p] = true
	}
	pros, err := slotCandidates(ctx, tx, b.query(nil))
	if err != nil {
		return err
	}
	free := slots.Available(pros, b.start, b.request())
	eligible := make([]slots.Pro, 0, len(free))
	for _, p := range free {
		if !excluded[p.ID] {
			eligible = append(eligible, p)
		}
	}
	sort.Slice(eligible, func(i, j int) bool {
		a, c := eligible[i], eligible[j]
		wa, wc := slots.WeekLoad(a, b.start), slots.WeekLoad(c, b.start)
		if wa != wc {
			return wa < wc
		}
		if a.OffersReceived != c.OffersReceived {
			return a.OffersReceived < c.OffersReceived
		}
		return a.ID.String() < c.ID.String()
	})
	if len(eligible) == 0 {
		return tx.Commit(ctx)
	}
	expires := at.Add(AutomaticOfferWindow)
	if expires.After(*b.deadline) {
		expires = *b.deadline
	}
	for _, p := range eligible[:1] {
		offer := uuid.New()
		if _, err = tx.Exec(ctx, `INSERT INTO doorstep.booking_assignments(id,booking_id,pro_id,role,status,attempt,offered_at,offer_expires_at,created_at,updated_at)
   VALUES($1,$2,$3,'lead','offered',(SELECT count(*)+1 FROM doorstep.booking_assignments WHERE booking_id=$2),$4,$5,$4,$4)`, offer, id, p.ID, at, expires); err != nil {
			return err
		}
		if _, err = tx.Exec(ctx, `UPDATE doorstep.professionals SET offers_received=offers_received+1 WHERE id=$1`, p.ID); err != nil {
			return err
		}
		if err = s.enqueueOfferEvent(ctx, tx, events.ProOfferCreated, id, b.customer, p.UserID, at, events.ProOfferCreatedData{
			OfferID: offer, BookingID: id, ProUserID: p.UserID, CustomerUserID: b.customer, ExpiresAt: expires, Locality: b.locality}); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

func (s *Store) closeAutomaticOffers(ctx context.Context, tx pgx.Tx, b *automaticRow, except uuid.UUID, at time.Time, outcome string) error {
	rows, err := tx.Query(ctx, `UPDATE doorstep.booking_assignments a SET status='cancelled',updated_at=$3
 FROM doorstep.professionals p WHERE a.booking_id=$1 AND a.id<>$2 AND a.status='offered' AND p.id=a.pro_id
 RETURNING a.id,p.user_id`, b.id, except, at)
	if err != nil {
		return err
	}
	type owner struct{ id, user uuid.UUID }
	closed, err := collect(rows, func(r pgx.Rows) (owner, error) {
		var o owner
		err := r.Scan(&o.id, &o.user)
		return o, err
	})
	if err != nil {
		return err
	}
	for _, o := range closed {
		if err = s.enqueueOfferEvent(ctx, tx, events.ProOfferClosed, b.id, b.customer, o.user, at, events.ProOfferClosedData{
			OfferID: o.id, BookingID: b.id, ProUserID: o.user, CustomerUserID: b.customer, Outcome: outcome}); err != nil {
			return err
		}
	}
	return nil
}

// AcceptAutomatic revalidates current skills, service area, hours and capacity
// in the locked transaction. No money is taken here.
func (s *Store) AcceptAutomatic(ctx context.Context, in AcceptInput) (*AcceptResult, error) {
	var id uuid.UUID
	if err := s.db.QueryRow(ctx, `SELECT booking_id FROM doorstep.booking_assignments WHERE id=$1 AND pro_id=$2`, in.OfferID, in.ProID).Scan(&id); err != nil {
		return nil, mapErr(err)
	}
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }() // no-op after Commit
	b, err := lockAutomatic(ctx, tx, id)
	if err != nil {
		return nil, err
	}
	var status string
	var expires time.Time
	if err = tx.QueryRow(ctx, `SELECT status,offer_expires_at FROM doorstep.booking_assignments WHERE id=$1 AND pro_id=$2 FOR UPDATE`, in.OfferID, in.ProID).Scan(&status, &expires); err != nil {
		return nil, mapErr(err)
	}
	res := &AcceptResult{BookingID: id, Customer: b.customer, Status: b.status}
	if status == "accepted" {
		res.Already = true
		return res, tx.Commit(ctx)
	}
	if b.mode != "automatic" || b.status != "matching" || status != "offered" {
		return nil, ErrOfferTaken
	}
	if !in.At.Before(expires) || b.deadline == nil || !in.At.Before(*b.deadline) {
		return nil, ErrOfferExpired
	}
	// Serialise this partner's competing acceptances and profile mutations.
	var proUser uuid.UUID
	var name string
	if err = tx.QueryRow(ctx, `SELECT user_id,display_name FROM doorstep.professionals WHERE id=$1 FOR UPDATE`, in.ProID).Scan(&proUser, &name); err != nil {
		return nil, mapErr(err)
	}
	pros, err := slotCandidates(ctx, tx, b.query([]uuid.UUID{in.ProID}))
	if err != nil {
		return nil, err
	}
	if len(slots.Available(pros, b.start, b.request())) != 1 {
		return nil, ErrOfferTaken
	}
	until := in.At.Add(10 * time.Minute)
	if cutoff := b.start.Add(-5 * time.Minute); cutoff.Before(until) {
		until = cutoff
	}
	if !until.After(in.At) {
		return nil, ErrOfferExpired
	}
	kind, next := "hold", "pending_payment"
	var holdUntil *time.Time = &until
	if b.paid {
		kind, next, holdUntil = "booking", "assigned", nil
	}
	if _, err = holdFirstTx(ctx, tx, id, []uuid.UUID{in.ProID}, kind, b.start, b.end.Add(time.Duration(b.buffer)*time.Minute), holdUntil); err != nil {
		if errors.Is(err, ErrSlotTaken) {
			return nil, ErrOfferTaken
		}
		return nil, err
	}
	if _, err = tx.Exec(ctx, `UPDATE doorstep.bookings SET status=$5,reserved_pro_id=$2,assigned_at=$3,
 hold_expires_at=$4,matching_deadline=NULL,version=version+1,updated_at=$3 WHERE id=$1`, id, in.ProID, in.At, holdUntil, next); err != nil {
		return nil, err
	}
	if _, err = tx.Exec(ctx, `UPDATE doorstep.booking_assignments SET status='accepted',responded_at=$2,updated_at=$2 WHERE id=$1`, in.OfferID, in.At); err != nil {
		return nil, mapErr(err)
	}
	if _, err = tx.Exec(ctx, `UPDATE doorstep.professionals SET offers_accepted=offers_accepted+1 WHERE id=$1`, in.ProID); err != nil {
		return nil, err
	}
	if err = s.closeAutomaticOffers(ctx, tx, b, in.OfferID, in.At, "withdrawn"); err != nil {
		return nil, err
	}
	from := "matching"
	if err = historyTx(ctx, tx, id, &from, next, "pro", &proUser, nil, in.At); err != nil {
		return nil, err
	}
	core, err := bookingCoreTx(ctx, tx, id)
	if err != nil {
		return nil, err
	}
	if err = s.enqueueBookingEvent(ctx, tx, events.BookingAssigned, core, in.At, events.BookingAssignedData{BookingCore: core, ProFirstName: firstName(name), AssignmentID: in.OfferID}); err != nil {
		return nil, err
	}
	if err = s.enqueueOfferEvent(ctx, tx, events.ProOfferClosed, id, b.customer, proUser, in.At, events.ProOfferClosedData{OfferID: in.OfferID, BookingID: id, ProUserID: proUser, CustomerUserID: b.customer, Outcome: "accepted"}); err != nil {
		return nil, err
	}
	res.Status = next
	return res, tx.Commit(ctx)
}

func (s *Store) DeclineAutomatic(ctx context.Context, in AcceptInput) error {
	var id uuid.UUID
	if err := s.db.QueryRow(ctx, `SELECT booking_id FROM doorstep.booking_assignments WHERE id=$1 AND pro_id=$2`, in.OfferID, in.ProID).Scan(&id); err != nil {
		return mapErr(err)
	}
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }() // no-op after Commit
	b, err := lockAutomatic(ctx, tx, id)
	if err != nil {
		return err
	}
	if b.mode != "automatic" || b.status != "matching" {
		return ErrOfferTaken
	}
	tag, err := tx.Exec(ctx, `UPDATE doorstep.booking_assignments SET status='declined',responded_at=$3,updated_at=$3
 WHERE id=$1 AND pro_id=$2 AND status='offered' AND offer_expires_at>$3`, in.OfferID, in.ProID, in.At)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrOfferTaken
	}
	var proUser uuid.UUID
	if err = tx.QueryRow(ctx, `SELECT user_id FROM doorstep.professionals WHERE id=$1`, in.ProID).Scan(&proUser); err != nil {
		return err
	}
	if err = s.enqueueOfferEvent(ctx, tx, events.ProOfferClosed, id, b.customer, proUser, in.At, events.ProOfferClosedData{
		OfferID: in.OfferID, BookingID: id, ProUserID: proUser, CustomerUserID: b.customer, Outcome: "declined"}); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
