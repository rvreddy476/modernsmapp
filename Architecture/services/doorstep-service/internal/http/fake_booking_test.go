package http

import (
	"context"
	"crypto/sha256"
	"sort"
	"sync"
	"time"

	"github.com/atpost/doorstep-service/internal/cancelrules"
	"github.com/atpost/doorstep-service/internal/devseed"
	"github.com/atpost/doorstep-service/internal/model"
	"github.com/atpost/doorstep-service/internal/payments"
	"github.com/atpost/doorstep-service/internal/slots"
	"github.com/atpost/doorstep-service/internal/store"
	"github.com/atpost/shared/paymentsclient"
	"github.com/google/uuid"
)

// fakeBookingStore is an in-memory service.BookingStore for the booking
// contract fixtures. It mirrors the rules the fixtures depend on (one
// booking per idempotency key, a consumed quote, the calendar refusing an
// overlapping block, refund rows with their keys); the SQL itself is pinned
// by internal/itest on doorstep_it_test.
type fakeBookingStore struct {
	mu          sync.Mutex
	addresses   map[uuid.UUID]*store.AddressRow
	addrOrder   []uuid.UUID
	quotes      map[uuid.UUID]*store.QuoteFacts
	pros        []slots.Pro
	bookings    map[uuid.UUID]*fakeBooking
	order       []uuid.UUID
	outstanding map[uuid.UUID][]uuid.UUID // customer -> bills (45000 paise each)
	refunds     []*fakeRefund
	audits      []store.Actor
	loseRace    bool
}

type fakeBooking struct {
	nb          store.NewBooking
	status      string
	pro         uuid.UUID
	payment     store.PaymentRow
	paid        int64
	refunded    int64
	fee         int64
	hold        *time.Time
	reschedules int
	history     []model.HistoryEntry
	updated     time.Time
	block       slots.Interval
}

type fakeRefund struct {
	r       model.Refund
	booking uuid.UUID
	key     string
	command string
}

func newFakeBookingStore() *fakeBookingStore {
	return &fakeBookingStore{addresses: map[uuid.UUID]*store.AddressRow{}, quotes: map[uuid.UUID]*store.QuoteFacts{},
		bookings: map[uuid.UUID]*fakeBooking{}, outstanding: map[uuid.UUID][]uuid.UUID{}, pros: fakeSlotPros()}
}

var (
	fakePro1 = devseed.ID("fixture", "pro-asha")
	fakePro2 = devseed.ID("fixture", "pro-ravi")
	westZone = devseed.ID("zone", "west-hitec-gachibowli")
)

// fakeSlotPros: two approved deep-cleaning professionals in the west zone,
// Monday to Saturday 09:00-19:00 (no Sunday hours), background clear for a
// year; Asha already has a job on Monday 5 Oct 10:00-13:30.
func fakeSlotPros() []slots.Pro {
	mk := func(id uuid.UUID, gender string, dist float64) slots.Pro {
		p := slots.Pro{ID: id, UserID: devseed.ID("fixture", "user-"+id.String()), Status: slots.StatusApproved, Gender: gender,
			SkillVerified: true, InZone: true, WithinRadius: true, MaxJobsPerDay: 4, JobsByDay: map[string]int{}, DistanceM: dist,
			BackgroundClear: []slots.DateRange{{From: time.Date(2026, 6, 1, 0, 0, 0, 0, slots.IST), Until: time.Date(2027, 6, 1, 0, 0, 0, 0, slots.IST)}}}
		for d := 1; d <= 6; d++ {
			p.Hours = append(p.Hours, slots.Window{Weekday: d, Start: 9 * 60, End: 19 * 60})
		}
		return p
	}
	asha, ravi := mk(fakePro1, "female", 1200), mk(fakePro2, "male", 4800)
	asha.Blocks = []slots.Interval{{Start: time.Date(2026, 10, 5, 10, 0, 0, 0, slots.IST), End: time.Date(2026, 10, 5, 13, 30, 0, 0, slots.IST)}}
	asha.JobsByDay["2026-10-05"] = 1
	return []slots.Pro{asha, ravi}
}

// addQuote registers a quote the fixtures book from (a copy of the quote
// POST /quotes produced, under its own id).
func (f *fakeBookingStore) addQuote(q model.Quote, lat, lng float64) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.quotes[q.ID] = &store.QuoteFacts{Quote: q, Lat: lat, Lng: lng, CategoryID: devseed.ID("category", "home-cleaning"),
		CategorySlug: "home-cleaning", GenderRule: "any", ServiceName: "Kitchen deep cleaning", RequiredSkill: "deep_cleaning", Bookable: true}
}

// ---- addresses ----

func (f *fakeBookingStore) Addresses(_ context.Context, user uuid.UUID) ([]store.AddressRow, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := []store.AddressRow{}
	for _, id := range f.addrOrder {
		if a := f.addresses[id]; a != nil && a.UserID == user {
			out = append(out, *a)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].IsDefault && !out[j].IsDefault })
	return out, nil
}

func (f *fakeBookingStore) Address(_ context.Context, user, id uuid.UUID) (*store.AddressRow, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if a := f.addresses[id]; a != nil && a.UserID == user {
		cp := *a
		return &cp, nil
	}
	return nil, store.ErrNotFound
}

func (f *fakeBookingStore) InsertAddress(_ context.Context, id, user uuid.UUID, w store.AddressWrite, at time.Time) (*store.AddressRow, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	has := false
	for _, a := range f.addresses {
		if a.UserID == user {
			has = true
			if w.IsDefault {
				a.IsDefault = false
			}
		}
	}
	a := &store.AddressRow{ID: id, UserID: user, Label: w.Label, LinesSealed: w.LinesSealed, Locality: w.Locality, CityCode: w.CityCode,
		Pincode: w.Pincode, Lat: w.Lat, Lng: w.Lng, ZoneID: w.ZoneID, IsDefault: w.IsDefault || !has, CreatedAt: at}
	f.addresses[id] = a
	f.addrOrder = append(f.addrOrder, id)
	cp := *a
	return &cp, nil
}

func (f *fakeBookingStore) UpdateAddress(_ context.Context, user, id uuid.UUID, w store.AddressWrite, _ time.Time) (*store.AddressRow, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	a := f.addresses[id]
	if a == nil || a.UserID != user {
		return nil, store.ErrNotFound
	}
	a.Label, a.LinesSealed, a.Locality, a.CityCode, a.Pincode, a.Lat, a.Lng, a.ZoneID = w.Label, w.LinesSealed, w.Locality,
		w.CityCode, w.Pincode, w.Lat, w.Lng, w.ZoneID
	a.IsDefault = a.IsDefault || w.IsDefault
	cp := *a
	return &cp, nil
}

func (f *fakeBookingStore) DeleteAddress(_ context.Context, user, id uuid.UUID, _ time.Time) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if a := f.addresses[id]; a == nil || a.UserID != user {
		return store.ErrNotFound
	}
	delete(f.addresses, id)
	return nil
}

// ---- slots ----

func (f *fakeBookingStore) QuoteFacts(_ context.Context, _, quoteID uuid.UUID) (*store.QuoteFacts, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if q := f.quotes[quoteID]; q != nil {
		cp := *q
		return &cp, nil
	}
	return nil, store.ErrNotFound
}

func (f *fakeBookingStore) ZoneFacts(_ context.Context, zone uuid.UUID) (string, int, bool, error) {
	if zone == westZone || zone == devseed.ID("zone", "central-banjara-jubilee") {
		return "HYD", 30, true, nil
	}
	return "", 0, false, store.ErrNotFound
}

func (f *fakeBookingStore) SlotConfig(context.Context, string, uuid.UUID) (slots.Config, error) {
	return slots.Config{OpenMinute: 8 * 60, CloseMinute: 20 * 60, StepMinutes: 30, LeadMinutes: 120, HorizonDays: 7, HoldMinutes: 10}, nil
}

func (f *fakeBookingStore) SlotCandidates(_ context.Context, q store.CandidateQuery) ([]slots.Pro, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]slots.Pro, 0, len(f.pros))
	for _, p := range f.pros {
		cp := p
		cp.Blocks = append([]slots.Interval(nil), p.Blocks...)
		cp.JobsByDay = map[string]int{}
		for k, v := range p.JobsByDay {
			cp.JobsByDay[k] = v
		}
		for _, id := range f.order {
			b := f.bookings[id]
			if b.pro == p.ID && b.status != "cancelled" && b.status != "expired" && (q.ExcludeBooking == nil || *q.ExcludeBooking != id) {
				cp.Blocks = append(cp.Blocks, b.block)
				cp.JobsByDay[slots.DayKey(b.block.Start)]++
			}
		}
		out = append(out, cp)
	}
	return out, nil
}

func (f *fakeBookingStore) OutstandingDue(_ context.Context, customer uuid.UUID) (int64, []uuid.UUID, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	bills := f.outstanding[customer]
	return int64(len(bills)) * 45000, bills, nil
}

// ---- bookings ----

func (f *fakeBookingStore) busy(pro uuid.UUID, iv slots.Interval, except uuid.UUID) bool {
	for _, p := range f.pros {
		if p.ID == pro {
			for _, b := range p.Blocks {
				if b.Overlaps(iv) {
					return true
				}
			}
		}
	}
	for id, b := range f.bookings {
		if id != except && b.pro == pro && b.status != "cancelled" && b.status != "expired" && b.block.Overlaps(iv) {
			return true
		}
	}
	return false
}

func (f *fakeBookingStore) CreateBooking(_ context.Context, nb store.NewBooking) (uuid.UUID, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, b := range f.bookings {
		if b.nb.Customer == nb.Customer && b.nb.IdempotencyKey == nb.IdempotencyKey {
			return uuid.Nil, store.ErrReplay
		}
	}
	q := f.quotes[nb.QuoteID]
	if q == nil || q.Quote.Status != model.QuoteOpen || !nb.At.Before(q.Quote.ExpiresAt) {
		return uuid.Nil, store.ErrQuoteGone
	}
	block := slots.Interval{Start: nb.SlotStart, End: nb.BlockEnd}
	pro := uuid.Nil
	for _, c := range nb.Candidates {
		if !f.loseRace && !f.busy(c, block, uuid.Nil) {
			pro = c
			break
		}
	}
	if pro == uuid.Nil {
		return uuid.Nil, store.ErrSlotTaken
	}
	q.Quote.Status = model.QuoteConsumed
	hold := nb.HoldExpiresAt
	b := &fakeBooking{nb: nb, status: "pending_payment", pro: pro, hold: &hold, updated: nb.At, block: block,
		payment: store.PaymentRow{ID: nb.PaymentID, BookingID: nb.ID, ReferenceType: payments.RefBooking, ReferenceID: nb.ID,
			IntentKey: nb.IntentKey, AmountPaise: nb.TotalPaise, Status: "created", Checkout: map[string]string{}, CreatedAt: nb.At}}
	b.history = append(b.history, model.HistoryEntry{ToStatus: "pending_payment", ActorKind: "customer", CreatedAt: nb.At})
	f.bookings[nb.ID] = b
	f.order = append(f.order, nb.ID)
	return pro, nil
}

// confirm is what the payment consumer does on a matching capture.
func (f *fakeBookingStore) confirm(id uuid.UUID, at time.Time) {
	f.mu.Lock()
	defer f.mu.Unlock()
	b := f.bookings[id]
	from := b.status
	b.status, b.paid, b.hold, b.updated = "confirmed", b.nb.TotalPaise, nil, at
	b.payment.Status = "succeeded"
	b.history = append(b.history, model.HistoryEntry{FromStatus: &from, ToStatus: "confirmed", ActorKind: "payment_event", CreatedAt: at})
}

func (f *fakeBookingStore) BookingByKey(_ context.Context, customer uuid.UUID, key string) (*store.BookingKeyed, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, b := range f.bookings {
		if b.nb.Customer == customer && b.nb.IdempotencyKey == key {
			return &store.BookingKeyed{ID: b.nb.ID, QuoteID: b.nb.QuoteID, AddressID: b.nb.Address.AddressID, SlotStart: b.nb.SlotStart}, nil
		}
	}
	return nil, store.ErrNotFound
}

func (f *fakeBookingStore) BookingPayment(_ context.Context, id uuid.UUID) (*store.PaymentRow, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	b := f.bookings[id]
	if b == nil {
		return nil, store.ErrNotFound
	}
	cp := b.payment
	return &cp, nil
}

func (f *fakeBookingStore) AttachIntent(_ context.Context, paymentID uuid.UUID, intentID string, checkout map[string]string, _ time.Time) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, b := range f.bookings {
		if b.payment.ID == paymentID {
			b.payment.IntentID, b.payment.Checkout = &intentID, checkout
			if b.payment.Status == "created" {
				b.payment.Status = "pending"
			}
			return nil
		}
	}
	return store.ErrNotFound
}

func (f *fakeBookingStore) BookingRecord(_ context.Context, id uuid.UUID, customer *uuid.UUID) (*store.BookingRecord, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	b := f.bookings[id]
	if b == nil || (customer != nil && b.nb.Customer != *customer) {
		return nil, store.ErrNotFound
	}
	nb := b.nb
	a := nb.Address
	rec := &store.BookingRecord{CustomerUserID: nb.Customer, CategoryID: nb.CategoryID, GenderRule: nb.GenderRule,
		RequiredSkill: "deep_cleaning", ReservedProID: &b.pro, AddressSealed: nb.AddressSealed, RescheduleCount: b.reschedules,
		BufferMinutes: 30, Lat: a.Lat, Lng: a.Lng, History: append([]model.HistoryEntry(nil), b.history...)}
	var outstanding int64
	rec.Booking = model.Booking{ID: nb.ID, Status: b.status, ServiceID: nb.ServiceID, ServiceName: "Kitchen deep cleaning",
		CategorySlug: nb.CategorySlug, CityCode: nb.CityCode, ZoneID: nb.ZoneID, SlotStart: nb.SlotStart, SlotEnd: nb.SlotEnd,
		DurationMinutes: nb.Duration, RequireFemalePro: nb.RequireFemale, Items: nb.Items, TotalPaise: nb.TotalPaise,
		TaxablePaise: nb.TaxablePaise, TaxPaise: nb.TaxPaise, PaidPaise: b.paid, RefundedPaise: b.refunded,
		CancellationFeePaise: b.fee, OutstandingPaise: outstanding, HoldExpiresAt: b.hold,
		Address: model.Address{ID: a.AddressID, Label: a.Label, Locality: a.Locality, CityCode: a.CityCode, Pincode: a.Pincode,
			Lat: a.Lat, Lng: a.Lng, ZoneID: a.ZoneID, CreatedAt: a.CreatedAt},
		Photos: []model.Photo{}, CreatedAt: nb.At, UpdatedAt: b.updated}
	for _, h := range b.history {
		rec.Booking.StatusHistory = append(rec.Booking.StatusHistory, model.StatusStep{FromStatus: h.FromStatus, ToStatus: h.ToStatus, CreatedAt: h.CreatedAt})
	}
	return rec, nil
}

func (f *fakeBookingStore) summary(b *fakeBooking) model.BookingSummary {
	return model.BookingSummary{ID: b.nb.ID, Status: b.status, ServiceName: "Kitchen deep cleaning", CategorySlug: b.nb.CategorySlug,
		SlotStart: b.nb.SlotStart, SlotEnd: b.nb.SlotEnd, TotalPaise: b.nb.TotalPaise, CreatedAt: b.nb.At}
}

func (f *fakeBookingStore) CustomerBookings(_ context.Context, customer uuid.UUID, mode string, _ *store.BookingCursor, limit int) ([]model.BookingSummary, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := []model.BookingSummary{}
	for _, id := range f.order {
		if b := f.bookings[id]; b.nb.Customer == customer {
			out = append(out, f.summary(b))
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].SlotStart.After(out[j].SlotStart) })
	if len(out) > limit+1 {
		out = out[:limit+1]
	}
	return out, nil
}

func (f *fakeBookingStore) BookingMoney(_ context.Context, id uuid.UUID) ([]store.PaymentRow, []model.Refund, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	b := f.bookings[id]
	if b == nil {
		return nil, nil, store.ErrNotFound
	}
	refunds := []model.Refund{}
	for _, r := range f.refunds {
		if r.booking == id {
			refunds = append(refunds, r.r)
		}
	}
	return []store.PaymentRow{b.payment}, refunds, nil
}

func (f *fakeBookingStore) CancellationRules(context.Context, string) ([]cancelrules.Rule, error) {
	ip := func(v int) *int { return &v }
	k := func(s string) uuid.UUID { return devseed.ID("cancellation_rule", "HYD/"+s) }
	return []cancelrules.Rule{
		{ID: k("unassigned"), Stage: "unassigned", Allowed: true},
		{ID: k("assigned-lt-60"), Stage: "assigned", MinutesBeforeLT: ip(60), FeePaise: 15000, Allowed: true},
		{ID: k("assigned-lt-180"), Stage: "assigned", MinutesBeforeLT: ip(180), FeePaise: 7500, Allowed: true, SortOrder: 1},
		{ID: k("assigned-free"), Stage: "assigned", Allowed: true, SortOrder: 2},
		{ID: k("en-route"), Stage: "en_route", FeePaise: 15000, Allowed: true},
		{ID: k("arrived"), Stage: "arrived", FeePaise: 20000, Allowed: true},
		{ID: k("in-progress"), Stage: "in_progress", Allowed: false},
	}, nil
}

func (f *fakeBookingStore) locked(b *fakeBooking) *store.LockedBooking {
	var pending int64
	for _, r := range f.refunds {
		if r.booking == b.nb.ID && r.r.Status != "failed" {
			pending += r.r.AmountPaise
		}
	}
	pid := b.payment.ID
	return &store.LockedBooking{ID: b.nb.ID, Customer: b.nb.Customer, CategoryID: b.nb.CategoryID, CityCode: b.nb.CityCode,
		Status: b.status, SlotStart: b.nb.SlotStart, SlotEnd: b.nb.SlotEnd, Duration: b.nb.Duration, PaidPaise: b.paid,
		RefundedPaise: b.refunded, Refundable: b.paid - pending, PaymentID: &pid, PaymentStatus: b.payment.Status,
		RescheduleCount: b.reschedules, ReservedProID: &b.pro}
}

func (f *fakeBookingStore) addRefund(b *fakeBooking, cause, key string, amount int64, at time.Time) uuid.UUID {
	sum := sha256.Sum256([]byte("refund:" + key))
	id, _ := uuid.FromBytes(sum[:16])
	f.refunds = append(f.refunds, &fakeRefund{booking: b.nb.ID, key: key,
		r: model.Refund{ID: id, PaymentID: b.payment.ID, Cause: cause, AmountPaise: amount, Status: "requested", CreatedAt: at}})
	return id
}

func (f *fakeBookingStore) CancelBooking(_ context.Context, id uuid.UUID, customer *uuid.UUID, audit *store.Actor, at time.Time,
	decide func(*store.LockedBooking) (*store.CancelDecision, error)) (*uuid.UUID, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	b := f.bookings[id]
	if b == nil || (customer != nil && b.nb.Customer != *customer) {
		return nil, store.ErrNotFound
	}
	d, err := decide(f.locked(b))
	if err != nil {
		return nil, err
	}
	from, reason := b.status, d.Reason
	b.status, b.fee, b.hold, b.updated = "cancelled", d.FeePaise, nil, at
	b.history = append(b.history, model.HistoryEntry{FromStatus: &from, ToStatus: "cancelled", ActorKind: d.ActorKind, Reason: &reason, CreatedAt: at})
	if audit != nil {
		f.audits = append(f.audits, *audit)
	}
	if d.RefundPaise > 0 {
		rid := f.addRefund(b, d.RefundCause, d.RefundKey, d.RefundPaise, at)
		return &rid, nil
	}
	return nil, nil
}

func (f *fakeBookingStore) RescheduleBooking(_ context.Context, id, customer uuid.UUID, mv store.RescheduleMove, at time.Time,
	decide func(*store.LockedBooking) error) (uuid.UUID, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	b := f.bookings[id]
	if b == nil || b.nb.Customer != customer {
		return uuid.Nil, store.ErrNotFound
	}
	if err := decide(f.locked(b)); err != nil {
		return uuid.Nil, err
	}
	block := slots.Interval{Start: mv.SlotStart, End: mv.BlockEnd}
	for _, c := range mv.Candidates {
		if !f.busy(c, block, id) {
			b.pro, b.block = c, block
			b.nb.SlotStart, b.nb.SlotEnd = mv.SlotStart, mv.SlotEnd
			b.reschedules++
			b.updated = at
			return c, nil
		}
	}
	return uuid.Nil, store.ErrSlotTaken
}

func (f *fakeBookingStore) ExpireHolds(context.Context, int) (int, error) { return 0, nil }

// ---- refunds ----

func (f *fakeBookingStore) RefundJob(_ context.Context, id uuid.UUID) (*store.RefundJob, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, r := range f.refunds {
		if r.r.ID == id {
			b := f.bookings[r.booking]
			return &store.RefundJob{ID: id, BookingID: r.booking, PaymentID: r.r.PaymentID, IntentID: b.payment.IntentID, Cause: r.r.Cause,
				Key: r.key, AmountPaise: r.r.AmountPaise, Status: r.r.Status}, nil
		}
	}
	return nil, store.ErrNotFound
}

func (f *fakeBookingStore) UnsubmittedRefunds(context.Context, int) ([]store.RefundJob, error) {
	return nil, nil
}

func (f *fakeBookingStore) MarkRefundSubmitted(_ context.Context, id uuid.UUID, cmd string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, r := range f.refunds {
		if r.r.ID == id && r.r.Status == "requested" {
			r.r.Status, r.command = "pending", cmd
		}
	}
	return nil
}

func (f *fakeBookingStore) MarkRefundAttempt(context.Context, uuid.UUID, string, bool, time.Time) error {
	return nil
}

func (f *fakeBookingStore) AdminRequestRefund(_ context.Context, a store.Actor, in store.AdminRefundRequest) (*model.Refund, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	b := f.bookings[in.BookingID]
	if b == nil {
		return nil, false, store.ErrNotFound
	}
	for _, r := range f.refunds {
		if r.key == in.Key {
			cp := r.r
			return &cp, false, nil
		}
	}
	if in.AmountPaise > f.locked(b).Refundable {
		return nil, false, store.ErrExceedsPaid
	}
	f.audits = append(f.audits, a)
	id := f.addRefund(b, in.Cause, in.Key, in.AmountPaise, fixtureNow)
	cp := f.refunds[len(f.refunds)-1].r
	_ = id
	return &cp, true, nil
}

// ---- admin ----

func (f *fakeBookingStore) AdminBookings(_ context.Context, flt store.AdminBookingFilter) ([]model.BookingSummary, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := []model.BookingSummary{}
	for _, id := range f.order {
		if b := f.bookings[id]; flt.Status == "" || b.status == flt.Status {
			out = append(out, f.summary(b))
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].SlotStart.After(out[j].SlotStart) })
	return out, nil
}

func (f *fakeBookingStore) BookingAssignments(context.Context, uuid.UUID) ([]model.AssignmentView, error) {
	return []model.AssignmentView{}, nil
}

func (f *fakeBookingStore) BookingExtras(context.Context, uuid.UUID) ([]model.Extra, error) {
	return []model.Extra{}, nil
}

func (f *fakeBookingStore) AdminStats(context.Context, time.Time) (*model.AdminStats, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	st := &model.AdminStats{ProsApproved: 2, ProsPendingVerification: 1, DocumentsPending: 1}
	for _, b := range f.bookings {
		if b.status == "confirmed" {
			st.GMVTodayPaise += b.paid
		}
		if slots.DayKey(b.nb.SlotStart) == "2026-10-04" {
			st.BookingsToday++
		}
	}
	for _, r := range f.refunds {
		if r.r.Status == "succeeded" {
			st.RefundsTodayPaise += r.r.AmountPaise
		}
	}
	return st, nil
}

// ---- payments-service ----

// fakePaymentsAPI answers like payments-service with a Razorpay adapter:
// deterministic intent ids, a client session, refunds keyed by their
// idempotency key.
type fakePaymentsAPI struct {
	mu       sync.Mutex
	intents  map[string]*paymentsclient.Intent
	refunds  map[string]uuid.UUID
	provider string
}

func newFakePaymentsAPI() *fakePaymentsAPI {
	return &fakePaymentsAPI{intents: map[string]*paymentsclient.Intent{}, refunds: map[string]uuid.UUID{}, provider: "razorpay"}
}

func (p *fakePaymentsAPI) CreateIntent(_ context.Context, in paymentsclient.CreateIntentRequest) (*paymentsclient.Intent, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if i := p.intents[in.IdempotencyKey]; i != nil {
		return i, nil
	}
	i := &paymentsclient.Intent{ID: devseed.ID("intent", in.IdempotencyKey), Status: "pending", AmountMinor: in.AmountMinor,
		Currency: "INR", ReferenceType: payments.RefBooking, ReferenceID: in.ReferenceID, PayerID: in.PayerID, PayeeID: in.PayeeID,
		ProviderRef: "order_FixtureDoorstep01", ApplicationID: in.ApplicationID,
		ClientSession: &paymentsclient.ClientSession{Provider: p.provider, OrderID: "order_FixtureDoorstep01", KeyID: "rzp_test_fixture",
			MerchantDisplayName: "Doorstep"}}
	p.intents[in.IdempotencyKey] = i
	return i, nil
}

func (p *fakePaymentsAPI) GetIntent(_ context.Context, id uuid.UUID) (*paymentsclient.Intent, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, i := range p.intents {
		if i.ID == id {
			return i, nil
		}
	}
	return nil, &paymentsclient.Error{Kind: paymentsclient.KindRefused, StatusCode: 404}
}

func (p *fakePaymentsAPI) VerifyCallback(_ context.Context, id uuid.UUID, _ paymentsclient.CallbackRequest) (*paymentsclient.CallbackVerdict, error) {
	i, err := p.GetIntent(context.Background(), id)
	if err != nil {
		return nil, err
	}
	return &paymentsclient.CallbackVerdict{Verified: true, Advisory: true, Status: "pending", AmountMinor: i.AmountMinor,
		PayerID: i.PayerID, ReferenceType: i.ReferenceType, ReferenceID: i.ReferenceID}, nil
}

func (p *fakePaymentsAPI) Refund(_ context.Context, intent uuid.UUID, in paymentsclient.RefundRequest) (*paymentsclient.RefundAccepted, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	cmd, ok := p.refunds[in.IdempotencyKey]
	if !ok {
		cmd = devseed.ID("refund_command", in.IdempotencyKey)
		p.refunds[in.IdempotencyKey] = cmd
	}
	return &paymentsclient.RefundAccepted{CommandID: cmd, IntentID: intent, AmountMinor: in.AmountMinor, Status: "submitted"}, nil
}
