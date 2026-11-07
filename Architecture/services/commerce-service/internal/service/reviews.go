// Reviews: creating and listing product reviews and the seller's response
// to a review. Moved out of service.go unchanged.

package service

import (
	"context"

	"github.com/atpost/commerce-service/internal/store/postgres"
	"github.com/google/uuid"
)

// ─── Reviews ─────────────────────────────────────────────────

// CreateReview validates the supplied review against the reviewer's actual
// order history before persisting. Phase 0.6 — previously the handler set
// IsVerifiedPurchase=true blindly off the request fields, so any
// authenticated user could post a "verified" review for any product by
// supplying a fabricated order_item_id. Now we look up the order item and
// require:
//
//   - The order item belongs to an order owned by the reviewer.
//   - The item's product_id matches the reviewed product.
//   - The item's seller_id matches the supplied seller.
//   - The item is delivered (status=='delivered' or delivered_at set).
//
// IsVerifiedPurchase is derived from the validation; callers are no longer
// trusted to set it.
func (s *Service) CreateReview(ctx context.Context, r *postgres.Review) error {
	item, err := s.store.GetOrderItemByID(ctx, r.OrderItemID)
	if err != nil || item == nil {
		return ErrReviewOrderItemInvalid
	}
	if item.ProductID != r.ProductID || item.SellerID != r.SellerID {
		return ErrReviewOrderItemInvalid
	}
	order, err := s.store.GetOrderByID(ctx, item.OrderID)
	if err != nil || order == nil {
		return ErrReviewOrderItemInvalid
	}
	if order.CustomerUserID != r.ReviewerID {
		return ErrReviewOrderItemInvalid
	}
	if item.Status != "delivered" && item.DeliveredAt == nil {
		return ErrReviewItemNotDelivered
	}

	r.IsVerifiedPurchase = true // derived, not trusted from input

	if err := s.store.CreateReview(ctx, r); err != nil {
		return err
	}
	s.publish(ctx, "commerce.review.created", map[string]any{
		"product_id": r.ProductID, "seller_id": r.SellerID, "rating": r.Rating,
	})
	return nil
}

func (s *Service) GetProductReviews(ctx context.Context, productID uuid.UUID, sort string, limit, offset int) ([]*postgres.Review, int, error) {
	return s.store.GetProductReviews(ctx, productID, sort, limit, offset)
}

// AddSellerResponseToReview lets the seller of a reviewed product attach
// a public response. Phase 2.4. Only the seller may respond; the response
// timestamp is set on first write and overwritten on a subsequent edit.
//
// sellerID is the caller's sellers.id, resolved from their user id by the
// handler (sellerForCaller). L2 (audit 2026-10-30): this used to receive the
// caller's USER id and compare it with reviews.seller_id, a sellers.id, so
// the two never matched and every seller was refused.
func (s *Service) AddSellerResponseToReview(ctx context.Context, reviewID, sellerID uuid.UUID, response string) (*postgres.Review, error) {
	r, err := s.store.GetReviewByID(ctx, reviewID)
	if err != nil || r == nil {
		return nil, ErrReviewNotFound
	}
	if err := checkReviewSeller(r, sellerID); err != nil {
		return nil, err
	}
	if err := s.store.SetSellerResponse(ctx, reviewID, response); err != nil {
		return nil, err
	}
	return s.store.GetReviewByID(ctx, reviewID)
}

// checkReviewSeller refuses anyone but the seller the review is about.
// sellerID is a sellers.id, never a user id.
func checkReviewSeller(r *postgres.Review, sellerID uuid.UUID) error {
	if sellerID == uuid.Nil || r.SellerID != sellerID {
		return ErrNotReviewSeller
	}
	return nil
}
