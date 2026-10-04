package service

import (
	"context"
	"errors"
	"math"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/atpost/doorstep-service/internal/apperr"
	"github.com/atpost/doorstep-service/internal/catalogue"
	"github.com/atpost/doorstep-service/internal/matcher"
	"github.com/atpost/doorstep-service/internal/model"
	"github.com/atpost/doorstep-service/internal/slots"
	"github.com/atpost/doorstep-service/internal/store"
	"github.com/google/uuid"
)

// The customer picks the professional (B1, 4 Oct 2026): for a service, a
// selection and an address, every approved professional who passes every
// hard filter (approved, background check valid, verified skill, gender and
// salon rules, zone and radius, calendar) and has an approved price for
// every item of the selection, with that price (GST-inclusive, split by the
// tax computer), rating, a distance band (never exact) and the next free
// starts — or, for "as soon as possible", the on-duty professionals in range
// who opted in to same-day jobs, with an ETA. Sorted by price, rating or
// soonest. ASAP with nobody says so and lists the scheduled alternatives.

// Sorts of the professionals list.
const (
	SortPrice   = "price"
	SortRating  = "rating"
	SortSoonest = "soonest"
)

// List shapes.
const (
	ModeScheduled = "scheduled"
	ModeAsap      = "asap"
	// MaxProfessionals bounds one list.
	MaxProfessionals = 20
	// NextSlotsShown: starts per professional (on a chosen date: more).
	NextSlotsShown       = 3
	NextSlotsShownOnDate = 6
	// ReasonNoneAvailableNow: ASAP found nobody on duty in range who can
	// take it now.
	ReasonNoneAvailableNow = "none_available_now"
)

// ProfessionalsQuery is GET /services/{id}/professionals.
type ProfessionalsQuery struct {
	ServiceID     uuid.UUID
	OptionID      *uuid.UUID
	Quantity      *int
	AddonIDs      []uuid.UUID
	AddressID     *uuid.UUID
	Date          string
	Asap          bool
	Sort          string
	RequireFemale bool
}

// pickPlace is a selection at an address, ready to price and filter.
type pickPlace struct {
	bundle    *catalogue.ServiceBundle
	opt       catalogue.OptionRow
	addons    []catalogue.AddonRow
	sel       catalogue.Selection
	city      string
	state     string
	zoneID    uuid.UUID
	lat, lng  float64
	req       slots.Request
	cfg       slots.Config
	exclude   map[uuid.UUID]bool
	booking   *uuid.UUID
	baseTotal *int64 // the booking's total (alternatives)
}

func sortOf(raw string) (string, *apperr.Error) {
	switch strings.TrimSpace(raw) {
	case "", SortPrice:
		return SortPrice, nil
	case SortRating:
		return SortRating, nil
	case SortSoonest:
		return SortSoonest, nil
	}
	return "", apperr.Invalid("sort", "sort must be price, rating or soonest")
}

// ServiceProfessionals lists the professionals the customer may pick.
func (s *Service) ServiceProfessionals(ctx context.Context, user uuid.UUID, q ProfessionalsQuery) (*model.ProfessionalList, error) {
	if q.OptionID == nil || *q.OptionID == uuid.Nil {
		return nil, apperr.Invalid("option_id", "option_id is required")
	}
	if q.AddressID == nil || *q.AddressID == uuid.Nil {
		return nil, apperr.Invalid("address_id", "address_id is required")
	}
	if len(q.AddonIDs) > 50 {
		return nil, apperr.Invalid("addon_id", "too many add-ons")
	}
	sortBy, aerr := sortOf(q.Sort)
	if aerr != nil {
		return nil, aerr
	}
	if q.Asap && strings.TrimSpace(q.Date) != "" {
		return nil, apperr.Invalid("date", "send either date or asap=true, not both")
	}
	a, err := s.bk.Store.Address(ctx, user, *q.AddressID)
	if errors.Is(err, store.ErrNotFound) {
		return nil, addressNotFound()
	}
	if err != nil {
		return nil, internal(ctx, "address", err)
	}
	sel := catalogue.Selection{OptionID: *q.OptionID, Quantity: 1, AddonIDs: q.AddonIDs}
	if q.Quantity != nil {
		sel.Quantity = *q.Quantity
	}
	p, aerr := s.pickPlaceFor(ctx, a.CityCode, a.ZoneID, a.Lat, a.Lng, q.ServiceID, sel, q.RequireFemale)
	if aerr != nil {
		return nil, aerr
	}
	return s.professionalList(ctx, p, q.Date, q.Asap, sortBy)
}

// pickPlaceFor resolves a selection at a point: the bundle, the validated
// selection, the zone, the slot config and the request.
func (s *Service) pickPlaceFor(ctx context.Context, cityCode string, zoneID uuid.UUID, lat, lng float64, serviceID uuid.UUID,
	sel catalogue.Selection, requireFemale bool) (*pickPlace, *apperr.Error) {
	if zoneID == uuid.Nil {
		return nil, outsideArea()
	}
	city, buffer, active, err := s.bk.Store.ZoneFacts(ctx, zoneID)
	if errors.Is(err, store.ErrNotFound) || (err == nil && (!active || city != cityCode)) {
		return nil, outsideArea()
	}
	if err != nil {
		return nil, internal(ctx, "zone facts", err)
	}
	_, state, err := s.store.City(ctx, cityCode)
	if errors.Is(err, store.ErrNotFound) {
		return nil, outsideArea()
	}
	if err != nil {
		return nil, internal(ctx, "city", err)
	}
	now := s.now()
	b, err := s.store.ServiceBundle(ctx, cityCode, serviceID, now)
	if errors.Is(err, store.ErrNotFound) || (err == nil && !(b.Service.Active && b.Category.Active)) {
		return nil, apperr.New(http.StatusNotFound, apperr.CodeServiceNotFound, "service not found")
	}
	if err != nil {
		return nil, internal(ctx, "service bundle", err)
	}
	if !b.Visible() {
		return nil, catalogue.TaxPending()
	}
	opt, addons, verr := catalogue.ValidateSelection(b, sel)
	if verr != nil {
		return nil, verr
	}
	cfg, err := s.bk.Store.SlotConfig(ctx, cityCode, b.Category.ID)
	if errors.Is(err, store.ErrNotFound) {
		return nil, notAvailable()
	}
	if err != nil {
		return nil, internal(ctx, "slot config", err)
	}
	duration := opt.DurationMinutes * sel.Quantity
	for _, a := range addons {
		duration += a.ExtraDurationMinutes
	}
	return &pickPlace{bundle: b, opt: opt, addons: addons, sel: sel, city: cityCode, state: state, zoneID: zoneID, lat: lat, lng: lng,
		cfg: cfg, exclude: map[uuid.UUID]bool{},
		req: slots.Request{DurationMinutes: duration, BufferMinutes: buffer, GenderRule: b.Category.GenderRule, RequireFemale: requireFemale}}, nil
}

// professionalList builds the list for a place.
func (s *Service) professionalList(ctx context.Context, p *pickPlace, date string, asap bool, sortBy string) (*model.ProfessionalList, error) {
	now := s.nowUTC()
	out := &model.ProfessionalList{ServiceID: p.bundle.Service.ID, OptionID: p.opt.ID, Quantity: p.sel.Quantity, AddonIDs: p.sel.AddonIDs,
		BookingID: p.booking, Mode: ModeScheduled, Sort: sortBy, Timezone: "Asia/Kolkata", Items: []model.ProfessionalCard{},
		ScheduledAlternatives: []model.ProfessionalCard{}}
	if out.AddonIDs == nil {
		out.AddonIDs = []uuid.UUID{}
	}
	var day time.Time
	if date = strings.TrimSpace(date); date != "" {
		d, err := slots.ParseDay(date)
		if err != nil || !slots.InHorizon(now, d, p.cfg) {
			return nil, apperr.Invalid("date", "date must be YYYY-MM-DD within the booking horizon")
		}
		day, out.Date = d, &date
	}
	if asap {
		out.Mode = ModeAsap
	}
	cards, err := s.cards(ctx, p, now, day, asap, sortBy)
	if err != nil {
		return nil, err
	}
	out.Items = cards
	if asap && len(cards) == 0 {
		// Founder, 4 Oct: "include schedule as well in case no worker found".
		reason := ReasonNoneAvailableNow
		out.NoProfessional, out.NoProfessionalReason = true, &reason
		if out.ScheduledAlternatives, err = s.cards(ctx, p, now, time.Time{}, false, SortSoonest); err != nil {
			return nil, err
		}
	} else if !asap && len(cards) == 0 {
		out.NoProfessional = true
	}
	return out, nil
}

// cards prices and filters every candidate professional for a place.
func (s *Service) cards(ctx context.Context, p *pickPlace, now, day time.Time, asap bool, sortBy string) ([]model.ProfessionalCard, error) {
	from, to := slots.Range(now, p.cfg)
	pros, err := s.bk.Store.SlotCandidates(ctx, store.CandidateQuery{City: p.city, ZoneID: p.zoneID, Lat: p.lat, Lng: p.lng,
		Skill: p.bundle.Service.RequiredSkill, From: from, To: to, ServiceID: p.bundle.Service.ID, ExcludeBooking: p.booking})
	if err != nil {
		return nil, internal(ctx, "candidates", err)
	}
	ids := make([]uuid.UUID, 0, len(pros))
	for _, pr := range pros {
		if !p.exclude[pr.ID] {
			ids = append(ids, pr.ID)
		}
	}
	ps, err := s.pricing(ctx)
	if err != nil {
		return nil, err
	}
	prices, err := ps.ProItemPrices(ctx, p.city, now, ids, p.sel.ItemIDs())
	if err != nil {
		return nil, internal(ctx, "professional prices", err)
	}
	type ranked struct {
		card   model.ProfessionalCard
		rating float64
		first  time.Time
	}
	var list []ranked
	for _, pr := range pros {
		if p.exclude[pr.ID] {
			continue
		}
		priced, err := catalogue.PriceSelection(p.bundle, p.opt, p.addons, p.sel.Quantity, prices[pr.ID], p.state, now, s.tax)
		var ae *apperr.Error
		if errors.As(err, &ae) {
			continue // no approved price for the whole selection: not listed
		}
		if err != nil {
			return nil, internal(ctx, "price selection", err)
		}
		card := model.ProfessionalCard{ProID: pr.ID, FirstName: firstNameOf(pr.DisplayName), PhotoMediaID: pr.PhotoMediaID,
			RatingCount: pr.RatingCount, JobsCompleted: pr.JobsCompleted, NextSlots: []model.NextSlot{}, SameDay: pr.SameDay,
			Price: cardPrice(priced)}
		if pr.RatingCount > 0 {
			avg := math.Round(float64(pr.RatingSum)/float64(pr.RatingCount)*10) / 10
			card.RatingAvg = &avg
		}
		if p.baseTotal != nil {
			d := priced.TotalPaise - *p.baseTotal
			card.DifferencePaise = &d
		}
		var first time.Time
		if asap {
			if _, ok := slots.QualifiesASAP(pr, p.req, now); !ok {
				continue
			}
			w := slots.ASAPFor(pr, now, p.req)
			if _, ok := slots.FreeASAP(pr, w, p.cfg); !ok {
				continue
			}
			eta := w.EtaMinutes
			card.EtaMinutes, card.DistanceBand, first = &eta, slots.DistanceBand(pr.LiveDistanceM), w.Start
		} else {
			if _, ok := slots.Qualifies(pr, p.req); !ok {
				continue
			}
			limit := NextSlotsShown
			if !day.IsZero() {
				limit = NextSlotsShownOnDate
			}
			free := slots.NextFree(pr, now, p.cfg, p.req, day, limit)
			if len(free) == 0 {
				continue
			}
			for _, f := range free {
				card.NextSlots = append(card.NextSlots, model.NextSlot{Start: f.Start, End: f.End})
			}
			card.DistanceBand, first = slots.DistanceBand(pr.DistanceM), free[0].Start
		}
		list = append(list, ranked{card: card, first: first, rating: matcher.SmoothedRating(matcher.Candidate{
			RatingSum: pr.RatingSum, RatingCount: pr.RatingCount})})
	}
	sort.SliceStable(list, func(i, j int) bool {
		a, b := list[i], list[j]
		byPrice := func() (bool, bool) {
			if a.card.Price.TotalPaise != b.card.Price.TotalPaise {
				return a.card.Price.TotalPaise < b.card.Price.TotalPaise, true
			}
			return false, false
		}
		byRating := func() (bool, bool) {
			if a.rating != b.rating {
				return a.rating > b.rating, true
			}
			return false, false
		}
		bySoonest := func() (bool, bool) {
			if !a.first.Equal(b.first) {
				return a.first.Before(b.first), true
			}
			return false, false
		}
		order := []func() (bool, bool){byPrice, byRating, bySoonest}
		switch sortBy {
		case SortRating:
			order = []func() (bool, bool){byRating, byPrice, bySoonest}
		case SortSoonest:
			order = []func() (bool, bool){bySoonest, byPrice, byRating}
		}
		for _, f := range order {
			if less, decided := f(); decided {
				return less
			}
		}
		return a.card.ProID.String() < b.card.ProID.String()
	})
	out := make([]model.ProfessionalCard, 0, min(len(list), MaxProfessionals))
	for i := 0; i < len(list) && i < MaxProfessionals; i++ {
		out = append(out, list[i].card)
	}
	return out, nil
}

func cardPrice(p *catalogue.Priced) model.CardPrice {
	c := model.CardPrice{TotalPaise: p.TotalPaise, TaxablePaise: p.TaxablePaise, TaxPaise: p.TaxPaise, Lines: make([]model.PriceLine, 0, len(p.Lines))}
	for _, l := range p.Lines {
		c.Lines = append(c.Lines, model.PriceLine{Kind: l.Kind, RefID: l.RefID, Name: l.Name, Unit: l.Unit, Quantity: l.Quantity,
			UnitPricePaise: l.UnitPricePaise, LineTotalPaise: l.LineTotalPaise})
	}
	return c
}

func firstNameOf(display string) string {
	f := strings.Fields(display)
	if len(f) == 0 {
		return ""
	}
	return f[0]
}
