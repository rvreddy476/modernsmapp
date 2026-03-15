package service

import (
	"context"
	"errors"
	"fmt"

	"github.com/atpost/orders-service/internal/store/postgres"
	"github.com/google/uuid"
)

// ErrReturnNotFound is returned when a return request cannot be located.
var ErrReturnNotFound = errors.New("return request not found")

// ErrReturnAlreadyExists is returned when an order already has a return request.
var ErrReturnAlreadyExists = errors.New("a return request already exists for this order")

// ErrReturnInvalidTransition is returned on an invalid status transition.
var ErrReturnInvalidTransition = errors.New("invalid return status transition")

// validReturnReasons is the set of allowed return reason values.
var validReturnReasons = map[string]bool{
	"wrong_item":       true,
	"damaged":          true,
	"not_as_described": true,
	"changed_mind":     true,
	"never_arrived":    true,
}

// validRefundMethods is the set of allowed refund method values.
var validRefundMethods = map[string]bool{
	"original_payment": true,
	"store_credit":     true,
	"upi":              true,
}

// CreateReturnRequest validates that the buyer owns a delivered order and no return already exists,
// then creates the return request.
func (s *Service) CreateReturnRequest(ctx context.Context, buyerID, orderID uuid.UUID, reason, description string, evidenceURLs []string) (*postgres.ReturnRequest, error) {
	if !validReturnReasons[reason] {
		return nil, fmt.Errorf("invalid reason: %s", reason)
	}

	order, err := s.store.GetOrder(ctx, orderID)
	if err != nil {
		return nil, fmt.Errorf("order not found")
	}
	if order.BuyerID != buyerID {
		return nil, fmt.Errorf("forbidden")
	}
	if order.Status != "delivered" && order.Status != "completed" {
		return nil, fmt.Errorf("order must be delivered or completed to request a return (current status: %s)", order.Status)
	}

	// Check no existing return for this order.
	existing, _ := s.store.GetReturnRequestByOrder(ctx, orderID)
	if existing != nil {
		return nil, ErrReturnAlreadyExists
	}

	if evidenceURLs == nil {
		evidenceURLs = []string{}
	}

	r, err := s.store.CreateReturnRequest(ctx, &postgres.ReturnRequest{
		OrderID:      orderID,
		BuyerID:      buyerID,
		Reason:       reason,
		Description:  description,
		EvidenceURLs: evidenceURLs,
	})
	if err != nil {
		return nil, err
	}

	s.publishEvent(ctx, "return.requested", buyerID.String(), r)
	return r, nil
}

// ApproveReturn transitions a return from requested → approved (seller action).
func (s *Service) ApproveReturn(ctx context.Context, sellerID, returnID uuid.UUID, sellerNote string, refundAmount float64, refundMethod string) error {
	ret, err := s.store.GetReturnRequest(ctx, returnID)
	if err != nil {
		return ErrReturnNotFound
	}
	if ret.Status != "requested" {
		return fmt.Errorf("%w: cannot approve from status %s", ErrReturnInvalidTransition, ret.Status)
	}
	if !validRefundMethods[refundMethod] {
		return fmt.Errorf("invalid refund_method: %s", refundMethod)
	}

	order, err := s.store.GetOrder(ctx, ret.OrderID)
	if err != nil {
		return fmt.Errorf("order not found")
	}
	if order.SellerID != sellerID {
		return fmt.Errorf("forbidden")
	}

	note := &sellerNote
	if sellerNote == "" {
		note = nil
	}
	return s.store.UpdateReturnStatus(ctx, returnID, "approved", note, &refundAmount, &refundMethod)
}

// RejectReturn transitions a return from requested → rejected (seller action).
func (s *Service) RejectReturn(ctx context.Context, sellerID, returnID uuid.UUID, sellerNote string) error {
	ret, err := s.store.GetReturnRequest(ctx, returnID)
	if err != nil {
		return ErrReturnNotFound
	}
	if ret.Status != "requested" {
		return fmt.Errorf("%w: cannot reject from status %s", ErrReturnInvalidTransition, ret.Status)
	}

	order, err := s.store.GetOrder(ctx, ret.OrderID)
	if err != nil {
		return fmt.Errorf("order not found")
	}
	if order.SellerID != sellerID {
		return fmt.Errorf("forbidden")
	}

	note := &sellerNote
	if sellerNote == "" {
		note = nil
	}
	return s.store.UpdateReturnStatus(ctx, returnID, "rejected", note, nil, nil)
}

// MarkItemReceived transitions a return from approved → item_received (seller action).
func (s *Service) MarkItemReceived(ctx context.Context, sellerID, returnID uuid.UUID) error {
	ret, err := s.store.GetReturnRequest(ctx, returnID)
	if err != nil {
		return ErrReturnNotFound
	}
	if ret.Status != "approved" {
		return fmt.Errorf("%w: cannot mark item_received from status %s", ErrReturnInvalidTransition, ret.Status)
	}

	order, err := s.store.GetOrder(ctx, ret.OrderID)
	if err != nil {
		return fmt.Errorf("order not found")
	}
	if order.SellerID != sellerID {
		return fmt.Errorf("forbidden")
	}

	return s.store.UpdateReturnStatus(ctx, returnID, "item_received", nil, nil, nil)
}

// ProcessRefund transitions a return from item_received → refunded → completed.
func (s *Service) ProcessRefund(ctx context.Context, returnID uuid.UUID) error {
	ret, err := s.store.GetReturnRequest(ctx, returnID)
	if err != nil {
		return ErrReturnNotFound
	}
	if ret.Status != "item_received" {
		return fmt.Errorf("%w: cannot process refund from status %s", ErrReturnInvalidTransition, ret.Status)
	}

	if err := s.store.UpdateReturnStatus(ctx, returnID, "refunded", nil, nil, nil); err != nil {
		return err
	}

	s.publishEvent(ctx, "return.refunded", returnID.String(), map[string]any{
		"return_id":     returnID,
		"order_id":      ret.OrderID,
		"refund_amount": ret.RefundAmount,
		"refund_method": ret.RefundMethod,
	})

	return s.store.UpdateReturnStatus(ctx, returnID, "completed", nil, nil, nil)
}

// UpdateReturnTracking allows the buyer to provide a tracking number after shipping the item back.
func (s *Service) UpdateReturnTracking(ctx context.Context, buyerID, returnID uuid.UUID, tracking string) error {
	ret, err := s.store.GetReturnRequest(ctx, returnID)
	if err != nil {
		return ErrReturnNotFound
	}
	if ret.BuyerID != buyerID {
		return fmt.Errorf("forbidden")
	}
	if ret.Status != "approved" {
		return fmt.Errorf("%w: tracking can only be set when status is approved (current: %s)", ErrReturnInvalidTransition, ret.Status)
	}
	return s.store.UpdateReturnTracking(ctx, returnID, tracking)
}

// GetReturnRequest fetches a return request, ensuring the user is the buyer or seller of the order.
func (s *Service) GetReturnRequest(ctx context.Context, userID, returnID uuid.UUID) (*postgres.ReturnRequest, error) {
	ret, err := s.store.GetReturnRequest(ctx, returnID)
	if err != nil {
		return nil, ErrReturnNotFound
	}

	if ret.BuyerID != userID {
		// Check if user is the seller.
		order, err := s.store.GetOrder(ctx, ret.OrderID)
		if err != nil || order.SellerID != userID {
			return nil, fmt.Errorf("forbidden")
		}
	}
	return ret, nil
}

// ListReturnsByBuyer returns paginated return requests for the authenticated buyer.
func (s *Service) ListReturnsByBuyer(ctx context.Context, buyerID uuid.UUID, limit, offset int) ([]postgres.ReturnRequest, error) {
	return s.store.ListReturnsByBuyer(ctx, buyerID, limit, offset)
}

// ListReturnsBySeller returns paginated return requests for the authenticated seller.
func (s *Service) ListReturnsBySeller(ctx context.Context, sellerID uuid.UUID, status string, limit, offset int) ([]postgres.ReturnRequest, error) {
	return s.store.ListReturnsBySeller(ctx, sellerID, status, limit, offset)
}
