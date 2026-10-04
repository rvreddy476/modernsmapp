package service

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/atpost/doorstep-service/internal/apperr"
	"github.com/atpost/doorstep-service/internal/dispatch"
	"github.com/atpost/doorstep-service/internal/model"
	"github.com/atpost/doorstep-service/internal/prokyc"
	"github.com/atpost/doorstep-service/internal/slots"
	"github.com/atpost/doorstep-service/internal/store"
	"github.com/google/uuid"
)

// A professional's working day (A4): duty and location, offers, jobs, and
// the reads of their own profile the app needs (zones to pick, skills,
// area, masked bank account, documents).

// ---------------------------------------------------------------- duty

// ProDutyState is the professional's duty.
func (s *Service) ProDutyState(ctx context.Context, user uuid.UUID) (*model.DutyState, error) {
	p, err := s.mine(ctx, user)
	if err != nil {
		return nil, err
	}
	d, err := s.ds.Store.ProDuty(ctx, p.ID)
	if err != nil {
		return nil, internal(ctx, "duty", err)
	}
	return d, nil
}

// readFix validates an optional fix.
func readFix(in *model.LocationInput, required bool) (*store.Fix, *apperr.Error) {
	if in == nil || (in.Lat == nil && in.Lng == nil && in.AccuracyM == nil) {
		if required {
			return nil, apperr.Invalid("lat", "lat and lng are required")
		}
		return nil, nil
	}
	lat, lng, aerr := checkPoint(in.Lat, in.Lng)
	if aerr != nil {
		return nil, aerr
	}
	if in.AccuracyM != nil && (*in.AccuracyM < 0 || *in.AccuracyM > 100000) {
		return nil, apperr.Invalid("accuracy_m", "accuracy_m must be between 0 and 100000")
	}
	return &store.Fix{Lat: lat, Lng: lng, AccuracyM: in.AccuracyM}, nil
}

// ProDutyOn puts an approved professional on duty: everything onboarding
// asks for done, not suspended, a background check clear today. A fix
// given with it is recorded.
func (s *Service) ProDutyOn(ctx context.Context, user uuid.UUID, in *model.LocationInput) (*model.DutyState, error) {
	fix, aerr := readFix(in, false)
	if aerr != nil {
		return nil, aerr
	}
	p, err := s.mine(ctx, user)
	if err != nil {
		return nil, err
	}
	st, err := s.pro.Store.ProFacts(ctx, p.ID, s.today())
	if err != nil {
		return nil, internal(ctx, "pro facts", err)
	}
	switch {
	case st.Status == "suspended" || st.IncidentSuspended:
		return nil, apperr.New(http.StatusForbidden, apperr.CodeProSuspended, "your account is suspended")
	case st.Status != "approved":
		return nil, apperr.New(http.StatusForbidden, apperr.CodeProNotApproved, "your account is not approved for jobs yet")
	case !st.BackgroundClear:
		return nil, apperr.New(http.StatusForbidden, apperr.CodeBackgroundCheckRequired, "your background check is not valid today")
	}
	if missing := prokyc.MissingSteps(st.Facts); len(missing) > 0 {
		return nil, incomplete(missing...)
	}
	now := s.nowUTC()
	d, changed, err := s.ds.Store.SetDuty(ctx, p.ID, true, fix, now)
	if err != nil {
		return nil, internal(ctx, "duty on", err)
	}
	if fix != nil && s.ds.Presence != nil {
		if err := s.ds.Presence.Upsert(ctx, p.CityCode, p.ID, fix.Lat, fix.Lng); err != nil {
			slog.WarnContext(ctx, "doorstep: presence upsert failed", "pro_id", p.ID, "error", err)
		}
	}
	if changed {
		s.publish(ctx, ProTopic(user), FrameDuty, DutyFrame{OnDuty: true, Since: d.Since, Reason: "professional", At: now})
	}
	return d, nil
}

// ProDutyOff takes the professional off duty (idempotent).
func (s *Service) ProDutyOff(ctx context.Context, user uuid.UUID) (*model.DutyState, error) {
	p, err := s.mine(ctx, user)
	if err != nil {
		return nil, err
	}
	if err := s.offDuty(ctx, p.ID, user, p.CityCode, "professional"); err != nil {
		return nil, internal(ctx, "duty off", err)
	}
	d, err := s.ds.Store.ProDuty(ctx, p.ID)
	if err != nil {
		return nil, internal(ctx, "duty", err)
	}
	return d, nil
}

// offDuty takes a professional off duty and out of the live map.
func (s *Service) offDuty(ctx context.Context, proID, user uuid.UUID, city, reason string) error {
	now := s.nowUTC()
	_, changed, err := s.ds.Store.SetDuty(ctx, proID, false, nil, now)
	if err != nil {
		return err
	}
	if s.ds.Presence != nil {
		if err := s.ds.Presence.Remove(ctx, city, proID); err != nil {
			slog.WarnContext(ctx, "doorstep: presence remove failed", "pro_id", proID, "error", err)
		}
	}
	if changed {
		s.publish(ctx, ProTopic(user), FrameDuty, DutyFrame{OnDuty: false, Reason: reason, At: now})
	}
	return nil
}

// ProLocation records the on-duty professional's latest fix (no trail is
// kept), mirrors it into the city's live map, and while they travel to a
// job tells that booking's customer where they are.
func (s *Service) ProLocation(ctx context.Context, user uuid.UUID, in model.LocationInput) error {
	fix, aerr := readFix(&in, true)
	if aerr != nil {
		return aerr
	}
	p, err := s.mine(ctx, user)
	if err != nil {
		return err
	}
	now := s.nowUTC()
	if err := s.ds.Store.RecordFix(ctx, p.ID, *fix, now); errors.Is(err, store.ErrNotOnDuty) {
		return apperr.New(http.StatusConflict, apperr.CodeNotOnDuty, "go on duty before sending your location")
	} else if err != nil {
		return internal(ctx, "record fix", err)
	}
	if s.ds.Presence != nil {
		if err := s.ds.Presence.Upsert(ctx, p.CityCode, p.ID, fix.Lat, fix.Lng); err != nil {
			slog.WarnContext(ctx, "doorstep: presence upsert failed", "pro_id", p.ID, "error", err)
		}
	}
	if s.ds.Realtime != nil {
		jobs, err := s.ds.Store.TravellingTo(ctx, p.ID)
		if err != nil {
			slog.WarnContext(ctx, "doorstep: en-route jobs not read", "pro_id", p.ID, "error", err)
		}
		for _, j := range jobs {
			s.publish(ctx, BookingTopic(j.BookingID), FrameProLocation, LocationFrame{BookingID: j.BookingID, Lat: fix.Lat, Lng: fix.Lng,
				EtaMinutes: etaMinutes(slots.HaversineM(fix.Lat, fix.Lng, j.Lat, j.Lng)), At: now})
		}
	}
	return nil
}

// ---------------------------------------------------------------- offers

// ProOffers lists the professional's open offers.
func (s *Service) ProOffers(ctx context.Context, user uuid.UUID) ([]model.Offer, error) {
	p, err := s.mine(ctx, user)
	if err != nil {
		return nil, err
	}
	out, err := s.ds.Store.ProOffers(ctx, p.ID, s.nowUTC())
	if err != nil {
		return nil, internal(ctx, "offers", err)
	}
	return out, nil
}

// ProOffer reads one of the professional's offers in any state (status
// says whether it can still be accepted).
func (s *Service) ProOffer(ctx context.Context, user, id uuid.UUID) (*model.Offer, error) {
	p, err := s.mine(ctx, user)
	if err != nil {
		return nil, err
	}
	o, err := s.ds.Store.ProOffer(ctx, p.ID, id, s.nowUTC())
	if errors.Is(err, store.ErrNotFound) {
		return nil, offerNotFound()
	}
	if err != nil {
		return nil, internal(ctx, "offer", err)
	}
	return o, nil
}

// ---------------------------------------------------------------- jobs

// proJob assembles a booking as the professional sees it: the locality
// always; the address and chat from acceptance to completion + 2 h.
func (s *Service) proJob(ctx context.Context, proID, bookingID uuid.UUID) (*model.ProJob, error) {
	now := s.nowUTC()
	r, err := s.ds.Store.ProJobRecord(ctx, proID, bookingID, now)
	if errors.Is(err, store.ErrNotFound) {
		return nil, bookingNotFound()
	}
	if err != nil {
		return nil, internal(ctx, "job", err)
	}
	b := r.Booking.Booking
	job := &model.ProJob{BookingID: b.ID, Status: b.Status, ServiceName: b.ServiceName, CategorySlug: b.CategorySlug,
		SlotStart: b.SlotStart, SlotEnd: b.SlotEnd, Items: b.Items, Locality: b.Address.Locality,
		PhotosRequired: model.PhotoCounts{Before: r.MinBefore, After: r.MinAfter},
		PhotosUploaded: r.Uploaded, ArrivedAt: r.ArrivedAt, Finished: r.FinishedAt != nil,
		EarningEstimatePaise: dispatch.EarningEstimate(b.TaxablePaise, r.CommissionBPS)}
	if job.Items == nil {
		job.Items = []model.QuoteLine{}
	}
	if r.Family == "BEAUTY_SALON" {
		job.PhotosRequired.KitSeal = 1 // a sealed-kit photo at start (salon)
	}
	if dispatch.AddressVisible(r.AssignmentStatus, b.Status, r.CompletedAt, now) {
		l, aerr := s.openLines(ctx, r.Booking.AddressSealed)
		if aerr != nil {
			return nil, aerr
		}
		addr := b.Address
		addr.Line1, addr.Line2, addr.Landmark = l.Line1, l.Line2, l.Landmark
		job.Address, job.ChatOpen = &addr, true
	}
	return job, nil
}

// ProJob is one of the professional's jobs (or an offered one, locality
// only).
func (s *Service) ProJob(ctx context.Context, user, bookingID uuid.UUID) (*model.ProJob, error) {
	p, err := s.mine(ctx, user)
	if err != nil {
		return nil, err
	}
	return s.proJob(ctx, p.ID, bookingID)
}

var proJobModes = map[string]string{"": store.JobsAll, "upcoming": store.JobsUpcoming, "active": store.JobsActive,
	"past": store.JobsPast, "all": store.JobsAll}

// ProJobs pages the professional's accepted jobs: status upcoming, active,
// past or all; date (YYYY-MM-DD, IST) narrows to one day.
func (s *Service) ProJobs(ctx context.Context, user uuid.UUID, status, date, cursor string, limit int) (*model.ProJobPage, error) {
	mode, ok := proJobModes[strings.TrimSpace(status)]
	if !ok {
		return nil, apperr.Invalid("status", "status must be upcoming, active, past or all")
	}
	date = strings.TrimSpace(date)
	if date != "" {
		if _, err := time.Parse("2006-01-02", date); err != nil || !dateRe.MatchString(date) {
			return nil, apperr.Invalid("date", "date must be YYYY-MM-DD")
		}
	}
	after, aerr := decodeBookingCursor(cursor)
	if aerr != nil {
		return nil, aerr
	}
	p, err := s.mine(ctx, user)
	if err != nil {
		return nil, err
	}
	keys, err := s.ds.Store.ProJobIDs(ctx, p.ID, mode, date, after, limit)
	if err != nil {
		return nil, internal(ctx, "jobs", err)
	}
	out := &model.ProJobPage{Items: []model.ProJob{}}
	if len(keys) > limit {
		keys = keys[:limit]
		next := EncodeCursor(keys[limit-1])
		out.NextCursor = &next
	}
	for _, k := range keys {
		j, err := s.proJob(ctx, p.ID, k.ID)
		if err != nil {
			return nil, err
		}
		out.Items = append(out.Items, *j)
	}
	return out, nil
}

// ---------------------------------------------------------------- profile reads

// ProZones lists the zones of the professional's city.
func (s *Service) ProZones(ctx context.Context, user uuid.UUID) ([]model.ProZone, error) {
	p, err := s.mine(ctx, user)
	if err != nil {
		return nil, err
	}
	z, err := s.ds.Store.CityZones(ctx, p.CityCode)
	if err != nil {
		return nil, internal(ctx, "zones", err)
	}
	return z, nil
}

// ProMySkills lists the professional's declared skills and their review.
func (s *Service) ProMySkills(ctx context.Context, user uuid.UUID) ([]model.ProSkill, error) {
	p, err := s.mine(ctx, user)
	if err != nil {
		return nil, err
	}
	out, err := s.pro.Store.ProSkills(ctx, p.ID)
	if err != nil {
		return nil, internal(ctx, "skills", err)
	}
	return out, nil
}

// ProMyArea is the saved service area.
func (s *Service) ProMyArea(ctx context.Context, user uuid.UUID) (*model.ProArea, error) {
	p, err := s.mine(ctx, user)
	if err != nil {
		return nil, err
	}
	a, err := s.ds.Store.ProArea(ctx, p.ID)
	if err != nil {
		return nil, internal(ctx, "area", err)
	}
	if a.ZoneIDs == nil {
		a.ZoneIDs = []uuid.UUID{}
	}
	return a, nil
}

// ProMyBank is the masked payout account (null before one is saved).
func (s *Service) ProMyBank(ctx context.Context, user uuid.UUID) (*model.PayoutAccount, error) {
	p, err := s.mine(ctx, user)
	if err != nil {
		return nil, err
	}
	a, err := s.pro.Store.PayoutAccount(ctx, p.ID)
	if err != nil {
		return nil, internal(ctx, "payout account", err)
	}
	return a, nil
}

// ProMyDocuments lists the professional's uploaded documents (police and
// trade certificates) and their review.
func (s *Service) ProMyDocuments(ctx context.Context, user uuid.UUID) ([]model.ProDocument, error) {
	p, err := s.mine(ctx, user)
	if err != nil {
		return nil, err
	}
	d, err := s.pro.Store.ProDocuments(ctx, p.ID)
	if err != nil {
		return nil, internal(ctx, "documents", err)
	}
	return d, nil
}
