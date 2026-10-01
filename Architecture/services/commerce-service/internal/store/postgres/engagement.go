package postgres

// Product engagement: like / dislike, shares, review helpful votes, and the
// two reads the delivery estimate needs (a buyer's default pincode, a
// seller's dispatch SLA). Migration 037 and setup.sql's review_votes.
//
// ─── THE TWO COUNTERS, AND WHY THE PARENT ROW IS LOCKED FIRST ────────────
//
// products.like_count and reviews.helpful_count are denormalised: every
// product tile carries like_count and every review row carries
// helpful_count, so neither is a COUNT(*) on the read path. A counter is
// only honest if the row it counts and the counter move together, so each
// write is one transaction that
//
//  1. locks the PARENT row (the product, the review) FOR NO KEY UPDATE —
//     the lock the counter UPDATE would take anyway, taken first so two
//     writers on the same parent serialise and never deadlock on the
//     child-then-parent / parent-then-child order;
//  2. reads the shopper's previous reaction / vote;
//  3. writes the new one (or deletes it);
//  4. moves the counter by the difference.
//
// Only a LIKE moves like_count and only a HELPFUL vote moves helpful_count.
// A dislike and a not-helpful vote are stored (so the shopper sees their own
// choice) and counted nowhere.

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Reaction kinds. The CHECK on product_reactions.kind is the same pair.
const (
	ReactionLike    = "like"
	ReactionDislike = "dislike"
)

// ErrInvalidReaction is a kind outside ReactionLike / ReactionDislike.
var ErrInvalidReaction = errors.New("commerce: a reaction is like or dislike")

// likeDelta is how far like_count moves when a shopper's reaction goes from
// prev to next ("" = none). Only likes count.
func likeDelta(prev, next string) int64 {
	var d int64
	if prev == ReactionLike {
		d--
	}
	if next == ReactionLike {
		d++
	}
	return d
}

// helpfulDelta is likeDelta for review votes: prev/next are nil (no vote),
// true (helpful) or false (not helpful). Only helpful counts.
func helpfulDelta(prev, next *bool) int {
	d := 0
	if prev != nil && *prev {
		d--
	}
	if next != nil && *next {
		d++
	}
	return d
}

// SetProductReaction records userID's reaction to productID, replacing any
// previous one, and returns the product's like count after the write.
// Idempotent: repeating the same reaction moves nothing.
func (s *Store) SetProductReaction(ctx context.Context, productID, userID uuid.UUID, kind string) (int64, error) {
	if kind != ReactionLike && kind != ReactionDislike {
		return 0, ErrInvalidReaction
	}
	return s.writeProductReaction(ctx, productID, userID, kind)
}

// ClearProductReaction removes userID's reaction, if any, and returns the
// like count after. Removing a reaction that is not there succeeds.
func (s *Store) ClearProductReaction(ctx context.Context, productID, userID uuid.UUID) (int64, error) {
	return s.writeProductReaction(ctx, productID, userID, "")
}

func (s *Store) writeProductReaction(ctx context.Context, productID, userID uuid.UUID, next string) (int64, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	var likes int64
	if err := tx.QueryRow(ctx,
		`SELECT like_count FROM products WHERE id = $1 FOR NO KEY UPDATE`, productID).Scan(&likes); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return 0, ErrProductNotFound
		}
		return 0, err
	}

	var prev string
	err = tx.QueryRow(ctx,
		`SELECT kind FROM product_reactions WHERE product_id = $1 AND user_id = $2`,
		productID, userID).Scan(&prev)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return 0, err
	}

	if next == "" {
		if _, err := tx.Exec(ctx,
			`DELETE FROM product_reactions WHERE product_id = $1 AND user_id = $2`, productID, userID); err != nil {
			return 0, err
		}
	} else if prev != next {
		if _, err := tx.Exec(ctx, `
			INSERT INTO product_reactions (product_id, user_id, kind) VALUES ($1, $2, $3)
			ON CONFLICT (product_id, user_id) DO UPDATE SET kind = EXCLUDED.kind, created_at = NOW()`,
			productID, userID, next); err != nil {
			return 0, err
		}
	}

	if d := likeDelta(prev, next); d != 0 {
		if err := tx.QueryRow(ctx,
			`UPDATE products SET like_count = like_count + $2 WHERE id = $1 RETURNING like_count`,
			productID, d).Scan(&likes); err != nil {
			return 0, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, err
	}
	return likes, nil
}

// ProductReactionOf is userID's reaction to productID: "like", "dislike",
// or "" for none.
func (s *Store) ProductReactionOf(ctx context.Context, productID, userID uuid.UUID) (string, error) {
	var kind string
	err := s.db.QueryRow(ctx,
		`SELECT kind FROM product_reactions WHERE product_id = $1 AND user_id = $2`,
		productID, userID).Scan(&kind)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil
	}
	return kind, err
}

// ProductLikeCount is products.like_count.
func (s *Store) ProductLikeCount(ctx context.Context, productID uuid.UUID) (int64, error) {
	var n int64
	err := s.db.QueryRow(ctx, `SELECT like_count FROM products WHERE id = $1`, productID).Scan(&n)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, ErrProductNotFound
	}
	return n, err
}

// IncrProductShareCount counts one share. No per-user row: a share is a
// number on the product page, not a fact about a shopper.
func (s *Store) IncrProductShareCount(ctx context.Context, productID uuid.UUID) error {
	tag, err := s.db.Exec(ctx, `UPDATE products SET share_count = share_count + 1 WHERE id = $1`, productID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrProductNotFound
	}
	return nil
}

// ─── Review helpful votes ────────────────────────────────────────────────

// ReviewVoteTarget is what a vote needs to know about a review before it is
// cast: whose it is, which product it is on, and whether shoppers can see
// it at all (published and not rejected by moderation).
type ReviewVoteTarget struct {
	ProductID  uuid.UUID
	ReviewerID uuid.UUID
	Listed     bool
}

// ErrReviewNotFoundP0 is a review id that does not exist.
var ErrReviewNotFoundP0 = errors.New("commerce: review not found")

// GetReviewVoteTarget reads ReviewVoteTarget for one review.
func (s *Store) GetReviewVoteTarget(ctx context.Context, reviewID uuid.UUID) (*ReviewVoteTarget, error) {
	var t ReviewVoteTarget
	err := s.db.QueryRow(ctx, `
		SELECT product_id, reviewer_id,
		       (is_published = TRUE AND COALESCE(moderation_status,'approved') <> 'rejected')
		  FROM reviews WHERE id = $1`, reviewID).Scan(&t.ProductID, &t.ReviewerID, &t.Listed)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrReviewNotFoundP0
	}
	if err != nil {
		return nil, err
	}
	return &t, nil
}

// SetReviewVote records userID's helpful / not-helpful vote on reviewID,
// replacing any previous one, and returns the review's helpful count after.
func (s *Store) SetReviewVote(ctx context.Context, reviewID, userID uuid.UUID, helpful bool) (int, error) {
	return s.writeReviewVote(ctx, reviewID, userID, &helpful)
}

// ClearReviewVote removes userID's vote, if any.
func (s *Store) ClearReviewVote(ctx context.Context, reviewID, userID uuid.UUID) (int, error) {
	return s.writeReviewVote(ctx, reviewID, userID, nil)
}

func (s *Store) writeReviewVote(ctx context.Context, reviewID, userID uuid.UUID, next *bool) (int, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	var helpful int
	if err := tx.QueryRow(ctx,
		`SELECT helpful_count FROM reviews WHERE id = $1 FOR NO KEY UPDATE`, reviewID).Scan(&helpful); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return 0, ErrReviewNotFoundP0
		}
		return 0, err
	}

	var prev *bool
	var was bool
	err = tx.QueryRow(ctx,
		`SELECT is_helpful FROM review_votes WHERE review_id = $1 AND user_id = $2`, reviewID, userID).Scan(&was)
	switch {
	case err == nil:
		prev = &was
	case errors.Is(err, pgx.ErrNoRows):
	default:
		return 0, err
	}

	if next == nil {
		if _, err := tx.Exec(ctx,
			`DELETE FROM review_votes WHERE review_id = $1 AND user_id = $2`, reviewID, userID); err != nil {
			return 0, err
		}
	} else if prev == nil || *prev != *next {
		if _, err := tx.Exec(ctx, `
			INSERT INTO review_votes (review_id, user_id, is_helpful) VALUES ($1, $2, $3)
			ON CONFLICT (review_id, user_id) DO UPDATE SET is_helpful = EXCLUDED.is_helpful, created_at = NOW()`,
			reviewID, userID, *next); err != nil {
			return 0, err
		}
	}

	if d := helpfulDelta(prev, next); d != 0 {
		if err := tx.QueryRow(ctx,
			`UPDATE reviews SET helpful_count = GREATEST(helpful_count + $2, 0) WHERE id = $1 RETURNING helpful_count`,
			reviewID, d).Scan(&helpful); err != nil {
			return 0, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, err
	}
	return helpful, nil
}

// ReviewVotesOf reports userID's vote on each of reviewIDs that has one, in
// ONE query: true = helpful, false = not helpful, absent = no vote.
func (s *Store) ReviewVotesOf(ctx context.Context, userID uuid.UUID, reviewIDs []uuid.UUID) (map[uuid.UUID]bool, error) {
	out := make(map[uuid.UUID]bool, len(reviewIDs))
	if userID == uuid.Nil || len(reviewIDs) == 0 {
		return out, nil
	}
	rows, err := s.db.Query(ctx,
		`SELECT review_id, is_helpful FROM review_votes WHERE user_id = $1 AND review_id = ANY($2)`,
		userID, reviewIDs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id uuid.UUID
		var helpful bool
		if err := rows.Scan(&id, &helpful); err != nil {
			return nil, err
		}
		out[id] = helpful
	}
	return out, rows.Err()
}

// ─── Delivery estimate inputs ────────────────────────────────────────────

// DefaultAddressPincode is the pincode of the buyer's default address (or,
// when none is marked default, their newest), the same order the address
// book lists them in. ok=false when the buyer has no address at all.
//
// postal_code is one of the three columns migration 011 deliberately left
// in plaintext (delivery routing needs it), so this reads no ciphertext.
func (s *Store) DefaultAddressPincode(ctx context.Context, userID uuid.UUID) (pin string, ok bool, err error) {
	err = s.db.QueryRow(ctx, `
		SELECT COALESCE(postal_code,'') FROM customer_addresses
		 WHERE user_id = $1
		 ORDER BY is_default DESC, created_at DESC
		 LIMIT 1`, userID).Scan(&pin)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return pin, true, nil
}

// SellerDispatchSLAHours is seller_fulfillment_settings.dispatch_sla_hours,
// or 0 when the seller never saved fulfilment settings (the caller applies
// the default).
func (s *Store) SellerDispatchSLAHours(ctx context.Context, sellerID uuid.UUID) (int, error) {
	var h int
	err := s.db.QueryRow(ctx,
		`SELECT dispatch_sla_hours FROM seller_fulfillment_settings WHERE seller_id = $1`, sellerID).Scan(&h)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("commerce: reading the seller's dispatch SLA: %w", err)
	}
	return h, nil
}
