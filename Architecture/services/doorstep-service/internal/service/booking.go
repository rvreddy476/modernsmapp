package service

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/atpost/doorstep-service/internal/apperr"
	"github.com/atpost/doorstep-service/internal/matcher"
	"github.com/atpost/doorstep-service/internal/model"
	"github.com/atpost/doorstep-service/internal/payments"
	"github.com/atpost/doorstep-service/internal/slots"
	"github.com/atpost/doorstep-service/internal/store"
	"github.com/atpost/shared/paymentsclient"
	"github.com/google/uuid"
)

func bookingNotFound() *apperr.Error {
	return apperr.New(http.StatusNotFound, apperr.CodeBookingNotFound, "booking not found")
}

func quoteExpired() *apperr.Error {
	return apperr.New(http.StatusGone, apperr.CodeQuoteExpired, "this quote has expired; price the service again")
}

func paymentsUnavailable() *apperr.Error {
	return apperr.New(http.StatusServiceUnavailable, apperr.CodePaymentsUnavailable, "payments are unavailable right now; try again shortly")
}

func slotUnavailable(reason string) *apperr.Error {
	return apperr.New(http.StatusUnprocessableEntity, apperr.CodeSlotUnavailable, "this slot cannot be booked").
		WithDetails(map[string]any{"reason": reason})
}

func bookingTransition(status string) *apperr.Error {
	return apperr.New(http.StatusConflict, apperr.CodeInvalidTransition, "not allowed while the booking is "+status).
		WithDetails(map[string]any{"status": status})
}

// outstanding refuses a customer with unpaid extras.
func (s *Service) outstanding(ctx context.Context, user uuid.UUID) *apperr.Error {
	total, bills, err := s.bk.Store.OutstandingDue(ctx, user)
	if err != nil {
		return internal(ctx, "outstanding", err)
	}
	if total > 0 {
		ids := make([]string, 0, len(bills))
		for _, b := range bills {
			ids = append(ids, b.String())
		}
		return apperr.New(http.StatusConflict, apperr.CodeOutstandingDue, "pay your outstanding extras before booking again").
			WithDetails(map[string]any{"outstanding_paise": total, "extras_bill_ids": ids})
	}
	return nil
}

// slotPlace is where and what a slot search or booking is for.
type slotPlace struct {
	City          string
	ZoneID        uuid.UUID
	CategoryID    uuid.UUID
	Lat, Lng      float64
	Skill         string
	Req           slots.Request
	Exclude       *uuid.UUID
	Address       *store.AddressRow
	QuoteFacts    *store.QuoteFacts
	BookingRecord *store.BookingRecord
}

// quotePlace resolves a quote (and optionally a saved address) to a place.
func (s *Service) quotePlace(ctx context.Context, user, quoteID uuid.UUID, addressID *uuid.UUID, requireFemale bool) (*slotPlace, *apperr.Error) {
	f, err := s.bk.Store.QuoteFacts(ctx, user, quoteID)
	if errors.Is(err, store.ErrNotFound) {
		return nil, apperr.New(http.StatusNotFound, apperr.CodeQuoteNotFound, "quote not found")
	}
	if err != nil {
		return nil, internal(ctx, "quote facts", err)
	}
	if f.Quote.Status != model.QuoteOpen || !s.now().Before(f.Quote.ExpiresAt) {
		return nil, quoteExpired()
	}
	if !f.Bookable {
		return nil, notAvailable()
	}
	p := &slotPlace{City: f.Quote.CityCode, ZoneID: f.Quote.ZoneID, CategoryID: f.CategoryID, Lat: f.Lat, Lng: f.Lng,
		Skill: f.RequiredSkill, QuoteFacts: f,
		Req: slots.Request{DurationMinutes: f.Quote.DurationMinutes, GenderRule: f.GenderRule, RequireFemale: requireFemale}}
	if addressID != nil {
		a, err := s.bk.Store.Address(ctx, user, *addressID)
		if errors.Is(err, store.ErrNotFound) {
			return nil, addressNotFound()
		}
		if err != nil {
			return nil, internal(ctx, "address", err)
		}
		// Prices are per city: the address must be in the quote's city.
		if a.CityCode != f.Quote.CityCode || a.ZoneID == uuid.Nil {
			return nil, outsideArea()
		}
		p.ZoneID, p.Lat, p.Lng, p.Address = a.ZoneID, a.Lat, a.Lng, a
	}
	city, buffer, active, err := s.bk.Store.ZoneFacts(ctx, p.ZoneID)
	if errors.Is(err, store.ErrNotFound) || (err == nil && (!active || city != p.City)) {
		return nil, outsideArea()
	}
	if err != nil {
		return nil, internal(ctx, "zone facts", err)
	}
	p.Req.BufferMinutes = buffer
	return p, nil
}

// bookingPlace is a reschedule search: the booking's own place, excluding
// its own calendar block.
func (s *Service) bookingPlace(ctx context.Context, rec *store.BookingRecord) *slotPlace {
	id := rec.Booking.ID
	return &slotPlace{City: rec.Booking.CityCode, ZoneID: rec.Booking.ZoneID, CategoryID: rec.CategoryID, Lat: rec.Lat, Lng: rec.Lng,
		Skill: rec.RequiredSkill, Exclude: &id, BookingRecord: rec,
		Req: slots.Request{DurationMinutes: rec.Booking.DurationMinutes, BufferMinutes: rec.BufferMinutes,
			GenderRule: rec.GenderRule, RequireFemale: rec.Booking.RequireFemalePro}}
}

func (s *Service) slotConfig(ctx context.Context, p *slotPlace) (slots.Config, *apperr.Error) {
	cfg, err := s.bk.Store.SlotConfig(ctx, p.City, p.CategoryID)
	if errors.Is(err, store.ErrNotFound) {
		return cfg, notAvailable()
	}
	if err != nil {
		return cfg, internal(ctx, "slot config", err)
	}
	return cfg, nil
}

func (s *Service) candidates(ctx context.Context, p *slotPlace, from, to time.Time) ([]slots.Pro, *apperr.Error) {
	pros, err := s.bk.Store.SlotCandidates(ctx, store.CandidateQuery{City: p.City, ZoneID: p.ZoneID, Lat: p.Lat, Lng: p.Lng,
		Skill: p.Skill, From: from, To: to, ExcludeBooking: p.Exclude})
	if err != nil {
		return nil, internal(ctx, "slot candidates", err)
	}
	return pros, nil
}

// SlotQuery is GET /slots.
type SlotQuery struct {
	QuoteID       *uuid.UUID
	BookingID     *uuid.UUID
	AddressID     *uuid.UUID
	RequireFemale bool
}

// Slots answers the slot grid for a quote (or, with booking_id, for moving
// a booking). Cached up to 30 s keyed by every input; advisory only.
func (s *Service) Slots(ctx context.Context, user uuid.UUID, q SlotQuery) (*model.SlotDays, error) {
	if (q.QuoteID == nil) == (q.BookingID == nil) {
		return nil, apperr.Invalid("quote_id", "send exactly one of quote_id or booking_id")
	}
	var p *slotPlace
	var aerr *apperr.Error
	if q.QuoteID != nil {
		if aerr = s.outstanding(ctx, user); aerr != nil {
			return nil, aerr
		}
		if p, aerr = s.quotePlace(ctx, user, *q.QuoteID, q.AddressID, q.RequireFemale); aerr != nil {
			return nil, aerr
		}
	} else {
		rec, err := s.bk.Store.BookingRecord(ctx, *q.BookingID, &user)
		if errors.Is(err, store.ErrNotFound) {
			return nil, bookingNotFound()
		}
		if err != nil {
			return nil, internal(ctx, "booking", err)
		}
		p = s.bookingPlace(ctx, rec)
	}
	cfg, aerr := s.slotConfig(ctx, p)
	if aerr != nil {
		return nil, aerr
	}
	key := slotCacheKey(p, cfg)
	if s.bk.Cache != nil {
		if raw, ok := s.bk.Cache.Get(ctx, key); ok {
			var cached model.SlotDays
			if json.Unmarshal(raw, &cached) == nil {
				return &cached, nil
			}
		}
	}
	now := s.now()
	from, to := slots.Range(now, cfg)
	pros, aerr := s.candidates(ctx, p, from, to)
	if aerr != nil {
		return nil, aerr
	}
	out := &model.SlotDays{Timezone: "Asia/Kolkata", Days: []model.SlotDay{}}
	for _, d := range slots.Days(now, cfg, p.Req, pros) {
		day := model.SlotDay{Date: d.Date, Slots: make([]model.Slot, 0, len(d.Slots))}
		for _, sl := range d.Slots {
			day.Slots = append(day.Slots, model.Slot{Start: sl.Start, End: sl.End, Available: sl.Available})
		}
		out.Days = append(out.Days, day)
	}
	if s.bk.Cache != nil {
		if raw, err := json.Marshal(out); err == nil {
			s.bk.Cache.Set(ctx, key, raw, SlotCacheTTL)
		}
	}
	return out, nil
}

func slotCacheKey(p *slotPlace, cfg slots.Config) string {
	exclude := ""
	if p.Exclude != nil {
		exclude = p.Exclude.String()
	}
	raw := fmt.Sprintf("%s|%s|%s|%.5f|%.5f|%s|%d|%d|%s|%t|%s|%+v", p.City, p.ZoneID, p.CategoryID, p.Lat, p.Lng, p.Skill,
		p.Req.DurationMinutes, p.Req.BufferMinutes, p.Req.GenderRule, p.Req.RequireFemale, exclude, cfg)
	sum := sha256.Sum256([]byte(raw))
	return "doorstep:slots:v1:" + hex.EncodeToString(sum[:16])
}

// ranked returns the professionals free for start, best first: the hard
// filters (slots.Available), minus anyone in exclude, scored by the matcher
// with the distance from each one's previous job that day (else home) to
// the job at (lat, lng). prefer, when free and not excluded, goes first.
func ranked(pros []slots.Pro, start time.Time, req slots.Request, prefer *uuid.UUID, lat, lng float64, exclude map[uuid.UUID]bool) []uuid.UUID {
	avail := slots.Available(pros, start, req)
	cands := make([]matcher.Candidate, 0, len(avail))
	for _, p := range avail {
		if exclude[p.ID] {
			continue
		}
		cands = append(cands, matcher.Candidate{ProID: p.ID, DistanceM: slots.DistanceFrom(p, start, lat, lng), RatingSum: p.RatingSum,
			RatingCount: p.RatingCount, OffersReceived: p.OffersReceived, OffersAccepted: p.OffersAccepted, Cancellations: p.Cancellations,
			JobsCompleted: p.JobsCompleted, WeekLoad: slots.WeekLoad(p, start)})
	}
	out := make([]uuid.UUID, 0, len(cands))
	if prefer != nil {
		for _, c := range cands {
			if c.ProID == *prefer {
				out = append(out, c.ProID)
			}
		}
	}
	for _, sc := range matcher.Rank(cands) {
		if prefer != nil && sc.Candidate.ProID == *prefer {
			continue
		}
		out = append(out, sc.Candidate.ProID)
	}
	return out
}

func slotReason(err error) string {
	switch {
	case errors.Is(err, slots.ErrTooSoon):
		return "inside_lead_time"
	case errors.Is(err, slots.ErrTooFar):
		return "beyond_horizon"
	default:
		return "not_on_grid"
	}
}

// maxIdempotencyKey bounds the Idempotency-Key header.
const maxIdempotencyKey = 128

// CreateBooking re-validates the quote, picks and holds the best-fitting
// professional, creates the booking (pending_payment) and opens the payment
// intent. Idempotent on (customer, Idempotency-Key): a replay answers the
// same booking (and retries the intent if it was never opened).
func (s *Service) CreateBooking(ctx context.Context, user uuid.UUID, idemKey string, req model.BookingCreateRequest) (*model.BookingCreated, error) {
	idemKey = strings.TrimSpace(idemKey)
	if idemKey == "" || len(idemKey) > maxIdempotencyKey {
		return nil, apperr.Invalid("Idempotency-Key", "an Idempotency-Key header of 1 to 128 characters is required")
	}
	if req.QuoteID == nil || *req.QuoteID == uuid.Nil {
		return nil, apperr.Invalid("quote_id", "quote_id is required")
	}
	if req.AddressID == nil || *req.AddressID == uuid.Nil {
		return nil, apperr.Invalid("address_id", "address_id is required")
	}
	if req.SlotStart == nil {
		return nil, apperr.Invalid("slot_start", "slot_start is required")
	}
	notes, aerr := optText("notes", req.Notes, 500)
	if aerr != nil {
		return nil, aerr
	}
	if s.bk.Payments == nil {
		return nil, paymentsUnavailable()
	}
	if replay, err := s.replay(ctx, user, idemKey, req); replay != nil || err != nil {
		return replay, err
	}
	if aerr := s.outstanding(ctx, user); aerr != nil {
		return nil, aerr
	}
	requireFemale := req.RequireFemalePro != nil && *req.RequireFemalePro
	p, aerr := s.quotePlace(ctx, user, *req.QuoteID, req.AddressID, requireFemale)
	if aerr != nil {
		return nil, aerr
	}
	cfg, aerr := s.slotConfig(ctx, p)
	if aerr != nil {
		return nil, aerr
	}
	now := s.nowUTC()
	start := req.SlotStart.UTC()
	if err := slots.Validate(now, start, cfg, p.Req.DurationMinutes); err != nil {
		return nil, slotUnavailable(slotReason(err))
	}
	day := slots.Interval{Start: start.AddDate(0, 0, -1), End: start.AddDate(0, 0, 7)}
	pros, aerr := s.candidates(ctx, p, day.Start, day.End)
	if aerr != nil {
		return nil, aerr
	}
	order := ranked(pros, start, p.Req, nil, p.Lat, p.Lng, nil)
	if len(order) == 0 {
		return nil, slotUnavailable("no_professional")
	}
	f, a := p.QuoteFacts, p.Address
	block := slots.BlockFor(start, p.Req)
	id := s.bk.NewID()
	nb := store.NewBooking{
		ID: id, Customer: user, IdempotencyKey: idemKey, QuoteID: f.Quote.ID, CityCode: p.City, ZoneID: p.ZoneID,
		CategoryID: f.CategoryID, CategorySlug: f.CategorySlug, ServiceID: f.Quote.ServiceID,
		Address: store.AddressSnapshot{AddressID: a.ID, Label: a.Label, Locality: a.Locality, CityCode: a.CityCode, Pincode: a.Pincode,
			Lat: a.Lat, Lng: a.Lng, ZoneID: a.ZoneID, CreatedAt: a.CreatedAt},
		AddressSealed: a.LinesSealed,
		SlotStart:     start, SlotEnd: start.Add(time.Duration(p.Req.DurationMinutes) * time.Minute), BlockEnd: block.End,
		Duration: p.Req.DurationMinutes, GenderRule: p.Req.GenderRule, RequireFemale: requireFemale, Notes: notes,
		TotalPaise: f.Quote.TotalPaise, TaxablePaise: f.Quote.TaxablePaise, TaxPaise: f.Quote.TaxPaise, Items: f.Quote.Lines,
		HoldExpiresAt: now.Add(time.Duration(cfg.HoldMinutes) * time.Minute), Candidates: order,
		PaymentID: s.bk.NewID(), IntentKey: payments.IntentKey(id), At: now,
	}
	_, err := s.bk.Store.CreateBooking(ctx, nb)
	switch {
	case errors.Is(err, store.ErrReplay):
		if replay, err := s.replay(ctx, user, idemKey, req); replay != nil || err != nil {
			return replay, err
		}
		return nil, internal(ctx, "booking replay", errors.New("idempotency key used but no booking found"))
	case errors.Is(err, store.ErrQuoteGone):
		return nil, quoteExpired()
	case errors.Is(err, store.ErrSlotTaken):
		return nil, apperr.New(http.StatusConflict, apperr.CodeSlotTaken, "this slot was just taken; pick another")
	case err != nil:
		return nil, internal(ctx, "create booking", err)
	}
	return s.created(ctx, user, id)
}

// replay answers an Idempotency-Key the customer already used: the same
// booking when the body names the same quote, address and slot, a 400 when
// it does not. (nil, nil): the key is new.
func (s *Service) replay(ctx context.Context, user uuid.UUID, key string, req model.BookingCreateRequest) (*model.BookingCreated, error) {
	prev, err := s.bk.Store.BookingByKey(ctx, user, key)
	if errors.Is(err, store.ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, internal(ctx, "booking by key", err)
	}
	if prev.QuoteID != *req.QuoteID || prev.AddressID != *req.AddressID || !prev.SlotStart.Equal(req.SlotStart.UTC()) {
		return nil, apperr.Invalid("Idempotency-Key", "this Idempotency-Key was used for a different booking")
	}
	return s.created(ctx, user, prev.ID)
}

// created is the POST /bookings answer: the booking and its intent (opened
// now if it never was).
func (s *Service) created(ctx context.Context, user, id uuid.UUID) (*model.BookingCreated, error) {
	rec, err := s.bk.Store.BookingRecord(ctx, id, &user)
	if err != nil {
		return nil, internal(ctx, "booking", err)
	}
	intent, aerr := s.ensureIntent(ctx, rec)
	if aerr != nil {
		return nil, aerr
	}
	b, aerr := s.bookingView(ctx, rec)
	if aerr != nil {
		return nil, aerr
	}
	return &model.BookingCreated{Booking: *b, PaymentIntent: *intent}, nil
}

// ensureIntent opens the payments-service intent for the booking's payment
// row once (idempotent on doorstep:booking:{id}) and returns the row's view.
func (s *Service) ensureIntent(ctx context.Context, rec *store.BookingRecord) (*model.PaymentIntent, *apperr.Error) {
	pay, err := s.bk.Store.BookingPayment(ctx, rec.Booking.ID)
	if err != nil {
		return nil, internal(ctx, "booking payment", err)
	}
	if pay.IntentID == nil && rec.Booking.Status == "pending_payment" {
		if s.bk.Payments == nil {
			return nil, paymentsUnavailable()
		}
		intent, err := s.bk.Payments.CreateIntent(ctx, paymentsclient.CreateIntentRequest{
			ApplicationID: payments.ApplicationID, ReferenceID: rec.Booking.ID, PayerID: rec.CustomerUserID,
			PayeeID: payments.PayeeID, AmountMinor: pay.AmountPaise, Currency: payments.Currency, Method: payments.Method,
			IdempotencyKey: pay.IntentKey,
		})
		if err != nil {
			slog.ErrorContext(ctx, "doorstep: payment intent not opened", "booking_id", rec.Booking.ID, "error", err)
			return nil, paymentsUnavailable()
		}
		checkout := intent.ClientSession.AsMap()
		if checkout == nil {
			checkout = map[string]string{}
		}
		if err := s.bk.Store.AttachIntent(ctx, pay.ID, intent.ID.String(), checkout, s.nowUTC()); err != nil {
			return nil, internal(ctx, "attach intent", err)
		}
		if pay, err = s.bk.Store.BookingPayment(ctx, rec.Booking.ID); err != nil {
			return nil, internal(ctx, "booking payment", err)
		}
	}
	v := paymentView(*pay)
	return &v, nil
}

func paymentView(p store.PaymentRow) model.PaymentIntent {
	checkout := p.Checkout
	if checkout == nil {
		checkout = map[string]string{}
	}
	return model.PaymentIntent{PaymentID: p.ID, ReferenceType: p.ReferenceType, ReferenceID: p.ReferenceID,
		AmountPaise: p.AmountPaise, Status: p.Status, Checkout: checkout}
}

// customerCancellable are the statuses a customer may cancel from (in
// progress is refused by the rules: no cancel after the start OTP).
var customerCancellable = map[string]bool{"pending_payment": true, "confirmed": true, "assigned": true, "en_route": true, "arrived": true}

// reschedulable are the statuses a booking can move from.
var reschedulable = map[string]bool{"confirmed": true, "assigned": true}

// RescheduleCutoff: free once, up to 3 h before the slot.
const RescheduleCutoff = 3 * time.Hour

// bookingView assembles the customer's booking: the address lines opened,
// OTPs not set before the visit lane (null), before/after photos only.
func (s *Service) bookingView(ctx context.Context, rec *store.BookingRecord) (*model.Booking, *apperr.Error) {
	b := rec.Booking
	l, aerr := s.openLines(ctx, rec.AddressSealed)
	if aerr != nil {
		return nil, aerr
	}
	b.Address.Line1, b.Address.Line2, b.Address.Landmark = l.Line1, l.Line2, l.Landmark
	b.StartOTP, b.EndOTP = nil, nil
	photos := make([]model.Photo, 0, len(b.Photos))
	for _, p := range b.Photos {
		if p.Phase == "before" || p.Phase == "after" {
			photos = append(photos, p)
		}
	}
	b.Photos = photos
	if b.Items == nil {
		b.Items = []model.QuoteLine{}
	}
	if b.StatusHistory == nil {
		b.StatusHistory = []model.StatusStep{}
	}
	now := s.now()
	b.CanCancel = customerCancellable[b.Status]
	b.CanReschedule = reschedulable[b.Status] && rec.RescheduleCount == 0 && !now.Add(RescheduleCutoff).After(b.SlotStart)
	return &b, nil
}

// Booking is one of the customer's bookings.
func (s *Service) Booking(ctx context.Context, user, id uuid.UUID) (*model.Booking, error) {
	rec, err := s.bk.Store.BookingRecord(ctx, id, &user)
	if errors.Is(err, store.ErrNotFound) {
		return nil, bookingNotFound()
	}
	if err != nil {
		return nil, internal(ctx, "booking", err)
	}
	b, aerr := s.bookingView(ctx, rec)
	if aerr != nil {
		return nil, aerr
	}
	return b, nil
}

// EncodeCursor / decodeCursor: an opaque keyset position.
func EncodeCursor(c store.BookingCursor) string {
	return base64.RawURLEncoding.EncodeToString([]byte(strconv.FormatInt(c.SlotStart.UnixMicro(), 10) + "|" + c.ID.String()))
}

func decodeBookingCursor(raw string) (*store.BookingCursor, *apperr.Error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	bad := apperr.Invalid("cursor", "cursor is not one this service issued")
	b, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		return nil, bad
	}
	ts, id, ok := strings.Cut(string(b), "|")
	if !ok {
		return nil, bad
	}
	micros, err := strconv.ParseInt(ts, 10, 64)
	if err != nil {
		return nil, bad
	}
	uid, err := uuid.Parse(id)
	if err != nil {
		return nil, bad
	}
	return &store.BookingCursor{SlotStart: time.UnixMicro(micros).UTC(), ID: uid}, nil
}

func page(items []model.BookingSummary, limit int) model.BookingPage {
	out := model.BookingPage{Items: items}
	if out.Items == nil {
		out.Items = []model.BookingSummary{}
	}
	if len(out.Items) > limit {
		out.Items = out.Items[:limit]
		last := out.Items[limit-1]
		next := EncodeCursor(store.BookingCursor{SlotStart: last.SlotStart, ID: last.ID})
		out.NextCursor = &next
	}
	return out
}

// ParseLimit reads a page size (default 50, 1..100).
func ParseLimit(raw string) (int, *apperr.Error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 50, nil
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < 1 || n > 100 {
		return 0, apperr.Invalid("limit", "limit must be between 1 and 100")
	}
	return n, nil
}

// Bookings pages the customer's bookings.
func (s *Service) Bookings(ctx context.Context, user uuid.UUID, mode, cursor string, limit int) (*model.BookingPage, error) {
	switch mode {
	case "":
		mode = store.ListAll
	case store.ListUpcoming, store.ListPast, store.ListAll:
	default:
		return nil, apperr.Invalid("status", "status must be upcoming, past or all")
	}
	after, aerr := decodeBookingCursor(cursor)
	if aerr != nil {
		return nil, aerr
	}
	items, err := s.bk.Store.CustomerBookings(ctx, user, mode, after, limit)
	if err != nil {
		return nil, internal(ctx, "bookings", err)
	}
	p := page(items, limit)
	return &p, nil
}

// BookingPayments is the booking's payment rows and refunds: the only
// source a client may read "paid" from.
func (s *Service) BookingPayments(ctx context.Context, user, id uuid.UUID) (*model.BookingPayments, error) {
	if _, err := s.bk.Store.BookingRecord(ctx, id, &user); errors.Is(err, store.ErrNotFound) {
		return nil, bookingNotFound()
	} else if err != nil {
		return nil, internal(ctx, "booking", err)
	}
	return s.moneyView(ctx, id)
}

func (s *Service) moneyView(ctx context.Context, id uuid.UUID) (*model.BookingPayments, error) {
	pays, refunds, err := s.bk.Store.BookingMoney(ctx, id)
	if err != nil {
		return nil, internal(ctx, "booking money", err)
	}
	out := &model.BookingPayments{Payments: make([]model.PaymentIntent, 0, len(pays)), Refunds: refunds}
	for _, p := range pays {
		out.Payments = append(out.Payments, paymentView(p))
	}
	if out.Refunds == nil {
		out.Refunds = []model.Refund{}
	}
	return out, nil
}

// PaymentIntent (re)opens the intent of a pending booking.
func (s *Service) PaymentIntent(ctx context.Context, user, id uuid.UUID) (*model.PaymentIntent, error) {
	rec, err := s.bk.Store.BookingRecord(ctx, id, &user)
	if errors.Is(err, store.ErrNotFound) {
		return nil, bookingNotFound()
	}
	if err != nil {
		return nil, internal(ctx, "booking", err)
	}
	pay, err := s.bk.Store.BookingPayment(ctx, id)
	if err != nil {
		return nil, internal(ctx, "booking payment", err)
	}
	switch {
	case pay.Status == "succeeded" || pay.Status == "refunded" || pay.Status == "partially_refunded":
		return nil, apperr.New(http.StatusConflict, apperr.CodePaymentAlreadySettled, "this booking is already paid")
	case rec.Booking.Status == "expired",
		rec.Booking.Status == "pending_payment" && rec.Booking.HoldExpiresAt != nil && !s.now().Before(*rec.Booking.HoldExpiresAt):
		return nil, apperr.New(http.StatusGone, apperr.CodeHoldExpired, "the slot hold lapsed; book again")
	case rec.Booking.Status != "pending_payment":
		return nil, bookingTransition(rec.Booking.Status)
	}
	v, aerr := s.ensureIntent(ctx, rec)
	if aerr != nil {
		return nil, aerr
	}
	return v, nil
}

// StubConfirm settles a booking's intent through payments-service's stub
// gateway (local/dev only). It never marks anything paid: payments-service
// verifies the callback, its stub settlement publishes payment.succeeded,
// and the consumer confirms the booking from that signed event. Refused
// outside dev (404) and whenever payments-service has a real provider.
func (s *Service) StubConfirm(ctx context.Context, user, id uuid.UUID) (*model.BookingPayments, error) {
	if !s.bk.DevStubPayments {
		return nil, apperr.New(http.StatusNotFound, apperr.CodeNotFound, "not available on this deployment")
	}
	rec, err := s.bk.Store.BookingRecord(ctx, id, &user)
	if errors.Is(err, store.ErrNotFound) {
		return nil, bookingNotFound()
	}
	if err != nil {
		return nil, internal(ctx, "booking", err)
	}
	if rec.Booking.Status != "pending_payment" {
		return nil, bookingTransition(rec.Booking.Status)
	}
	if s.bk.Payments == nil {
		return nil, paymentsUnavailable()
	}
	if _, aerr := s.ensureIntent(ctx, rec); aerr != nil {
		return nil, aerr
	}
	pay, err := s.bk.Store.BookingPayment(ctx, id)
	if err != nil || pay.IntentID == nil {
		return nil, internal(ctx, "booking payment", fmt.Errorf("no intent after ensureIntent: %v", err))
	}
	intentID, err := uuid.Parse(*pay.IntentID)
	if err != nil {
		return nil, internal(ctx, "intent id", err)
	}
	intent, err := s.bk.Payments.GetIntent(ctx, intentID)
	if err != nil {
		slog.ErrorContext(ctx, "doorstep: stub confirm could not ask payments which provider is live", "booking_id", id, "error", err)
		return nil, paymentsUnavailable()
	}
	if intent.ClientSession != nil && intent.ClientSession.Provider != payments.StubProvider {
		return nil, apperr.New(http.StatusConflict, apperr.CodeStubUnavailable,
			"payments-service has a real provider here; pay through checkout")
	}
	verdict, err := s.bk.Payments.VerifyCallback(ctx, intentID, paymentsclient.CallbackRequest{
		ProviderOrderID: intent.ProviderRef, ProviderPaymentID: "pay_stub_" + strings.ReplaceAll(id.String(), "-", ""),
		Signature: "stub", ExpectedAmountMinor: pay.AmountPaise,
	})
	if err != nil {
		slog.ErrorContext(ctx, "doorstep: stub confirm refused by payments", "booking_id", id, "error", err)
		return nil, paymentsUnavailable()
	}
	if verdict.ReferenceType != payments.RefBooking || verdict.ReferenceID != id || verdict.PayerID != rec.CustomerUserID ||
		verdict.AmountMinor != pay.AmountPaise {
		return nil, apperr.New(http.StatusConflict, apperr.CodeStubUnavailable, "the intent does not belong to this booking")
	}
	return s.moneyView(ctx, id)
}
