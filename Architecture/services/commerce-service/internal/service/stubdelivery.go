package service

// The stub courier's delivery timer (dev only).
//
// The stub courier books instantly and never sends a webhook, so on a dev
// stack an order that reached `shipped` stayed there forever: nothing
// downstream of shipping — the delivered notice, reviews, the seller's
// delivered tab — could be exercised at all. This worker stands in for the
// courier's webhooks: after COURIER_STUB_AUTO_DELIVER_AFTER a shipped order
// goes out for delivery, and after the same interval again it is delivered,
// through exactly the path a real webhook takes (AdvanceDelivery: the
// matrix, the history row, the lines following in the same transaction, the
// shipment's status and event, and commerce.order.delivered with user_id).
//
// It REFUSES to run under any other courier. A real courier's webhooks are
// the authority on where a parcel is, and a timer that contradicted them
// would mark real parcels delivered on a schedule.

import (
	"context"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"
)

// StubCourierName is what the stub courier adapter answers to Name().
const StubCourierName = "stub"

// StubAutoDeliveryAllowed reports whether the timer may run: the configured
// courier must be the stub. Exported so cmd/server can log the refusal by
// name and so the test can pin the guard.
func (s *Service) StubAutoDeliveryAllowed() bool {
	return s.courier != nil && strings.EqualFold(s.courier.Name(), StubCourierName)
}

// RunStubAutoDelivery advances shipped orders on the stub courier until the
// context ends. `after` is the dwell time in each state; `tick` is how often
// the sweep runs (a quarter of `after`, capped between 5 s and 1 min, when
// zero). It returns immediately, without touching anything, when the
// configured courier is not the stub.
func (s *Service) RunStubAutoDelivery(ctx context.Context, after, tick time.Duration) {
	if after <= 0 {
		return
	}
	if !s.StubAutoDeliveryAllowed() {
		name := "<none>"
		if s.courier != nil {
			name = s.courier.Name()
		}
		slog.Error("commerce: COURIER_STUB_AUTO_DELIVER_AFTER is set but the courier is not the stub; "+
			"the delivery timer will NOT run — a real courier's webhooks decide delivery",
			"courier", name, "after", after)
		return
	}
	if tick <= 0 {
		tick = after / 4
		if tick < 5*time.Second {
			tick = 5 * time.Second
		}
		if tick > time.Minute {
			tick = time.Minute
		}
	}
	slog.Warn("commerce: stub courier delivery timer started — orders are delivered on a schedule, "+
		"which only a dev stack may do", "after", after, "tick", tick)
	t := time.NewTicker(tick)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			s.SweepStubDelivery(ctx, after)
		}
	}
}

// SweepStubDelivery runs one pass: shipped -> out_for_delivery, then
// out_for_delivery -> delivered, each after `after` in the previous state.
// Exported for the integration proof; guarded exactly as the loop is.
func (s *Service) SweepStubDelivery(ctx context.Context, after time.Duration) (advanced int) {
	if !s.StubAutoDeliveryAllowed() {
		return 0
	}
	for _, step := range []struct{ from, to string }{
		{"shipped", "out_for_delivery"},
		{"out_for_delivery", "delivered"},
	} {
		ids, err := s.store.OrdersDueForStubDelivery(ctx, step.from, after, 100)
		if err != nil {
			slog.Warn("commerce: stub delivery sweep failed", "from", step.from, "error", err)
			continue
		}
		for _, id := range ids {
			if s.advanceStubDelivery(ctx, id, step.to) {
				advanced++
			}
		}
	}
	return advanced
}

func (s *Service) advanceStubDelivery(ctx context.Context, orderID uuid.UUID, to string) bool {
	now := time.Now()
	t, err := s.store.AdvanceDelivery(ctx, orderID, to, "stub courier timer")
	if err != nil {
		slog.Warn("commerce: stub delivery could not advance the order", "order_id", orderID, "to", to, "error", err)
		return false
	}
	if !t.Applied {
		return false
	}
	// The shipment rows follow, as the webhook path writes them.
	shipments, err := s.store.ListShipmentsByOrder(ctx, orderID)
	if err != nil {
		slog.Warn("commerce: stub delivery could not list shipments", "order_id", orderID, "error", err)
	}
	for _, sh := range shipments {
		if err := s.store.AppendShipmentEvent(ctx, sh.ID, to, "", "stub courier timer", now); err != nil {
			slog.Warn("commerce: stub delivery event failed", "shipment_id", sh.ID, "error", err)
		}
		if err := s.store.UpdateShipmentStatus(ctx, sh.ID, to, now); err != nil {
			slog.Warn("commerce: stub delivery shipment status failed", "shipment_id", sh.ID, "error", err)
		}
	}
	if to == "delivered" {
		order, _ := s.store.GetOrderByID(ctx, orderID)
		var shipmentID uuid.UUID
		if len(shipments) > 0 {
			shipmentID = shipments[0].ID
		}
		s.publishOrderDelivered(ctx, orderID, order, shipmentID, now)
	}
	slog.Info("commerce: stub courier advanced an order", "order_id", orderID, "to", to)
	return true
}
