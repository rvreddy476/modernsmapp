package postgres

import (
	"context"
	"crypto/rand"
	"fmt"
	"math/big"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// generateOTP returns an N-digit numeric OTP using crypto/rand.
func generateOTP(digits int) (string, error) {
	if digits <= 0 || digits > 8 {
		digits = 6
	}
	maxv := big.NewInt(10)
	out := make([]byte, digits)
	for i := 0; i < digits; i++ {
		n, err := rand.Int(rand.Reader, maxv)
		if err != nil {
			return "", err
		}
		out[i] = byte('0' + n.Int64())
	}
	return string(out), nil
}

// EnsureDeliveryCodes generates pickup_code + delivery_code (4-digit
// each) if they haven't been minted yet. Returns the current pair.
// Idempotent — re-runs return the existing codes.
func (s *Store) EnsureDeliveryCodes(ctx context.Context, orderID uuid.UUID) (pickup, delivery string, err error) {
	pickupCode, err := generateOTP(4)
	if err != nil {
		return "", "", err
	}
	deliveryCode, err := generateOTP(4)
	if err != nil {
		return "", "", err
	}
	if err := s.db.QueryRow(ctx, `
		UPDATE food.delivery_assignments
		SET pickup_code   = COALESCE(NULLIF(pickup_code, ''), $2),
			delivery_code = COALESCE(NULLIF(delivery_code, ''), $3)
		WHERE order_id = $1
		RETURNING COALESCE(pickup_code, ''), COALESCE(delivery_code, '')
	`, orderID, pickupCode, deliveryCode).Scan(&pickup, &delivery); err != nil {
		return "", "", fmt.Errorf("ensure delivery codes: %w", err)
	}
	return pickup, delivery, nil
}

// VerifyPickupCode is the restaurant-side OTP check. The partner reads
// the OTP off their screen and the restaurant agent (or the restaurant
// terminal) submits it here.
//
// Sets pickup_verified_at + transitions order to PICKED_UP.
func (s *Store) VerifyPickupCode(ctx context.Context, ownerID, orderID uuid.UUID, code string) error {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var storedCode, restaurantID string
	if err := tx.QueryRow(ctx, `
		SELECT COALESCE(da.pickup_code, ''), o.restaurant_id::text
		FROM food.delivery_assignments da
		JOIN food.orders o ON o.id = da.order_id
		WHERE da.order_id = $1
		FOR UPDATE OF da
	`, orderID).Scan(&storedCode, &restaurantID); err != nil {
		return err
	}
	var owned int
	if err := tx.QueryRow(ctx, `
		SELECT COUNT(*) FROM food.restaurants
		WHERE id = $1::uuid AND owner_user_id = $2
	`, restaurantID, ownerID).Scan(&owned); err != nil {
		return err
	}
	if owned == 0 {
		return pgx.ErrNoRows
	}
	if storedCode == "" {
		return fmt.Errorf("pickup_code not set")
	}
	if storedCode != code {
		return fmt.Errorf("invalid pickup_code")
	}
	if _, err := tx.Exec(ctx, `
		UPDATE food.delivery_assignments SET pickup_verified_at = NOW(), picked_up_at = NOW()
		WHERE order_id = $1
	`, orderID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `
		UPDATE food.orders SET status = 'PICKED_UP' WHERE id = $1 AND status IN ('DELIVERY_ASSIGNED','READY_FOR_PICKUP')
	`, orderID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// VerifyDeliveryCode is the customer-side OTP check at drop. Sets
// delivery_verified_at + transitions order to DELIVERED.
func (s *Store) VerifyDeliveryCode(ctx context.Context, customerID, orderID uuid.UUID, code string) error {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var storedCode string
	if err := tx.QueryRow(ctx, `
		SELECT COALESCE(da.delivery_code, '')
		FROM food.delivery_assignments da
		JOIN food.orders o ON o.id = da.order_id
		WHERE da.order_id = $1 AND o.user_id = $2
		FOR UPDATE OF da
	`, orderID, customerID).Scan(&storedCode); err != nil {
		return err
	}
	if storedCode == "" {
		return fmt.Errorf("delivery_code not set")
	}
	if storedCode != code {
		return fmt.Errorf("invalid delivery_code")
	}
	if _, err := tx.Exec(ctx, `
		UPDATE food.delivery_assignments
		SET delivery_verified_at = NOW(), delivered_at = NOW(), status = 'DELIVERED'
		WHERE order_id = $1
	`, orderID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `
		UPDATE food.orders
		SET status = 'DELIVERED', delivered_at = NOW()
		WHERE id = $1 AND status IN ('OUT_FOR_DELIVERY','PICKED_UP')
	`, orderID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// AttachProofURL is the partner-side photo upload (MinIO key returned
// from a presign endpoint). `which` is `pickup` or `delivery`.
func (s *Store) AttachProofURL(ctx context.Context, userID, orderID uuid.UUID, which, url string) error {
	col := ""
	switch which {
	case "pickup":
		col = "proof_of_pickup_url"
	case "delivery":
		col = "proof_of_delivery_url"
	default:
		return fmt.Errorf("invalid proof type: %s", which)
	}
	tag, err := s.db.Exec(ctx, `
		UPDATE food.delivery_assignments da
		SET `+col+` = $3
		FROM food.delivery_partners dp
		WHERE da.order_id = $1 AND dp.id = da.delivery_partner_id AND dp.user_id = $2
	`, orderID, userID, url)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return pgx.ErrNoRows
	}
	return nil
}
