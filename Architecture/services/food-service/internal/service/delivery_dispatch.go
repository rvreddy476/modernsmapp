package service

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/atpost/food-service/internal/foodevents"
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
// Partners get the push via the food.delivery_partner.{user_id}.assignments
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
		if g.restaurantLat == nil || g.restaurantLng == nil {
			// Fail closed: without a pickup point there is no radius to
			// search, and offering to "everyone online" is what this replaced.
			slog.Warn("food-service: restaurant has no location; orders not dispatched",
				"restaurant_id", g.restaurantID, "orders", len(g.orderIDs))
			continue
		}
		if len(g.orderIDs) == 1 {
			s.dispatchOneOrder(ctx, g, g.orderIDs[0])
			continue
		}
		s.dispatchOneBatch(ctx, g)
	}
	return nil
}

type orderGroup struct {
	restaurantID  uuid.UUID
	restaurantLat *float64
	restaurantLng *float64
	orderIDs      []uuid.UUID
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
	cur := newOrderGroup(in[0])
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
		cur = newOrderGroup(o)
		anchor = o.PlacedAt
	}
	groups = append(groups, cur)
	return groups
}

func newOrderGroup(o postgres.ReadyOrderForBatching) orderGroup {
	return orderGroup{
		restaurantID:  o.RestaurantID,
		restaurantLat: o.RestaurantLat,
		restaurantLng: o.RestaurantLng,
		orderIDs:      []uuid.UUID{o.OrderID},
	}
}

// dispatchCandidates returns the nearest fresh, ACTIVE, online partners
// around the group's restaurant.
func (s *Service) dispatchCandidates(ctx context.Context, g orderGroup) ([]postgres.DispatchCandidate, error) {
	return s.store.ListDispatchCandidates(ctx, postgres.DispatchQuery{
		Lat:            *g.restaurantLat,
		Lng:            *g.restaurantLng,
		RadiusKM:       s.dispatchRadiusKM,
		MaxLocationAge: s.dispatchLocationMaxAge,
		Limit:          offersPerOrder,
	})
}

func (s *Service) dispatchOneOrder(ctx context.Context, g orderGroup, orderID uuid.UUID) {
	candidates, err := s.dispatchCandidates(ctx, g)
	if err != nil {
		slog.Warn("food-service: dispatch candidates failed", "order_id", orderID, "error", err)
		return
	}
	expiresAt := time.Now().Add(offerTTL)
	for _, c := range candidates {
		distance := c.DistanceKM
		offer, err := s.store.CreateDeliveryOffer(ctx, orderID, c.PartnerID, expiresAt, &distance)
		if err != nil {
			slog.Warn("food-service: create offer failed",
				"order_id", orderID, "partner_id", c.PartnerID, "error", err)
			continue
		}
		s.emit(ctx, deliveryPartnerTopic(c.UserID), foodevents.DeliveryOffered, newDeliveryOfferedEvent(offer, c.UserID))
	}
}

// deliveryOfferedEvent is food.delivery.offered for a single order: the offer
// row plus the rider's user id. The offer only names the partner ROW
// (delivery_partner_id); notification-service addresses the push by
// delivery_partner_user_id and never reads this service's database.
type deliveryOfferedEvent struct {
	*postgres.DeliveryOffer
	DeliveryPartnerUserID string `json:"delivery_partner_user_id"`
}

func newDeliveryOfferedEvent(offer *postgres.DeliveryOffer, riderUserID uuid.UUID) deliveryOfferedEvent {
	return deliveryOfferedEvent{DeliveryOffer: offer, DeliveryPartnerUserID: riderUserID.String()}
}

// newBatchDeliveryOfferedEvent is food.delivery.offered for a batch. The
// rider's user id is top level, where notification-service reads it.
func newBatchDeliveryOfferedEvent(offer *postgres.DeliveryOffer, batch *postgres.DeliveryBatch, riderUserID uuid.UUID) map[string]any {
	return map[string]any{
		"offer":                    offer,
		"batch":                    batch,
		"is_batch":                 true,
		"delivery_partner_user_id": riderUserID.String(),
	}
}

// dispatchOneBatch creates a batch row for the order group, then mints
// one offer per nearby partner that points at the batch. Whichever
// partner accepts gets all member orders flipped to ASSIGNED in one tx.
func (s *Service) dispatchOneBatch(ctx context.Context, g orderGroup) {
	batch, err := s.store.CreateBatch(ctx, g.restaurantID, g.orderIDs)
	if err != nil {
		slog.Warn("food-service: create batch failed",
			"restaurant_id", g.restaurantID, "size", len(g.orderIDs), "error", err)
		// Fall back to per-order dispatch so progress isn't gated on
		// batching working.
		for _, oid := range g.orderIDs {
			s.dispatchOneOrder(ctx, g, oid)
		}
		return
	}
	candidates, err := s.dispatchCandidates(ctx, g)
	if err != nil {
		slog.Warn("food-service: dispatch candidates failed", "batch_id", batch.ID, "error", err)
		return
	}
	expiresAt := time.Now().Add(offerTTL)
	anchor := g.orderIDs[0]
	for _, c := range candidates {
		distance := c.DistanceKM
		offer, err := s.store.CreateDeliveryOfferForBatch(ctx, batch.ID, anchor, c.PartnerID, expiresAt, &distance)
		if err != nil {
			slog.Warn("food-service: create batch offer failed",
				"batch_id", batch.ID, "partner_id", c.PartnerID, "error", err)
			continue
		}
		s.emit(ctx, deliveryPartnerTopic(c.UserID), foodevents.DeliveryOffered,
			newBatchDeliveryOfferedEvent(offer, batch, c.UserID))
	}
	slog.Info("food-service: batch dispatched",
		"batch_id", batch.ID, "size", len(g.orderIDs), "offered_to", len(candidates))
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
// belongs to a batch, otherwise the legacy single-order path. Both mint the
// pickup + delivery OTPs. The accept transaction wrote one
// food.delivery.assigned per order to the outbox (B5c); this publishes the
// matching realtime frame with the batch detail. Neither ever carries the
// codes (B5a): the rider reads pickup_code from their assignment once
// accepted, the customer reads delivery_code from the order detail once the
// food is picked up.
func (s *Service) AcceptDeliveryOffer(ctx context.Context, userID, offerID uuid.UUID) error {
	// Try the batch path first — store returns a not-found / nil
	// batch_id error if this offer is single-order, which we treat as
	// signal to fall through to legacy.
	batch, partnerID, batchErr := s.store.AcceptBatchOfferTx(ctx, userID, offerID)
	if batchErr == nil && batch != nil {
		for _, m := range batch.Members {
			if _, _, cerr := s.store.EnsureDeliveryCodes(ctx, m.OrderID); cerr != nil {
				slog.Warn("food-service: ensure codes failed (batch)",
					"order_id", m.OrderID, "batch_id", batch.ID, "error", cerr)
			}
			s.publishRealtime(ctx, orderTopic(m.OrderID), foodevents.DeliveryAssigned, map[string]any{
				"order_id":       m.OrderID.String(),
				"partner_id":     partnerID.String(),
				"batch_id":       batch.ID.String(),
				"batch_sequence": m.Sequence,
				"batch_size":     len(batch.Members),
			})
		}
		return nil
	}
	// Only a single-order offer falls through. A real batch failure (member
	// cancelled, offer superseded) is returned rather than retried as a
	// single-order accept of the anchor.
	if batchErr != nil && !errors.Is(batchErr, postgres.ErrNotBatchOffer) {
		return batchErr
	}
	offer, err := s.store.AcceptDeliveryOfferTx(ctx, userID, offerID)
	if err != nil {
		return err
	}
	if _, _, cerr := s.store.EnsureDeliveryCodes(ctx, offer.OrderID); cerr != nil {
		slog.Warn("food-service: ensure codes failed", "order_id", offer.OrderID, "error", cerr)
	}
	s.publishRealtime(ctx, orderTopic(offer.OrderID), foodevents.DeliveryAssigned, map[string]any{
		"offer": offer,
	})
	return nil
}

// GetBatchForOrder returns the batch payload for an order with no access
// check. Admin only; the handler decides who reaches it.
func (s *Service) GetBatchForOrder(ctx context.Context, orderID uuid.UUID) (*postgres.DeliveryBatch, error) {
	return s.store.GetBatchForOrder(ctx, orderID)
}

// GetBatchForOrderForPartner returns the batch only to the delivery partner
// assigned to the order ("Stop 1 of 2"); anyone else gets pgx.ErrNoRows.
func (s *Service) GetBatchForOrderForPartner(ctx context.Context, userID, orderID uuid.UUID) (*postgres.DeliveryBatch, error) {
	return s.store.GetBatchForOrderForPartner(ctx, userID, orderID)
}

// VerifyPickupCode wraps the store call; publishes the pickup-confirmed frame
// so the customer screen ticks over to "out for delivery". The verify
// transaction wrote food.delivery.picked_up to the outbox.
func (s *Service) VerifyPickupCode(ctx context.Context, ownerID, orderID uuid.UUID, code string) error {
	if err := s.store.VerifyPickupCode(ctx, ownerID, orderID, code); err != nil {
		return err
	}
	s.publishRealtime(ctx, orderTopic(orderID), foodevents.DeliveryPickedUp, map[string]any{
		"order_id": orderID.String(),
	})
	return nil
}

// RiderVerifyDeliveryCode is the handover at the door: the rider holding
// assignmentID enters the code the customer shows. The verify transaction
// wrote food.delivery.delivered to the outbox; this publishes the realtime
// frame and fires the loyalty + referral hooks for the order's CUSTOMER —
// both are idempotent on (user, order), so a retried verify won't
// double-credit. Best-effort: a failure on either hook is logged but doesn't
// fail the verify.
func (s *Service) RiderVerifyDeliveryCode(ctx context.Context, riderUserID, assignmentID uuid.UUID, code string) (*postgres.DeliveryVerification, error) {
	v, err := s.store.RiderVerifyDeliveryCode(ctx, riderUserID, assignmentID, code)
	if err != nil {
		return nil, err
	}
	customerID, orderID := v.CustomerID, v.OrderID
	s.publishRealtime(ctx, orderTopic(orderID), foodevents.DeliveryDelivered, map[string]any{
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
	return v, nil
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
