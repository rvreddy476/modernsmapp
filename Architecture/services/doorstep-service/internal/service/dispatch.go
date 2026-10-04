package service

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/atpost/doorstep-service/internal/apperr"
	"github.com/atpost/doorstep-service/internal/dispatch"
	"github.com/atpost/doorstep-service/internal/events"
	"github.com/atpost/doorstep-service/internal/model"
	"github.com/atpost/doorstep-service/internal/payments"
	"github.com/atpost/doorstep-service/internal/slots"
	"github.com/atpost/doorstep-service/internal/store"
	"github.com/google/uuid"
)

// Dispatch (A4): offers and reassignment for scheduled jobs.
//
// A confirmed booking is offered to the professional its slot is held for
// (in-process right after the payment event commits, and by a worker for any
// confirmed booking left without a live offer). Accept assigns it. A decline,
// an expired offer, a job given back, a professional off duty 90 minutes out,
// a no-show or an ops redispatch closes that professional's assignment and
// re-runs the matcher excluding everyone who already let the booking go; the
// calendar block moves to the next professional in the same transaction
// (store.Redispatch). With nobody left the booking stays confirmed and ops
// are alerted; 45 minutes out it is cancelled with a full refund. Ops never
// pick a professional: a redispatch only adds exclusions.

// DispatchStore is what dispatch needs from the store.
type DispatchStore interface {
	DispatchFacts(ctx context.Context, id uuid.UUID) (*store.DispatchFacts, error)
	Redispatch(ctx context.Context, in store.Redispatch) (*store.RedispatchResult, error)
	AcceptOffer(ctx context.Context, in store.AcceptInput) (*store.AcceptResult, error)
	EndProNoShow(ctx context.Context, id uuid.UUID, close *store.CloseAssignment, from []string, reason, refundCause, refundKey string,
		at time.Time) ([]uuid.UUID, error)
	MakeProUnavailable(ctx context.Context, in store.Unavailable) (*store.UnavailableResult, error)

	DueOfferExpiries(ctx context.Context, now time.Time, limit int) ([]store.AssignmentRef, error)
	BookingsAwaitingOffer(ctx context.Context, now time.Time, limit int) ([]uuid.UUID, error)
	AlertUnassigned(ctx context.Context, now time.Time, limit int) ([]events.BookingCore, error)
	UnassignedPastCancel(ctx context.Context, now time.Time, limit int) ([]uuid.UUID, error)
	AssignedNotOnDuty(ctx context.Context, now time.Time, limit int) ([]store.AssignmentRef, error)
	MarkLate(ctx context.Context, now time.Time, limit int) ([]events.BookingCore, error)
	NoShows(ctx context.Context, now time.Time, limit int) ([]store.AssignmentRef, error)
	RescuesExpired(ctx context.Context, now time.Time, limit int) ([]store.RescueRef, error)
	StaleOnDuty(ctx context.Context, now time.Time, limit int) ([]store.ProRef, error)

	ProDuty(ctx context.Context, proID uuid.UUID) (*model.DutyState, error)
	SetDuty(ctx context.Context, proID uuid.UUID, on bool, fix *store.Fix, at time.Time) (*model.DutyState, bool, error)
	RecordFix(ctx context.Context, proID uuid.UUID, f store.Fix, at time.Time) error
	TravellingTo(ctx context.Context, proID uuid.UUID) ([]store.JobPoint, error)
	ProActiveBookingIDs(ctx context.Context, proID uuid.UUID) ([]uuid.UUID, error)

	ProOffers(ctx context.Context, proID uuid.UUID, now time.Time) ([]model.Offer, error)
	ProOffer(ctx context.Context, proID, offerID uuid.UUID, now time.Time) (*model.Offer, error)
	OfferRef(ctx context.Context, proID, offerID uuid.UUID) (*store.AssignmentRef, error)
	ProJobRecord(ctx context.Context, proID, bookingID uuid.UUID, now time.Time) (*store.ProJobRecord, error)
	ProJobIDs(ctx context.Context, proID uuid.UUID, mode, date string, after *store.BookingCursor, limit int) ([]store.BookingCursor, error)
	ProArea(ctx context.Context, proID uuid.UUID) (*model.ProArea, error)
	CityZones(ctx context.Context, city string) ([]model.ProZone, error)
}

// DispatchDeps wires dispatch, presence and realtime.
type DispatchDeps struct {
	Store DispatchStore
	// Realtime and Signer: nil turns live updates off (the token routes
	// answer 503).
	Realtime RealtimePublisher
	Signer   TokenSigner
	// Presence: nil keeps the latest fix in Postgres only.
	Presence Presence
}

// WithDispatch wires dispatch (A4).
func (s *Service) WithDispatch(d DispatchDeps) *Service {
	s.ds = d
	return s
}

func offerNotFound() *apperr.Error {
	return apperr.New(http.StatusNotFound, apperr.CodeOfferNotFound, "offer not found")
}

func offerExpired() *apperr.Error {
	return apperr.New(http.StatusGone, apperr.CodeOfferExpired, "this offer has expired")
}

func offerTaken() *apperr.Error {
	return apperr.New(http.StatusConflict, apperr.CodeOfferTaken, "this offer is no longer open")
}

// offerReserved offers a confirmed booking to the professional the
// customer picked (reserved_pro_id, whose block holds the slot) — and to
// nobody else (B1). Idempotent: a booking with a live offer or an accepted
// professional is left alone. A booking with no professional left (no
// reserved professional, or their block cannot be kept) goes to
// pro_unavailable for the customer to choose.
func (s *Service) offerReserved(ctx context.Context, id uuid.UUID) error {
	for attempt := 0; attempt < 3; attempt++ {
		f, err := s.ds.Store.DispatchFacts(ctx, id)
		if err != nil {
			return err
		}
		if f.Status != "confirmed" || f.Live != nil {
			return nil
		}
		if f.ReservedProID == nil {
			_, err := s.proUnavailable(ctx, id, unavailableSpec{from: []string{"confirmed"}, version: f.Version, actor: "system",
				cause: events.UnavailableNoProfessional, reason: "no professional holds the job"})
			if errors.Is(err, store.ErrStale) {
				continue
			}
			return err
		}
		now := s.nowUTC()
		req := f.Request()
		in := store.Redispatch{BookingID: id, From: []string{"confirmed"}, Version: f.Version, ActorKind: "system",
			Start: f.SlotStart, End: f.SlotEnd, BlockEnd: slots.BlockFor(f.SlotStart, req).End,
			Candidates: []uuid.UUID{*f.ReservedProID}, OfferExpiresAt: s.offerExpiry(now, f), At: now}
		res, err := s.ds.Store.Redispatch(ctx, in)
		if errors.Is(err, store.ErrStale) {
			continue
		}
		if err != nil {
			return err
		}
		if res.Exhausted {
			// The picked professional's calendar no longer admits the job.
			_, err := s.proUnavailable(ctx, id, unavailableSpec{from: []string{"confirmed"}, actor: "system",
				cause: events.UnavailableNoProfessional, reason: "the professional's calendar no longer admits the job",
				exclude: []uuid.UUID{*f.ReservedProID}})
			if errors.Is(err, store.ErrStale) {
				return nil
			}
			return err
		}
		if res.OfferID != nil && res.ProUserID != nil {
			s.publish(ctx, ProTopic(*res.ProUserID), FrameOfferNew, OfferFrame{OfferID: *res.OfferID, BookingID: id,
				ExpiresAt: res.ExpiresAt, SlotStart: f.SlotStart.UTC(), SlotEnd: f.SlotEnd.UTC(), At: now})
		}
		return nil
	}
	return store.ErrStale
}

// offerExpiry is when an offer made now lapses: 3 minutes for an ASAP job,
// else the city's windows (2 h when the slot is > 12 h away, else 10 min),
// never past a rescue deadline.
func (s *Service) offerExpiry(now time.Time, f *store.DispatchFacts) time.Time {
	if f.Asap {
		return now.Add(dispatch.ASAPOfferWindow)
	}
	return dispatch.OfferExpiry(now, f.SlotStart, f.Windows, f.RescueUntil)
}

// Dispatch offers a confirmed booking to the professional the customer
// picked. Idempotent.
func (s *Service) Dispatch(ctx context.Context, id uuid.UUID) error {
	if s.ds.Store == nil {
		return nil
	}
	err := s.offerReserved(ctx, id)
	if errors.Is(err, store.ErrStale) || errors.Is(err, store.ErrNotFound) {
		return nil // no longer confirmed: nothing to offer
	}
	return err
}

// endUnserved ends a booking whose professional did not turn up and whom
// nobody can replace: pro_no_show and a full refund.
func (s *Service) endUnserved(ctx context.Context, id uuid.UUID, close *store.CloseAssignment, from []string, reason string) error {
	live := s.liveBefore(ctx, id)
	refundID, err := s.ds.Store.EndProNoShow(ctx, id, close, from, reason, payments.CauseProNoShow,
		payments.RefundKey(id, payments.CauseProNoShow), s.nowUTC())
	if err != nil {
		return err
	}
	s.SubmitRefunds(ctx, refundIDs(refundID)...)
	s.afterCancelled(ctx, id, live)
	return nil
}

// endRescue ends a last-minute reassignment nobody accepted: after a
// no-show the booking is pro_no_show, otherwise cancelled by the system;
// either way with a full refund.
func (s *Service) endRescue(ctx context.Context, id uuid.UUID, cause string) error {
	if cause == events.CauseProNoShow {
		err := s.endUnserved(ctx, id, nil, []string{"confirmed"}, "nobody could replace the professional who did not arrive")
		if errors.Is(err, store.ErrStale) {
			return nil
		}
		return err
	}
	return s.systemCancel(ctx, id, "nobody could take over the job in time", payments.CauseUnassigned)
}

// systemCancel cancels a still-confirmed booking for the system with a
// full refund (store.CancelBooking, actor system, key
// doorstep:refund:{id}:<cause>).
func (s *Service) systemCancel(ctx context.Context, id uuid.UUID, reason, cause string) error {
	live := s.liveBefore(ctx, id)
	refundID, err := s.bk.Store.CancelBooking(ctx, id, nil, nil, s.nowUTC(), func(b *store.LockedBooking) (*store.CancelDecision, error) {
		if b.Status != "confirmed" {
			return nil, store.ErrStale
		}
		return &store.CancelDecision{ActorKind: "system", Reason: reason, RefundPaise: b.Refundable, RefundCause: cause,
			RefundKey: payments.RefundKey(id, cause)}, nil
	})
	if errors.Is(err, store.ErrStale) {
		return nil
	}
	if err != nil {
		return err
	}
	s.SubmitRefunds(ctx, refundIDs(refundID)...)
	s.afterCancelled(ctx, id, live)
	s.publishBookingNow(ctx, id)
	return nil
}

// ---------------------------------------------------------------- accept / decline / give back

// AcceptOffer accepts one of the professional's offers and answers the job,
// now with the address.
func (s *Service) AcceptOffer(ctx context.Context, user, offerID uuid.UUID) (*model.ProJob, error) {
	p, err := s.mine(ctx, user)
	if err != nil {
		return nil, err
	}
	res, err := s.ds.Store.AcceptOffer(ctx, store.AcceptInput{OfferID: offerID, ProID: p.ID, At: s.nowUTC()})
	var inel *store.IneligibleError
	switch {
	case errors.Is(err, store.ErrOfferNotFound), errors.Is(err, store.ErrNotFound):
		return nil, offerNotFound()
	case errors.Is(err, store.ErrOfferExpired):
		return nil, offerExpired()
	case errors.Is(err, store.ErrOfferTaken):
		return nil, offerTaken()
	case errors.As(err, &inel):
		return nil, ineligible(inel.Reason)
	case err != nil:
		return nil, internal(ctx, "accept offer", err)
	}
	if !res.Already {
		at := s.nowUTC()
		s.publish(ctx, ProTopic(user), FrameOfferClosed, OfferClosedFrame{OfferID: offerID, BookingID: res.BookingID,
			Outcome: dispatch.OfferAccepted, At: at})
		s.publish(ctx, ProTopic(user), FrameJobAssigned, JobFrame{BookingID: res.BookingID, Status: "assigned", At: at})
		s.publishBookingNow(ctx, res.BookingID)
	}
	return s.proJob(ctx, p.ID, res.BookingID)
}

func ineligible(reason string) *apperr.Error {
	switch reason {
	case "gender":
		return apperr.New(http.StatusForbidden, apperr.CodeGenderRule, "this job needs a professional of another gender")
	case "background":
		return apperr.New(http.StatusForbidden, apperr.CodeBackgroundCheckRequired,
			"your background check is not valid on the job's date")
	case "suspended":
		return apperr.New(http.StatusForbidden, apperr.CodeProSuspended, "your account is suspended")
	}
	return apperr.New(http.StatusForbidden, apperr.CodeProNotApproved, "your account is not approved for jobs")
}

// DeclineReasons are the reasons a professional may give.
var DeclineReasons = map[string]bool{"too_far": true, "busy": true, "not_my_skill": true, "other": true}

// DeclineOffer declines one of the professional's open offers; the next
// professional is offered the job in the same transaction. Declining twice
// is a no-op.
func (s *Service) DeclineOffer(ctx context.Context, user, offerID uuid.UUID, in model.DeclineInput) error {
	if in.Reason != nil && !DeclineReasons[*in.Reason] {
		return apperr.Invalid("reason", "reason must be too_far, busy, not_my_skill or other")
	}
	p, err := s.mine(ctx, user)
	if err != nil {
		return err
	}
	check := func() (*store.AssignmentRef, error) {
		ref, err := s.ds.Store.OfferRef(ctx, p.ID, offerID)
		if errors.Is(err, store.ErrNotFound) {
			return nil, offerNotFound()
		}
		if err != nil {
			return nil, internal(ctx, "offer", err)
		}
		switch ref.Status {
		case "offered":
			return ref, nil
		case "declined":
			return nil, nil
		case "expired":
			return nil, offerExpired()
		}
		return nil, offerTaken()
	}
	ref, err := check()
	if ref == nil || err != nil {
		return err
	}
	// B1: the booking waits for the customer to pick again (pro_unavailable);
	// nobody else is offered the job silently.
	_, err = s.proUnavailable(ctx, ref.BookingID, unavailableSpec{from: []string{"confirmed"}, actor: "pro", actorID: &user,
		cause: events.UnavailableDeclined, reason: "the professional declined the job",
		close: &store.CloseAssignment{ID: ref.ID, ProID: p.ID, From: []string{"offered"}, To: "declined", DeclineReason: in.Reason}})
	if errors.Is(err, store.ErrStale) {
		if _, err := check(); err != nil {
			return err
		}
		return offerTaken()
	}
	if err != nil {
		return internal(ctx, "decline offer", err)
	}
	return nil
}

// proCancellable are the statuses a professional may give a job back from
// (once arrived, the unsafe-exit and customer no-show routes apply).
var proCancellable = []string{"assigned", "en_route"}

// ProCancelJob gives an accepted job back before it starts: the penalty
// tier by how close the slot is, the cancellation counted, and the booking
// waits in pro_unavailable for the customer to pick again (B1).
func (s *Service) ProCancelJob(ctx context.Context, user, bookingID uuid.UUID, in model.ProCancelInput) error {
	reason, aerr := text("reason", in.Reason, 1, 500)
	if aerr != nil {
		return aerr
	}
	p, err := s.mine(ctx, user)
	if err != nil {
		return err
	}
	f, err := s.ds.Store.DispatchFacts(ctx, bookingID)
	if errors.Is(err, store.ErrNotFound) {
		return bookingNotFound()
	}
	if err != nil {
		return internal(ctx, "dispatch facts", err)
	}
	if f.Live == nil || f.Live.Status != "accepted" || f.Live.ProID != p.ID {
		return bookingNotFound()
	}
	if !store.Contains(proCancellable, f.Status) {
		return bookingTransition(f.Status)
	}
	penalty, _ := dispatch.CancelPenalty(s.nowUTC(), f.SlotStart, f.Status)
	_, err = s.proUnavailable(ctx, bookingID, unavailableSpec{from: proCancellable, actor: "pro", actorID: &user, reason: reason,
		cause: events.UnavailableProCancel,
		close: &store.CloseAssignment{ID: f.Live.ID, ProID: p.ID, From: []string{"accepted"}, To: "cancelled",
			Cause: events.CauseProCancel, PenaltyPaise: penalty, CountCancel: true}})
	if errors.Is(err, store.ErrStale) {
		return bookingTransition("changed")
	}
	if err != nil {
		return internal(ctx, "give job back", err)
	}
	return nil
}

// adminRedispatchable: ops may re-run dispatch up to arrival.
var adminRedispatchable = []string{"confirmed", "assigned", "en_route", "arrived"}

// AdminRedispatch takes the job off the current professional for ops (B1):
// the booking goes to pro_unavailable and the customer picks another
// professional (the current one and anyone ops list excluded). Ops never
// choose who gets the job, and nobody is reassigned silently. Audited with
// the change.
func (s *Service) AdminRedispatch(ctx context.Context, a store.Actor, id uuid.UUID, in model.AdminRedispatchInput) (*model.Booking, error) {
	reason, aerr := text("reason", in.Reason, 1, 1000)
	if aerr != nil {
		return nil, aerr
	}
	if len(in.ExcludeProIDs) > 50 {
		return nil, apperr.Invalid("exclude_pro_ids", "exclude at most 50 professionals")
	}
	f, err := s.ds.Store.DispatchFacts(ctx, id)
	if errors.Is(err, store.ErrNotFound) {
		return nil, apperr.New(http.StatusNotFound, apperr.CodeNotFound, "booking not found")
	}
	if err != nil {
		return nil, internal(ctx, "dispatch facts", err)
	}
	if !store.Contains(adminRedispatchable, f.Status) {
		return nil, bookingTransition(f.Status)
	}
	actor := a.UserID
	sp := unavailableSpec{from: adminRedispatchable, actor: "admin", actorID: &actor, reason: reason, exclude: in.ExcludeProIDs,
		cause: events.UnavailableOpsRedispatch, audit: &a}
	if f.Live != nil {
		from := "offered"
		if f.Live.Status == "accepted" {
			from = "accepted"
		}
		sp.close = &store.CloseAssignment{ID: f.Live.ID, ProID: f.Live.ProID, From: []string{from}, To: "released",
			Cause: events.CauseOpsRedispatch}
	} else if f.Status != "confirmed" {
		return nil, bookingTransition(f.Status)
	} else if f.ReservedProID != nil {
		sp.exclude = append(sp.exclude, *f.ReservedProID)
	}
	if _, err := s.proUnavailable(ctx, id, sp); errors.Is(err, store.ErrStale) {
		return nil, bookingTransition("changed")
	} else if err != nil {
		return nil, internal(ctx, "redispatch", err)
	}
	d, err := s.AdminBooking(ctx, id)
	if err != nil {
		return nil, err
	}
	return &d.Booking, nil
}

// ---------------------------------------------------------------- workers

// DispatchReport counts what one worker tick did. ChoiceTimeouts (B1):
// pro_unavailable bookings nobody picked a professional for in time.
type DispatchReport struct {
	Expired, Rescued, NoShows, Late, OffDuty, Cancelled, Alerted, Dispatched, StaleOff, ChoiceTimeouts int
}

const workerBatch = 100

// DispatchTick runs every dispatch rule once.
func (s *Service) DispatchTick(ctx context.Context) (DispatchReport, error) {
	var r DispatchReport
	if s.ds.Store == nil {
		return r, nil
	}
	var errs []error
	note := func(op string, err error) {
		if err != nil {
			slog.ErrorContext(ctx, "doorstep: dispatch worker "+op, "error", err)
			errs = append(errs, err)
		}
	}
	now := s.nowUTC()

	// Offers past their expiry: pro_unavailable, the customer picks again
	// (B1; never the next professional silently).
	if refs, err := s.ds.Store.DueOfferExpiries(ctx, now, workerBatch); err != nil {
		note("offer expiries", err)
	} else {
		for _, a := range refs {
			_, err := s.proUnavailable(ctx, a.BookingID, unavailableSpec{from: []string{"confirmed"}, actor: "system",
				cause: events.UnavailableOfferExpired, reason: "the professional did not answer the offer in time",
				close: &store.CloseAssignment{ID: a.ID, ProID: a.ProID, From: []string{"offered"}, To: "expired"}})
			if err == nil {
				r.Expired++
			} else if !errors.Is(err, store.ErrStale) {
				note("expire offer", err)
			}
		}
	}
	// Rescues nobody accepted.
	if refs, err := s.ds.Store.RescuesExpired(ctx, now, workerBatch); err != nil {
		note("rescues", err)
	} else {
		for _, x := range refs {
			if err := s.endRescue(ctx, x.BookingID, x.Cause); err != nil {
				note("end rescue", err)
			} else {
				r.Rescued++
			}
		}
	}
	// Slot + 15 min not arrived: the customer is told and may cancel free.
	if late, err := s.ds.Store.MarkLate(ctx, now, workerBatch); err != nil {
		note("late", err)
	} else {
		for _, c := range late {
			s.publish(ctx, BookingTopic(c.BookingID), FrameBookingLate, LateFrame{BookingID: c.BookingID,
				MinutesLate: int(now.Sub(c.SlotStart) / time.Minute), FreeCancel: true, At: now})
			r.Late++
		}
	}
	// Slot + 30 min not arrived: no-show (penalty); pro_unavailable, the
	// customer picks another professional (ASAP or a time) or cancels (B1).
	if refs, err := s.ds.Store.NoShows(ctx, now, workerBatch); err != nil {
		note("no-shows", err)
	} else {
		for _, a := range refs {
			_, err := s.proUnavailable(ctx, a.BookingID, unavailableSpec{from: []string{"assigned", "en_route"}, actor: "system",
				reason: "the professional did not arrive", cause: events.UnavailableProNoShow,
				close: &store.CloseAssignment{ID: a.ID, ProID: a.ProID, From: []string{"accepted"}, To: "no_show",
					Cause: events.CauseProNoShow, PenaltyPaise: dispatch.PenaltyNoShowPaise, CountNoShow: true}})
			if err == nil {
				r.NoShows++
			} else if !errors.Is(err, store.ErrStale) {
				note("no-show", err)
			}
		}
	}
	// Not on duty 90 minutes before the slot: pro_unavailable (B1).
	if refs, err := s.ds.Store.AssignedNotOnDuty(ctx, now, workerBatch); err != nil {
		note("not on duty", err)
	} else {
		for _, a := range refs {
			_, err := s.proUnavailable(ctx, a.BookingID, unavailableSpec{from: []string{"assigned"}, actor: "system",
				reason: "the professional was not on duty 90 minutes before the slot", cause: events.UnavailableNotOnDuty,
				close: &store.CloseAssignment{ID: a.ID, ProID: a.ProID, From: []string{"accepted"}, To: "released",
					Cause: events.CauseNotOnDuty}})
			if err == nil {
				r.OffDuty++
			} else if !errors.Is(err, store.ErrStale) {
				note("not on duty", err)
			}
		}
	}
	// T-45 unassigned: cancelled by the system, full refund.
	if ids, err := s.ds.Store.UnassignedPastCancel(ctx, now, workerBatch); err != nil {
		note("unassigned", err)
	} else {
		for _, id := range ids {
			if err := s.systemCancel(ctx, id, "no professional accepted the job in time", payments.CauseUnassigned); err != nil {
				note("cancel unassigned", err)
			} else {
				r.Cancelled++
			}
		}
	}
	// T-2 h unassigned: ops alert (once).
	if cores, err := s.ds.Store.AlertUnassigned(ctx, now, workerBatch); err != nil {
		note("alert", err)
	} else {
		for _, c := range cores {
			s.publish(ctx, AdminLiveTopic, FrameAdminUnassigned, AdminUnassignedFrame{BookingID: c.BookingID,
				Reason: events.AlertTMinus2h, MinutesToSlot: int(c.SlotStart.Sub(now) / time.Minute), At: now})
			r.Alerted++
		}
	}
	// Confirmed bookings with no live offer: offer them (again).
	if ids, err := s.ds.Store.BookingsAwaitingOffer(ctx, now, workerBatch); err != nil {
		note("awaiting offer", err)
	} else {
		for _, id := range ids {
			if err := s.Dispatch(ctx, id); err != nil {
				note("dispatch", err)
			} else {
				r.Dispatched++
			}
		}
	}
	// Stale GPS: off duty.
	if pros, err := s.ds.Store.StaleOnDuty(ctx, now, workerBatch); err != nil {
		note("stale fixes", err)
	} else {
		for _, p := range pros {
			if err := s.offDuty(ctx, p.ProID, p.UserID, p.CityCode, "no_location"); err != nil {
				note("stale off duty", err)
			} else {
				r.StaleOff++
			}
		}
	}
	// B1: pro_unavailable with no choice within 30 minutes: cancelled with a
	// full refund.
	if s.bk.Store != nil {
		if ids, err := s.bk.Store.ChoiceTimeouts(ctx, now, workerBatch); err != nil {
			note("choice timeouts", err)
		} else {
			for _, id := range ids {
				if err := s.choiceTimeout(ctx, id); err != nil {
					note("choice timeout", err)
				} else {
					r.ChoiceTimeouts++
				}
			}
		}
	}
	return r, errors.Join(errs...)
}

// RunDispatchWorkers runs DispatchTick until ctx ends.
func (s *Service) RunDispatchWorkers(ctx context.Context, every time.Duration) {
	if every <= 0 {
		every = 15 * time.Second
	}
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		if r, _ := s.DispatchTick(ctx); r != (DispatchReport{}) {
			slog.InfoContext(ctx, "doorstep: dispatch tick", "expired", r.Expired, "rescued", r.Rescued, "no_shows", r.NoShows,
				"late", r.Late, "off_duty", r.OffDuty, "cancelled", r.Cancelled, "alerted", r.Alerted, "dispatched", r.Dispatched,
				"stale_off", r.StaleOff, "choice_timeouts", r.ChoiceTimeouts)
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}
