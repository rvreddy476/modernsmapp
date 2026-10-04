package http

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/atpost/doorstep-service/internal/devseed"
	"github.com/atpost/doorstep-service/internal/dispatch"
	"github.com/atpost/doorstep-service/internal/events"
	"github.com/atpost/doorstep-service/internal/model"
	"github.com/atpost/doorstep-service/internal/store"
	"github.com/google/uuid"
)

// fakeDispatchStore is an in-memory service.DispatchStore for the A4
// contract fixtures, over the booking and professional fakes. It mirrors the
// rules the fixtures depend on (only the offered professional accepts, an
// expired or closed offer is refused, a redispatch closes the assignment and
// offers the first candidate, the address only after acceptance is the
// service's call); the SQL, the block move and every worker are pinned by
// internal/itest on doorstep_it_test.
type fakeDispatchStore struct {
	mu      sync.Mutex
	bk      *fakeBookingStore
	pro     *fakeProStore
	duty    map[uuid.UUID]*model.DutyState
	fixes   map[uuid.UUID]store.Fix
	assigns []*fakeAssign
	users   map[uuid.UUID]uuid.UUID // pro id -> user id
	n       int
}

type fakeAssign struct {
	id, booking, pro uuid.UUID
	status           string
	expires          time.Time
}

func newFakeDispatchStore(bk *fakeBookingStore, pro *fakeProStore) *fakeDispatchStore {
	f := &fakeDispatchStore{bk: bk, pro: pro, duty: map[uuid.UUID]*model.DutyState{}, fixes: map[uuid.UUID]store.Fix{},
		users: map[uuid.UUID]uuid.UUID{}}
	for _, p := range bk.pros {
		f.users[p.ID] = p.UserID
	}
	return f
}

func (f *fakeDispatchStore) nextID() uuid.UUID {
	f.n++
	return devseed.ID("fixture", fmt.Sprintf("offer-%d", f.n))
}

func (f *fakeDispatchStore) ref(a *fakeAssign) store.AssignmentRef {
	return store.AssignmentRef{ID: a.id, BookingID: a.booking, ProID: a.pro, ProUserID: f.users[a.pro], Status: a.status, ExpiresAt: a.expires}
}

func (f *fakeDispatchStore) DispatchFacts(_ context.Context, id uuid.UUID) (*store.DispatchFacts, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.bk.mu.Lock()
	b := f.bk.bookings[id]
	f.bk.mu.Unlock()
	if b == nil {
		return nil, store.ErrNotFound
	}
	nb := b.nb
	pro := b.pro
	fa := &store.DispatchFacts{BookingID: id, Customer: nb.Customer, Status: b.status, Version: 1, CityCode: nb.CityCode,
		ZoneID: nb.ZoneID, CategoryID: nb.CategoryID, CategorySlug: nb.CategorySlug, Skill: "deep_cleaning", GenderRule: nb.GenderRule,
		RequireFemale: nb.RequireFemale, Lat: nb.Address.Lat, Lng: nb.Address.Lng, Locality: nb.Address.Locality,
		SlotStart: nb.SlotStart, SlotEnd: nb.SlotEnd, Duration: nb.Duration, BufferMinutes: 30, Windows: dispatch.DefaultWindows}
	if pro != uuid.Nil {
		fa.ReservedProID = &pro
	}
	for _, a := range f.assigns {
		if a.booking != id {
			continue
		}
		switch a.status {
		case "offered", "accepted":
			r := f.ref(a)
			fa.Live = &r
		default:
			fa.Excluded = append(fa.Excluded, a.pro)
		}
	}
	return fa, nil
}

func (f *fakeDispatchStore) Redispatch(_ context.Context, in store.Redispatch) (*store.RedispatchResult, error) {
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
	res := &store.RedispatchResult{Customer: b.nb.Customer, Status: b.status}
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
		res.ClosedOfferID, res.ClosedProUserID = &closed.id, &u
		if was == "offered" {
			res.ClosedOutcome = map[string]string{"declined": "declined", "expired": "expired"}[in.Close.To]
			if res.ClosedOutcome == "" {
				res.ClosedOutcome = "withdrawn"
			}
		}
	}
	for _, a := range f.assigns {
		if a.booking == in.BookingID && (a.status == "offered" || a.status == "accepted") {
			res.NoOp = true
			return res, nil
		}
	}
	if b.status != "confirmed" {
		from := b.status
		b.status = "confirmed"
		b.history = append(b.history, model.HistoryEntry{FromStatus: &from, ToStatus: "confirmed", ActorKind: in.ActorKind,
			Reason: &in.Reason, CreatedAt: in.At})
		res.PreviousProUserID, res.Status = res.ClosedProUserID, "confirmed"
	}
	if len(in.Candidates) == 0 {
		b.pro = uuid.Nil
		res.Exhausted = true
		return res, nil
	}
	pro := in.Candidates[0]
	b.pro = pro
	a := &fakeAssign{id: f.nextID(), booking: in.BookingID, pro: pro, status: "offered", expires: in.OfferExpiresAt}
	f.assigns = append(f.assigns, a)
	u := f.users[pro]
	res.OfferID, res.ProID, res.ProUserID, res.ExpiresAt = &a.id, &a.pro, &u, a.expires
	if in.Audit != nil {
		f.bk.audits = append(f.bk.audits, *in.Audit)
	}
	return res, nil
}

func (f *fakeDispatchStore) AcceptOffer(_ context.Context, in store.AcceptInput) (*store.AcceptResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.bk.mu.Lock()
	defer f.bk.mu.Unlock()
	for _, a := range f.assigns {
		if a.id != in.OfferID || a.pro != in.ProID {
			continue
		}
		b := f.bk.bookings[a.booking]
		res := &store.AcceptResult{BookingID: a.booking, Customer: b.nb.Customer}
		switch a.status {
		case "accepted":
			res.Already = true
			return res, nil
		case "expired":
			return nil, store.ErrOfferExpired
		case "offered":
		default:
			return nil, store.ErrOfferTaken
		}
		if !in.At.Before(a.expires) {
			return nil, store.ErrOfferExpired
		}
		if b.status != "confirmed" || b.pro != in.ProID {
			return nil, store.ErrOfferTaken
		}
		a.status, b.status = "accepted", "assigned"
		from := "confirmed"
		b.history = append(b.history, model.HistoryEntry{FromStatus: &from, ToStatus: "assigned", ActorKind: "pro", CreatedAt: in.At})
		return res, nil
	}
	return nil, store.ErrOfferNotFound
}

func (f *fakeDispatchStore) EndProNoShow(context.Context, uuid.UUID, *store.CloseAssignment, []string, string, string, string, time.Time) (*uuid.UUID, error) {
	return nil, errors.New("fake: EndProNoShow is pinned by internal/itest")
}

func (f *fakeDispatchStore) DueOfferExpiries(context.Context, time.Time, int) ([]store.AssignmentRef, error) {
	return nil, nil
}
func (f *fakeDispatchStore) BookingsAwaitingOffer(context.Context, time.Time, int) ([]uuid.UUID, error) {
	return nil, nil
}
func (f *fakeDispatchStore) AlertUnassigned(context.Context, time.Time, int) ([]events.BookingCore, error) {
	return nil, nil
}
func (f *fakeDispatchStore) UnassignedPastCancel(context.Context, time.Time, int) ([]uuid.UUID, error) {
	return nil, nil
}
func (f *fakeDispatchStore) AssignedNotOnDuty(context.Context, time.Time, int) ([]store.AssignmentRef, error) {
	return nil, nil
}
func (f *fakeDispatchStore) MarkLate(context.Context, time.Time, int) ([]events.BookingCore, error) {
	return nil, nil
}
func (f *fakeDispatchStore) NoShows(context.Context, time.Time, int) ([]store.AssignmentRef, error) {
	return nil, nil
}
func (f *fakeDispatchStore) RescuesExpired(context.Context, time.Time, int) ([]store.RescueRef, error) {
	return nil, nil
}
func (f *fakeDispatchStore) StaleOnDuty(context.Context, time.Time, int) ([]store.ProRef, error) {
	return nil, nil
}

func (f *fakeDispatchStore) ProDuty(_ context.Context, proID uuid.UUID) (*model.DutyState, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if d := f.duty[proID]; d != nil {
		cp := *d
		return &cp, nil
	}
	return &model.DutyState{}, nil
}

func (f *fakeDispatchStore) SetDuty(_ context.Context, proID uuid.UUID, on bool, fix *store.Fix, at time.Time) (*model.DutyState, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	cur := f.duty[proID]
	changed := cur == nil || cur.OnDuty != on
	if changed {
		d := &model.DutyState{OnDuty: on}
		if on {
			t := at
			d.Since = &t
		}
		f.duty[proID] = d
	}
	if on && fix != nil {
		f.fixes[proID] = *fix
	}
	cp := *f.duty[proID]
	return &cp, changed, nil
}

func (f *fakeDispatchStore) RecordFix(_ context.Context, proID uuid.UUID, fix store.Fix, _ time.Time) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if d := f.duty[proID]; d == nil || !d.OnDuty {
		return store.ErrNotOnDuty
	}
	f.fixes[proID] = fix
	return nil
}

func (f *fakeDispatchStore) TravellingTo(context.Context, uuid.UUID) ([]store.JobPoint, error) {
	return nil, nil
}

func (f *fakeDispatchStore) ProActiveBookingIDs(_ context.Context, proID uuid.UUID) ([]uuid.UUID, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := []uuid.UUID{}
	for _, a := range f.assigns {
		if a.pro == proID && a.status == "accepted" {
			out = append(out, a.booking)
		}
	}
	return out, nil
}

// offerView reads an assignment as an offer: the fixture distance and the
// city's 20% commission.
func (f *fakeDispatchStore) offerView(a *fakeAssign, now time.Time) model.Offer {
	f.bk.mu.Lock()
	defer f.bk.mu.Unlock()
	nb := f.bk.bookings[a.booking].nb
	return model.Offer{ID: a.id, BookingID: a.booking, Status: dispatch.OfferStatus(a.status, a.expires, now),
		ServiceName: "Kitchen deep cleaning", CategorySlug: nb.CategorySlug, Locality: nb.Address.Locality, DistanceM: 1834,
		SlotStart: nb.SlotStart, SlotEnd: nb.SlotEnd, EarningEstimatePaise: dispatch.EarningEstimate(nb.TaxablePaise, 2000),
		ExpiresAt: a.expires}
}

func (f *fakeDispatchStore) ProOffers(_ context.Context, proID uuid.UUID, now time.Time) ([]model.Offer, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := []model.Offer{}
	for _, a := range f.assigns {
		if a.pro == proID && a.status == "offered" && a.expires.After(now) {
			out = append(out, f.offerView(a, now))
		}
	}
	return out, nil
}

func (f *fakeDispatchStore) ProOffer(_ context.Context, proID, offerID uuid.UUID, now time.Time) (*model.Offer, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, a := range f.assigns {
		if a.id == offerID && a.pro == proID {
			o := f.offerView(a, now)
			return &o, nil
		}
	}
	return nil, store.ErrNotFound
}

func (f *fakeDispatchStore) OfferRef(_ context.Context, proID, offerID uuid.UUID) (*store.AssignmentRef, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, a := range f.assigns {
		if a.id == offerID && a.pro == proID {
			r := f.ref(a)
			return &r, nil
		}
	}
	return nil, store.ErrNotFound
}

func (f *fakeDispatchStore) ProJobRecord(ctx context.Context, proID, bookingID uuid.UUID, _ time.Time) (*store.ProJobRecord, error) {
	f.mu.Lock()
	var mine *fakeAssign
	for _, a := range f.assigns {
		if a.booking == bookingID && a.pro == proID && (a.status == "offered" || a.status == "accepted" || a.status == "completed") {
			mine = a
		}
	}
	f.mu.Unlock()
	if mine == nil {
		return nil, store.ErrNotFound
	}
	rec, err := f.bk.BookingRecord(ctx, bookingID, nil)
	if err != nil {
		return nil, err
	}
	return &store.ProJobRecord{Booking: rec, AssignmentID: mine.id, AssignmentStatus: mine.status, Family: "HOME_CLEANING",
		MinBefore: 2, MinAfter: 2, CommissionBPS: 2000}, nil
}

func (f *fakeDispatchStore) ProJobIDs(_ context.Context, proID uuid.UUID, mode, date string, _ *store.BookingCursor, limit int) ([]store.BookingCursor, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.bk.mu.Lock()
	defer f.bk.mu.Unlock()
	out := []store.BookingCursor{}
	for _, a := range f.assigns {
		if a.pro != proID || a.status != "accepted" {
			continue
		}
		b := f.bk.bookings[a.booking]
		if mode == store.JobsUpcoming && b.status != "assigned" {
			continue
		}
		if date != "" && b.nb.SlotStart.In(istZone).Format("2006-01-02") != date {
			continue
		}
		out = append(out, store.BookingCursor{SlotStart: b.nb.SlotStart, ID: a.booking})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].SlotStart.Before(out[j].SlotStart) })
	if len(out) > limit+1 {
		out = out[:limit+1]
	}
	return out, nil
}

var istZone = time.FixedZone("IST", 5*3600+30*60)

func (f *fakeDispatchStore) ProArea(ctx context.Context, proID uuid.UUID) (*model.ProArea, error) {
	zones, err := f.pro.ProZoneIDs(ctx, proID)
	if err != nil {
		return nil, err
	}
	lat, lng := 17.44, 78.37
	return &model.ProArea{ZoneIDs: zones, HomeLat: &lat, HomeLng: &lng, RadiusM: 5000}, nil
}

func (f *fakeDispatchStore) CityZones(_ context.Context, city string) ([]model.ProZone, error) {
	if city != "HYD" {
		return []model.ProZone{}, nil
	}
	return []model.ProZone{
		{ID: devseed.ID("zone", "central-banjara-jubilee"), CityCode: "HYD", Name: "Banjara - Jubilee Hills", Slug: "central-banjara-jubilee",
			Boundary: json.RawMessage(`{"type":"Polygon","coordinates":[[[78.4,17.4],[78.47,17.4],[78.47,17.45],[78.4,17.45],[78.4,17.4]]]}`)},
		{ID: devseed.ID("zone", "west-hitec-gachibowli"), CityCode: "HYD", Name: "Gachibowli - HITEC City", Slug: "west-hitec-gachibowli",
			Boundary: json.RawMessage(`{"type":"Polygon","coordinates":[[[78.33,17.4],[78.4,17.4],[78.4,17.48],[78.33,17.48],[78.33,17.4]]]}`)},
	}, nil
}

// ---- realtime fakes ----

type fakeFrames struct {
	mu     sync.Mutex
	frames []string // topic|type|json
}

func (r *fakeFrames) Publish(_ context.Context, topic, typ string, data any) error {
	raw, err := json.Marshal(data)
	if err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.frames = append(r.frames, topic+"|"+typ+"|"+string(raw))
	return nil
}

// fixtureSigner signs deterministically (shared/realtime stamps its own
// expiry, which would change the fixture bytes every run).
type fixtureSigner struct{}

func (fixtureSigner) Sign(user string, topics []string) (string, error) {
	return "rt-fixture-token." + devseed.ID("rt", user).String(), nil
}
