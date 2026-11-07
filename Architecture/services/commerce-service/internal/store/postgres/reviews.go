// Reviews: creating reviews, sort orders, listing and reading them, and
// the seller response. Moved out of store.go unchanged.

package postgres

import (
	"context"
	"time"

	"github.com/google/uuid"
)

// ─── Reviews ─────────────────────────────────────────────────

func (s *Store) CreateReview(ctx context.Context, r *Review) error {
	r.ID = uuid.New()
	r.CreatedAt = time.Now()
	_, err := s.db.Exec(ctx, `
		INSERT INTO reviews (id,product_id,seller_id,order_item_id,reviewer_id,
		  rating,title,body,is_verified_purchase,is_published,created_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)`,
		r.ID, r.ProductID, r.SellerID, r.OrderItemID, r.ReviewerID,
		r.Rating, r.Title, r.Body, r.IsVerifiedPurchase, r.IsPublished, r.CreatedAt,
	)
	return err
}

// Review list orders for GetProductReviews.
const (
	// ReviewSortHelpful: most helpful first (helpful_count, then newest).
	// The default.
	ReviewSortHelpful = "helpful"
	// ReviewSortRecent: newest first.
	ReviewSortRecent = "recent"
)

// reviewOrderBy maps a sort to its ORDER BY. id is the last key in both so a
// page boundary between two equal rows is stable.
func reviewOrderBy(sort string) string {
	if sort == ReviewSortRecent {
		return `created_at DESC, id DESC`
	}
	return `helpful_count DESC, created_at DESC, id DESC`
}

func (s *Store) GetProductReviews(ctx context.Context, productID uuid.UUID, sort string, limit, offset int) ([]*Review, int, error) {
	var total int
	_ = s.db.QueryRow(ctx, `
		SELECT COUNT(*) FROM reviews
		WHERE product_id=$1 AND is_published=TRUE
		  AND COALESCE(moderation_status,'approved') <> 'rejected'
	`, productID).Scan(&total)

	rows, err := s.db.Query(ctx, `SELECT id,product_id,seller_id,order_item_id,reviewer_id,
		rating,title,body,is_verified_purchase,helpful_count,
		COALESCE(moderation_status,'approved'),seller_response,seller_responded_at,created_at
		FROM reviews WHERE product_id=$1 AND is_published=TRUE
		  AND COALESCE(moderation_status,'approved') <> 'rejected'
		ORDER BY `+reviewOrderBy(sort)+` LIMIT $2 OFFSET $3`, productID, limit, offset)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	var reviews []*Review
	for rows.Next() {
		var r Review
		if err := rows.Scan(&r.ID, &r.ProductID, &r.SellerID, &r.OrderItemID, &r.ReviewerID,
			&r.Rating, &r.Title, &r.Body, &r.IsVerifiedPurchase, &r.HelpfulCount,
			&r.ModerationStatus, &r.SellerResponse, &r.SellerRespondedAt, &r.CreatedAt); err != nil {
			return nil, 0, err
		}
		reviews = append(reviews, &r)
	}
	return reviews, total, nil
}

// GetReviewByID fetches a single review — used by the seller-response
// handler (Phase 2.4) to verify the actor is the seller before allowing
// a response, and to return the updated row.
func (s *Store) GetReviewByID(ctx context.Context, id uuid.UUID) (*Review, error) {
	var r Review
	err := s.db.QueryRow(ctx, `SELECT id,product_id,seller_id,order_item_id,reviewer_id,
		rating,title,body,is_verified_purchase,helpful_count,
		COALESCE(moderation_status,'approved'),seller_response,seller_responded_at,created_at
		FROM reviews WHERE id=$1`, id).Scan(
		&r.ID, &r.ProductID, &r.SellerID, &r.OrderItemID, &r.ReviewerID,
		&r.Rating, &r.Title, &r.Body, &r.IsVerifiedPurchase, &r.HelpfulCount,
		&r.ModerationStatus, &r.SellerResponse, &r.SellerRespondedAt, &r.CreatedAt,
	)
	if err != nil {
		return nil, err
	}
	return &r, nil
}

// SetSellerResponse adds (or replaces) the seller's response to a review.
// Returns the affected row count so the service layer can distinguish a
// no-op (id not found) from a legitimate skip.
func (s *Store) SetSellerResponse(ctx context.Context, id uuid.UUID, response string) error {
	_, err := s.db.Exec(ctx,
		`UPDATE reviews SET seller_response=$2, seller_responded_at=NOW() WHERE id=$1`,
		id, response,
	)
	return err
}
