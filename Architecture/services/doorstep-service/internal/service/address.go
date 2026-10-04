package service

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/atpost/doorstep-service/internal/apperr"
	"github.com/atpost/doorstep-service/internal/cancelrules"
	"github.com/atpost/doorstep-service/internal/model"
	"github.com/atpost/doorstep-service/internal/propii"
	"github.com/atpost/doorstep-service/internal/slots"
	"github.com/atpost/doorstep-service/internal/store"
	"github.com/atpost/shared/paymentsclient"
	"github.com/google/uuid"
)

// Bookings (A3): addresses, slots, bookings, payments, refunds, cancel and
// reschedule, and the admin booking pages.

// BookingStore is what bookings need from the store.
type BookingStore interface {
	Addresses(ctx context.Context, user uuid.UUID) ([]store.AddressRow, error)
	Address(ctx context.Context, user, id uuid.UUID) (*store.AddressRow, error)
	InsertAddress(ctx context.Context, id, user uuid.UUID, w store.AddressWrite, at time.Time) (*store.AddressRow, error)
	UpdateAddress(ctx context.Context, user, id uuid.UUID, w store.AddressWrite, at time.Time) (*store.AddressRow, error)
	DeleteAddress(ctx context.Context, user, id uuid.UUID, at time.Time) error

	QuoteFacts(ctx context.Context, customer, quoteID uuid.UUID) (*store.QuoteFacts, error)
	ZoneFacts(ctx context.Context, zoneID uuid.UUID) (city string, bufferMinutes int, active bool, err error)
	SlotConfig(ctx context.Context, city string, categoryID uuid.UUID) (slots.Config, error)
	SlotCandidates(ctx context.Context, q store.CandidateQuery) ([]slots.Pro, error)
	OutstandingDue(ctx context.Context, customer uuid.UUID) (int64, []uuid.UUID, error)

	CreateBooking(ctx context.Context, nb store.NewBooking) (uuid.UUID, error)
	BookingByKey(ctx context.Context, customer uuid.UUID, key string) (*store.BookingKeyed, error)
	BookingPayment(ctx context.Context, bookingID uuid.UUID) (*store.PaymentRow, error)
	AttachIntent(ctx context.Context, paymentID uuid.UUID, intentID string, checkout map[string]string, at time.Time) error
	BookingRecord(ctx context.Context, id uuid.UUID, customer *uuid.UUID) (*store.BookingRecord, error)
	CustomerBookings(ctx context.Context, customer uuid.UUID, mode string, after *store.BookingCursor, limit int) ([]model.BookingSummary, error)
	BookingMoney(ctx context.Context, bookingID uuid.UUID) ([]store.PaymentRow, []model.Refund, error)
	CancellationRules(ctx context.Context, city string) ([]cancelrules.Rule, error)
	CancelBooking(ctx context.Context, id uuid.UUID, customer *uuid.UUID, audit *store.Actor, at time.Time,
		decide func(*store.LockedBooking) (*store.CancelDecision, error)) (*uuid.UUID, error)
	RescheduleBooking(ctx context.Context, id, customer uuid.UUID, mv store.RescheduleMove, at time.Time,
		decide func(*store.LockedBooking) error) (uuid.UUID, error)
	ExpireHolds(ctx context.Context, limit int) (int, error)

	RefundJob(ctx context.Context, id uuid.UUID) (*store.RefundJob, error)
	UnsubmittedRefunds(ctx context.Context, limit int) ([]store.RefundJob, error)
	MarkRefundSubmitted(ctx context.Context, id uuid.UUID, commandID string) error
	MarkRefundAttempt(ctx context.Context, id uuid.UUID, lastErr string, terminal bool, next time.Time) error
	AdminRequestRefund(ctx context.Context, a store.Actor, in store.AdminRefundRequest) (*model.Refund, bool, error)

	AdminBookings(ctx context.Context, f store.AdminBookingFilter) ([]model.BookingSummary, error)
	BookingAssignments(ctx context.Context, id uuid.UUID) ([]model.AssignmentView, error)
	BookingExtras(ctx context.Context, id uuid.UUID) ([]model.Extra, error)
	AdminStats(ctx context.Context, now time.Time) (*model.AdminStats, error)
}

// PaymentsAPI is payments-service as bookings use it (*paymentsclient.Client
// bound to doorstep_booking).
type PaymentsAPI interface {
	CreateIntent(ctx context.Context, in paymentsclient.CreateIntentRequest) (*paymentsclient.Intent, error)
	GetIntent(ctx context.Context, id uuid.UUID) (*paymentsclient.Intent, error)
	VerifyCallback(ctx context.Context, intentID uuid.UUID, in paymentsclient.CallbackRequest) (*paymentsclient.CallbackVerdict, error)
	Refund(ctx context.Context, intentID uuid.UUID, in paymentsclient.RefundRequest) (*paymentsclient.RefundAccepted, error)
}

// AddressSealer seals address lines (*propii.Crypto; a nil one answers
// propii.ErrNotConfigured).
type AddressSealer interface {
	SealAddress(ctx context.Context, v string) (propii.Sealed, error)
	OpenAddress(ctx context.Context, blob []byte) (string, error)
}

// SlotCache is the ≤30 s slot answer cache (Redis). Never the source of
// truth: a miss or an error just computes.
type SlotCache interface {
	Get(ctx context.Context, key string) ([]byte, bool)
	Set(ctx context.Context, key string, v []byte, ttl time.Duration)
}

// BookingDeps wires bookings.
type BookingDeps struct {
	Store    BookingStore
	Payments PaymentsAPI // nil: payment routes answer 503
	PII      AddressSealer
	Cache    SlotCache // nil: no cache
	// DevStubPayments admits POST .../payment/stub-confirm (local/dev only;
	// it still refuses when payments-service has a real provider).
	DevStubPayments bool
	// NewID mints booking, payment and address ids; nil is uuid.New
	// (contract fixtures pin a sequence).
	NewID func() uuid.UUID
}

// SlotCacheTTL bounds how stale a slot answer can be.
const SlotCacheTTL = 30 * time.Second

// WithBookings wires bookings.
func (s *Service) WithBookings(d BookingDeps) *Service {
	if d.NewID == nil {
		d.NewID = uuid.New
	}
	if p, ok := d.Payments.(*paymentsclient.Client); ok && p == nil {
		d.Payments = nil
	}
	s.bk = d
	return s
}

func (s *Service) nowUTC() time.Time { return s.now().UTC().Truncate(time.Microsecond) }

func addressNotFound() *apperr.Error {
	return apperr.New(http.StatusNotFound, apperr.CodeAddressNotFound, "address not found")
}

func outsideArea() *apperr.Error {
	return apperr.New(http.StatusUnprocessableEntity, apperr.CodeOutsideServiceArea, "this address is outside the service area")
}

// addressLines is the sealed part of an address.
type addressLines struct {
	Line1    string  `json:"line1"`
	Line2    *string `json:"line2"`
	Landmark *string `json:"landmark"`
}

var pincodeRe = regexp.MustCompile(`^[1-9][0-9]{5}$`)

func text(field string, v *string, min, max int) (string, *apperr.Error) {
	if v == nil {
		if min > 0 {
			return "", apperr.Invalid(field, field+" is required")
		}
		return "", nil
	}
	t := strings.TrimSpace(*v)
	n := utf8.RuneCountInString(t)
	if n < min || n > max || !utf8.ValidString(t) {
		return "", apperr.Invalid(field, field+" must be between "+strconv.Itoa(min)+" and "+strconv.Itoa(max)+" characters")
	}
	return t, nil
}

func optText(field string, v *string, max int) (*string, *apperr.Error) {
	if v == nil {
		return nil, nil
	}
	t, aerr := text(field, v, 0, max)
	if aerr != nil {
		return nil, aerr
	}
	if t == "" {
		return nil, nil
	}
	return &t, nil
}

// addressWrite validates a complete input, resolves the zone (serviceability
// on save) and seals the lines.
func (s *Service) addressWrite(ctx context.Context, in model.AddressInput) (store.AddressWrite, *apperr.Error) {
	var w store.AddressWrite
	var aerr *apperr.Error
	if w.Label, aerr = text("label", in.Label, 1, 40); aerr != nil {
		return w, aerr
	}
	var lines addressLines
	if lines.Line1, aerr = text("line1", in.Line1, 1, 200); aerr != nil {
		return w, aerr
	}
	if lines.Line2, aerr = optText("line2", in.Line2, 200); aerr != nil {
		return w, aerr
	}
	if lines.Landmark, aerr = optText("landmark", in.Landmark, 200); aerr != nil {
		return w, aerr
	}
	if w.Locality, aerr = text("locality", in.Locality, 1, 100); aerr != nil {
		return w, aerr
	}
	pin, aerr := text("pincode", in.Pincode, 6, 6)
	if aerr != nil || !pincodeRe.MatchString(pin) {
		return w, apperr.Invalid("pincode", "pincode must be a six-digit Indian PIN code")
	}
	w.Pincode = pin
	lat, lng, aerr := checkPoint(in.Lat, in.Lng)
	if aerr != nil {
		return w, aerr
	}
	w.Lat, w.Lng = lat, lng
	w.IsDefault = in.IsDefault != nil && *in.IsDefault
	hit, err := s.store.LocateZone(ctx, lat, lng)
	if err != nil {
		return w, internal(ctx, "locate address zone", err)
	}
	if hit == nil {
		return w, outsideArea()
	}
	w.CityCode, w.ZoneID = hit.City.Code, hit.Zone.ID
	raw, err := json.Marshal(lines)
	if err != nil {
		return w, internal(ctx, "encode address", err)
	}
	sealed, err := s.bk.PII.SealAddress(ctx, string(raw))
	if errors.Is(err, propii.ErrNotConfigured) {
		return w, piiUnavailable()
	}
	if err != nil {
		return w, internal(ctx, "seal address", err)
	}
	w.LinesSealed, w.KeyVersion = sealed.Blob, sealed.KeyVersion
	return w, nil
}

// openLines opens sealed lines.
func (s *Service) openLines(ctx context.Context, blob []byte) (addressLines, *apperr.Error) {
	var l addressLines
	if len(blob) == 0 {
		return l, nil
	}
	raw, err := s.bk.PII.OpenAddress(ctx, blob)
	if errors.Is(err, propii.ErrNotConfigured) {
		return l, piiUnavailable()
	}
	if err != nil {
		return l, internal(ctx, "open address", err)
	}
	if err := json.Unmarshal([]byte(raw), &l); err != nil {
		return l, internal(ctx, "decode address", err)
	}
	return l, nil
}

func (s *Service) addressView(ctx context.Context, a *store.AddressRow) (model.Address, *apperr.Error) {
	l, aerr := s.openLines(ctx, a.LinesSealed)
	if aerr != nil {
		return model.Address{}, aerr
	}
	return model.Address{ID: a.ID, Label: a.Label, Line1: l.Line1, Line2: l.Line2, Landmark: l.Landmark, Locality: a.Locality,
		CityCode: a.CityCode, Pincode: a.Pincode, Lat: a.Lat, Lng: a.Lng, ZoneID: a.ZoneID, IsDefault: a.IsDefault,
		CreatedAt: a.CreatedAt}, nil
}

// ListAddresses lists the customer's addresses.
func (s *Service) ListAddresses(ctx context.Context, user uuid.UUID) ([]model.Address, error) {
	rows, err := s.bk.Store.Addresses(ctx, user)
	if err != nil {
		return nil, internal(ctx, "addresses", err)
	}
	out := make([]model.Address, 0, len(rows))
	for i := range rows {
		v, aerr := s.addressView(ctx, &rows[i])
		if aerr != nil {
			return nil, aerr
		}
		out = append(out, v)
	}
	return out, nil
}

// maxAddresses bounds a customer's saved addresses.
const maxAddresses = 20

// CreateAddress saves an address; its point must be in an active zone.
func (s *Service) CreateAddress(ctx context.Context, user uuid.UUID, in model.AddressInput) (*model.Address, error) {
	existing, err := s.bk.Store.Addresses(ctx, user)
	if err != nil {
		return nil, internal(ctx, "addresses", err)
	}
	if len(existing) >= maxAddresses {
		return nil, apperr.New(http.StatusConflict, apperr.CodeConflict, "too many saved addresses; delete one first")
	}
	w, aerr := s.addressWrite(ctx, in)
	if aerr != nil {
		return nil, aerr
	}
	row, err := s.bk.Store.InsertAddress(ctx, s.bk.NewID(), user, w, s.nowUTC())
	if err != nil {
		return nil, internal(ctx, "insert address", err)
	}
	v, aerr := s.addressView(ctx, row)
	if aerr != nil {
		return nil, aerr
	}
	return &v, nil
}

// UpdateAddress edits an address: absent fields keep their values.
func (s *Service) UpdateAddress(ctx context.Context, user, id uuid.UUID, in model.AddressInput) (*model.Address, error) {
	cur, err := s.bk.Store.Address(ctx, user, id)
	if errors.Is(err, store.ErrNotFound) {
		return nil, addressNotFound()
	}
	if err != nil {
		return nil, internal(ctx, "address", err)
	}
	l, aerr := s.openLines(ctx, cur.LinesSealed)
	if aerr != nil {
		return nil, aerr
	}
	merged := model.AddressInput{Label: &cur.Label, Line1: &l.Line1, Line2: l.Line2, Landmark: l.Landmark, Locality: &cur.Locality,
		Pincode: &cur.Pincode, Lat: &cur.Lat, Lng: &cur.Lng, IsDefault: in.IsDefault}
	for _, f := range []struct {
		dst **string
		v   *string
	}{{&merged.Label, in.Label}, {&merged.Line1, in.Line1}, {&merged.Line2, in.Line2},
		{&merged.Landmark, in.Landmark}, {&merged.Locality, in.Locality}, {&merged.Pincode, in.Pincode}} {
		if f.v != nil {
			*f.dst = f.v
		}
	}
	if (in.Lat == nil) != (in.Lng == nil) {
		return nil, apperr.Invalid("lat", "lat and lng change together")
	}
	if in.Lat != nil {
		merged.Lat, merged.Lng = in.Lat, in.Lng
	}
	w, aerr := s.addressWrite(ctx, merged)
	if aerr != nil {
		return nil, aerr
	}
	row, err := s.bk.Store.UpdateAddress(ctx, user, id, w, s.nowUTC())
	if errors.Is(err, store.ErrNotFound) {
		return nil, addressNotFound()
	}
	if err != nil {
		return nil, internal(ctx, "update address", err)
	}
	v, aerr := s.addressView(ctx, row)
	if aerr != nil {
		return nil, aerr
	}
	return &v, nil
}

// DeleteAddress removes an address (bookings keep their snapshot).
func (s *Service) DeleteAddress(ctx context.Context, user, id uuid.UUID) error {
	err := s.bk.Store.DeleteAddress(ctx, user, id, s.nowUTC())
	if errors.Is(err, store.ErrNotFound) {
		return addressNotFound()
	}
	if err != nil {
		return internal(ctx, "delete address", err)
	}
	return nil
}
