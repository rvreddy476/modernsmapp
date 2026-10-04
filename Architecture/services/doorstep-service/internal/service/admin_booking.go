package service

import (
	"context"
	"errors"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/atpost/doorstep-service/internal/apperr"
	"github.com/atpost/doorstep-service/internal/model"
	"github.com/atpost/doorstep-service/internal/payments"
	"github.com/atpost/doorstep-service/internal/store"
	"github.com/google/uuid"
)

// Admin booking pages (admin-service tokens only). The admin views never
// carry the start or end OTP; list rows carry no address.

var dateRe = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)

var bookingStatuses = map[string]bool{"pending_payment": true, "confirmed": true, "assigned": true, "en_route": true,
	"arrived": true, "in_progress": true, "awaiting_extras_payment": true, "completed": true, "cancelled": true,
	"expired": true, "customer_no_show": true, "pro_no_show": true, "pro_unavailable": true}

// AdminBookings pages bookings (filters: status, city, IST date).
func (s *Service) AdminBookings(ctx context.Context, status, city, date, cursor string) (*model.BookingPage, error) {
	status, city, date = strings.TrimSpace(status), strings.ToUpper(strings.TrimSpace(city)), strings.TrimSpace(date)
	if status != "" && !bookingStatuses[status] {
		return nil, apperr.Invalid("status", "unknown booking status")
	}
	if city != "" && !cityCodeRe.MatchString(city) {
		return nil, apperr.Invalid("city", "city must be a three-letter city code")
	}
	if date != "" {
		if _, err := time.Parse("2006-01-02", date); err != nil || !dateRe.MatchString(date) {
			return nil, apperr.Invalid("date", "date must be YYYY-MM-DD")
		}
	}
	after, aerr := decodeBookingCursor(cursor)
	if aerr != nil {
		return nil, aerr
	}
	const limit = 50
	items, err := s.bk.Store.AdminBookings(ctx, store.AdminBookingFilter{Status: status, City: city, Date: date, After: after, Limit: limit})
	if err != nil {
		return nil, internal(ctx, "admin bookings", err)
	}
	p := page(items, limit)
	return &p, nil
}

// AdminBooking is the full booking for ops: history with actors, offers,
// payments, refunds, extras, photos. Never an OTP.
func (s *Service) AdminBooking(ctx context.Context, id uuid.UUID) (*model.AdminBookingDetail, error) {
	rec, err := s.bk.Store.BookingRecord(ctx, id, nil)
	if errors.Is(err, store.ErrNotFound) {
		return nil, apperr.New(http.StatusNotFound, apperr.CodeNotFound, "booking not found")
	}
	if err != nil {
		return nil, internal(ctx, "booking", err)
	}
	photos := rec.Booking.Photos
	b, aerr := s.bookingView(ctx, rec)
	if aerr != nil {
		return nil, aerr
	}
	b.StartOTP, b.EndOTP = nil, nil
	b.CanCancel, b.CanReschedule = false, false
	money, err := s.moneyView(ctx, id)
	if err != nil {
		return nil, err
	}
	assignments, err := s.bk.Store.BookingAssignments(ctx, id)
	if err != nil {
		return nil, internal(ctx, "assignments", err)
	}
	extras, err := s.bk.Store.BookingExtras(ctx, id)
	if err != nil {
		return nil, internal(ctx, "extras", err)
	}
	d := &model.AdminBookingDetail{Booking: *b, CustomerUserID: rec.CustomerUserID, ReservedProID: rec.ReservedProID,
		NeedsAttention: rec.NeedsAttention, AttentionReason: rec.AttentionReason, History: rec.History,
		Assignments: assignments, Payments: money.Payments, Refunds: money.Refunds, Extras: extras, Photos: photos}
	if d.History == nil {
		d.History = []model.HistoryEntry{}
	}
	if d.Assignments == nil {
		d.Assignments = []model.AssignmentView{}
	}
	if d.Extras == nil {
		d.Extras = []model.Extra{}
	}
	if d.Photos == nil {
		d.Photos = []model.Photo{}
	}
	d.ExcludedProIDs = rec.ExcludedProIDs
	if d.ExcludedProIDs == nil {
		d.ExcludedProIDs = []uuid.UUID{}
	}
	if d.ProChanges, err = s.proChanges(ctx, id); err != nil {
		return nil, err
	}
	return d, nil
}

// adminCancellable: ops may cancel up to and including in progress.
var adminCancellable = map[string]bool{"pending_payment": true, "confirmed": true, "assigned": true, "en_route": true,
	"arrived": true, "in_progress": true, "pro_unavailable": true}

// AdminCancel cancels for ops: a full refund unless fee_paise is given
// (capped at what is refundable). Audited in the same transaction.
func (s *Service) AdminCancel(ctx context.Context, a store.Actor, id uuid.UUID, in model.AdminCancelInput) (*model.AdminBookingDetail, error) {
	reason, aerr := text("reason", in.Reason, 1, 1000)
	if aerr != nil {
		return nil, aerr
	}
	var fee int64
	if in.FeePaise != nil {
		if *in.FeePaise < 0 {
			return nil, apperr.Invalid("fee_paise", "fee_paise must not be negative")
		}
		fee = *in.FeePaise
	}
	actor := a.UserID
	live := s.liveBefore(ctx, id)
	refundID, err := s.bk.Store.CancelBooking(ctx, id, nil, &a, s.nowUTC(), func(b *store.LockedBooking) (*store.CancelDecision, error) {
		if !adminCancellable[b.Status] {
			return nil, bookingTransition(b.Status)
		}
		f := fee
		if f > b.Refundable {
			f = b.Refundable
		}
		return &store.CancelDecision{ActorKind: "admin", ActorID: &actor, Reason: reason, FeePaise: f,
			RefundPaise: b.Refundable - f, RefundCause: payments.CauseAdminCancel,
			RefundKey: payments.RefundKey(id, payments.CauseAdminCancel)}, nil
	})
	if errors.Is(err, store.ErrNotFound) {
		return nil, apperr.New(http.StatusNotFound, apperr.CodeNotFound, "booking not found")
	}
	if aerr := cancelErr(ctx, err); aerr != nil {
		return nil, aerr
	}
	s.SubmitRefunds(ctx, refundIDs(refundID)...)
	s.afterCancelled(ctx, id, live)
	return s.AdminBooking(ctx, id)
}

// AdminRefund issues an ops refund of the booking payment: amount ≤ captured
// minus refunds that have not failed. Idempotent on the Idempotency-Key
// admin-service forwards (its two-person approval replays the same key).
func (s *Service) AdminRefund(ctx context.Context, a store.Actor, id uuid.UUID, idemKey string, in model.AdminRefundInput) (*model.Refund, bool, error) {
	idemKey = strings.TrimSpace(idemKey)
	if idemKey == "" || len(idemKey) > maxIdempotencyKey {
		return nil, false, apperr.Invalid("Idempotency-Key", "an Idempotency-Key header of 1 to 128 characters is required")
	}
	if in.AmountPaise == nil || *in.AmountPaise < 1 {
		return nil, false, apperr.Invalid("amount_paise", "amount_paise must be at least 1")
	}
	reason, aerr := text("reason", in.Reason, 1, 1000)
	if aerr != nil {
		return nil, false, aerr
	}
	if in.Payment != nil && *in.Payment != "booking" {
		if *in.Payment != "extras" {
			return nil, false, apperr.Invalid("payment", "payment must be booking or extras")
		}
		// Extras are paid from the visit lane; nothing is refundable here yet.
		return nil, false, apperr.New(http.StatusUnprocessableEntity, apperr.CodeRefundExceedsPaid, "no extras payment to refund")
	}
	cause := payments.CauseAdminPrefix + store.ShortHash(idemKey)
	r, created, err := s.bk.Store.AdminRequestRefund(ctx, a, store.AdminRefundRequest{BookingID: id, AmountPaise: *in.AmountPaise,
		Reason: reason, Cause: cause, Key: payments.RefundKey(id, cause)})
	switch {
	case errors.Is(err, store.ErrNotFound):
		return nil, false, apperr.New(http.StatusNotFound, apperr.CodeNotFound, "booking not found")
	case errors.Is(err, store.ErrExceedsPaid):
		return nil, false, apperr.New(http.StatusUnprocessableEntity, apperr.CodeRefundExceedsPaid,
			"the refund is more than was paid and not already refunded")
	case err != nil:
		return nil, false, internal(ctx, "admin refund", err)
	}
	if created {
		s.SubmitRefunds(ctx, r.ID)
		// Answer the row as submission left it (a replay reads the same).
		if j, err := s.bk.Store.RefundJob(ctx, r.ID); err == nil {
			r.Status = j.Status
		}
	}
	return r, created, nil
}

// AdminStats are the dashboard counts.
func (s *Service) AdminStats(ctx context.Context) (*model.AdminStats, error) {
	st, err := s.bk.Store.AdminStats(ctx, s.now())
	if err != nil {
		return nil, internal(ctx, "admin stats", err)
	}
	return st, nil
}
