package service

import (
	"context"
	"log/slog"
	"time"

	"github.com/atpost/food-service/internal/store/postgres"
	"github.com/google/uuid"
)

// StartDeliveryDispatchWorker runs every 10 seconds. Three passes per
// tick:
//
//  1. ExpireDeliveryOffers — flips any pending offer past its
//     expires_at to `expired`. Keeps the partner's offer inbox clean.
//  2. Group unbatched DELIVERY_ASSIGNING orders by restaurant within a
//     5-minute window into batches of up to 3 (P2 — delivery batching).
//  3. For each batch (singletons included), mint up to 5 offers to
//     nearby online partners with a 25-second TTL.
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

const (
	offerTTL        = 25 * time.Second
	offersPerOrder  = 5
	batchWindow     = 5 * time.Minute
	maxBatchSize    = 3
)

// dispatchPendingOrders pulls ready (DELIVERY_ASSIGNING) orders that
// don't yet have an offer + groups them into batches by restaurant +
// 5-min placed_at window. Each batch (or singleton order) gets one
// round of offers fanned out to nearby partners.
func (s *Service) dispatchPendingOrders(ctx context.Context) error {
	ready, err := s.store.ListUnbatchedReadyOrders(ctx, 25)
	if err != nil {
		return err
	}
	if len(ready) == 0 {
		return nil
	}
	groups := groupOrdersForBatching(ready)
	for _, g := range groups {
		if len(g.orderIDs) == 1 {
			s.dispatchOneOrder(ctx, g.orderIDs[0])
			continue
		}
		s.dispatchOneBatch(ctx, g.restaurantID, g.orderIDs)
	}
	return nil
}

type orderGroup struct {
	restaurantID uuid.UUID
	orderIDs     []uuid.UUID
}

// groupOrdersForBatching walks the orders (which the store returned
// sorted by restaurant_id then placed_at) and slots them into batches
// of up to 3 where consecutive orders share a restaurant and their
// placed_at fall within `batchWindow` of the group anchor.
//
// The anchor is the first order's placed_at; later orders join the
// batch if they're within `batchWindow` of the anchor (not the
// previous member) — this caps the worst-case wait for the earliest
// order at one window.
func groupOrdersForBatching(in []postgres.ReadyOrderForBatching) []orderGroup {
	if len(in) == 0 {
		return nil
	}
	var groups []orderGroup
	cur := orderGroup{restaurantID: in[0].RestaurantID, orderIDs: []uuid.UUID{in[0].OrderID}}
	anchor := in[0].PlacedAt
	for i := 1; i < len(in); i++ {
		o := in[i]
		sameRestaurant := o.RestaurantID == cur.restaurantID
		inWindow := o.PlacedAt.Sub(anchor) <= batchWindow
		hasRoom := len(cur.orderIDs) < maxBatchSize
		if sameRestaurant && inWindow && hasRoom {
			cur.orderIDs = append(cur.orderIDs, o.OrderID)
			continue
		}
		groups = append(groups, cur)
		cur = orderGroup{restaurantID: o.RestaurantID, orderIDs: []uuid.UUID{o.OrderID}}
		anchor = o.PlacedAt
	}
	groups = append(groups, cur)
	return groups
}

func (s *Service) dispatchOneOrder(ctx context.Context, orderID uuid.UUID) {
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
		s.emit(ctx,
			"food.delivery_partner."+pid.String()+".assignments",
			"food.delivery.offered",
			offer,
		)
	}
}

// dispatchOneBatch creates a batch row for the order group, then mints
// one offer per nearby partner that points at the batch. Whichever
// partner accepts gets all member orders flipped to ASSIGNED in one tx.
func (s *Service) dispatchOneBatch(ctx context.Context, restaurantID uuid.UUID, orderIDs []uuid.UUID) {
	batch, err := s.store.CreateBatch(ctx, restaurantID, orderIDs)
	if err != nil {
		slog.Warn("food-service: create batch failed",
			"restaurant_id", restaurantID, "size", len(orderIDs), "error", err)
		// Fall back to per-order dispatch so progress isn't gated on
		// batching working.
		for _, oid := range orderIDs {
			s.dispatchOneOrder(ctx, oid)
		}
		return
	}
	partners, err := s.store.ListEligibleDeliveryPartners(ctx, "", offersPerOrder)
	if err != nil {
		slog.Warn("food-service: eligible partners failed", "batch_id", batch.ID, "error", err)
		return
	}
	if len(partners) == 0 {
		return
	}
	expiresAt := time.Now().Add(offerTTL)
	anchor := orderIDs[0]
	for _, pid := range partners {
		offer, err := s.store.CreateDeliveryOfferForBatch(ctx, batch.ID, anchor, pid, expiresAt)
		if err != nil {
			slog.Warn("food-service: create batch offer failed",
				"batch_id", batch.ID, "partner_id", pid, "error", err)
			continue
		}
		s.emit(ctx,
			"food.delivery_partner."+pid.String()+".assignments",
			"food.delivery.offered",
			map[string]any{
				"offer":   offer,
				"batch":   batch,
				"is_batch": true,
			},
		)
	}
	slog.Info("food-service: batch dispatched",
		"batch_id", batch.ID, "size", len(orderIDs), "offered_to", len(partners))
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

// AcceptDeliveryOffer routes to the batch accept path when the offer
// belongs to a batch, otherwise the legacy single-order path. Both
// emit the per-order assignment event with pickup + delivery OTPs so
// downstream consumers don't need to know about batching.
func (s *Service) AcceptDeliveryOffer(ctx context.Context, userID, offerID uuid.UUID) error {
	// Try the batch path first — store returns a not-found / nil
	// batch_id error if this offer is single-order, which we treat as
	// signal to fall through to legacy.
	batch, partnerID, batchErr := s.store.AcceptBatchOfferTx(ctx, userID, offerID)
	if batchErr == nil && batch != nil {
		for _, m := range batch.Members {
			pickup, delivery, cerr := s.store.EnsureDeliveryCodes(ctx, m.OrderID)
			if cerr != nil {
				slog.Warn("food-service: ensure codes failed (batch)",
					"order_id", m.OrderID, "batch_id", batch.ID, "error", cerr)
			}
			s.emit(ctx, "food.order."+m.OrderID.String(), "food.delivery.assigned", map[string]any{
				"order_id":      m.OrderID.String(),
				"partner_id":    partnerID.String(),
				"pickup_code":   pickup,
				"delivery_code": delivery,
				"batch_id":      batch.ID.String(),
				"batch_sequence": m.Sequence,
				"batch_size":    len(batch.Members),
			})
		}
		return nil
	}
	// Fall through to single-order accept for non-batch offers.
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

// GetBatchForOrder returns the batch payload for an order, or nil if
// the order isn't batched. Partner + customer UI uses this to render
// "Stop 1 of 2" + the sibling order summary.
func (s *Service) GetBatchForOrder(ctx context.Context, orderID uuid.UUID) (*postgres.DeliveryBatch, error) {
	return s.store.GetBatchForOrder(ctx, orderID)
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
