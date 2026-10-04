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
		at time.Time) (*uuid.UUID, error)

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

// spec is one redispatch.
type spec struct {
	from    []string
	close   *store.CloseAssignment
	cause   string // reassigned cause when an assigned booking goes back
	actor   string
	actorID *uuid.UUID
	reason  string
	exclude []uuid.UUID
	// rescue: a cause to rescue the booking under when its slot is already
	// inside the unassigned-cancel deadline.
	rescue string
	// moveNow: a no-show; the job moves to the earliest a replacement can
	// start (within an hour) and is rescued.
	moveNow bool
	audit   *store.Actor
	// endIfNone ends the booking instead of leaving it confirmed when no
	// professional is free (a no-show with no replacement).
	endIfNone bool
}

// redispatch reads the booking, ranks the free professionals (minus every
// exclusion) and runs store.Redispatch, retrying when the booking moved
// under it. It then publishes the live frames and ends a rescue nobody can
// take.
func (s *Service) redispatch(ctx context.Context, id uuid.UUID, sp spec) (*store.RedispatchResult, error) {
	var lastErr error
	for attempt := 0; attempt < 3; attempt++ {
		f, err := s.ds.Store.DispatchFacts(ctx, id)
		if err != nil {
			return nil, err
		}
		if !store.Contains(sp.from, f.Status) {
			return nil, store.ErrStale
		}
		if sp.close == nil && f.Live != nil {
			return &store.RedispatchResult{NoOp: true, Status: f.Status, Customer: f.Customer}, nil
		}
		now := s.nowUTC()
		exclude := map[uuid.UUID]bool{}
		for _, p := range f.Excluded {
			exclude[p] = true
		}
		for _, p := range sp.exclude {
			exclude[p] = true
		}
		if sp.close != nil {
			exclude[sp.close.ProID] = true
		}
		in := store.Redispatch{BookingID: id, From: sp.from, Version: f.Version, Close: sp.close, Cause: sp.cause,
			ActorKind: sp.actor, ActorID: sp.actorID, Reason: sp.reason, Start: f.SlotStart, End: f.SlotEnd, Audit: sp.audit, At: now}
		req := f.Request()
		rescueUntil := f.RescueUntil
		feasible := true
		switch {
		case sp.moveNow:
			start, ok := dispatch.RescueStart(now, f.BufferMinutes)
			feasible = ok
			in.Start, in.End, in.MoveSlot = start, start.Add(time.Duration(f.Duration)*time.Minute), true
			ru := now.Add(dispatch.RescueWindow)
			in.RescueUntil, in.RescueCause, rescueUntil = &ru, sp.rescue, &ru
		case sp.rescue != "" && f.RescueUntil == nil && dispatch.NeedsRescue(now, f.SlotStart):
			ru := now.Add(dispatch.RescueWindow)
			in.RescueUntil, in.RescueCause, rescueUntil = &ru, sp.rescue, &ru
		}
		in.BlockEnd = slots.BlockFor(in.Start, req).End
		if feasible {
			pros, err := s.bk.Store.SlotCandidates(ctx, store.CandidateQuery{City: f.CityCode, ZoneID: f.ZoneID, Lat: f.Lat, Lng: f.Lng,
				Skill: f.Skill, From: in.Start.AddDate(0, 0, -1), To: in.Start.AddDate(0, 0, 7), ExcludeBooking: &id})
			if err != nil {
				return nil, err
			}
			in.Candidates = ranked(pros, in.Start, req, f.ReservedProID, f.Lat, f.Lng, exclude)
		}
		if len(in.Candidates) == 0 && sp.endIfNone {
			return nil, s.endUnserved(ctx, id, sp.close, sp.from, "the professional did not arrive and nobody could replace them")
		}
		in.OfferExpiresAt = dispatch.OfferExpiry(now, in.Start, f.Windows, rescueUntil)
		res, err := s.ds.Store.Redispatch(ctx, in)
		if errors.Is(err, store.ErrStale) {
			lastErr = err
			continue
		}
		if err != nil {
			return nil, err
		}
		s.afterRedispatch(ctx, id, f, in, res)
		if res.Exhausted && res.Rescue {
			cause := sp.rescue
			if f.RescueCause != nil {
				cause = *f.RescueCause
			}
			if err := s.endRescue(ctx, id, cause); err != nil {
				return res, err
			}
		}
		return res, nil
	}
	return nil, lastErr
}

// afterRedispatch publishes what a run changed.
func (s *Service) afterRedispatch(ctx context.Context, id uuid.UUID, f *store.DispatchFacts, in store.Redispatch, res *store.RedispatchResult) {
	at := s.nowUTC()
	if res.ClosedOutcome != "" && res.ClosedProUserID != nil && res.ClosedOfferID != nil {
		s.publish(ctx, ProTopic(*res.ClosedProUserID), FrameOfferClosed, OfferClosedFrame{OfferID: *res.ClosedOfferID, BookingID: id,
			Outcome: res.ClosedOutcome, At: at})
	}
	if res.PreviousProUserID != nil {
		s.publish(ctx, ProTopic(*res.PreviousProUserID), FrameJobRemoved, JobFrame{BookingID: id, Cause: in.Cause, At: at})
	}
	if res.Status != f.Status || in.MoveSlot {
		s.publishBooking(ctx, id, res.Status, in.Start, in.End)
	}
	if res.OfferID != nil && res.ProUserID != nil {
		s.publish(ctx, ProTopic(*res.ProUserID), FrameOfferNew, OfferFrame{OfferID: *res.OfferID, BookingID: id,
			ExpiresAt: res.ExpiresAt, SlotStart: in.Start.UTC(), SlotEnd: in.End.UTC(), At: at})
	}
	if res.Exhausted {
		s.publish(ctx, AdminLiveTopic, FrameAdminUnassigned, AdminUnassignedFrame{BookingID: id,
			Reason: events.AlertNoProfessionalLeft, MinutesToSlot: int(in.Start.Sub(at) / time.Minute), At: at})
	}
}

// Dispatch offers a confirmed booking to the professional its slot is held
// for (else the best free one). Idempotent: a booking with a live offer or
// an accepted professional is left alone.
func (s *Service) Dispatch(ctx context.Context, id uuid.UUID) error {
	if s.ds.Store == nil {
		return nil
	}
	_, err := s.redispatch(ctx, id, spec{from: []string{"confirmed"}, actor: "system"})
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
	_, err = s.redispatch(ctx, ref.BookingID, spec{from: []string{"confirmed"}, actor: "pro", actorID: &user,
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
// tier by how close the slot is, the cancellation counted, and the job
// re-offered to the next professional in the same transaction.
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
	_, err = s.redispatch(ctx, bookingID, spec{from: proCancellable, actor: "pro", actorID: &user, reason: reason,
		cause: events.CauseProCancel, rescue: events.CauseProCancel,
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

// AdminRedispatch re-runs dispatch for ops, excluding the current
// professional (offered or accepted) and anyone ops list. Ops never choose
// who gets the job. Audited with the change.
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
	sp := spec{from: adminRedispatchable, actor: "admin", actorID: &actor, reason: reason, exclude: in.ExcludeProIDs,
		cause: events.CauseOpsRedispatch, rescue: events.CauseOpsRedispatch, audit: &a}
	if f.Live != nil {
		from := "offered"
		if f.Live.Status == "accepted" {
			from = "accepted"
		}
		sp.close = &store.CloseAssignment{ID: f.Live.ID, ProID: f.Live.ProID, From: []string{from}, To: "released",
			Cause: events.CauseOpsRedispatch}
	} else if f.Status != "confirmed" {
		return nil, bookingTransition(f.Status)
	}
	if _, err := s.redispatch(ctx, id, sp); errors.Is(err, store.ErrStale) {
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

// DispatchReport counts what one worker tick did.
type DispatchReport struct {
	Expired, Rescued, NoShows, Late, OffDuty, Cancelled, Alerted, Dispatched, StaleOff int
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

	// Offers past their expiry: the next professional, in one transaction.
	if refs, err := s.ds.Store.DueOfferExpiries(ctx, now, workerBatch); err != nil {
		note("offer expiries", err)
	} else {
		for _, a := range refs {
			_, err := s.redispatch(ctx, a.BookingID, spec{from: []string{"confirmed"}, actor: "system",
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
	// Slot + 30 min not arrived: no-show; a replacement who can start
	// within the hour, else pro_no_show and a full refund.
	if refs, err := s.ds.Store.NoShows(ctx, now, workerBatch); err != nil {
		note("no-shows", err)
	} else {
		for _, a := range refs {
			_, err := s.redispatch(ctx, a.BookingID, spec{from: []string{"assigned", "en_route"}, actor: "system",
				reason: "the professional did not arrive", cause: events.CauseProNoShow, rescue: events.CauseProNoShow,
				moveNow: true, endIfNone: true,
				close: &store.CloseAssignment{ID: a.ID, ProID: a.ProID, From: []string{"accepted"}, To: "no_show",
					Cause: events.CauseProNoShow, PenaltyPaise: dispatch.PenaltyNoShowPaise, CountNoShow: true}})
			if err == nil {
				r.NoShows++
			} else if !errors.Is(err, store.ErrStale) {
				note("no-show", err)
			}
		}
	}
	// Not on duty 90 minutes before the slot: reassigned.
	if refs, err := s.ds.Store.AssignedNotOnDuty(ctx, now, workerBatch); err != nil {
		note("not on duty", err)
	} else {
		for _, a := range refs {
			_, err := s.redispatch(ctx, a.BookingID, spec{from: []string{"assigned"}, actor: "system",
				reason: "the professional was not on duty 90 minutes before the slot", cause: events.CauseNotOnDuty,
				rescue: events.CauseNotOnDuty,
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
				"stale_off", r.StaleOff)
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}
