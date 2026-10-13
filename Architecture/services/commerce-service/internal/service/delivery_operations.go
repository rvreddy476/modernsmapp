package service

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/atpost/commerce-service/internal/store/postgres"
	"github.com/atpost/shared/partnerwork"
	"github.com/google/uuid"
)

type deliveryWorkCoordinator interface {
	Read(context.Context, uuid.UUID) (partnerwork.State, error)
	SetDuty(context.Context, uuid.UUID, *partnerwork.Category, int64) error
	Offer(context.Context, uuid.UUID, partnerwork.Category, uuid.UUID, time.Time, time.Time) (partnerwork.Reservation, error)
	Decide(context.Context, uuid.UUID, partnerwork.Category, uuid.UUID, string) error
}

func (s *Service) WithLocalDelivery(enabled bool, coordinator *partnerwork.Client) *Service {
	s.localDeliveryEnabled = enabled
	if coordinator != nil {
		s.deliveryWork = coordinator
	}
	return s
}
func (s *Service) DeliveryAvailability(ctx context.Context, user uuid.UUID) (postgres.DeliveryAvailability, error) {
	a, err := s.store.DeliveryAvailability(ctx, user)
	if err != nil {
		return a, err
	}
	if s.deliveryWork == nil || !s.localDeliveryEnabled {
		a.Online = false
		return a, nil
	}
	state, err := s.deliveryWork.Read(ctx, user)
	if err != nil {
		return a, err
	}
	a.Online = a.Online && state.Session.Category != nil && *state.Session.Category == partnerwork.CommerceDelivery
	a.ExpectedWorkRevision = state.Session.Revision
	return a, nil
}
func (s *Service) SaveDeliveryAvailability(ctx context.Context, user uuid.UUID, a postgres.DeliveryAvailability) error {
	if a.Online {
		if !s.localDeliveryEnabled || s.deliveryWork == nil {
			return postgres.ErrDeliveryWorkConflict
		}
		p, err := s.store.DeliveryProfile(ctx, user)
		if err != nil {
			return err
		}
		if p.Status != "approved" {
			return postgres.ErrDeliveryIncomplete
		}
	}
	if s.deliveryWork == nil {
		return postgres.ErrDeliveryWorkConflict
	}
	if !a.Online {
		state, err := s.deliveryWork.Read(ctx, user)
		if err != nil {
			return err
		}
		if state.Session.Category == nil || *state.Session.Category != partnerwork.CommerceDelivery {
			return s.store.SaveDeliveryAvailability(ctx, user, a)
		}
	}
	var category *partnerwork.Category
	if a.Online {
		c := partnerwork.CommerceDelivery
		category = &c
	}
	if err := s.deliveryWork.SetDuty(ctx, user, category, a.ExpectedWorkRevision); err != nil {
		return err
	}
	return s.store.SaveDeliveryAvailability(ctx, user, a)
}
func (s *Service) SaveDeliveryLocation(ctx context.Context, user, id uuid.UUID, kind string, l postgres.DeliveryLocation) error {
	return s.store.SaveDeliveryLocation(ctx, user, id, kind, l)
}
func (s *Service) SellerDeliveryLocation(ctx context.Context, user, id uuid.UUID) (*postgres.DeliveryLocation, error) {
	return s.store.SellerDeliveryLocation(ctx, user, id)
}
func (s *Service) SellerDeliveryReady(ctx context.Context, user, id uuid.UUID) (string, error) {
	if !s.localDeliveryEnabled {
		return "", postgres.ErrDeliveryWorkConflict
	}
	return s.store.SellerDeliveryReady(ctx, user, id)
}
func (s *Service) DeliveryCustomerCode(ctx context.Context, user, id uuid.UUID) (string, error) {
	return s.store.DeliveryCustomerCode(ctx, user, id)
}
func (s *Service) DeliveryOffers(ctx context.Context, user uuid.UUID) ([]postgres.DeliveryOffer, error) {
	if !s.localDeliveryEnabled || s.deliveryWork == nil {
		return []postgres.DeliveryOffer{}, nil
	}
	state, err := s.deliveryWork.Read(ctx, user)
	if err != nil {
		return nil, err
	}
	if state.Session.Category == nil || *state.Session.Category != partnerwork.CommerceDelivery {
		return []postgres.DeliveryOffer{}, nil
	}
	offers, err := s.store.DeliveryOffers(ctx, user)
	if err != nil {
		return nil, err
	}
	result := []postgres.DeliveryOffer{}
	for _, o := range offers {
		for _, r := range state.Reservations {
			if r.ID == o.ReservationID && r.State == "offered" && r.JobID == o.JobID {
				result = append(result, o)
				break
			}
		}
	}
	return result, nil
}
func (s *Service) DeliveryJobs(ctx context.Context, user *uuid.UUID) ([]postgres.DeliveryJob, error) {
	return s.store.DeliveryJobs(ctx, user)
}
func (s *Service) DeliveryJob(ctx context.Context, user *uuid.UUID, id uuid.UUID) (*postgres.DeliveryJob, error) {
	j, err := s.store.DeliveryJob(ctx, id)
	if err != nil {
		return nil, err
	}
	if user != nil && (j.PartnerUserID == nil || *j.PartnerUserID != *user) {
		return nil, postgres.ErrDeliveryWorkConflict
	}
	return j, nil
}
func (s *Service) DecideDeliveryOffer(ctx context.Context, user, id uuid.UUID, accept bool) (*postgres.DeliveryJob, error) {
	return decideDeliveryOffer(ctx, s.store, s.deliveryWork, user, id, accept)
}

type deliveryDecisionStore interface {
	DeliveryOffer(context.Context, uuid.UUID, uuid.UUID) (postgres.DeliveryOffer, error)
	DecideDeliveryOffer(context.Context, uuid.UUID, uuid.UUID, bool) (*postgres.DeliveryJob, error)
}

func decideDeliveryOffer(ctx context.Context, store deliveryDecisionStore, work deliveryWorkCoordinator, user, id uuid.UUID, accept bool) (*postgres.DeliveryJob, error) {
	if work == nil {
		return nil, postgres.ErrDeliveryWorkConflict
	}
	o, err := store.DeliveryOffer(ctx, user, id)
	if err != nil {
		return nil, err
	}
	if !accept {
		// Persist a validated terminal decision before releasing its lease. The
		// durable release worker retries; stale decline cannot release accepted work.
		return store.DecideDeliveryOffer(ctx, user, id, false)
	}
	if err = work.Decide(ctx, user, partnerwork.CommerceDelivery, o.ReservationID, "accept"); err != nil {
		return nil, err
	}
	if accept {
		if err = work.Decide(ctx, user, partnerwork.CommerceDelivery, o.ReservationID, "start"); err != nil {
			return nil, err
		}
	}
	// Never compensate a generic conflict: another concurrent accept may already
	// own this same lease. Expiry/cancellation workers release proven terminal rows.
	return store.DecideDeliveryOffer(ctx, user, id, true)
}
func (s *Service) TransitionDeliveryJob(ctx context.Context, user, id uuid.UUID, action, code, reason string, revision int64) (*postgres.DeliveryJob, error) {
	return s.store.TransitionDeliveryJob(ctx, user, id, action, code, reason, revision)
}
func (s *Service) DeliverySupport(ctx context.Context, user, id uuid.UUID, reason string) error {
	return s.store.DeliverySupport(ctx, user, id, reason)
}
func (s *Service) DeliveryEarnings(ctx context.Context, user uuid.UUID) ([]postgres.DeliveryEarning, error) {
	return s.store.DeliveryEarnings(ctx, user)
}
func (s *Service) DeliveryJobContact(ctx context.Context, user, id uuid.UUID) (map[string]string, error) {
	j, err := s.DeliveryJob(ctx, &user, id)
	if err != nil {
		return nil, err
	}
	if j.Status != "assigned" && j.Status != "picked_up" {
		return nil, postgres.ErrDeliveryWorkConflict
	}
	order, err := s.store.GetOrderByID(ctx, j.OrderID)
	if err != nil {
		return nil, err
	}
	seller, err := s.store.GetSellerByID(ctx, j.SellerID)
	if err != nil {
		return nil, err
	}
	out := map[string]string{"pickup_name": seller.StoreName, "pickup_phone": "", "customer_phone": "", "customer_name": ""}
	if seller.Phone != nil {
		out["pickup_phone"] = *seller.Phone
	}
	if order.DeliveryAddressID != nil {
		row, err := s.store.GetAddressRow(ctx, *order.DeliveryAddressID)
		if err != nil {
			return nil, err
		}
		address, err := s.openAddressRow(ctx, row)
		if err != nil {
			return nil, err
		}
		out["customer_name"] = address.ContactName
		out["customer_phone"] = address.Phone
	}
	return out, nil
}
func (s *Service) RunDeliveryDispatcher(ctx context.Context) {
	if !s.localDeliveryEnabled || s.deliveryWork == nil {
		return
	}
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := s.DispatchDeliveries(ctx); err != nil {
				slog.Warn("local delivery dispatch deferred", "error", err)
			}
		}
	}
}
func (s *Service) DispatchDeliveries(ctx context.Context) error {
	if !s.localDeliveryEnabled || s.deliveryWork == nil {
		return nil
	}
	// Release is durable: terminal domain commits survive coordinator downtime.
	releases, err := s.store.DeliveryPendingReleases(ctx)
	if err != nil {
		return err
	}
	for _, j := range releases {
		if j.PartnerUserID != nil && j.ReservationID != nil {
			if err = s.deliveryWork.Decide(ctx, *j.PartnerUserID, partnerwork.CommerceDelivery, *j.ReservationID, "release"); err != nil {
				continue
			}
		}
		if err = s.store.DeliveryReleaseDone(ctx, j.ID); err != nil {
			return err
		}
	}
	if err = s.store.ExpireDeliveryOffers(ctx); err != nil {
		return err
	}
	forRelease, err := s.store.DeliveryOfferReleases(ctx)
	if err != nil {
		return err
	}
	for _, o := range forRelease {
		if err = s.deliveryWork.Decide(ctx, o.PartnerUserID, partnerwork.CommerceDelivery, o.ReservationID, "release"); err == nil {
			if err = s.store.DeliveryOfferReleaseDone(ctx, o.ID); err != nil {
				return err
			}
		}
	}
	jobs, err := s.store.UnassignedDeliveryJobs(ctx)
	if err != nil {
		return err
	}
	for _, j := range jobs {
		if err = s.store.PostponeDeliveryDispatch(ctx, j.ID); err != nil {
			return err
		}
		users, e := s.store.DeliveryCandidates(ctx, j)
		if e != nil {
			return e
		}
		for _, user := range users {
			now := time.Now().UTC()
			reservation, e := s.deliveryWork.Offer(ctx, user, partnerwork.CommerceDelivery, j.ID, now, now.Add(2*time.Hour))
			if e != nil {
				if errors.Is(e, partnerwork.ErrOffDuty) || errors.Is(e, partnerwork.ErrBusy) || errors.Is(e, partnerwork.ErrConflict) || errors.Is(e, partnerwork.ErrExpired) {
					continue
				}
				return e
			}
			if reservation.State != "offered" || !reservation.OfferExpiresAt.After(time.Now()) {
				continue
			}
			_, e = s.store.CreateDeliveryOffer(ctx, j.ID, user, reservation.ID, reservation.OfferExpiresAt)
			if e == nil {
				break
			}
			// A second dispatcher can receive the same idempotent reservation. Do
			// not release another worker's committed offer after a local conflict.
			// Unpersisted reservations expire at their original short deadline.
		}
	}
	return nil
}
func (s *Service) AdminDeliveryAction(ctx context.Context, actor, id uuid.UUID, action, reason string, revision int64) error {
	return s.store.AdminDeliveryAction(ctx, actor, id, action, reason, revision)
}
func (s *Service) DeliverySupportTickets(ctx context.Context) ([]postgres.DeliverySupportTicket, error) {
	return s.store.DeliverySupportTickets(ctx)
}
func (s *Service) UpdateDeliveryLocation(ctx context.Context, user uuid.UUID, l postgres.DeliveryLocation) error {
	return s.store.UpdateDeliveryLocation(ctx, user, l)
}
func (s *Service) OrderDeliveryJobs(ctx context.Context, user, id uuid.UUID) ([]postgres.DeliveryJob, error) {
	return s.store.OrderDeliveryJobs(ctx, user, id)
}

// Existing seller Ship action means ready for pickup for local parcels only.
func (s *Service) ReadySellerLocalShipments(ctx context.Context, user, order uuid.UUID) (map[string]string, error) {
	out := map[string]string{}
	if !s.localDeliveryEnabled {
		return out, nil
	}
	jobs, err := s.store.OrderDeliveryJobs(ctx, user, order)
	if err != nil {
		return nil, err
	}
	for _, j := range jobs {
		seller, e := s.store.GetSellerByID(ctx, j.SellerID)
		if e != nil {
			return nil, e
		}
		if seller.UserID != user {
			continue
		}
		if j.Status == "awaiting_ready" || j.Status == "unassigned" {
			code, e := s.store.SellerDeliveryReady(ctx, user, j.ID)
			if e != nil {
				return nil, e
			}
			out[j.ID.String()] = code
		}
	}
	return out, nil
}
func (s *Service) SellerLocalPickupCodes(ctx context.Context, user, order uuid.UUID) (map[string]string, error) {
	return s.store.SellerLocalPickupCodes(ctx, user, order)
}
func (s *Service) ResolveDeliverySupport(ctx context.Context, actor, id uuid.UUID, note string) error {
	return s.store.ResolveDeliverySupport(ctx, actor, id, note)
}
