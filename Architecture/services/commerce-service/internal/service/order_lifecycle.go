// Order lifecycle after checkout: who the caller is on an order, applying
// verified payment and refund events, marking a payment failed and
// cancelling. Moved out of service.go unchanged.

package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/atpost/commerce-service/internal/store/postgres"
	"github.com/atpost/shared/events"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// OrderActorRole describes how the supplied actor relates to an order —
// used by the shipment / invoice / return handlers to gate writes to the
// seller of at least one item and to gate reads to the customer or a
// seller. Returns ErrOrderNotFound when the order does not exist.
type OrderActorRole struct {
	IsCustomer bool
	IsSeller   bool
}

// OrderActor inspects the order + its items and reports whether the
// supplied actor is the customer and/or a seller of any item.
//
// ─── TWO KINDS OF ID ────────────────────────────────────────────────────
//
// `actorID` is a USER id — it arrives as X-User-Id. `order_items.seller_id`
// is a SELLER id, the primary key of a row in `sellers`. They are drawn
// from different tables and never collide, so the comparison
//
//	if it.SellerID == actorID
//
// could not be true for any caller, and IsSeller was permanently false.
// Every write it gates — POST /orders/:id/shipment, POST /orders/:id/invoice
// — answered 403 to the seller who actually owned the line, and the reads
// (GET …/shipment, …/shipments, …/invoice) answered 403 too because the
// seller is not the customer either. A seller could not act on their own
// order at all, and the 403 said "actor is not a seller on this order",
// which is exactly what it looked like from the outside.
//
// The caller's seller profile is resolved first and the comparison is
// seller-id to seller-id. A caller with no seller profile is simply not a
// seller — that is not an error, so a plain buyer still gets the customer
// half of the role rather than a 500.
func (s *Service) OrderActor(ctx context.Context, orderID, actorID uuid.UUID) (OrderActorRole, error) {
	order, err := s.store.GetOrderByID(ctx, orderID)
	if err != nil {
		return OrderActorRole{}, err
	}
	if order == nil {
		return OrderActorRole{}, ErrOrderNotFound
	}
	role := OrderActorRole{IsCustomer: order.CustomerUserID == actorID}

	sellerID, err := s.actorSellerID(ctx, actorID)
	if err != nil {
		return OrderActorRole{}, err
	}
	if sellerID == uuid.Nil {
		return role, nil
	}
	items, err := s.store.GetOrderItems(ctx, orderID)
	if err != nil {
		return OrderActorRole{}, err
	}
	for _, it := range items {
		if it.SellerID == sellerID {
			role.IsSeller = true
			break
		}
	}
	return role, nil
}

// actorSellerID maps a user id to their seller id, or uuid.Nil when the
// caller has no seller profile. "No seller row" is a fact about the caller,
// not a failure, so it is not an error; anything else is.
func (s *Service) actorSellerID(ctx context.Context, actorID uuid.UUID) (uuid.UUID, error) {
	seller, err := s.store.GetSellerByUserID(ctx, actorID)
	if err != nil {
		if errors.Is(err, postgres.ErrNoSellerRow) || errors.Is(err, pgx.ErrNoRows) {
			return uuid.Nil, nil
		}
		return uuid.Nil, err
	}
	if seller == nil {
		return uuid.Nil, nil
	}
	return seller.ID, nil
}

// ApplyVerifiedPaymentEvent is the system entry point the Kafka payments
// consumer uses when payments-service publishes payment.succeeded. The
// webhook has already verified the Razorpay signature upstream, so we
// trust the event and apply the paid status directly. Idempotent.
func (s *Service) ApplyVerifiedPaymentEvent(ctx context.Context, orderID uuid.UUID, paymentID string) error {
	return s.applyPaidStatus(ctx, orderID, paymentID, "razorpay", nil, "system")
}

// applyPaidStatus is the shared "actually mark this order paid + fire the
// downstream side effects" core, called from the stub-gateway
// ConfirmPayment and the service-level ApplyVerifiedPaymentEvent. (The
// production webhook path is Store.ApplyPaymentSucceeded, which applies the
// same transition guard inside its inbox transaction.)
//
// The transition itself is one guarded UPDATE (store.MarkOrderPaid), so
// when two callers race exactly one sees Applied=true and runs the side
// effects; the other observes payment_status=paid and returns nil. An
// order that is not payable any more (cancelled, refunded, …) yields
// ErrOrderNotPayable — money arrived for a dead order and someone has to
// refund it; the caller decides how loudly to report that.
func (s *Service) applyPaidStatus(ctx context.Context, orderID uuid.UUID, paymentID, gateway string, actorID *uuid.UUID, actorType string) error {
	if s.orders == nil {
		return fmt.Errorf("order store not configured")
	}
	t, err := s.orders.MarkOrderPaid(ctx, orderID, paymentID, gateway, actorID, actorType)
	if err != nil {
		if errors.Is(err, postgres.ErrOrderNotFound) {
			return ErrOrderNotFound
		}
		return err
	}
	if !t.Applied {
		if t.PaymentStatus == "paid" {
			return nil // converged: the other caller won the race
		}
		return fmt.Errorf("%w (status=%s payment_status=%s)", ErrOrderNotPayable, t.Status, t.PaymentStatus)
	}

	// Deduct inventory (best-effort).
	items, _ := s.orders.GetOrderItems(ctx, orderID)
	for _, item := range items {
		if err := s.orders.DeductStock(ctx, item.VariantID, item.Quantity, orderID); err != nil {
			slog.Warn("failed to deduct stock", "variant", item.VariantID, "error", err)
		}
	}

	order, _ := s.orders.GetOrderByID(ctx, orderID)
	var buyerEmail, orderNumber string
	var amount float64 // money-exempt: seller earnings, fenced at /v1/commerce/seller/earnings (B5)
	if order != nil {
		buyerEmail, _ = s.resolveBuyer(ctx, order.CustomerUserID)
		orderNumber = order.OrderNumber
		amount = order.FinalAmount
	}
	paid := map[string]any{
		"order_id":     orderID,
		"order_number": orderNumber,
		"amount":       amount,
		"payment_id":   paymentID,
		"buyer_email":  buyerEmail,
	}
	if order != nil {
		paid["user_id"] = order.CustomerUserID
		paid["amount_minor"] = order.TotalMinor()
	}
	// The seller the new-order notice goes to, as the signed-event path's
	// outbox row carries it (store.OrderEventParties). Best effort: a failed
	// read leaves the keys absent, which the notification consumer counts.
	if s.store != nil {
		if parties, err := s.store.OrderEventParties(ctx, orderID); err == nil {
			parties.FillMissing(paid)
		} else {
			slog.Warn("commerce: paid event without its parties", "order_id", orderID, "error", err)
		}
	}
	s.publish(ctx, events.EventCommerceOrderPaid, paid)

	// Phase 6.1 — enqueue a durable fulfillment job rather than firing
	// `go s.fulfillPaidOrder(orderID)`. A service restart between this
	// point and the side effects (invoice + shipment) used to drop the
	// work entirely; now the worker picks it back up.
	s.EnqueueFulfillPaidOrder(ctx, orderID)
	return nil
}

// MarkPaymentFailed flags an order's payment as failed and releases the stock
// reservation made at checkout so other customers can buy the units. The
// order itself stays in payment_pending so the customer can retry — switching
// to a hard "payment_failed" terminal state would force them to rebuild the
// cart. Idempotent: a second call on an already-failed intent is a no-op,
// and a late payment.failed arriving after the order was paid is refused
// by the transition guard (failed is only reachable from pending /
// processing) so it can never clobber a captured payment.
func (s *Service) MarkPaymentFailed(ctx context.Context, orderID uuid.UUID, paymentID string) error {
	if s.orders == nil {
		return fmt.Errorf("order store not configured")
	}
	applied, err := s.orders.TransitionPaymentStatus(ctx, orderID, "failed", paymentID, "razorpay")
	if err != nil {
		return err
	}
	if !applied {
		return nil
	}
	items, _ := s.orders.GetOrderItems(ctx, orderID)
	order, err := s.getOrderForPayment(ctx, orderID)
	if err != nil {
		return nil
	}
	for _, item := range items {
		if err := s.orders.ReleaseReservation(ctx, item.VariantID, order.CustomerUserID, item.Quantity); err != nil {
			slog.Warn("failed to release reservation",
				"variant", item.VariantID, "order", orderID, "error", err)
		}
	}
	return nil
}

// CancelOrder cancels an order that hasn't shipped yet. For prepaid
// orders we also kick off a refund via payments-service — without this,
// money landed in escrow at confirmation and stayed there. Refund
// happens best-effort: the cancel itself succeeds even when the refund
// call fails (an admin can retry); but on success we stamp the intent
// ID on the order so the refund consumer can flip payment_status when
// the gateway settles.
// CancelOrder delegates to the transactional implementation.
//
// LB-10 / M-2 / LB-8. The body that used to live here had three defects
// stacked on top of each other:
//
//  1. It never compared `actorID` to `order.customer_user_id`, so knowing
//     an order UUID was enough to cancel a stranger's order.
//  2. It never released or restored inventory, so a cancelled order's stock
//     was lost permanently — the seller's catalogue silently drained.
//  3. It had three `slog.Warn` + `return nil` branches on the refund leg
//     (no client, no intent, call failed), each reporting success while no
//     money moved and nothing remembered the debt.
//
// Store.CancelOrder does the ownership check, releases or restocks under the
// same lock as the status change, and writes a DURABLE refund command that a
// worker owns. The permitted (state, actor) pairs live in the
// order_status_transitions table so the rule and the audit trail cannot
// drift apart.
func (s *Service) CancelOrder(ctx context.Context, orderID, actorID uuid.UUID, actorType, reason string) error {
	return s.store.CancelOrder(ctx, orderID, actorID, actorType, reason)
}

// ApplyRefundEvent is the system entry point the Kafka payments consumer
// uses when payments-service publishes payment.refunded. The refund may
// be a per-line return refund (set up by ApproveReturn) or an order-
// level cancel refund (set up by CancelOrder). We try both keyed lookups
// and log a no-op if neither matches — payments-service can refund
// intents that commerce-service didn't initiate, and that's fine.
func (s *Service) ApplyRefundEvent(ctx context.Context, intentID string) error {
	if intentID == "" {
		return nil
	}
	if n, err := s.store.MarkReturnRefundSucceededByIntent(ctx, intentID); err != nil {
		slog.Warn("refund consumer: return update failed", "intent_id", intentID, "error", err)
	} else if n > 0 {
		slog.Info("refund consumer: return marked succeeded", "intent_id", intentID, "rows", n)
		return nil
	}
	if n, err := s.store.MarkOrderRefundedByPayment(ctx, intentID); err != nil {
		slog.Warn("refund consumer: order update failed", "intent_id", intentID, "error", err)
	} else if n > 0 {
		slog.Info("refund consumer: order marked refunded", "intent_id", intentID, "rows", n)
		return nil
	}
	// No match — this was a refund commerce-service didn't initiate
	// (manual gateway action, etc.). Quiet info-level log.
	slog.Info("refund consumer: no matching commerce row", "intent_id", intentID)
	return nil
}
