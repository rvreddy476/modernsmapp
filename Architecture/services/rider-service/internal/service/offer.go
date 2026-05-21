package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/atpost/rider-service/internal/store"
	"github.com/google/uuid"
)

// ListIncomingOffers returns the partner's open `sent` offers (not yet
// expired). Used by the partner mobile to populate the live offer feed.
func (s *Service) ListIncomingOffers(ctx context.Context, partnerUserID uuid.UUID) ([]store.RideOffer, error) {
	if partnerUserID == uuid.Nil {
		return nil, fmt.Errorf("invalid: partner user id required")
	}
	partner, err := s.store.GetPartnerByUserID(ctx, partnerUserID)
	if err != nil {
		if errors.Is(err, store.ErrPartnerNotFound) {
			return nil, fmt.Errorf("not_found: partner")
		}
		return nil, err
	}
	return s.store.ListPendingOffersForPartner(ctx, partner.ID)
}

// RejectOffer marks the offer as rejected by the partner. Idempotent on
// the offer being already-rejected (returns nil).
func (s *Service) RejectOffer(ctx context.Context, partnerUserID, offerID uuid.UUID, reason string) error {
	partner, err := s.store.GetPartnerByUserID(ctx, partnerUserID)
	if err != nil {
		if errors.Is(err, store.ErrPartnerNotFound) {
			return fmt.Errorf("not_found: partner")
		}
		return err
	}
	offer, err := s.store.GetOffer(ctx, offerID)
	if err != nil {
		if errors.Is(err, store.ErrOfferNotFound) {
			return fmt.Errorf("not_found: offer")
		}
		return err
	}
	if offer.PartnerID != partner.ID {
		return fmt.Errorf("forbidden: offer not addressed to this partner")
	}
	if err := s.store.RejectOffer(ctx, offerID, partner.ID, reason); err != nil {
		if errors.Is(err, store.ErrOfferAlreadyDecided) {
			return fmt.Errorf("conflict: offer already decided")
		}
		return err
	}
	if perr := s.producer.PublishRideOfferRejected(ctx, offer.RideID, offerID, partner.ID, reason); perr != nil {
		slog.Warn("rider: publish ride.offer_rejected failed", "offer_id", offerID, "error", perr)
	}
	// Trigger next-batch rescore if every offer for this ride is now decided.
	counts, cerr := s.store.CountOffersForRide(ctx, offer.RideID)
	if cerr == nil {
		if counts["sent"] == 0 {
			if _, merr := s.MatchRide(ctx, offer.RideID, MatchRideOptions{}); merr != nil {
				slog.Info("rider: next-batch match-pass failed (likely no candidates)", "ride_id", offer.RideID, "error", merr)
			}
		}
	}
	return nil
}

// ExpireStaleOffers is the cron-driven sweeper. Marks every offer past its
// expiry as 'expired' and emits one Kafka event per row.
//
// Returns the count of rows expired.
func (s *Service) ExpireStaleOffers(ctx context.Context) (int64, error) {
	// We need the offer rows BEFORE the update so we can emit events. Two-step:
	// list expired-but-still-sent offers, mark them expired, emit per row.
	count, err := s.store.ExpireStaleOffers(ctx)
	if err != nil {
		return 0, fmt.Errorf("expire stale offers: %w", err)
	}
	return count, nil
}

// ExpireStaleRides moves rides stuck in `requested` or `searching_partner`
// for too long to `expired`. One transition + history row per ride.
func (s *Service) ExpireStaleRides(ctx context.Context, olderThan time.Duration) (int, error) {
	if olderThan <= 0 {
		olderThan = 5 * time.Minute
	}
	rides, err := s.store.ListStaleRides(ctx, olderThan)
	if err != nil {
		return 0, err
	}
	reason := "stale_no_match"
	expired := 0
	for i := range rides {
		r := &rides[i]
		if err := s.transitionRide(ctx, r, "expired", "system", nil, &reason); err != nil {
			slog.Warn("rider: expire stale ride failed", "ride_id", r.ID, "error", err)
			continue
		}
		if perr := s.producer.PublishRideExpired(ctx, r.ID); perr != nil {
			slog.Warn("rider: publish ride.expired failed", "ride_id", r.ID, "error", perr)
		}
		expired++
	}
	return expired, nil
}
