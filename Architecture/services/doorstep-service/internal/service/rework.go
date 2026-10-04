package service

import (
	"context"
	"github.com/atpost/doorstep-service/internal/apperr"
	"github.com/atpost/doorstep-service/internal/catalogue"
	"github.com/atpost/doorstep-service/internal/model"
	"github.com/atpost/doorstep-service/internal/slots"
	"github.com/atpost/doorstep-service/internal/store"
	"github.com/google/uuid"
	"strings"
	"time"
	"unicode/utf8"
)

func zeroReworkPrice(p *catalogue.Priced) {
	p.TotalPaise, p.TaxablePaise, p.TaxPaise = 0, 0, 0
	for i := range p.Lines {
		l := &p.Lines[i]
		l.UnitPricePaise, l.LineTotalPaise, l.TaxablePaise, l.TaxPaise = 0, 0, 0, 0
	}
}

func reworkGuard(f *store.VisitFacts, user uuid.UUID, at time.Time) error {
	if e := visitOwner(f, user, true); e != nil {
		return e
	}
	if f.Status != "completed" || f.ParentBookingID != nil || f.CompletedAt == nil || f.ReworkDays <= 0 || !at.Before(f.CompletedAt.Add(time.Duration(f.ReworkDays)*24*time.Hour)) {
		return apperr.New(409, "DOORSTEP_REWORK_WINDOW_CLOSED", "a rework is available only within this service's coverage window")
	}
	return nil
}
func (s *Service) VisitReworks(ctx context.Context, user, id uuid.UUID) ([]model.ReworkRequest, error) {
	return s.aftercare.VisitReworks(ctx, id, user)
}
func (s *Service) RequestVisitRework(ctx context.Context, user, id uuid.UUID, in model.ReworkInput) (*model.ReworkRequest, error) {
	if in.Reason == nil || strings.TrimSpace(*in.Reason) == "" || utf8.RuneCountInString(*in.Reason) > 2000 {
		return nil, apperr.Invalid("reason", "describe the issue within 2000 characters")
	}
	if len(in.MediaIDs) > 5 {
		return nil, apperr.Invalid("media_ids", "attach up to 5 images")
	}
	f, e := s.visitAccess(ctx, user, id, true)
	if e != nil {
		return nil, e
	}
	at := s.nowUTC()
	if e = reworkGuard(f, user, at); e != nil {
		return nil, e
	}
	for _, raw := range in.MediaIDs {
		v, err := parseMediaID("media_ids", &raw)
		if err != nil {
			return nil, err
		}
		if err := s.prepareVisitPhoto(ctx, "media_ids", v, user); err != nil {
			return nil, err
		}
	}
	rec, e := s.bk.Store.BookingRecord(ctx, id, &user)
	if e != nil {
		return nil, s.visitErr(ctx, e)
	}
	same := in.SameProfessional == nil || *in.SameProfessional
	spec := store.ReworkSpec{ID: s.bk.NewID(), ChildID: s.bk.NewID(), BookingID: id, User: user, Reason: strings.TrimSpace(*in.Reason), MediaIDs: in.MediaIDs, Same: same, At: at, SlotStart: in.SlotStart}
	if in.SlotStart != nil {
		p := s.bookingPlace(ctx, rec)
		cfg, err := s.slotConfig(ctx, p)
		if err != nil {
			return nil, err
		}
		start := in.SlotStart.UTC()
		if e := slots.Validate(at, start, cfg, p.Req.DurationMinutes); e != nil {
			return nil, slotUnavailable(slotReason(e))
		}
		spec.SlotStart = &start
		spec.SlotEnd = start.Add(time.Duration(p.Req.DurationMinutes) * time.Minute)
		block := slots.BlockFor(start, p.Req)
		spec.BlockStart, spec.BlockEnd = block.Start, block.End
		if same {
			from, to := slots.Range(at, cfg)
			pros, err := s.candidates(ctx, p, from, to)
			if err != nil {
				return nil, err
			}
			if rec.ReservedProID == nil || len(ranked(pros, start, p.Req, rec.ReservedProID, rec.Lat, rec.Lng, nil)) == 0 {
				return nil, slotUnavailable("professional_unavailable")
			}
			spec.ProID = *rec.ReservedProID
		}
	}
	v, e := s.aftercare.RequestVisitRework(ctx, spec, func(locked *store.VisitFacts) error { return reworkGuard(locked, user, at) })
	if e != nil {
		return nil, s.visitErr(ctx, e)
	}
	if v.ChildBookingID != nil {
		s.publishBookingNow(ctx, *v.ChildBookingID)
		if same {
			_ = s.Dispatch(ctx, *v.ChildBookingID)
		}
	}
	return v, nil
}
