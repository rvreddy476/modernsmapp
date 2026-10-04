package service

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"math/big"
	"net/http"
	"regexp"
	"time"

	"github.com/atpost/doorstep-service/internal/apperr"
	"github.com/atpost/doorstep-service/internal/mediaclient"
	"github.com/atpost/doorstep-service/internal/model"
	"github.com/atpost/doorstep-service/internal/payments"
	"github.com/atpost/doorstep-service/internal/store"
	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"
)

// VisitStore keeps decisions inside the booking transaction. The caller's
// first read is only a hint; every write rechecks ownership and status.
type VisitStore interface {
	WithdrawVisitExtra(context.Context, uuid.UUID, uuid.UUID, time.Time, func(*store.VisitFacts) error) error
	SuspendedVisitBookings(context.Context, int) ([]uuid.UUID, error)
	VisitFacts(context.Context, uuid.UUID, time.Time) (*store.VisitFacts, error)
	MoveVisit(context.Context, store.StepInput, func(*store.VisitFacts) error) (*store.VisitFacts, error)
	AddPhoto(context.Context, store.PhotoWrite, func(*store.VisitFacts) error) (*model.Photo, error)
	PhotoByMedia(context.Context, uuid.UUID, string) (*store.PhotoRef, error)
	VisitOTP(context.Context, uuid.UUID, string) (*store.OTPRow, error)
	EnsureOTP(context.Context, uuid.UUID, string, string, []byte, time.Time) error
	StartVisit(context.Context, store.OTPStep, func(*store.VisitFacts) error) (*store.VisitFacts, error)
	FinishVisit(context.Context, store.FinishInput, func(*store.VisitFacts) error) (*store.FinishResult, error)
	CompleteVisit(context.Context, store.OTPStep, store.CompleteSpec, func(*store.VisitFacts) error) (*store.VisitFacts, error)
	CustomerNoShow(context.Context, uuid.UUID, uuid.UUID, time.Time, func(*store.VisitFacts) (*store.NoShowDecision, error)) ([]uuid.UUID, error)
	MaxCancellationFee(context.Context, string, uuid.UUID) (int64, error)
	VisitExtraOptions(context.Context, uuid.UUID, time.Time) ([]store.VisitExtraOption, error)
	ProposeVisitExtra(context.Context, store.NewExtra, func(*store.VisitFacts, store.VisitExtraOption) (int64, int64, error)) (*model.Extra, error)
	DecideVisitExtra(context.Context, uuid.UUID, uuid.UUID, string, time.Time, uuid.UUID, uuid.UUID, string, func(*store.VisitFacts) error) (*model.Extra, error)
	VisitBill(context.Context, uuid.UUID) (*model.ExtrasBill, error)
	VisitBillPayment(context.Context, uuid.UUID, uuid.UUID) (*store.PaymentRow, error)
	VisitOutstanding(context.Context, uuid.UUID) (*model.Outstanding, error)
	ExpireVisitBills(context.Context, time.Time, int) (int, error)
}

func (s *Service) WithVisit(st VisitStore) *Service { s.visit = st; return s }

func visitOwner(f *store.VisitFacts, user uuid.UUID, customer bool) error {
	if customer {
		if f.Customer != user {
			return bookingNotFound()
		}
	} else if f.Pro == nil || f.Pro.UserID != user || !f.HeldBy(f.Pro.ProID) {
		return bookingNotFound()
	}
	return nil
}

func visitActivePro(f *store.VisitFacts, user uuid.UUID) error {
	if err := visitOwner(f, user, false); err != nil {
		return err
	}
	if !f.Holds(f.Pro.ProID) {
		return bookingTransition(f.Status)
	}
	if f.Pro.AccountStatus != "approved" {
		return apperr.New(http.StatusForbidden, apperr.CodeProSuspended, "your account cannot perform visits")
	}
	return nil
}

func visitPhotos(f *store.VisitFacts, before bool) error {
	missing := f.Photos.After < f.MinAfter
	if before {
		missing = f.Photos.Before < f.MinBefore || (f.Family == "BEAUTY_SALON" && f.Photos.KitSeal < 1)
	}
	if missing {
		return apperr.New(422, apperr.CodePhotosRequired, "upload the required workspace photos before continuing")
	}
	return nil
}

func visitStartGuard(f *store.VisitFacts, user uuid.UUID) error {
	if err := visitActivePro(f, user); err != nil {
		return err
	}
	if f.Status != "arrived" {
		return bookingTransition(f.Status)
	}
	return visitPhotos(f, true)
}

func visitFinishGuard(f *store.VisitFacts, user uuid.UUID) error {
	if err := visitActivePro(f, user); err != nil {
		return err
	}
	if f.Status != "in_progress" || f.FinishedAt != nil {
		return bookingTransition(f.Status)
	}
	if f.ProposedExtras > 0 {
		return apperr.New(409, "DOORSTEP_EXTRAS_PENDING", "the customer must approve or decline the proposed extras first")
	}
	return visitPhotos(f, false)
}

func visitCompleteGuard(f *store.VisitFacts, user uuid.UUID) error {
	if err := visitActivePro(f, user); err != nil {
		return err
	}
	if (f.Status != "in_progress" && f.Status != "awaiting_extras_payment") || f.FinishedAt == nil {
		return bookingTransition(f.Status)
	}
	if f.UnpaidBills > 0 || f.ApprovedUnbilled > 0 || f.ProposedExtras > 0 {
		return apperr.New(409, "DOORSTEP_EXTRAS_PENDING", "extras must be paid or recorded as outstanding before completion")
	}
	return visitPhotos(f, false)
}

func distanceM(lat1, lng1, lat2, lng2 float64) float64 {
	r := math.Pi / 180
	a := math.Pow(math.Sin((lat2-lat1)*r/2), 2) + math.Cos(lat1*r)*math.Cos(lat2*r)*math.Pow(math.Sin((lng2-lng1)*r/2), 2)
	return 6371000 * 2 * math.Atan2(math.Sqrt(a), math.Sqrt(math.Max(0, 1-a)))
}

func visitMoveGuard(f *store.VisitFacts, user uuid.UUID, to string, fix *store.Fix) error {
	if err := visitActivePro(f, user); err != nil {
		return err
	}
	if to == "en_route" && f.Status != "assigned" {
		return bookingTransition(f.Status)
	}
	if to == "arrived" {
		if f.Status != "en_route" {
			return bookingTransition(f.Status)
		}
		if fix == nil {
			return apperr.Invalid("lat", "a location fix is required")
		}
		d := distanceM(f.Lat, f.Lng, fix.Lat, fix.Lng)
		if d > 200 {
			return apperr.New(422, "DOORSTEP_GEO_CHECK_FAILED", "move closer to the booking address before marking arrival").WithDetails(map[string]any{"distance_m": int(math.Ceil(d)), "max_distance_m": 200})
		}
	}
	return nil
}

func (s *Service) visitErr(ctx context.Context, err error) error {
	if err == nil {
		return nil
	}
	var ae *apperr.Error
	if errors.As(err, &ae) {
		return ae
	}
	var oe *store.OTPError
	if errors.As(err, &oe) {
		if oe.Locked {
			return apperr.New(423, apperr.CodeOTPLocked, "too many incorrect codes; try again after the lock expires").WithDetails(map[string]any{"locked_until": oe.Until})
		}
		return apperr.New(422, "DOORSTEP_OTP_INVALID", "the code does not match; ask the customer to check it").WithDetails(map[string]any{"attempts_left": oe.AttemptsLeft})
	}
	if errors.Is(err, store.ErrNotFound) {
		return bookingNotFound()
	}
	if errors.Is(err, store.ErrOTPMissing) {
		return apperr.New(409, "DOORSTEP_OTP_NOT_READY", "the customer must open this booking to see the code")
	}
	if errors.Is(err, store.ErrPhotoTaken) || errors.Is(err, store.ErrStale) {
		return apperr.New(409, apperr.CodeConflict, "the booking changed; refresh and try again")
	}
	return internal(ctx, "visit", err)
}

func (s *Service) ProVisitMove(ctx context.Context, user, id uuid.UUID, to string, in *model.LocationInput) (*model.ProJob, error) {
	fix, ae := readFix(in, to == "arrived")
	if ae != nil {
		return nil, ae
	}
	_, err := s.visit.MoveVisit(ctx, store.StepInput{BookingID: id, ProUser: user, To: to, Fix: fix, At: s.nowUTC()}, func(f *store.VisitFacts) error { return visitMoveGuard(f, user, to, fix) })
	if err != nil {
		return nil, s.visitErr(ctx, err)
	}
	s.publishBookingNow(ctx, id)
	return s.ProJob(ctx, user, id)
}

func (s *Service) ProVisitPhoto(ctx context.Context, user, id uuid.UUID, in model.PhotoInput) (*model.Photo, error) {
	if in.Phase == nil || (*in.Phase != "before" && *in.Phase != "after" && *in.Phase != "kit_seal") {
		return nil, apperr.Invalid("phase", "choose before, after or kit_seal")
	}
	media, ae := parseMediaID("media_id", in.MediaID)
	if ae != nil {
		return nil, ae
	}
	f, err := s.visit.VisitFacts(ctx, id, s.nowUTC())
	if err != nil {
		return nil, s.visitErr(ctx, err)
	}
	guard := func(f *store.VisitFacts) error {
		if err := visitActivePro(f, user); err != nil {
			return err
		}
		if f.Status != "arrived" && f.Status != "in_progress" {
			return bookingTransition(f.Status)
		}
		if f.FinishedAt != nil || (*in.Phase == "after" && f.Status != "in_progress") {
			return bookingTransition(f.Status)
		}
		if *in.Phase == "kit_seal" && f.Family != "BEAUTY_SALON" {
			return apperr.Invalid("phase", "kit_seal is only for salon visits")
		}
		return nil
	}
	if err := guard(f); err != nil {
		return nil, err
	}
	if in.Lat != nil || in.Lng != nil {
		if _, _, ae := checkPoint(in.Lat, in.Lng); ae != nil {
			return nil, ae
		}
	}
	if err := s.prepareVisitPhoto(ctx, "media_id", media, user); err != nil {
		return nil, err
	}
	p, err := s.visit.AddPhoto(ctx, store.PhotoWrite{ID: s.bk.NewID(), BookingID: id, ProID: f.Pro.ProID, Phase: *in.Phase, MediaID: media.String(), Lat: in.Lat, Lng: in.Lng, At: s.nowUTC()}, guard)
	return p, s.visitErr(ctx, err)
}

func (s *Service) prepareVisitPhoto(ctx context.Context, field string, media, owner uuid.UUID) error {
	preparer, ok := s.pro.Media.(mediaclient.VisitPhotoPreparer)
	if !ok {
		return apperr.New(503, apperr.CodeMediaUnavailable, "private photo verification is unavailable; try again")
	}
	err := preparer.PrepareVisitPhoto(ctx, media, owner)
	if err == nil {
		return nil
	}
	if errors.Is(err, mediaclient.ErrUnavailable) {
		return apperr.New(503, apperr.CodeMediaUnavailable, "private photo verification is unavailable; try again")
	}
	return apperr.Invalid(field, "use your own processed, approved private photo")
}

var visitOTPPattern = regexp.MustCompile(`^[0-9]{4}$`)

func otpCheck(in model.OTPInput, kind string) (store.OTPCheck, error) {
	if in.OTP == nil || !visitOTPPattern.MatchString(*in.OTP) {
		return store.OTPCheck{}, apperr.Invalid("otp", "enter the four-digit code")
	}
	code := *in.OTP
	return store.OTPCheck{Kind: kind, MaxAttempts: 5, Lock: 15 * time.Minute, Match: func(hash string) bool { return bcrypt.CompareHashAndPassword([]byte(hash), []byte(code)) == nil }}, nil
}

func (s *Service) ProVisitStart(ctx context.Context, user, id uuid.UUID, in model.OTPInput) (*model.ProJob, error) {
	c, err := otpCheck(in, "start")
	if err != nil {
		return nil, err
	}
	_, err = s.visit.StartVisit(ctx, store.OTPStep{BookingID: id, ProUser: user, Check: c, At: s.nowUTC()}, func(f *store.VisitFacts) error { return visitStartGuard(f, user) })
	if err != nil {
		return nil, s.visitErr(ctx, err)
	}
	s.publishBookingNow(ctx, id)
	return s.ProJob(ctx, user, id)
}

func (s *Service) ProVisitFinish(ctx context.Context, user, id uuid.UUID) (*model.ProJob, error) {
	bill := s.bk.NewID()
	_, err := s.visit.FinishVisit(ctx, store.FinishInput{BookingID: id, ProUser: user, BillID: bill, PaymentID: s.bk.NewID(), IntentKey: payments.ExtrasIntentKey(bill), At: s.nowUTC()}, func(f *store.VisitFacts) error { return visitFinishGuard(f, user) })
	if err != nil {
		return nil, s.visitErr(ctx, err)
	}
	s.publishBookingNow(ctx, id)
	return s.ProJob(ctx, user, id)
}

// OTP payloads are bound to booking and kind, even if ciphertext is swapped.
type visitOTPBlob struct {
	BookingID uuid.UUID `json:"booking_id"`
	Kind      string    `json:"kind"`
	Code      string    `json:"code"`
}

func (s *Service) customerVisitOTP(ctx context.Context, id uuid.UUID, kind string) (*string, error) {
	r, err := s.visit.VisitOTP(ctx, id, kind)
	if errors.Is(err, store.ErrNotFound) {
		n, e := rand.Int(rand.Reader, big.NewInt(10000))
		if e != nil {
			return nil, e
		}
		code := fmt.Sprintf("%04d", n.Int64())
		hash, e := bcrypt.GenerateFromPassword([]byte(code), bcrypt.DefaultCost)
		if e != nil {
			return nil, e
		}
		body, _ := json.Marshal(visitOTPBlob{BookingID: id, Kind: kind, Code: code})
		sealed, e := s.pro.PII.SealVisitOTP(ctx, string(body))
		if e != nil {
			return nil, piiUnavailable()
		}
		if e = s.visit.EnsureOTP(ctx, id, kind, string(hash), sealed.Blob, s.nowUTC()); e != nil {
			return nil, e
		}
		r, err = s.visit.VisitOTP(ctx, id, kind)
	}
	if err != nil {
		return nil, err
	}
	if r.Verified {
		return nil, nil
	}
	plain, err := s.pro.PII.OpenVisitOTP(ctx, r.Sealed)
	if err != nil {
		return nil, piiUnavailable()
	}
	var b visitOTPBlob
	if err = json.Unmarshal([]byte(plain), &b); err != nil || b.BookingID != id || b.Kind != kind || !visitOTPPattern.MatchString(b.Code) {
		return nil, apperr.Internal()
	}
	return &b.Code, nil
}

func (s *Service) customerVisitCodes(ctx context.Context, b *model.Booking) *apperr.Error {
	if s.visit == nil || (b.Status != "arrived" && b.Status != "in_progress" && b.Status != "awaiting_extras_payment") {
		return nil
	}
	f, err := s.visit.VisitFacts(ctx, b.ID, s.nowUTC())
	if err != nil {
		return internal(ctx, "visit codes", err)
	}
	var code *string
	if b.Status == "arrived" {
		code, err = s.customerVisitOTP(ctx, b.ID, "start")
		b.StartOTP = code
	}
	if f.FinishedAt != nil && f.UnpaidBills == 0 && f.ApprovedUnbilled == 0 && f.ProposedExtras == 0 && visitPhotos(f, false) == nil {
		code, err = s.customerVisitOTP(ctx, b.ID, "end")
		b.EndOTP = code
	}
	if err != nil {
		var ae *apperr.Error
		if errors.As(err, &ae) {
			return ae
		}
		return internal(ctx, "visit codes", err)
	}
	return nil
}

func (s *Service) VisitPhotoBytes(ctx context.Context, user, id uuid.UUID, media string) ([]byte, string, error) {
	f, err := s.visit.VisitFacts(ctx, id, s.nowUTC())
	if err != nil {
		return nil, "", s.visitErr(ctx, err)
	}
	if f.Customer != user && visitOwner(f, user, false) != nil {
		return nil, "", bookingNotFound()
	}
	r, err := s.visit.PhotoByMedia(ctx, id, media)
	if err != nil {
		return nil, "", s.visitErr(ctx, err)
	}
	if f.Customer != user && (f.Pro == nil || r.ProID != f.Pro.ProID) {
		return nil, "", bookingNotFound()
	}
	m, err := uuid.Parse(media)
	if err != nil {
		return nil, "", apperr.Invalid("media_id", "invalid media id")
	}
	if s.pro.Images == nil {
		return nil, "", apperr.New(503, apperr.CodeMediaUnavailable, "photos are temporarily unavailable")
	}
	data, ct, err := s.pro.Images.FetchImage(ctx, m)
	if err != nil {
		return nil, "", apperr.New(503, apperr.CodeMediaUnavailable, "photos are temporarily unavailable")
	}
	return data, ct, nil
}
