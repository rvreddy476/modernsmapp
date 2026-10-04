package service

import (
	"context"
	"errors"
	"github.com/atpost/doorstep-service/internal/apperr"
	"github.com/atpost/doorstep-service/internal/model"
	"github.com/atpost/doorstep-service/internal/payments"
	"github.com/atpost/doorstep-service/internal/store"
	"github.com/atpost/doorstep-service/internal/tax"
	"github.com/google/uuid"
	"net/http"
	"time"
)

func (s *Service) visitAccess(ctx context.Context, user, id uuid.UUID, customer bool) (*store.VisitFacts, error) {
	f, err := s.visit.VisitFacts(ctx, id, s.nowUTC())
	if err != nil {
		return nil, s.visitErr(ctx, err)
	}
	if err = visitOwner(f, user, customer); err != nil {
		return nil, err
	}
	return f, nil
}
func (s *Service) VisitExtras(ctx context.Context, user, id uuid.UUID, customer bool) ([]model.Extra, error) {
	if _, err := s.visitAccess(ctx, user, id, customer); err != nil {
		return nil, err
	}
	items, err := s.bk.Store.BookingExtras(ctx, id)
	if items == nil {
		items = []model.Extra{}
	}
	return items, s.visitErr(ctx, err)
}
func (s *Service) ProExtraOptions(ctx context.Context, user, id uuid.UUID) ([]model.ExtraOption, error) {
	f, err := s.visitAccess(ctx, user, id, false)
	if err != nil {
		return nil, err
	}
	if err = visitActivePro(f, user); err != nil {
		return nil, err
	}
	options, err := s.visit.VisitExtraOptions(ctx, id, s.nowUTC())
	if err != nil {
		return nil, s.visitErr(ctx, err)
	}
	out := make([]model.ExtraOption, 0, len(options))
	for _, o := range options {
		out = append(out, o.ExtraOption)
	}
	return out, nil
}
func extraGuard(f *store.VisitFacts, user uuid.UUID, quantity int, o store.VisitExtraOption) error {
	if err := visitActivePro(f, user); err != nil {
		return err
	}
	if f.Status != "in_progress" || f.FinishedAt != nil {
		return bookingTransition(f.Status)
	}
	if (f.Family == "BEAUTY_SALON" && o.Kind != "addon") || (f.ExtrasPolicy == "catalogue_addons_only" && o.Kind != "addon") {
		return apperr.New(422, "DOORSTEP_EXTRAS_NOT_ALLOWED", "only catalogue add-ons are allowed for this visit")
	}
	if quantity < 1 || quantity > o.MaxQuantity {
		return apperr.New(422, "DOORSTEP_EXTRA_INVALID", "quantity is outside the allowed range")
	}
	// Separate goods/parts need the founder's tax adviser decision; fail closed.
	if o.IsPart {
		return apperr.New(422, "DOORSTEP_EXTRAS_NOT_ALLOWED", "parts billing is not available until the tax treatment is confirmed")
	}
	return nil
}
func (s *Service) ProProposeExtra(ctx context.Context, user, id uuid.UUID, in model.ExtraInput) (*model.Extra, error) {
	if (in.RateCardID == nil) == (in.AddonID == nil) {
		return nil, apperr.Invalid("rate_card_id", "choose exactly one rate-card item or add-on")
	}
	if in.Quantity == nil {
		return nil, apperr.Invalid("quantity", "quantity is required")
	}
	if _, err := s.visitAccess(ctx, user, id, false); err != nil {
		return nil, err
	}
	media, ae := parseMediaID("evidence_media_id", in.EvidenceMediaID)
	if ae != nil {
		return nil, ae
	}
	if err := s.prepareVisitPhoto(ctx, "evidence_media_id", media, user); err != nil {
		return nil, err
	}
	e, err := s.visit.ProposeVisitExtra(ctx, store.NewExtra{ID: s.bk.NewID(), BookingID: id, User: user, Input: in, At: s.nowUTC()}, func(f *store.VisitFacts, o store.VisitExtraOption) (int64, int64, error) {
		if err := extraGuard(f, user, *in.Quantity, o); err != nil {
			return 0, 0, err
		}
		result, err := s.tax.SplitInclusive(tax.Input{Family: f.Family, PlaceOfSupplyState: f.StateCode, At: s.nowUTC(), Lines: []tax.Line{{Ref: "extra", GrossPaise: o.UnitPricePaise * int64(*in.Quantity)}}})
		if err != nil {
			return 0, 0, err
		}
		if len(result.Lines) != 1 {
			return 0, 0, errors.New("missing tax split")
		}
		return result.Lines[0].TaxablePaise, result.Lines[0].TaxPaise, nil
	})
	if errors.Is(err, store.ErrBadReference) {
		return nil, apperr.New(422, "DOORSTEP_EXTRA_INVALID", "this item is not available for this professional and service")
	}
	return e, s.visitErr(ctx, err)
}
func (s *Service) DecideExtra(ctx context.Context, user, id, extra uuid.UUID, status string) (*model.Extra, error) {
	bill := s.bk.NewID()
	e, err := s.visit.DecideVisitExtra(ctx, id, extra, status, s.nowUTC(), bill, s.bk.NewID(), payments.ExtrasIntentKey(bill), func(f *store.VisitFacts) error {
		if err := visitOwner(f, user, true); err != nil {
			return err
		}
		if f.Status != "in_progress" || f.FinishedAt != nil {
			return bookingTransition(f.Status)
		}
		return nil
	})
	if err != nil {
		return nil, s.visitErr(ctx, err)
	}
	s.publishBookingNow(ctx, id)
	return e, nil
}

func (s *Service) ProWithdrawExtra(ctx context.Context, user, id, extra uuid.UUID) error {
	err := s.visit.WithdrawVisitExtra(ctx, id, extra, s.nowUTC(), func(f *store.VisitFacts) error {
		if err := visitActivePro(f, user); err != nil {
			return err
		}
		if f.Status != "in_progress" || f.FinishedAt != nil {
			return bookingTransition(f.Status)
		}
		return nil
	})
	if err == nil {
		s.publishBookingNow(ctx, id)
	}
	return s.visitErr(ctx, err)
}
func (s *Service) CustomerExtrasBill(ctx context.Context, user, id uuid.UUID) (*model.ExtrasBill, error) {
	if _, err := s.visitAccess(ctx, user, id, true); err != nil {
		return nil, err
	}
	b, err := s.visit.VisitBill(ctx, id)
	return b, s.visitErr(ctx, err)
}
func (s *Service) ExtrasPaymentIntent(ctx context.Context, user, bill uuid.UUID) (*model.PaymentIntent, error) {
	p, err := s.visit.VisitBillPayment(ctx, bill, user)
	if err != nil {
		return nil, s.visitErr(ctx, err)
	}
	if ae := s.openChangeIntent(ctx, p.ID, user); ae != nil {
		return nil, ae
	}
	p, err = s.bk.Store.PaymentByID(ctx, p.ID)
	if err != nil {
		return nil, s.visitErr(ctx, err)
	}
	v := paymentView(*p)
	return &v, nil
}
func (s *Service) Outstanding(ctx context.Context, user uuid.UUID) (*model.Outstanding, error) {
	out, err := s.visit.VisitOutstanding(ctx, user)
	return out, s.visitErr(ctx, err)
}

func (s *Service) ProVisitNoShow(ctx context.Context, user, id uuid.UUID) (*model.ProJob, error) {
	f, err := s.visitAccess(ctx, user, id, false)
	if err != nil {
		return nil, err
	}
	fee, err := s.visit.MaxCancellationFee(ctx, f.CityCode, f.CategoryID)
	if err != nil {
		return nil, s.visitErr(ctx, err)
	}
	refunds, err := s.visit.CustomerNoShow(ctx, id, user, s.nowUTC(), func(f *store.VisitFacts) (*store.NoShowDecision, error) {
		if err := visitActivePro(f, user); err != nil {
			return nil, err
		}
		if f.Status != "arrived" || f.ArrivedAt == nil || s.nowUTC().Before(f.ArrivedAt.Add(15*time.Minute)) {
			return nil, apperr.New(http.StatusConflict, apperr.CodeInvalidTransition, "wait at least 15 minutes after arrival")
		}
		kept := min(fee, f.Refundable)
		return &store.NoShowDecision{FeePaise: kept, RefundPaise: f.Refundable - kept, RefundCause: "customer_no_show", RefundKey: payments.RefundKey(id, "customer_no_show"), Compensation: kept}, nil
	})
	if err != nil {
		return nil, s.visitErr(ctx, err)
	}
	s.SubmitRefunds(ctx, refunds...)
	s.publishBookingNow(ctx, id)
	return s.ProJob(ctx, user, id)
}
