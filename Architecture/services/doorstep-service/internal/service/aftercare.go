package service

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"math"
	"net/http"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/atpost/doorstep-service/internal/apperr"
	"github.com/atpost/doorstep-service/internal/model"
	"github.com/atpost/doorstep-service/internal/store"
	"github.com/google/uuid"
)

// All writes recheck the participant and lifecycle on the locked booking.
type AftercareStore interface {
	VisitReworks(context.Context, uuid.UUID, uuid.UUID) ([]model.ReworkRequest, error)
	RequestVisitRework(context.Context, store.ReworkSpec, func(*store.VisitFacts) error) (*model.ReworkRequest, error)
	RateVisit(context.Context, uuid.UUID, uuid.UUID, string, model.RatingInput, time.Time, func(*store.VisitFacts) error) (*model.Rating, error)
	VisitMessages(context.Context, uuid.UUID, uuid.UUID, string, *uuid.UUID, time.Time, func(*store.VisitFacts) error) (*model.MessagePage, error)
	SendVisitMessage(context.Context, uuid.UUID, uuid.UUID, string, string, time.Time, func(*store.VisitFacts) error) (*model.Message, error)
	ReadVisitMessage(context.Context, uuid.UUID, uuid.UUID, uuid.UUID, time.Time) error
	SaveVisitShare(context.Context, uuid.UUID, uuid.UUID, string, time.Time, time.Time, func(*store.VisitFacts) error) error
	RevokeVisitShare(context.Context, uuid.UUID, uuid.UUID, time.Time, func(*store.VisitFacts) error) error
	SharedVisit(context.Context, string, time.Time) (*model.SharedBookingView, error)
	SaveTrustedContact(context.Context, uuid.UUID, string, []byte, string, time.Time) (*model.TrustedContact, error)
	TrustedContact(context.Context, uuid.UUID) (*model.TrustedContact, error)
	VisitTicket(context.Context, uuid.UUID, uuid.UUID) (*model.Ticket, error)
	VisitTickets(context.Context, uuid.UUID) ([]model.Ticket, error)
	OpenVisitTicket(context.Context, uuid.UUID, string, model.TicketInput, time.Time) (*model.Ticket, error)
	VisitEarnings(context.Context, uuid.UUID, time.Time, time.Time) (*model.Earnings, error)
	RaiseVisitIncident(context.Context, uuid.UUID, uuid.UUID, string, string, model.SOSInput, time.Time, func(*store.VisitFacts) error) (*store.IncidentResult, error)
}

func (s *Service) WithAftercare(st AftercareStore) *Service { s.aftercare = st; return s }

var regexpPhone = regexp.MustCompile(`^\+91[6-9][0-9]{9}$`)

func participantGuard(f *store.VisitFacts, u uuid.UUID, kind string) error {
	return visitOwner(f, u, kind == "customer")
}
func ratingGuard(f *store.VisitFacts, u uuid.UUID, kind string) error {
	if e := participantGuard(f, u, kind); e != nil {
		return e
	}
	if f.Status != "completed" {
		return bookingTransition(f.Status)
	}
	return nil
}
func chatOpen(f *store.VisitFacts, at time.Time) bool {
	if f.Pro == nil {
		return false
	}
	if f.Status == "completed" {
		return f.CompletedAt != nil && at.Before(f.CompletedAt.Add(2*time.Hour))
	}
	return f.Pro.Status == "accepted" && store.Contains([]string{"assigned", "en_route", "arrived", "in_progress", "awaiting_extras_payment"}, f.Status)
}
func chatWriteGuard(f *store.VisitFacts, u uuid.UUID, kind string, at time.Time) error {
	if e := participantGuard(f, u, kind); e != nil {
		return e
	}
	if !chatOpen(f, at) {
		return apperr.New(409, "DOORSTEP_CHAT_CLOSED", "this visit conversation is closed")
	}
	return nil
}
func (s *Service) RateVisit(ctx context.Context, u, id uuid.UUID, kind string, in model.RatingInput) (*model.Rating, error) {
	if in.Stars == nil || *in.Stars < 1 || *in.Stars > 5 {
		return nil, apperr.Invalid("stars", "choose 1 to 5 stars")
	}
	if len(in.Tags) > 10 {
		return nil, apperr.Invalid("tags", "choose at most 10 tags")
	}
	for _, tag := range in.Tags {
		if utf8.RuneCountInString(tag) > 40 || strings.TrimSpace(tag) == "" {
			return nil, apperr.Invalid("tags", "each tag must be between 1 and 40 characters")
		}
	}
	if in.Comment != nil && utf8.RuneCountInString(*in.Comment) > 1000 {
		return nil, apperr.Invalid("comment", "keep the comment within 1000 characters")
	}
	return s.aftercare.RateVisit(ctx, id, u, kind, in, s.nowUTC(), func(f *store.VisitFacts) error { return ratingGuard(f, u, kind) })
}
func (s *Service) VisitMessages(ctx context.Context, u, id uuid.UUID, kind, rawCursor string) (*model.MessagePage, error) {
	var cursor *uuid.UUID
	if rawCursor != "" {
		v, e := uuid.Parse(rawCursor)
		if e != nil || v == uuid.Nil {
			return nil, apperr.Invalid("cursor", "cursor must be a message id")
		}
		cursor = &v
	}
	return s.aftercare.VisitMessages(ctx, id, u, kind, cursor, s.nowUTC(), func(f *store.VisitFacts) error { return participantGuard(f, u, kind) })
}
func (s *Service) SendVisitMessage(ctx context.Context, u, id uuid.UUID, kind string, in model.MessageInput) (*model.Message, error) {
	if in.Body == nil {
		return nil, apperr.Invalid("body", "enter a message")
	}
	body := strings.TrimSpace(*in.Body)
	if utf8.RuneCountInString(body) < 1 || utf8.RuneCountInString(body) > 1000 {
		return nil, apperr.Invalid("body", "use 1 to 1000 characters")
	}
	at := s.nowUTC()
	v, e := s.aftercare.SendVisitMessage(ctx, id, u, kind, body, at, func(f *store.VisitFacts) error { return chatWriteGuard(f, u, kind, at) })
	if e == nil {
		s.publishBookingNow(ctx, id)
	}
	return v, e
}
func (s *Service) ReadVisitMessage(ctx context.Context, u, id, msg uuid.UUID) error {
	return s.aftercare.ReadVisitMessage(ctx, id, u, msg, s.nowUTC())
}
func shareHash(raw string) string { v := sha256.Sum256([]byte(raw)); return hex.EncodeToString(v[:]) }
func (s *Service) ShareVisit(ctx context.Context, u, id uuid.UUID) (*model.ShareToken, error) {
	var bytes [32]byte
	if _, e := rand.Read(bytes[:]); e != nil {
		return nil, internal(ctx, "share randomness", e)
	}
	raw := base64.RawURLEncoding.EncodeToString(bytes[:])
	if s.pro.NewToken != nil {
		v, e := s.pro.NewToken()
		if e != nil {
			return nil, internal(ctx, "share randomness", e)
		}
		raw = v
	}
	at := s.nowUTC()
	until := at.Add(24 * time.Hour)
	e := s.aftercare.SaveVisitShare(ctx, id, u, shareHash(raw), until, at, func(f *store.VisitFacts) error {
		if e := visitOwner(f, u, true); e != nil {
			return e
		}
		if store.Contains([]string{"cancelled", "expired", "completed", "customer_no_show", "pro_no_show", "pro_unavailable"}, f.Status) {
			return bookingTransition(f.Status)
		}
		return nil
	})
	if e != nil {
		return nil, e
	}
	return &model.ShareToken{Token: raw, URL: "/doorstep/share/" + raw, ExpiresAt: until}, nil
}
func (s *Service) SharedVisit(ctx context.Context, raw string) (*model.SharedBookingView, error) {
	if len(raw) != 43 {
		return nil, bookingNotFound()
	}
	return s.aftercare.SharedVisit(ctx, shareHash(raw), s.nowUTC())
}
func (s *Service) RevokeVisitShare(ctx context.Context, u, id uuid.UUID) error {
	return s.aftercare.RevokeVisitShare(ctx, id, u, s.nowUTC(), func(f *store.VisitFacts) error { return visitOwner(f, u, true) })
}
func (s *Service) SaveTrustedContact(ctx context.Context, u uuid.UUID, in model.TrustedContactInput) (*model.TrustedContact, error) {
	if in.Name == nil || strings.TrimSpace(*in.Name) == "" || utf8.RuneCountInString(*in.Name) > 80 {
		return nil, apperr.Invalid("name", "enter a name within 80 characters")
	}
	if in.Phone == nil {
		return nil, apperr.Invalid("phone", "enter the contact's phone number")
	}
	phone := strings.TrimSpace(*in.Phone)
	if !regexpPhone.MatchString(phone) {
		return nil, apperr.Invalid("phone", "use an Indian mobile number with +91")
	}
	sealed, e := s.pro.PII.SealTrustedContact(ctx, u.String()+":"+phone)
	if e != nil {
		return nil, apperr.New(503, apperr.CodePIIUnavailable, "trusted contact storage is unavailable")
	}
	return s.aftercare.SaveTrustedContact(ctx, u, strings.TrimSpace(*in.Name), sealed.Blob, phone[len(phone)-4:], s.nowUTC())
}
func (s *Service) TrustedContact(ctx context.Context, u uuid.UUID) (*model.TrustedContact, error) {
	v, e := s.aftercare.TrustedContact(ctx, u)
	if errors.Is(e, store.ErrNotFound) {
		return nil, nil
	}
	return v, e
}
func (s *Service) VisitTickets(ctx context.Context, u uuid.UUID) ([]model.Ticket, error) {
	return s.aftercare.VisitTickets(ctx, u)
}
func (s *Service) VisitTicket(ctx context.Context, u, id uuid.UUID) (*model.Ticket, error) {
	return s.aftercare.VisitTicket(ctx, id, u)
}
func (s *Service) OpenVisitTicket(ctx context.Context, u uuid.UUID, in model.TicketInput) (*model.Ticket, error) {
	if in.Category == nil || !store.Contains([]string{"payment", "quality", "safety", "damage", "professional", "other"}, *in.Category) {
		return nil, apperr.Invalid("category", "choose a support category")
	}
	if in.Subject == nil || strings.TrimSpace(*in.Subject) == "" || utf8.RuneCountInString(*in.Subject) > 160 {
		return nil, apperr.Invalid("subject", "use a subject within 160 characters")
	}
	if in.Body == nil || strings.TrimSpace(*in.Body) == "" || utf8.RuneCountInString(*in.Body) > 4000 {
		return nil, apperr.Invalid("body", "describe the issue within 4000 characters")
	}
	return s.aftercare.OpenVisitTicket(ctx, u, "customer", in, s.nowUTC())
}
func (s *Service) VisitEarnings(ctx context.Context, u uuid.UUID, from, to string) (*model.Earnings, error) {
	at := s.nowUTC()
	start := at.AddDate(0, -1, 0)
	end := at
	var e error
	if from != "" {
		start, e = time.Parse("2006-01-02", from)
		if e != nil {
			return nil, apperr.Invalid("from", "use YYYY-MM-DD")
		}
	}
	if to != "" {
		end, e = time.Parse("2006-01-02", to)
		if e != nil {
			return nil, apperr.Invalid("to", "use YYYY-MM-DD")
		}
		end = end.Add(24 * time.Hour)
	}
	if end.Before(start) || end.Sub(start) > 366*24*time.Hour {
		return nil, apperr.Invalid("to", "choose a period of up to one year")
	}
	return s.aftercare.VisitEarnings(ctx, u, start, end)
}
func safetyGuard(f *store.VisitFacts, u uuid.UUID, kind string) error {
	if e := participantGuard(f, u, kind); e != nil {
		return e
	}
	if f.Pro == nil || !store.Contains([]string{"assigned", "en_route", "arrived", "in_progress", "awaiting_extras_payment"}, f.Status) {
		return bookingTransition(f.Status)
	}
	return nil
}
func (s *Service) VisitSOS(ctx context.Context, u, id uuid.UUID, kind string, unsafe bool, in model.SOSInput) (*model.Incident, error) {
	if in.Note != nil && utf8.RuneCountInString(*in.Note) > 2000 {
		return nil, apperr.Invalid("note", "use at most 2000 characters")
	}
	if (in.Lat == nil) != (in.Lng == nil) {
		return nil, apperr.Invalid("lat", "provide both latitude and longitude")
	}
	if in.Lat != nil && (math.IsNaN(*in.Lat) || math.IsNaN(*in.Lng) || math.IsInf(*in.Lat, 0) || math.IsInf(*in.Lng, 0) || *in.Lat < -90 || *in.Lat > 90 || *in.Lng < -180 || *in.Lng > 180) {
		return nil, apperr.Invalid("lat", "use a valid location")
	}
	action := "sos"
	if unsafe {
		if kind != "pro" {
			return nil, apperr.New(http.StatusForbidden, apperr.CodeInvalidRequest, "only the professional may leave a visit")
		}
		action = "unsafe_exit"
	}
	result, e := s.aftercare.RaiseVisitIncident(ctx, id, u, kind, action, in, s.nowUTC(), func(f *store.VisitFacts) error { return safetyGuard(f, u, kind) })
	if errors.Is(e, store.ErrNotFound) {
		return nil, bookingNotFound()
	}
	if e != nil {
		return nil, e
	}
	s.SubmitRefunds(ctx, result.RefundIDs...)
	for _, b := range result.ChangedBookings {
		s.publishBookingNow(ctx, b)
	}
	return result.Incident, nil
}
