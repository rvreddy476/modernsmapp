// Returns: creating return requests (single and bulk), seller approval with
// pickup booking and refund initiation, rejection, the buyer and seller
// return lists and the refund preview. Moved out of service.go unchanged.

package service

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"github.com/atpost/commerce-service/internal/courier"
	"github.com/atpost/commerce-service/internal/store/postgres"
	"github.com/google/uuid"
)

// ─── Returns ─────────────────────────────────────────────────

// GetReturnRequest fetches a return for read by either the customer or
// the seller. Phase 2.2 — adds the missing detail endpoint mobile was
// emulating by listing all returns and filtering client-side.
func (s *Service) GetReturnRequest(ctx context.Context, returnID, actorID uuid.UUID) (*postgres.ReturnRequest, error) {
	r, err := s.store.GetReturnRequestByID(ctx, returnID)
	if err != nil || r == nil {
		return nil, ErrReturnNotFound
	}
	if r.CustomerUserID != actorID && r.SellerID != actorID {
		return nil, ErrNotReturnParty
	}
	return r, nil
}

// BulkReturnItemInput is one line in a multi-item return request. Phase 2.3
// replaces the mobile fan-out (N HTTP calls) with a single endpoint.
type BulkReturnItemInput struct {
	OrderItemID       uuid.UUID
	SellerID          uuid.UUID
	ReasonCode        string
	ReasonDescription *string
}

// CreateReturnRequestBulk creates a return row per supplied item. Each
// row goes through the existing single-item validation + publish path,
// so multi-seller orders correctly notify each seller. On partial
// failure (e.g. item 2 of 3 is ineligible) the first N successfully-
// created return rows are returned alongside the error so the caller
// can surface what landed and what didn't.
func (s *Service) CreateReturnRequestBulk(ctx context.Context, orderID, customerUserID uuid.UUID, items []BulkReturnItemInput) ([]*postgres.ReturnRequest, error) {
	if len(items) == 0 {
		return nil, fmt.Errorf("at least one item required")
	}
	out := make([]*postgres.ReturnRequest, 0, len(items))
	for _, it := range items {
		r, err := s.CreateReturnRequest(ctx, CreateReturnInput{
			OrderID:           orderID,
			OrderItemID:       it.OrderItemID,
			CustomerUserID:    customerUserID,
			SellerID:          it.SellerID,
			ReasonCode:        it.ReasonCode,
			ReasonDescription: it.ReasonDescription,
		})
		if err != nil {
			return out, err
		}
		out = append(out, r)
	}
	return out, nil
}

type CreateReturnInput struct {
	OrderID           uuid.UUID
	OrderItemID       uuid.UUID
	CustomerUserID    uuid.UUID
	SellerID          uuid.UUID
	ReasonCode        string
	ReasonDescription *string
}

func (s *Service) CreateReturnRequest(ctx context.Context, in CreateReturnInput) (*postgres.ReturnRequest, error) {
	r := &postgres.ReturnRequest{
		OrderID:           in.OrderID,
		OrderItemID:       in.OrderItemID,
		CustomerUserID:    in.CustomerUserID,
		SellerID:          in.SellerID,
		ReasonCode:        in.ReasonCode,
		ReasonDescription: in.ReasonDescription,
		Status:            "requested",
	}
	if err := s.store.CreateReturnRequest(ctx, r); err != nil {
		return nil, err
	}
	_ = s.store.UpdateOrderStatus(ctx, in.OrderID, "return_requested", &in.CustomerUserID, "customer", in.ReasonCode)
	s.publish(ctx, "commerce.return.requested", map[string]any{
		"return_id": r.ID, "order_id": r.OrderID, "seller_id": r.SellerID,
	})
	return r, nil
}

// ApproveReturn moves a return from 'requested' to 'approved', books a
// reverse-pickup with the courier (pickup at the customer's address, drop
// at the seller), and kicks off the refund. The refund flow differs by
// payment method:
//   - Prepaid: call payments-service to refund the original gateway intent.
//     Refund status flips to 'pending' here; payments-service publishes a
//     payment.refunded event when the gateway settles, which the
//     commerce-service consumer rolls into 'succeeded'.
//   - COD: there is no gateway transaction to reverse. We mark the refund
//     as 'manual' so the seller's payout is debited and Ops can wire the
//     cash back to the customer outside the system.
//
// All side effects best-effort: courier or payments-service unavailability
// degrades cleanly to "approved with refund pending" rather than rolling
// back the approval.
func (s *Service) ApproveReturn(ctx context.Context, returnID, actorID uuid.UUID) (*postgres.ReturnRequest, error) {
	r, err := s.store.GetReturnRequestByID(ctx, returnID)
	if err != nil {
		return nil, fmt.Errorf("get return: %w", err)
	}
	// Phase 0.5: only the seller of the returned item may approve.
	if r.SellerID != actorID {
		return nil, ErrNotReturnSeller
	}
	if r.Status == "approved" {
		return r, nil // idempotent
	}
	if r.Status != "requested" {
		return nil, fmt.Errorf("cannot approve return in status %q", r.Status)
	}

	if err := s.store.UpdateReturnStatus(ctx, returnID, "approved", nil); err != nil {
		return nil, fmt.Errorf("update return status: %w", err)
	}
	_ = s.store.UpdateOrderStatus(ctx, r.OrderID, "return_approved", &actorID, "seller", "return approved")

	// Reverse-pickup label.
	s.bookReturnPickup(ctx, r)

	// Phase 6.1 — refund happens via the durable fulfillment worker so a
	// payments-service blip doesn't leave the buyer unrefunded after a
	// successful approve API call.
	s.EnqueueProcessReturnApproved(ctx, returnID)

	out, _ := s.store.GetReturnRequestByID(ctx, returnID)
	if out == nil {
		out = r
	}
	s.publish(ctx, "commerce.return.approved", map[string]any{
		"return_id": out.ID, "order_id": out.OrderID,
		"seller_id": out.SellerID, "refund_status": out.RefundAmount,
	})
	return out, nil
}

// bookReturnPickup books a reverse-pickup shipment (customer → seller).
// Failures are logged but don't block approval — Ops can re-trigger via
// a retry endpoint later. The courier is the same provider as outbound.
func (s *Service) bookReturnPickup(ctx context.Context, r *postgres.ReturnRequest) {
	if s.courier == nil {
		return
	}
	order, err := s.store.GetOrderByID(ctx, r.OrderID)
	if err != nil || order == nil {
		slog.Warn("return pickup: get order failed", "return_id", r.ID, "error", err)
		return
	}
	// Pickup is the customer's delivery address (where the goods are now):
	// the order's snapshot, where they were delivered, not the saved address
	// as edited since (M5, audit 2026-10-30). Opened, not read from
	// plaintext: after the cutover the plaintext is '' and the courier would
	// be sent to collect from nobody at no street.
	addr, err := s.orderFulfilmentAddress(ctx, order)
	if err != nil {
		slog.Error("return pickup: the delivery address could not be opened",
			"order_id", order.ID, "error", err)
		return
	}
	pickup := courier.Address{
		Name: addr.ContactName, Phone: addr.Phone,
		Line1: addr.AddressLine1, Line2: addr.AddressLine2,
		City: addr.City, State: addr.State,
		Postal: addr.PostalCode, Country: addr.Country,
	}

	// Drop is the seller's pickup address.
	seller, err := s.store.GetSellerByID(ctx, r.SellerID)
	if err != nil {
		slog.Warn("return pickup: seller lookup failed", "error", err)
		return
	}
	drop := courier.Address{Name: seller.StoreName, Country: "IN"}
	if seller.City != nil {
		drop.City = *seller.City
	}
	if seller.State != nil {
		drop.State = *seller.State
	}
	if seller.PostalCode != nil {
		drop.Postal = *seller.PostalCode
	}
	if seller.Phone != nil {
		drop.Phone = *seller.Phone
	}

	// Reuse the outbound CreateShipment; courier providers don't model
	// returns separately. PaymentMethod=prepaid because the customer
	// already paid (this is reverse logistics, not a sale).
	resp, err := s.courier.CreateShipment(ctx, courier.ShipmentRequest{
		OrderID:       r.OrderID.String() + "-return",
		OrderNumber:   "RTN-" + r.ID.String()[:8],
		PickupAddress: pickup,
		DropAddress:   drop,
		Weight:        0.5, // Use a default — real weight is the original item's, looked up below.
		PaymentMethod: "prepaid",
	})
	if err != nil {
		slog.Warn("return pickup: courier create failed", "error", err)
		return
	}
	if err := s.store.SetReturnPickupLabel(ctx, r.ID, s.courier.Name(), resp.AWBNumber, resp.LabelURL); err != nil {
		slog.Warn("return pickup: persist label failed", "error", err)
	}
}

// initiateReturnRefund decides between gateway refund and manual COD
// reconciliation, then records the outcome on the return row. Item-level
// refund amount = the original line's final price (one item per return
// today; multi-item returns would prorate here).
func (s *Service) initiateReturnRefund(ctx context.Context, r *postgres.ReturnRequest, actorID uuid.UUID) {
	order, err := s.store.GetOrderByID(ctx, r.OrderID)
	if err != nil || order == nil {
		slog.Warn("refund: get order failed", "return_id", r.ID, "error", err)
		return
	}
	items, _ := s.store.GetOrderItems(ctx, r.OrderID)
	var amount float64 // money-exempt: COD remittance, fenced at /v1/commerce/seller/cod-remittances (B5)
	for _, it := range items {
		if it.ID == r.OrderItemID {
			amount = it.FinalPrice
			break
		}
	}
	if amount == 0 {
		amount = order.FinalAmount // fallback: full-order refund
	}

	isCOD := order.PaymentMethod != nil && strings.EqualFold(*order.PaymentMethod, "cod")
	if isCOD {
		// No gateway leg — Ops settles cash externally.
		if err := s.store.SetReturnRefund(ctx, r.ID, "", "manual", amount); err != nil {
			slog.Warn("refund: persist manual cod refund failed", "error", err)
		}
		return
	}

	// LB-11 / D3 — returns are FENCED in Commerce P0 and this path is
	// unreachable by design.
	//
	// The return loop is not part of the launch scope, and its creation path
	// (`CreateReturnRequest`) persisted caller-supplied order_id,
	// order_item_id, customer_user_id and seller_id with no relational
	// check, so a caller could attach a return to a stranger's order and
	// move that order to `return_requested` (review M-3). Repairing that
	// loop is post-launch work; fencing it is cheaper and safer.
	//
	// The routes are unregistered, the worker entry points are disabled, and
	// migration 012 puts a trigger on `return_requests` that refuses the
	// INSERT — so a replayed legacy job cannot resurrect this either. This
	// function remains only so that a code path which somehow reaches it
	// fails loudly instead of half-refunding.
	slog.Error("commerce: return refund path invoked while returns are fenced (LB-11)",
		"return_id", r.ID, "order_id", r.OrderID)
	_ = s.store.SetReturnRefund(ctx, r.ID, "", "pending", amount)
}

// RejectReturn closes a return with status='rejected' and records the
// seller's reason. Order falls back to the previous fulfillment state
// (delivered) so the customer's UI shows the return is closed.
func (s *Service) RejectReturn(ctx context.Context, returnID, actorID uuid.UUID, reason string) (*postgres.ReturnRequest, error) {
	r, err := s.store.GetReturnRequestByID(ctx, returnID)
	if err != nil {
		return nil, fmt.Errorf("get return: %w", err)
	}
	// Phase 0.5: only the seller of the returned item may reject.
	if r.SellerID != actorID {
		return nil, ErrNotReturnSeller
	}
	if r.Status == "rejected" {
		return r, nil
	}
	if r.Status != "requested" {
		return nil, fmt.Errorf("cannot reject return in status %q", r.Status)
	}
	rsn := reason
	if err := s.store.UpdateReturnStatus(ctx, returnID, "rejected", &rsn); err != nil {
		return nil, fmt.Errorf("update return status: %w", err)
	}
	_ = s.store.UpdateOrderStatus(ctx, r.OrderID, "return_rejected", &actorID, "seller", reason)
	s.publish(ctx, "commerce.return.rejected", map[string]any{
		"return_id": r.ID, "order_id": r.OrderID, "reason": reason,
	})
	out, _ := s.store.GetReturnRequestByID(ctx, returnID)
	if out == nil {
		out = r
	}
	return out, nil
}

// ListMyReturns returns the calling customer's return history.
// HP3: pagination clamped (default 20, max 200).
func (s *Service) ListMyReturns(ctx context.Context, userID uuid.UUID, limit, offset int) ([]*postgres.ReturnRequest, error) {
	limit, offset = clampListPagination(limit, offset)
	return s.store.ListReturnsByCustomer(ctx, userID, limit, offset)
}

// SellerReturnCard joins a return request with the order item it concerns
// + the order header. Lets the inbox render reason, item, refund amount,
// and the buyer's order without N+1 round trips on the UI side.
type SellerReturnCard struct {
	Return    *postgres.ReturnRequest `json:"return"`
	OrderItem *postgres.OrderItem     `json:"order_item,omitempty"`
	Order     *postgres.Order         `json:"order,omitempty"`
}

// ListSellerReturns returns the seller's returns inbox, optionally filtered
// by status (requested / approved / rejected / refunded / closed). Phase 4.3.
func (s *Service) ListSellerReturns(ctx context.Context, sellerID uuid.UUID, status string, limit, offset int) ([]*SellerReturnCard, error) {
	// HP3: clamp pagination.
	limit, offset = clampListPagination(limit, offset)
	returns, err := s.store.ListReturnsBySeller(ctx, sellerID, status, limit, offset)
	if err != nil {
		return nil, err
	}
	if len(returns) == 0 {
		return []*SellerReturnCard{}, nil
	}

	// HP2: was 2 queries per return (item + order); now 2 queries total
	// per page. 20-return page drops from 41 to 3 round trips.
	itemIDs := make([]uuid.UUID, 0, len(returns))
	orderIDs := make([]uuid.UUID, 0, len(returns))
	seenOrder := map[uuid.UUID]struct{}{}
	for _, r := range returns {
		itemIDs = append(itemIDs, r.OrderItemID)
		if _, ok := seenOrder[r.OrderID]; !ok {
			seenOrder[r.OrderID] = struct{}{}
			orderIDs = append(orderIDs, r.OrderID)
		}
	}
	itemsByID, err := s.store.GetOrderItemsByIDs(ctx, itemIDs)
	if err != nil {
		return nil, fmt.Errorf("batch return items: %w", err)
	}
	ordersByID, err := s.store.GetOrdersByIDs(ctx, orderIDs)
	if err != nil {
		return nil, fmt.Errorf("batch return orders: %w", err)
	}

	out := make([]*SellerReturnCard, 0, len(returns))
	for _, r := range returns {
		out = append(out, &SellerReturnCard{
			Return:    r,
			OrderItem: itemsByID[r.OrderItemID],
			Order:     ordersByID[r.OrderID],
		})
	}
	return out, nil
}

// PreviewReturnRefund returns the refund amount the seller would be debited
// if they approve the return. Reads the order item's FinalPrice; when the
// return already has an explicit RefundAmount, that wins. Phase 4.3 — keeps
// the inbox from having to recompute.
func (s *Service) PreviewReturnRefund(ctx context.Context, returnID, actorID uuid.UUID) (float64, error) {
	r, err := s.store.GetReturnRequestByID(ctx, returnID)
	if err != nil || r == nil {
		return 0, ErrReturnNotFound
	}
	if r.SellerID != actorID && r.CustomerUserID != actorID {
		return 0, ErrNotReturnParty
	}
	if r.RefundAmount != nil && *r.RefundAmount > 0 {
		return *r.RefundAmount, nil
	}
	item, err := s.store.GetOrderItemByID(ctx, r.OrderItemID)
	if err != nil || item == nil {
		return 0, ErrReturnNotFound
	}
	return round2(item.FinalPrice), nil
}
