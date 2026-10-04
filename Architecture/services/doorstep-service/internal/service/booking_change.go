package service

import (
	"context"
	"errors"
	"log/slog"
	"math"
	"net/http"
	"time"

	"github.com/atpost/doorstep-service/internal/apperr"
	"github.com/atpost/doorstep-service/internal/cancelrules"
	"github.com/atpost/doorstep-service/internal/model"
	"github.com/atpost/doorstep-service/internal/payments"
	"github.com/atpost/doorstep-service/internal/slots"
	"github.com/atpost/doorstep-service/internal/store"
	"github.com/atpost/shared/paymentsclient"
	"github.com/google/uuid"
)

func cancelNotAllowed() *apperr.Error {
	return apperr.New(http.StatusConflict, apperr.CodeCancelNotAllowed, "this booking can no longer be cancelled (the job has started)")
}

// RuleProLate is the cancel-preview rule when the professional is late.
const RuleProLate = "pro_late_free"

func minutesLeft(now, slotStart time.Time) int {
	return int(math.Floor(slotStart.Sub(now).Minutes()))
}

// customerVerdict applies the cancellation rules to a booking.
func (s *Service) customerVerdict(ctx context.Context, b *store.LockedBooking) (cancelrules.Verdict, *apperr.Error) {
	stage, ok := cancelrules.Stage(b.Status)
	if !ok || !customerCancellable[b.Status] && b.Status != "in_progress" {
		return cancelrules.Verdict{}, bookingTransition(b.Status)
	}
	rules, err := s.bk.Store.CancellationRules(ctx, b.CityCode)
	if err != nil {
		return cancelrules.Verdict{}, internal(ctx, "cancellation rules", err)
	}
	v := cancelrules.Decide(rules, b.CategoryID, stage, minutesLeft(s.now(), b.SlotStart), b.Refundable)
	// A professional 15 minutes late (A4) makes cancelling free.
	if b.ProLateAt != nil && v.Allowed && (b.Status == "assigned" || b.Status == "en_route") {
		v.FeePaise, v.RefundPaise, v.Rule = 0, b.Refundable, RuleProLate
	}
	return v, nil
}

// CancelPreview is what cancelling now would cost.
func (s *Service) CancelPreview(ctx context.Context, user, id uuid.UUID) (*model.CancelPreview, error) {
	rec, err := s.bk.Store.BookingRecord(ctx, id, &user)
	if errors.Is(err, store.ErrNotFound) {
		return nil, bookingNotFound()
	}
	if err != nil {
		return nil, internal(ctx, "booking", err)
	}
	lb, err := s.previewLock(ctx, rec)
	if err != nil {
		return nil, err
	}
	if _, ok := cancelrules.Stage(lb.Status); !ok {
		return &model.CancelPreview{Allowed: false, Rule: lb.Status}, nil
	}
	v, aerr := s.customerVerdict(ctx, lb)
	if aerr != nil {
		return nil, aerr
	}
	return &model.CancelPreview{Allowed: v.Allowed, FeePaise: v.FeePaise, RefundPaise: v.RefundPaise, Rule: v.Rule}, nil
}

// previewLock builds the decision input from a read (no lock: a preview).
func (s *Service) previewLock(ctx context.Context, rec *store.BookingRecord) (*store.LockedBooking, error) {
	pays, refunds, err := s.bk.Store.BookingMoney(ctx, rec.Booking.ID)
	if err != nil {
		return nil, internal(ctx, "booking money", err)
	}
	var pending int64
	for _, r := range refunds {
		if r.Status != "failed" {
			pending += r.AmountPaise
		}
	}
	lb := &store.LockedBooking{ID: rec.Booking.ID, Customer: rec.CustomerUserID, CategoryID: rec.CategoryID, CityCode: rec.Booking.CityCode,
		Status: rec.Booking.Status, SlotStart: rec.Booking.SlotStart, PaidPaise: rec.Booking.PaidPaise, RefundedPaise: rec.Booking.RefundedPaise,
		Refundable: rec.Booking.PaidPaise - pending, RescheduleCount: rec.RescheduleCount, ProLateAt: rec.ProLateAt}
	if lb.Refundable < 0 {
		lb.Refundable = 0
	}
	for _, p := range pays {
		if p.ReferenceType == payments.RefBooking {
			lb.PaymentStatus = p.Status
		}
	}
	return lb, nil
}

// CancelBooking cancels the customer's booking: free before assignment (or
// 3 h out), the rule's fee otherwise, never after the start OTP. The refund
// (paid - fee) is written with the cancellation and submitted after commit.
func (s *Service) CancelBooking(ctx context.Context, user, id uuid.UUID, req model.CancelRequest) (*model.Booking, error) {
	reason, aerr := text("reason", req.Reason, 1, 500)
	if aerr != nil {
		return nil, aerr
	}
	live := s.liveBefore(ctx, id)
	refundID, err := s.bk.Store.CancelBooking(ctx, id, &user, nil, s.nowUTC(), func(b *store.LockedBooking) (*store.CancelDecision, error) {
		v, aerr := s.customerVerdict(ctx, b)
		if aerr != nil {
			return nil, aerr
		}
		if !v.Allowed {
			return nil, cancelNotAllowed()
		}
		return &store.CancelDecision{ActorKind: "customer", ActorID: &user, Reason: reason, FeePaise: v.FeePaise,
			RefundPaise: v.RefundPaise, RefundCause: payments.CauseCustomerCancel,
			RefundKey: payments.RefundKey(id, payments.CauseCustomerCancel)}, nil
	})
	if aerr := cancelErr(ctx, err); aerr != nil {
		return nil, aerr
	}
	s.SubmitRefunds(ctx, refundIDs(refundID)...)
	s.afterCancelled(ctx, id, live)
	return s.Booking(ctx, user, id)
}

func cancelErr(ctx context.Context, err error) *apperr.Error {
	var ae *apperr.Error
	switch {
	case err == nil:
		return nil
	case errors.As(err, &ae):
		return ae
	case errors.Is(err, store.ErrNotFound):
		return bookingNotFound()
	}
	return internal(ctx, "change booking", err)
}

func refundIDs(id *uuid.UUID) []uuid.UUID {
	if id == nil {
		return nil
	}
	return []uuid.UUID{*id}
}

// Reschedule moves the booking to another slot: free once, up to 3 h
// before. The current professional is kept when still free; otherwise the
// best other one is held (an assigned booking then goes back to confirmed
// and is offered again). New block then old released, one transaction.
func (s *Service) Reschedule(ctx context.Context, user, id uuid.UUID, req model.RescheduleRequest) (*model.Booking, error) {
	if req.SlotStart == nil {
		return nil, apperr.Invalid("slot_start", "slot_start is required")
	}
	rec, err := s.bk.Store.BookingRecord(ctx, id, &user)
	if errors.Is(err, store.ErrNotFound) {
		return nil, bookingNotFound()
	}
	if err != nil {
		return nil, internal(ctx, "booking", err)
	}
	now := s.nowUTC()
	check := func(status string, count int, slotStart time.Time) *apperr.Error {
		if !reschedulable[status] {
			return bookingTransition(status)
		}
		if count > 0 || now.Add(RescheduleCutoff).After(slotStart) {
			return apperr.New(http.StatusConflict, apperr.CodeRescheduleNotAllowed,
				"a booking can be moved once, up to 3 hours before the slot")
		}
		return nil
	}
	if aerr := check(rec.Booking.Status, rec.RescheduleCount, rec.Booking.SlotStart); aerr != nil {
		return nil, aerr
	}
	p := s.bookingPlace(ctx, rec)
	cfg, aerr := s.slotConfig(ctx, p)
	if aerr != nil {
		return nil, aerr
	}
	start := req.SlotStart.UTC()
	if start.Equal(rec.Booking.SlotStart) {
		return nil, apperr.Invalid("slot_start", "the booking is already at this slot")
	}
	if err := slots.Validate(now, start, cfg, p.Req.DurationMinutes); err != nil {
		return nil, slotUnavailable(slotReason(err))
	}
	pros, aerr := s.candidates(ctx, p, start.AddDate(0, 0, -1), start.AddDate(0, 0, 7))
	if aerr != nil {
		return nil, aerr
	}
	order := ranked(pros, start, p.Req, rec.ReservedProID, p.Lat, p.Lng, nil)
	if len(order) == 0 {
		return nil, slotUnavailable("no_professional")
	}
	live := s.liveBefore(ctx, id)
	newPro, err := s.bk.Store.RescheduleBooking(ctx, id, user, store.RescheduleMove{SlotStart: start,
		SlotEnd: start.Add(time.Duration(p.Req.DurationMinutes) * time.Minute), BlockEnd: slots.BlockFor(start, p.Req).End,
		Candidates: order}, now, func(b *store.LockedBooking) error {
		if aerr := check(b.Status, b.RescheduleCount, b.SlotStart); aerr != nil {
			return aerr
		}
		return nil
	})
	if errors.Is(err, store.ErrSlotTaken) {
		return nil, apperr.New(http.StatusConflict, apperr.CodeSlotTaken, "this slot was just taken; pick another")
	}
	if aerr := cancelErr(ctx, err); aerr != nil {
		return nil, aerr
	}
	s.afterReschedule(ctx, id, live, newPro)
	return s.Booking(ctx, user, id)
}

// afterReschedule tells the professionals and re-offers (A4): a
// professional who keeps the job hears it moved; one who lost it hears it
// was taken back (the outbox carries doorstep.booking.reassigned naming
// them); the booking is offered to whoever holds the new slot.
func (s *Service) afterReschedule(ctx context.Context, id uuid.UUID, live *store.AssignmentRef, newPro uuid.UUID) {
	s.publishBookingNow(ctx, id)
	if live != nil {
		if live.ProID == newPro {
			s.publish(ctx, ProTopic(live.ProUserID), FrameJobUpdated, JobFrame{BookingID: id, Status: live.Status,
				Cause: "rescheduled", At: s.nowUTC()})
		} else {
			s.tellRemoved(ctx, id, live, "rescheduled")
		}
	}
	if err := s.Dispatch(ctx, id); err != nil {
		slog.ErrorContext(ctx, "doorstep: re-offer after reschedule failed; the worker retries", "booking_id", id, "error", err)
	}
}

// ---------------------------------------------------------------- refunds

// refundBackoff is the resubmission schedule of a refund payments never
// accepted (attempt n waits refundBackoff[min(n, len-1)]).
var refundBackoff = []time.Duration{time.Minute, 2 * time.Minute, 5 * time.Minute, 15 * time.Minute, time.Hour}

// SubmitRefunds submits refund rows to payments-service after the
// transaction that wrote them. Failures stay requested (the worker retries
// with the same key) or, when payments refuses, fail and flag the booking.
func (s *Service) SubmitRefunds(ctx context.Context, ids ...uuid.UUID) {
	for _, id := range ids {
		j, err := s.bk.Store.RefundJob(ctx, id)
		if err != nil {
			slog.ErrorContext(ctx, "doorstep: refund not read for submission; the worker will retry", "refund_id", id, "error", err)
			continue
		}
		s.submitRefund(ctx, *j)
	}
}

func (s *Service) submitRefund(ctx context.Context, j store.RefundJob) bool {
	if j.Status != "requested" {
		return false
	}
	next := s.nowUTC().Add(refundBackoff[min(j.Attempts, len(refundBackoff)-1)])
	if s.bk.Payments == nil || j.IntentID == nil {
		_ = s.bk.Store.MarkRefundAttempt(ctx, j.ID, "payments not configured or intent unknown", false, next)
		return false
	}
	intentID, err := uuid.Parse(*j.IntentID)
	if err != nil {
		_ = s.bk.Store.MarkRefundAttempt(ctx, j.ID, "intent id is not a UUID", true, next)
		return false
	}
	acc, err := s.bk.Payments.Refund(ctx, intentID, paymentsclient.RefundRequest{ApplicationID: payments.ApplicationID,
		AmountMinor: j.AmountPaise, Reason: "doorstep " + j.Cause, IdempotencyKey: j.Key})
	if err != nil {
		terminal := errors.Is(err, paymentsclient.ErrRefused)
		slog.WarnContext(ctx, "doorstep: refund submission failed", "refund_id", j.ID, "booking_id", j.BookingID,
			"terminal", terminal, "error", err)
		if mErr := s.bk.Store.MarkRefundAttempt(ctx, j.ID, err.Error(), terminal, next); mErr != nil {
			slog.ErrorContext(ctx, "doorstep: refund attempt not recorded", "refund_id", j.ID, "error", mErr)
		}
		return false
	}
	if err := s.bk.Store.MarkRefundSubmitted(ctx, j.ID, acc.CommandID.String()); err != nil {
		slog.ErrorContext(ctx, "doorstep: refund accepted but not recorded; the same key resubmits it", "refund_id", j.ID, "error", err)
		return false
	}
	return true
}

// ResubmitPendingRefunds submits every refund payments never accepted whose
// next attempt is due (food's ResubmitPendingSystemRefunds). It returns how
// many were accepted.
func (s *Service) ResubmitPendingRefunds(ctx context.Context) (int, error) {
	jobs, err := s.bk.Store.UnsubmittedRefunds(ctx, 50)
	if err != nil {
		return 0, err
	}
	n := 0
	for _, j := range jobs {
		if s.submitRefund(ctx, j) {
			n++
		}
	}
	return n, nil
}

// AfterPaymentEvent runs after a payment event committed: it submits the
// refunds the event created (late capture) and, for a booking the event
// confirmed, publishes it and offers it to the professional its slot is held
// for (A4; the dispatch worker retries any offer that does not go out here).
func (s *Service) AfterPaymentEvent(ctx context.Context, a payments.Applied) {
	if len(a.RefundIDs) > 0 {
		s.SubmitRefunds(ctx, a.RefundIDs...)
	}
	switch a.Decision.Outcome {
	case payments.OutcomeConfirmed, payments.OutcomeLateCaptureConfirmed:
		s.publishBookingNow(ctx, a.BookingID)
		if err := s.Dispatch(ctx, a.BookingID); err != nil {
			slog.ErrorContext(ctx, "doorstep: dispatch after confirm failed; the worker retries", "booking_id", a.BookingID, "error", err)
		}
	}
}

// ExpireHolds runs the hold sweeper once.
func (s *Service) ExpireHolds(ctx context.Context) (int, error) {
	total := 0
	for i := 0; i < 20; i++ {
		n, err := s.bk.Store.ExpireHolds(ctx, 100)
		total += n
		if err != nil || n < 100 {
			return total, err
		}
	}
	return total, nil
}

// RunWorkers runs the hold sweeper and the refund resubmitter until ctx ends.
func (s *Service) RunWorkers(ctx context.Context, every time.Duration) {
	if every <= 0 {
		every = 30 * time.Second
	}
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		if n, err := s.ExpireHolds(ctx); err != nil {
			slog.ErrorContext(ctx, "doorstep: hold sweeper failed", "error", err)
		} else if n > 0 {
			slog.InfoContext(ctx, "doorstep: holds expired", "bookings", n)
		}
		if n, err := s.ResubmitPendingRefunds(ctx); err != nil {
			slog.ErrorContext(ctx, "doorstep: refund resubmission failed", "error", err)
		} else if n > 0 {
			slog.InfoContext(ctx, "doorstep: refunds resubmitted", "accepted", n)
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}
