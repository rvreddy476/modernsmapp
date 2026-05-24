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
// store layer guarantees only one partner can win per order. On accept
// we also mint the pickup + delivery OTPs (idempotent) so both the
// partner UI and the customer/restaurant UI can render them.
func (s *Service) AcceptDeliveryOffer(ctx context.Context, userID, offerID uuid.UUID) error {
	offer, err := s.store.AcceptDeliveryOfferTx(ctx, userID, offerID)
	if err != nil {
		return err
	}
	pickup, delivery, cerr := s.store.EnsureDeliveryCodes(ctx, offer.OrderID)
	if cerr != nil {
		slog.Warn("food-service: ensure codes failed", "order_id", offer.OrderID, "error", cerr)
	}
	s.emit(ctx, "food.order."+offer.OrderID.String(), "food.delivery.assigned", map[string]any{
		"offer":         offer,
		"pickup_code":   pickup,
		"delivery_code": delivery,
	})
	return nil
}

// VerifyPickupCode wraps the store call; emits the pickup-confirmed
// event so the customer screen ticks over to "out for delivery".
func (s *Service) VerifyPickupCode(ctx context.Context, ownerID, orderID uuid.UUID, code string) error {
	if err := s.store.VerifyPickupCode(ctx, ownerID, orderID, code); err != nil {
		return err
	}
	s.emit(ctx, "food.order."+orderID.String(), "food.delivery.picked_up", map[string]any{
		"order_id": orderID.String(),
	})
	return nil
}

// VerifyDeliveryCode wraps the store call; emits the delivered event
// so settlement + ratings flows kick in downstream. Also fires the
// loyalty + referral hooks — both are idempotent on (user, order)
// so a re-run of the verify (e.g. retry on flaky network) won't
// double-credit. Best-effort: a failure on either hook is logged
// but doesn't fail the verify.
func (s *Service) VerifyDeliveryCode(ctx context.Context, customerID, orderID uuid.UUID, code string) error {
	if err := s.store.VerifyDeliveryCode(ctx, customerID, orderID, code); err != nil {
		return err
	}
	s.emit(ctx, "food.order."+orderID.String(), "food.delivery.delivered", map[string]any{
		"order_id": orderID.String(),
	})
	// G4.4 — award loyalty. Need the order's final_amount; pull it
	// via GetOrder. Cheap because we just touched the row.
	if order, err := s.store.GetOrder(ctx, customerID, orderID); err == nil {
		if _, err := s.EarnPointsFromDelivery(ctx, customerID, orderID, order.Totals.FinalAmount); err != nil {
			slog.Warn("food-service: loyalty earn failed",
				"customer_id", customerID, "order_id", orderID, "error", err)
		}
	}
	// G4.6 — first-delivery referral reward. Idempotent at store layer
	// (only pending referrals get marked rewarded; subsequent calls
	// no-op). Cheap to call on every delivery.
	if err := s.RewardReferralOnFirstDelivery(ctx, customerID); err != nil {
		slog.Warn("food-service: referral reward failed",
			"customer_id", customerID, "order_id", orderID, "error", err)
	}
	return nil
}

// AttachProofURL stores a MinIO object key as proof at pickup or drop.
// `which` must be "pickup" or "delivery".
func (s *Service) AttachProofURL(ctx context.Context, userID, orderID uuid.UUID, which, url string) error {
	return s.store.AttachProofURL(ctx, userID, orderID, which, url)
}

// RejectDeliveryOffer marks the offer rejected so the worker offers it
// to someone else on the next tick.
func (s *Service) RejectDeliveryOffer(ctx context.Context, userID, offerID uuid.UUID, reason string) error {
	return s.store.RejectDeliveryOffer(ctx, userID, offerID, reason)
}
