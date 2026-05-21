package service

import (
	"context"
	"log/slog"
	"time"

	"github.com/google/uuid"
)

// StartDeliveryDispatchWorker runs every 10 seconds. Two passes per
// tick:
//
//  1. ExpireDeliveryOffers — flips any pending offer past its
//     expires_at to `expired`. Keeps the partner's offer inbox clean.
//  2. For each DELIVERY_ASSIGNING order without an accepted
//     assignment, mint up to 5 offers to nearby online partners with
//     a 25-second TTL.
//
// Partners get the push via the food.delivery_partner.{id}.assignments
// realtime topic plus an outbox food.delivery.offered event for FCM.
func (s *Service) StartDeliveryDispatchWorker(ctx context.Context) {
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()
	slog.Info("food-service: delivery dispatch worker started")
	for {
		select {
		case <-ctx.Done():
			slog.Info("food-service: delivery dispatch worker stopped")
			return
		case <-ticker.C:
			if n, err := s.store.ExpireDeliveryOffers(ctx); err != nil {
				slog.Warn("food-service: expire offers failed", "error", err)
			} else if n > 0 {
				slog.Info("food-service: expired stale delivery offers", "count", n)
			}
			if err := s.dispatchPendingOrders(ctx); err != nil {
				slog.Warn("food-service: dispatch pending failed", "error", err)
			}
		}
	}
}

const offerTTL = 25 * time.Second
const offersPerOrder = 5

func (s *Service) dispatchPendingOrders(ctx context.Context) error {
	ids, err := s.store.ListUnassignedReadyOrders(ctx, 25)
	if err != nil {
		return err
	}
	for _, orderID := range ids {
		s.dispatchOneOrder(ctx, orderID)
	}
	return nil
}

func (s *Service) dispatchOneOrder(ctx context.Context, orderID uuid.UUID) {
	// We don't have the restaurant city directly; for v1 we treat city
	// as a soft filter and let `''` match all. A follow-up will plumb
	// the order → restaurant city through.
	partners, err := s.store.ListEligibleDeliveryPartners(ctx, "", offersPerOrder)
	if err != nil {
		slog.Warn("food-service: eligible partners failed", "order_id", orderID, "error", err)
		return
	}
	if len(partners) == 0 {
		return
	}
	expiresAt := time.Now().Add(offerTTL)
	for _, pid := range partners {
		offer, err := s.store.CreateDeliveryOffer(ctx, orderID, pid, expiresAt)
		if err != nil {
			slog.Warn("food-service: create offer failed",
				"order_id", orderID, "partner_id", pid, "error", err)
			continue
		}
		// One offer per (order, partner) thanks to the UNIQUE
		// constraint; idempotent on retry.
		s.emit(ctx,
			"food.delivery_partner."+pid.String()+".assignments",
			"food.delivery.offered",
			offer,
		)
	}
}

// ListMyPendingDeliveryOffers exposes the inbox view for the partner
// mobile app. Returns offers still pending and not yet expired.
func (s *Service) ListMyPendingDeliveryOffers(ctx context.Context, userID uuid.UUID) ([]any, error) {
	offers, err := s.store.ListMyPendingDeliveryOffers(ctx, userID)
	if err != nil {
		return nil, err
	}
	out := make([]any, 0, len(offers))
	for _, o := range offers {
		out = append(out, o)
	}
	return out, nil
}

// AcceptDeliveryOffer is the partner-facing accept path. Atomic — the
// store layer guarantees only one partner can win per order.
func (s *Service) AcceptDeliveryOffer(ctx context.Context, userID, offerID uuid.UUID) error {
	offer, err := s.store.AcceptDeliveryOfferTx(ctx, userID, offerID)
	if err != nil {
		return err
	}
	s.emit(ctx, "food.order."+offer.OrderID.String(), "food.delivery.assigned", offer)
	return nil
}

// RejectDeliveryOffer marks the offer rejected so the worker offers it
// to someone else on the next tick.
func (s *Service) RejectDeliveryOffer(ctx context.Context, userID, offerID uuid.UUID, reason string) error {
	return s.store.RejectDeliveryOffer(ctx, userID, offerID, reason)
}
