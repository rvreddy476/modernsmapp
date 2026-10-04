package service

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/atpost/doorstep-service/internal/apperr"
	"github.com/atpost/doorstep-service/internal/catalogue"
	"github.com/atpost/doorstep-service/internal/dispatch"
	"github.com/atpost/doorstep-service/internal/model"
	"github.com/atpost/doorstep-service/internal/payments"
	"github.com/atpost/doorstep-service/internal/slots"
	"github.com/atpost/doorstep-service/internal/store"
	"github.com/atpost/shared/paymentsclient"
	"github.com/google/uuid"
)

// pro_unavailable (B1, 4 Oct 2026). When the professional the customer
// picked declines, lets the offer lapse, gives the job back, is not on duty
// 90 minutes out, does not turn up, or ops take the job off them, the
// booking is NOT reassigned silently: it goes to pro_unavailable (payment
// kept, calendar released) and the customer is told (event + realtime). The
// customer then picks another professional and a time — scheduled or ASAP —
// for the same booking: no new payment; a cheaper professional's difference
// is refunded at once, a dearer one's is charged as a pro_change extras bill
// (payments reference doorstep_extras, key doorstep:extras:{bill}) while the
// professional is held; or cancels for a full refund. No choice within 30
// minutes cancels it with a full refund.

// unavailableSpec is one loss of the professional.
type unavailableSpec struct {
	from    []string
	close   *store.CloseAssignment
	cause   string
	actor   string
	actorID *uuid.UUID
	reason  string
	exclude []uuid.UUID
	audit   *store.Actor
	version int
}

// FrameProUnavailable tells the customer to pick again (doorstep.booking.<id>).
const FrameProUnavailable = "doorstep.booking.pro_unavailable"

// ProUnavailableFrame is the customer's frame: the cause and the deadline.
type ProUnavailableFrame struct {
	BookingID      uuid.UUID `json:"booking_id"`
	Cause          string    `json:"cause"`
	ChoiceDeadline time.Time `json:"choice_deadline"`
	At             time.Time `json:"at"`
}

// proUnavailable moves a booking to pro_unavailable and publishes what
// changed: the closed offer or the job taken back to the professional, and
// the booking's status and the choice to the customer.
func (s *Service) proUnavailable(ctx context.Context, id uuid.UUID, sp unavailableSpec) (*store.UnavailableResult, error) {
	now := s.nowUTC()
	deadline := now.Add(dispatch.ChoiceWindow)
	res, err := s.ds.Store.MakeProUnavailable(ctx, store.Unavailable{BookingID: id, From: sp.from, Version: sp.version, Close: sp.close,
		Cause: sp.cause, ActorKind: sp.actor, ActorID: sp.actorID, Reason: sp.reason, Exclude: sp.exclude, Deadline: deadline,
		Audit: sp.audit, At: now})
	if err != nil {
		return nil, err
	}
	s.SubmitRefunds(ctx, res.RefundIDs...)
	if res.ClosedOutcome != "" && res.ClosedOfferID != nil && res.ClosedProUserID != nil {
		s.publish(ctx, ProTopic(*res.ClosedProUserID), FrameOfferClosed, OfferClosedFrame{OfferID: *res.ClosedOfferID, BookingID: id,
			Outcome: res.ClosedOutcome, At: now})
	}
	if res.RemovedAccepted && res.ClosedProUserID != nil {
		s.publish(ctx, ProTopic(*res.ClosedProUserID), FrameJobRemoved, JobFrame{BookingID: id, Cause: sp.cause, At: now})
	}
	s.publishBooking(ctx, id, "pro_unavailable", res.SlotStart, res.SlotEnd)
	s.publish(ctx, BookingTopic(id), FrameProUnavailable, ProUnavailableFrame{BookingID: id, Cause: sp.cause,
		ChoiceDeadline: deadline, At: now})
	return res, nil
}

// proChanges is a booking's changes of professional, each pending one with
// its payment intent.
func (s *Service) proChanges(ctx context.Context, id uuid.UUID) ([]model.ProChange, error) {
	views, err := s.bk.Store.ProChanges(ctx, id)
	if err != nil {
		return nil, internal(ctx, "pro changes", err)
	}
	list := make([]model.ProChange, 0, len(views))
	for _, cv := range views {
		c := cv.Change
		if c.Status == "pending_payment" && cv.PaymentID != nil {
			pay, err := s.bk.Store.PaymentByID(ctx, *cv.PaymentID)
			if err != nil {
				return nil, internal(ctx, "change payment", err)
			}
			v := paymentView(*pay)
			c.PaymentIntent = &v
		}
		list = append(list, c)
	}
	return list, nil
}

// pendingChange is the booking's change waiting for payment, if any.
func (s *Service) pendingChange(ctx context.Context, rec *store.BookingRecord) (*model.ProChange, *apperr.Error) {
	if rec.PendingChangeID == nil {
		return nil, nil
	}
	list, err := s.proChanges(ctx, rec.Booking.ID)
	if err != nil {
		var ae *apperr.Error
		if errors.As(err, &ae) {
			return nil, ae
		}
		return nil, internal(ctx, "pending change", err)
	}
	for i := range list {
		if list[i].ID == *rec.PendingChangeID {
			return &list[i], nil
		}
	}
	return nil, nil
}

// alternativesPlace is a pro_unavailable booking's own selection and
// address, excluding everyone who let it go.
func (s *Service) alternativesPlace(ctx context.Context, rec *store.BookingRecord) (*pickPlace, *apperr.Error) {
	b := rec.Booking
	sel := catalogue.Selection{}
	for _, l := range b.Items {
		switch l.Kind {
		case "option":
			sel.OptionID, sel.Quantity = l.RefID, l.Quantity
		case "addon":
			sel.AddonIDs = append(sel.AddonIDs, l.RefID)
		}
	}
	if sel.OptionID == uuid.Nil {
		return nil, notAvailable()
	}
	p, aerr := s.pickPlaceFor(ctx, b.CityCode, b.ZoneID, rec.Lat, rec.Lng, b.ServiceID, sel, b.RequireFemalePro)
	if aerr != nil {
		return nil, aerr
	}
	for _, x := range rec.ExcludedProIDs {
		p.exclude[x] = true
	}
	id, total := b.ID, b.TotalPaise
	p.booking, p.baseTotal = &id, &total
	p.rework = b.ParentBookingID != nil
	return p, nil
}

func choiceClosed() *apperr.Error {
	return apperr.New(http.StatusConflict, apperr.CodeChoiceWindowClosed, "the time to pick another professional is over")
}

// BookingProfessionals lists the professionals the customer may pick for a
// pro_unavailable booking: the same selection and address, everyone who let
// it go excluded, each with the difference against what was paid.
func (s *Service) BookingProfessionals(ctx context.Context, user, id uuid.UUID, date string, asap bool, sortRaw string) (*model.ProfessionalList, error) {
	sortBy, aerr := sortOf(sortRaw)
	if aerr != nil {
		return nil, aerr
	}
	if asap && strings.TrimSpace(date) != "" {
		return nil, apperr.Invalid("date", "send either date or asap=true, not both")
	}
	rec, err := s.bk.Store.BookingRecord(ctx, id, &user)
	if errors.Is(err, store.ErrNotFound) {
		return nil, bookingNotFound()
	}
	if err != nil {
		return nil, internal(ctx, "booking", err)
	}
	if rec.Booking.Status != "pro_unavailable" {
		return nil, bookingTransition(rec.Booking.Status)
	}
	p, aerr := s.alternativesPlace(ctx, rec)
	if aerr != nil {
		return nil, aerr
	}
	return s.professionalList(ctx, p, date, asap, sortBy)
}

// ChangeProfessional applies the customer's pick for a pro_unavailable
// booking (Idempotency-Key): the professional's approved prices for the
// booking's selection, their calendar for the slot (or ASAP), then
// store.ChangeProfessional. A dearer professional opens the difference's
// payment intent (doorstep_extras) and waits for the signed event; a cheaper
// or equal one is confirmed now (the difference refunded) and offered the
// job.
func (s *Service) ChangeProfessional(ctx context.Context, user, id uuid.UUID, idemKey string, req model.ProChangeRequest) (*model.ProChangeResult, error) {
	idemKey = strings.TrimSpace(idemKey)
	if idemKey == "" || len(idemKey) > maxIdempotencyKey {
		return nil, apperr.Invalid("Idempotency-Key", "an Idempotency-Key header of 1 to 128 characters is required")
	}
	if req.ProID == nil || *req.ProID == uuid.Nil {
		return nil, apperr.Invalid("pro_id", "pro_id is required")
	}
	asap := req.Asap != nil && *req.Asap
	if (req.SlotStart == nil) == !asap {
		return nil, apperr.Invalid("slot_start", "send exactly one of slot_start or asap=true")
	}
	rec, err := s.bk.Store.BookingRecord(ctx, id, &user)
	if errors.Is(err, store.ErrNotFound) {
		return nil, bookingNotFound()
	}
	if err != nil {
		return nil, internal(ctx, "booking", err)
	}
	now := s.nowUTC()
	// A replay of a change already made answers it, whatever the status now.
	if prev, err := s.changeByKey(ctx, rec, idemKey); err != nil || prev != nil {
		if err != nil {
			return nil, err
		}
		if prev.ProID != *req.ProID || prev.Asap != asap || (!asap && !prev.SlotStart.Equal(req.SlotStart.UTC())) {
			return nil, apperr.Invalid("Idempotency-Key", "this Idempotency-Key was used for a different change")
		}
		return s.changeResult(ctx, user, id, prev.ID)
	}
	if rec.Booking.Status != "pro_unavailable" {
		return nil, bookingTransition(rec.Booking.Status)
	}
	if rec.Booking.ChoiceDeadline == nil || !now.Before(*rec.Booking.ChoiceDeadline) {
		return nil, choiceClosed()
	}
	for _, x := range rec.ExcludedProIDs {
		if x == *req.ProID {
			return nil, slotUnavailable("professional_excluded")
		}
	}
	p, aerr := s.alternativesPlace(ctx, rec)
	if aerr != nil {
		return nil, aerr
	}
	ps, err := s.pricing(ctx)
	if err != nil {
		return nil, err
	}
	prices, err := ps.ProItemPrices(ctx, p.city, now, []uuid.UUID{*req.ProID}, p.sel.ItemIDs())
	if err != nil {
		return nil, internal(ctx, "professional prices", err)
	}
	priced, err := catalogue.PriceSelection(p.bundle, p.opt, p.addons, p.sel.Quantity, prices[*req.ProID], p.state, now, s.tax)
	if err != nil {
		var ae *apperr.Error
		if errors.As(err, &ae) {
			return nil, ae
		}
		return nil, internal(ctx, "price selection", err)
	}
	if rec.Booking.ParentBookingID != nil {
		zeroReworkPrice(priced)
	}
	from, to := now.AddDate(0, 0, -1), now.AddDate(0, 0, p.cfg.HorizonDays+1)
	pros, err := s.bk.Store.SlotCandidates(ctx, store.CandidateQuery{City: p.city, ZoneID: p.zoneID, Lat: p.lat, Lng: p.lng,
		Skill: p.bundle.Service.RequiredSkill, From: from, To: to, ServiceID: p.bundle.Service.ID, ExcludeBooking: &id,
		ProIDs: []uuid.UUID{*req.ProID}})
	if err != nil {
		return nil, internal(ctx, "candidates", err)
	}
	if len(pros) != 1 {
		return nil, slotUnavailable("professional_unavailable")
	}
	pro := pros[0]
	var start, end, blockStart, blockEnd time.Time
	hold := p.cfg.HoldMinutes
	if asap {
		if _, ok := slots.QualifiesASAP(pro, p.req, now); !ok {
			return nil, slotUnavailable("not_available_now")
		}
		w := slots.ASAPFor(pro, now, p.req)
		if _, ok := slots.FreeASAP(pro, w, p.cfg); !ok {
			return nil, slotUnavailable("not_available_now")
		}
		start, end, blockStart, blockEnd = w.Start, w.End, w.BlockStart, w.BlockEnd
		hold = min(hold, ASAPHoldMinutes)
	} else {
		start = req.SlotStart.UTC()
		if err := slots.Validate(now, start, p.cfg, p.req.DurationMinutes); err != nil {
			return nil, slotUnavailable(slotReason(err))
		}
		if _, ok := slots.Qualifies(pro, p.req); !ok {
			return nil, slotUnavailable("professional_unavailable")
		}
		if _, ok := slots.FreeAt(pro, start, p.req); !ok {
			return nil, slotUnavailable("professional_unavailable")
		}
		block := slots.BlockFor(start, p.req)
		end, blockStart, blockEnd = start.Add(time.Duration(p.req.DurationMinutes)*time.Minute), block.Start, block.End
	}
	diff := priced.TotalPaise - rec.Booking.TotalPaise
	if diff > 0 && s.bk.ExtrasPayments == nil {
		return nil, paymentsUnavailable()
	}
	changeID, billID, paymentID := s.bk.NewID(), s.bk.NewID(), s.bk.NewID()
	cause := payments.CauseProChangePrefix + store.ShortHash(changeID.String())
	res, err := s.bk.Store.ChangeProfessional(ctx, store.ChangeInput{
		ID: changeID, BookingID: id, Customer: user, IdempotencyKey: idemKey, ToPro: *req.ProID, Asap: asap,
		SlotStart: start, SlotEnd: end, BlockStart: blockStart, BlockEnd: blockEnd, Duration: p.req.DurationMinutes,
		Items: priced.Lines, TotalPaise: priced.TotalPaise, TaxablePaise: priced.TaxablePaise, TaxPaise: priced.TaxPaise,
		HoldExpiresAt: now.Add(time.Duration(hold) * time.Minute), BillID: billID, PaymentID: paymentID,
		IntentKey: payments.ExtrasIntentKey(billID), RefundCause: cause, RefundKey: payments.RefundKey(id, cause), At: now,
	}, func(b *store.LockedBooking) error {
		if b.Status != "pro_unavailable" {
			return bookingTransition(b.Status)
		}
		if b.ChoiceDeadline == nil || !now.Before(*b.ChoiceDeadline) {
			return choiceClosed()
		}
		return nil
	})
	var ae *apperr.Error
	switch {
	case errors.As(err, &ae):
		return nil, ae
	case errors.Is(err, store.ErrExcluded):
		return nil, slotUnavailable("professional_excluded")
	case errors.Is(err, store.ErrSlotTaken):
		return nil, apperr.New(http.StatusConflict, apperr.CodeSlotTaken, "this professional was just booked for this time; pick again")
	case errors.Is(err, store.ErrPriceGone):
		return nil, catalogue.PriceUnavailable(nil)
	case errors.Is(err, store.ErrNotFound):
		return nil, bookingNotFound()
	case err != nil:
		return nil, internal(ctx, "change professional", err)
	}
	if !res.Replay {
		if res.Applied {
			s.SubmitRefunds(ctx, res.RefundIDs...)
			s.publishBookingNow(ctx, id)
			if err := s.Dispatch(ctx, id); err != nil {
				slog.ErrorContext(ctx, "doorstep: offer after a change of professional failed; the worker retries", "booking_id", id, "error", err)
			}
		} else if res.PaymentID != nil {
			if aerr := s.openChangeIntent(ctx, *res.PaymentID, user); aerr != nil {
				slog.WarnContext(ctx, "doorstep: change payment intent not opened yet; the client retries", "booking_id", id)
			}
		}
	}
	return s.changeResult(ctx, user, id, res.ChangeID)
}

// changeByKey finds a change the customer already made with this key.
func (s *Service) changeByKey(ctx context.Context, rec *store.BookingRecord, key string) (*model.ProChange, error) {
	views, err := s.bk.Store.ProChanges(ctx, rec.Booking.ID)
	if err != nil {
		return nil, internal(ctx, "pro changes", err)
	}
	for _, cv := range views {
		if cv.Key == key {
			c := cv.Change
			return &c, nil
		}
	}
	return nil, nil
}

// openChangeIntent opens (once, on doorstep:extras:{bill}) the difference's
// payments-service intent and attaches it to the payment row.
func (s *Service) openChangeIntent(ctx context.Context, paymentID, customer uuid.UUID) *apperr.Error {
	pay, err := s.bk.Store.PaymentByID(ctx, paymentID)
	if err != nil {
		return internal(ctx, "change payment", err)
	}
	if pay.IntentID != nil {
		return nil
	}
	if s.bk.ExtrasPayments == nil {
		return paymentsUnavailable()
	}
	intent, err := s.bk.ExtrasPayments.CreateIntent(ctx, paymentsclient.CreateIntentRequest{
		ApplicationID: payments.ApplicationID, ReferenceID: pay.ReferenceID, PayerID: customer, PayeeID: payments.PayeeID,
		AmountMinor: pay.AmountPaise, Currency: payments.Currency, Method: payments.Method, IdempotencyKey: pay.IntentKey,
	})
	if err != nil {
		slog.ErrorContext(ctx, "doorstep: change payment intent not opened", "payment_id", paymentID, "error", err)
		return paymentsUnavailable()
	}
	checkout := intent.ClientSession.AsMap()
	if checkout == nil {
		checkout = map[string]string{}
	}
	if err := s.bk.Store.AttachIntent(ctx, pay.ID, intent.ID.String(), checkout, s.nowUTC()); err != nil {
		return internal(ctx, "attach change intent", err)
	}
	return nil
}

// changeResult answers a change: the booking and the change (its intent
// opened now if it never was).
func (s *Service) changeResult(ctx context.Context, user, id, changeID uuid.UUID) (*model.ProChangeResult, error) {
	views, err := s.bk.Store.ProChanges(ctx, id)
	if err != nil {
		return nil, internal(ctx, "pro changes", err)
	}
	for _, cv := range views {
		if cv.Change.ID == changeID && cv.Change.Status == "pending_payment" && cv.PaymentID != nil {
			if aerr := s.openChangeIntent(ctx, *cv.PaymentID, user); aerr != nil {
				return nil, aerr
			}
		}
	}
	all, err := s.proChanges(ctx, id)
	if err != nil {
		return nil, err
	}
	b, err := s.Booking(ctx, user, id)
	if err != nil {
		return nil, err
	}
	for _, c := range all {
		if c.ID == changeID {
			return &model.ProChangeResult{Booking: *b, Change: c}, nil
		}
	}
	return nil, internal(ctx, "change result", errors.New("change not found after it was made"))
}

// choiceTimeout cancels a pro_unavailable booking nobody picked a
// professional for within the window: a full refund (cause
// pro_unavailable), actor system.
func (s *Service) choiceTimeout(ctx context.Context, id uuid.UUID) error {
	now := s.nowUTC()
	ids, err := s.bk.Store.CancelBooking(ctx, id, nil, nil, now, func(b *store.LockedBooking) (*store.CancelDecision, error) {
		if b.Status != "pro_unavailable" || b.ChoiceDeadline == nil || b.ChoiceDeadline.After(now) ||
			(b.PendingChangeUntil != nil && b.PendingChangeUntil.After(now)) {
			return nil, store.ErrStale
		}
		return &store.CancelDecision{ActorKind: "system", Reason: "no other professional was picked in time",
			RefundPaise: b.Refundable, RefundCause: payments.CauseProUnavailable,
			RefundKey: payments.RefundKey(id, payments.CauseProUnavailable)}, nil
	})
	if errors.Is(err, store.ErrStale) {
		return nil
	}
	if err != nil {
		return err
	}
	s.SubmitRefunds(ctx, ids...)
	s.publishBookingNow(ctx, id)
	return nil
}
