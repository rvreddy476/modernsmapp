package http

import (
	"context"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/atpost/doorstep-service/internal/catalogue"
	"github.com/atpost/doorstep-service/internal/devseed"
	"github.com/atpost/doorstep-service/internal/model"
	"github.com/atpost/doorstep-service/internal/slots"
	"github.com/atpost/doorstep-service/internal/store"
	"github.com/google/uuid"
)

// fakePricingStore is an in-memory service.PricingStore for the B1
// contract fixtures: professionals' price rows (pending until an admin
// decides), the same-day opt-ins, and the live approved prices the
// professionals list, quotes and changes read. The SQL (effective dating,
// the approved-only guards, the audit trigger) is pinned by internal/itest.
type fakePricingStore struct {
	mu      sync.Mutex
	rows    []*fakePriceRow
	sameDay map[uuid.UUID]map[uuid.UUID]bool // pro -> service -> on
	// skills: the services each professional may price (declared skills).
	services map[uuid.UUID][]uuid.UUID
	audits   []store.Actor
	n        int
}

type fakePriceRow struct {
	pro uuid.UUID
	p   model.ProPrice
}

func newFakePricingStore() *fakePricingStore {
	f := &fakePricingStore{sameDay: map[uuid.UUID]map[uuid.UUID]bool{}, services: map[uuid.UUID][]uuid.UUID{}}
	// Approved, live prices (GST-inclusive paise). Asha matches the old city
	// prices; Ravi is cheaper; Lakshmi (the dispatch fixtures) as Asha.
	k, fc := "home-cleaning/kitchen-deep-cleaning", "salon-women/facial"
	kitchenPrices := func(occupied, chimney int64) map[string]int64 {
		return map[string]int64{"option:" + k + "/occupied": occupied, "option:" + k + "/empty": occupied - 30000,
			"addon:" + k + "/appliances/chimney": chimney, "addon:" + k + "/appliances/fridge": 29900}
	}
	facialPrices := map[string]int64{"option:" + fc + "/gold": 129900, "option:" + fc + "/cleanup": 69900,
		"addon:" + fc + "/mask/peel-off": 9900, "addon:" + fc + "/mask/charcoal": 19900,
		"addon:" + fc + "/add-ons/threading": 9900, "addon:" + fc + "/add-ons/head-massage": 29900}
	f.seed(fakePro1, devseed.ID("service", k), kitchenPrices(179900, 44900))
	f.seed(fakePro1, devseed.ID("service", fc), facialPrices)
	f.seed(fakePro2, devseed.ID("service", k), kitchenPrices(169900, 39900))
	// Ravi (a man) prices the women's facial too: the gender rule, not the
	// price list, keeps him off it.
	f.seed(fakePro2, devseed.ID("service", fc), facialPrices)
	f.seed(lakshmiPro, devseed.ID("service", k), kitchenPrices(179900, 44900))
	f.sameDay[fakePro2] = map[uuid.UUID]bool{devseed.ID("service", k): true}
	return f
}

var fakePricesFrom = time.Date(2026, 10, 1, 4, 30, 0, 0, time.UTC)

func (f *fakePricingStore) seed(pro, service uuid.UUID, prices map[string]int64) {
	keys := make([]string, 0, len(prices))
	for key := range prices {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		kind, item, _ := strings.Cut(key, ":")
		unit := "per_job"
		from := fakePricesFrom
		f.rows = append(f.rows, &fakePriceRow{pro: pro, p: model.ProPrice{ID: devseed.ID("fixture", "pro-price/"+pro.String()+"/"+key),
			ServiceID: service, ItemKind: kind, ItemID: devseed.ID(kind, item), Unit: unit, PricePaise: prices[key], Status: "approved",
			EffectiveFrom: &from, SubmittedAt: fakePricesFrom, ReviewedAt: &from}})
	}
	f.services[pro] = append(f.services[pro], service)
}

func (f *fakePricingStore) live(r *fakePriceRow, at time.Time) bool {
	p := r.p
	return p.Status == "approved" && p.EffectiveFrom != nil && !p.EffectiveFrom.After(at) && (p.EffectiveTo == nil || p.EffectiveTo.After(at))
}

func (f *fakePricingStore) ProItemPrices(_ context.Context, _ string, at time.Time, proIDs, itemIDs []uuid.UUID) (map[uuid.UUID]catalogue.ProPrices, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	want := map[uuid.UUID]bool{}
	for _, i := range itemIDs {
		want[i] = true
	}
	pros := map[uuid.UUID]bool{}
	for _, p := range proIDs {
		pros[p] = true
	}
	out := map[uuid.UUID]catalogue.ProPrices{}
	for _, r := range f.rows {
		if pros[r.pro] && want[r.p.ItemID] && f.live(r, at) {
			if out[r.pro] == nil {
				out[r.pro] = catalogue.ProPrices{}
			}
			out[r.pro][r.p.ItemID] = catalogue.ItemPrice{ID: r.p.ID, PricePaise: r.p.PricePaise, Unit: r.p.Unit}
		}
	}
	return out, nil
}

// priceLive reports whether a price row is an approved, live row of pro.
func (f *fakePricingStore) priceLive(pro, priceID uuid.UUID, at time.Time) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, r := range f.rows {
		if r.p.ID == priceID {
			return r.pro == pro && f.live(r, at)
		}
	}
	return false
}

func (f *fakePricingStore) ProPricing(_ context.Context, proID uuid.UUID, at time.Time) (*store.PricingFacts, []model.ProServicePricing, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := []model.ProServicePricing{}
	for _, svc := range f.services[proID] {
		if svc != devseed.ID("service", "home-cleaning/kitchen-deep-cleaning") {
			continue
		}
		v := model.ProServicePricing{ServiceID: svc, ServiceName: "Kitchen deep cleaning", CategorySlug: "home-cleaning",
			CategoryName: "Home cleaning", Family: "HOME_CLEANING", SkillCode: "deep_cleaning", SkillStatus: "verified",
			SameDay: f.sameDay[proID][svc], Items: []model.ProPriceItem{}}
		k := "home-cleaning/kitchen-deep-cleaning"
		for _, it := range []struct {
			kind, key, name, unit string
			max                   int
			suggested             int64
		}{
			{"option", k + "/empty", "Empty kitchen", "per_job", 1, 149900},
			{"option", k + "/occupied", "Occupied kitchen", "per_job", 1, 179900},
			{"addon", k + "/appliances/chimney", "Chimney cleaning", "per_job", 1, 44900},
			{"addon", k + "/appliances/fridge", "Fridge cleaning", "per_job", 1, 29900},
		} {
			s := it.suggested
			item := model.ProPriceItem{ItemKind: it.kind, ItemID: devseed.ID(it.kind, it.key), Name: it.name, Unit: it.unit,
				MaxQuantity: it.max, SuggestedPricePaise: &s}
			for _, r := range f.rows {
				if r.pro != proID || r.p.ItemID != item.ItemID {
					continue
				}
				p := r.p
				switch {
				case f.live(r, at):
					item.Approved = &p
				case p.Status == "pending":
					item.Pending = &p
				case p.Status == "rejected":
					item.Rejected = &p
				}
			}
			v.Items = append(v.Items, item)
		}
		out = append(out, v)
	}
	return &store.PricingFacts{Status: "approved"}, out, nil
}

func (f *fakePricingStore) SubmitProPrice(_ context.Context, in store.SubmitPrice) (*model.ProPrice, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	allowed := false
	for _, s := range f.services[in.ProID] {
		allowed = allowed || s == in.ServiceID
	}
	if !allowed {
		return nil, store.ErrSkillRequired
	}
	for _, r := range f.rows {
		if r.pro == in.ProID && r.p.ItemID == in.ItemID {
			if f.live(r, in.At) && r.p.PricePaise == in.PricePaise {
				return nil, store.ErrUnchanged
			}
			if r.p.Status == "pending" {
				reason := "superseded by a newer submission"
				r.p.Status, r.p.Reason = "withdrawn", &reason
			}
		}
	}
	f.n++
	p := model.ProPrice{ID: devseed.ID("fixture", "submitted-price-"+string(rune('a'+f.n))), ServiceID: in.ServiceID, ItemKind: in.ItemKind,
		ItemID: in.ItemID, Unit: "per_job", PricePaise: in.PricePaise, Status: "pending", SubmittedAt: in.At}
	f.rows = append(f.rows, &fakePriceRow{pro: in.ProID, p: p})
	return &p, nil
}

func (f *fakePricingStore) WithdrawProPrice(_ context.Context, proID, priceID uuid.UUID, at time.Time) (*model.ProPrice, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, r := range f.rows {
		if r.p.ID != priceID || r.pro != proID {
			continue
		}
		live := r.p.Status == "approved" && r.p.EffectiveTo == nil
		if r.p.Status != "pending" && !live {
			return nil, &store.TransitionError{Status: r.p.Status}
		}
		if live {
			t := at
			r.p.EffectiveTo = &t
		}
		r.p.Status = "withdrawn"
		cp := r.p
		return &cp, nil
	}
	return nil, store.ErrNotFound
}

func (f *fakePricingStore) SetSameDay(_ context.Context, proID, serviceID uuid.UUID, enabled bool, at time.Time) (*model.SameDaySetting, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	allowed := false
	for _, s := range f.services[proID] {
		allowed = allowed || s == serviceID
	}
	if !allowed {
		return nil, store.ErrSkillRequired
	}
	if f.sameDay[proID] == nil {
		f.sameDay[proID] = map[uuid.UUID]bool{}
	}
	f.sameDay[proID][serviceID] = enabled
	return &model.SameDaySetting{ServiceID: serviceID, SameDay: enabled, UpdatedAt: at}, nil
}

func (f *fakePricingStore) adminView(r *fakePriceRow) model.AdminProPrice {
	names := map[uuid.UUID]string{fakePro1: "Asha Rao", fakePro2: "Ravi Kumar", lakshmiPro: "Lakshmi Devi"}
	v := model.AdminProPrice{ProPrice: r.p, ProID: r.pro, ProDisplayName: names[r.pro], ProStatus: "approved", CityCode: "HYD",
		ServiceName: "Kitchen deep cleaning", CategorySlug: "home-cleaning", ItemName: "Occupied kitchen"}
	s := int64(179900)
	v.SuggestedPricePaise = &s
	for _, o := range f.rows {
		if o.pro == r.pro && o.p.ItemID == r.p.ItemID && o.p.Status == "approved" && o.p.EffectiveTo == nil && o.p.ID != r.p.ID {
			c := o.p.PricePaise
			v.CurrentApprovedPaise = &c
		}
	}
	return v
}

func (f *fakePricingStore) ListPriceReviews(_ context.Context, status, _ string, proID *uuid.UUID, _ *store.PriceCursor, limit int) ([]model.AdminProPrice, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := []model.AdminProPrice{}
	for _, r := range f.rows {
		if r.p.Status == status && (proID == nil || *proID == r.pro) && len(out) < limit {
			out = append(out, f.adminView(r))
		}
	}
	return out, nil
}

func (f *fakePricingStore) DecideProPrice(_ context.Context, a store.Actor, id uuid.UUID, approve bool, reason *string, at time.Time) (*model.AdminProPrice, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, r := range f.rows {
		if r.p.ID != id {
			continue
		}
		if r.p.Status != "pending" {
			return nil, &store.TransitionError{Status: r.p.Status}
		}
		t := at
		if approve {
			for _, o := range f.rows {
				if o.pro == r.pro && o.p.ItemID == r.p.ItemID && o.p.Status == "approved" && o.p.EffectiveTo == nil {
					o.p.EffectiveTo = &t
				}
			}
			r.p.Status, r.p.EffectiveFrom = "approved", &t
		} else {
			r.p.Status = "rejected"
		}
		r.p.ReviewedAt, r.p.Reason = &t, reason
		f.audits = append(f.audits, a)
		v := f.adminView(r)
		actor := a.UserID
		v.ReviewedBy = &actor
		return &v, nil
	}
	return nil, store.ErrNotFound
}

// ---- the booking fake's B1 half ----

func (f *fakeBookingStore) priceLive(pro, priceID uuid.UUID) bool {
	if f.prices == nil {
		return true
	}
	return f.prices.priceLive(pro, priceID, fixtureNow)
}

func firstNameOfFake(display string) string {
	if fs := strings.Fields(display); len(fs) > 0 {
		return fs[0]
	}
	return ""
}

func (f *fakeBookingStore) AbandonLapsedChanges(context.Context, time.Time, int) ([]uuid.UUID, error) {
	return nil, nil
}

func (f *fakeBookingStore) ChoiceTimeouts(_ context.Context, now time.Time, _ int) ([]uuid.UUID, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []uuid.UUID
	for _, id := range f.order {
		b := f.bookings[id]
		if b.status == "pro_unavailable" && b.deadline != nil && !b.deadline.After(now) {
			out = append(out, id)
		}
	}
	return out, nil
}

func (f *fakeBookingStore) PaymentByID(_ context.Context, id uuid.UUID) (*store.PaymentRow, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, b := range f.bookings {
		if b.payment.ID == id {
			cp := b.payment
			return &cp, nil
		}
		for _, p := range b.extra {
			if p.ID == id {
				cp := p
				return &cp, nil
			}
		}
	}
	return nil, store.ErrNotFound
}

func (f *fakeBookingStore) ProChanges(_ context.Context, id uuid.UUID) ([]store.ChangeView, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	b := f.bookings[id]
	if b == nil {
		return nil, store.ErrNotFound
	}
	out := []store.ChangeView{}
	for _, c := range b.changes {
		name := ""
		for _, p := range f.pros {
			if p.ID == c.in.ToPro {
				name = firstNameOfFake(p.DisplayName)
			}
		}
		v := model.ProChange{ID: c.in.ID, Status: c.status, ProID: c.in.ToPro, ProFirstName: name, Asap: c.in.Asap,
			SlotStart: c.in.SlotStart, SlotEnd: c.in.SlotEnd, PreviousTotalPaise: c.prev, NewTotalPaise: c.in.TotalPaise,
			DifferencePaise: c.in.TotalPaise - c.prev, CreatedAt: c.in.At}
		if v.Status == "applied" && v.DifferencePaise < 0 {
			v.RefundPaise = -v.DifferencePaise
		}
		cv := store.ChangeView{Change: v, Key: c.in.IdempotencyKey}
		if v.DifferencePaise > 0 {
			pid := c.in.PaymentID
			cv.PaymentID = &pid
			if c.status == "pending_payment" {
				h := c.in.HoldExpiresAt
				cv.Change.HoldExpiresAt = &h
			}
		}
		out = append(out, cv)
	}
	return out, nil
}

func (f *fakeBookingStore) ChangeProfessional(_ context.Context, in store.ChangeInput, decide func(*store.LockedBooking) error) (*store.ChangeResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	b := f.bookings[in.BookingID]
	if b == nil || b.nb.Customer != in.Customer {
		return nil, store.ErrNotFound
	}
	for _, c := range b.changes {
		if c.in.IdempotencyKey == in.IdempotencyKey {
			return &store.ChangeResult{ChangeID: c.in.ID, Replay: true, Applied: c.status == "applied"}, nil
		}
	}
	if err := decide(f.locked(b)); err != nil {
		return nil, err
	}
	for _, x := range b.excluded {
		if x == in.ToPro {
			return nil, store.ErrExcluded
		}
	}
	for _, c := range b.changes {
		if c.status == "pending_payment" {
			c.status = "abandoned"
		}
	}
	block := slots.Interval{Start: in.BlockStart, End: in.BlockEnd}
	if f.busy(in.ToPro, block, in.BookingID) {
		return nil, store.ErrSlotTaken
	}
	c := &fakeChange{in: in, prev: b.nb.TotalPaise}
	b.changes = append(b.changes, c)
	res := &store.ChangeResult{ChangeID: in.ID}
	diff := in.TotalPaise - b.nb.TotalPaise
	if diff > 0 {
		c.status = "pending_payment"
		b.extra = append(b.extra, store.PaymentRow{ID: in.PaymentID, BookingID: in.BookingID, ReferenceType: "doorstep_extras",
			ReferenceID: in.BillID, IntentKey: in.IntentKey, AmountPaise: diff, Status: "created", Checkout: map[string]string{}, CreatedAt: in.At})
		pid := in.PaymentID
		res.PaymentID = &pid
		return res, nil
	}
	c.status = "applied"
	from := b.status
	b.status, b.pro, b.block, b.deadline, b.cause, b.updated = "confirmed", in.ToPro, block, nil, nil, in.At
	b.nb.SlotStart, b.nb.SlotEnd, b.nb.Asap, b.nb.Items = in.SlotStart, in.SlotEnd, in.Asap, in.Items
	b.nb.TotalPaise, b.nb.TaxablePaise, b.nb.TaxPaise = in.TotalPaise, in.TaxablePaise, in.TaxPaise
	reason := "the customer picked another professional"
	b.history = append(b.history, model.HistoryEntry{FromStatus: &from, ToStatus: "confirmed", ActorKind: "customer", Reason: &reason, CreatedAt: in.At})
	if diff < 0 {
		res.RefundIDs = []uuid.UUID{f.addRefund(b, in.RefundCause, in.RefundKey, -diff, in.At)}
	}
	res.Applied = true
	return res, nil
}

// ---- the dispatch fake's B1 half ----

func (f *fakeDispatchStore) MakeProUnavailable(_ context.Context, in store.Unavailable) (*store.UnavailableResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.bk.mu.Lock()
	defer f.bk.mu.Unlock()
	b := f.bk.bookings[in.BookingID]
	if b == nil {
		return nil, store.ErrNotFound
	}
	if !store.Contains(in.From, b.status) {
		return nil, store.ErrStale
	}
	res := &store.UnavailableResult{Customer: b.nb.Customer, FromStatus: b.status, SlotStart: b.nb.SlotStart, SlotEnd: b.nb.SlotEnd}
	exclude := append([]uuid.UUID(nil), in.Exclude...)
	if in.Close != nil {
		var closed *fakeAssign
		for _, a := range f.assigns {
			if a.id == in.Close.ID && a.pro == in.Close.ProID && store.Contains(in.Close.From, a.status) {
				closed = a
			}
		}
		if closed == nil {
			return nil, store.ErrStale
		}
		was := closed.status
		closed.status = in.Close.To
		u := f.users[closed.pro]
		res.ClosedOfferID, res.ClosedProUserID, res.PreviousProUserID = &closed.id, &u, &u
		res.RemovedAccepted = was == "accepted"
		if was == "offered" {
			res.ClosedOutcome = map[string]string{"declined": "declined", "expired": "expired"}[in.Close.To]
			if res.ClosedOutcome == "" {
				res.ClosedOutcome = "withdrawn"
			}
		}
		exclude = append(exclude, closed.pro)
	}
	for _, a := range f.assigns {
		if a.booking == in.BookingID && (a.status == "offered" || a.status == "accepted") {
			a.status = "released"
		}
	}
	from := b.status
	d, cause := in.Deadline, in.Cause
	b.status, b.pro, b.deadline, b.cause, b.updated = "pro_unavailable", uuid.Nil, &d, &cause, in.At
	b.excluded = append(b.excluded, exclude...)
	reason := in.Reason
	b.history = append(b.history, model.HistoryEntry{FromStatus: &from, ToStatus: "pro_unavailable", ActorKind: in.ActorKind,
		Reason: &reason, CreatedAt: in.At})
	if in.Audit != nil {
		f.bk.audits = append(f.bk.audits, *in.Audit)
	}
	return res, nil
}
