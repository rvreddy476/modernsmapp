package postgres

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"errors"
	"fmt"
	"math/big"

	"github.com/atpost/food-service/internal/orderstate"
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

// MaxPickupCodeAttempts is how many wrong pickup codes one assignment takes
// before VerifyPickupCode refuses every further try. Like the delivery code it
// is four digits, so without a cap a kitchen terminal could walk it.
const MaxPickupCodeAttempts = 5

// ErrPickupCodeLocked: the assignment took MaxPickupCodeAttempts wrong pickup
// codes. HTTP 429 FOOD_PICKUP_CODE_ATTEMPTS_EXCEEDED.
var ErrPickupCodeLocked = errors.New("too many wrong pickup codes for this assignment")

// VerifyPickupCode is the restaurant-side OTP check. The partner reads
// the OTP off their screen and the restaurant agent (or the restaurant
// terminal) submits it here.
//
// Sets pickup_verified_at + transitions order to PICKED_UP.
//
// Only the restaurant's owner may verify (anyone else gets pgx.ErrNoRows and
// is not counted), only while the rider holds an accepted job, and only while
// fewer than MaxPickupCodeAttempts wrong codes were entered; a wrong code is
// counted and committed before the refusal is returned.
func (s *Store) VerifyPickupCode(ctx context.Context, ownerID, orderID uuid.UUID, code string) error {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var storedCode, restaurantID, assignmentStatus, orderStatus string
	var partnerID *uuid.UUID
	var failed int
	if err := tx.QueryRow(ctx, `
		SELECT COALESCE(da.pickup_code, ''), o.restaurant_id::text, da.status::text,
			da.delivery_partner_id, o.status::text, da.pickup_code_failed_attempts
		FROM food.delivery_assignments da
		JOIN food.orders o ON o.id = da.order_id
		WHERE da.order_id = $1
		FOR UPDATE OF da
	`, orderID).Scan(&storedCode, &restaurantID, &assignmentStatus, &partnerID, &orderStatus, &failed); err != nil {
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
	// The job must be held by a partner who ACCEPTED it and has not picked it
	// up yet. ASSIGNED (an offer accept the rider has not confirmed) is not
	// enough: the rider sees pickup_code only once they accept
	// (PickupCodeVisible), and a code verified before then proves nothing.
	if partnerID == nil {
		return fmt.Errorf("%w: no delivery partner holds this order", ErrAssignmentNotReady)
	}
	switch assignmentStatus {
	case "ACCEPTED", "ARRIVED_AT_RESTAURANT":
	default:
		return fmt.Errorf("%w: assignment is %s", ErrAssignmentNotReady, assignmentStatus)
	}
	if failed >= MaxPickupCodeAttempts {
		return ErrPickupCodeLocked
	}
	if !codeMatches(storedCode, code) {
		if _, err := tx.Exec(ctx, `
			UPDATE food.delivery_assignments
			SET pickup_code_failed_attempts = pickup_code_failed_attempts + 1
			WHERE order_id = $1
		`, orderID); err != nil {
			return err
		}
		if err := tx.Commit(ctx); err != nil {
			return err
		}
		return ErrDeliveryCodeInvalid
	}
	if _, err := tx.Exec(ctx, `
		UPDATE food.delivery_assignments
		SET status = 'PICKED_UP', pickup_verified_at = NOW(), picked_up_at = NOW()
		WHERE order_id = $1
	`, orderID); err != nil {
		return err
	}
	if err := transitionOrderTx(ctx, tx, OrderTransition{
		OrderID: orderID, From: orderStatus, To: orderstate.PickedUp,
		Actor: orderstate.ActorRestaurant, ChangedBy: &ownerID, Reason: "pickup code verified",
	}); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// codeMatches compares OTPs in constant time; an unset code never matches.
func codeMatches(stored, supplied string) bool {
	if stored == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(stored), []byte(supplied)) == 1
}

// MaxDeliveryCodeAttempts is how many wrong delivery codes one assignment
// takes before RiderVerifyDeliveryCode refuses every further try. The code is
// four digits and the rider, not the customer, now submits it, so without a
// cap it could be walked in minutes under the gateway's per-user rate limit.
const MaxDeliveryCodeAttempts = 5

// ErrDeliveryCodeLocked: the assignment took MaxDeliveryCodeAttempts wrong
// delivery codes. HTTP 429 FOOD_DELIVERY_CODE_ATTEMPTS_EXCEEDED.
var ErrDeliveryCodeLocked = errors.New("too many wrong delivery codes for this assignment")

// DeliveryVerification is what a successful rider delivery verify reports.
type DeliveryVerification struct {
	OrderID    uuid.UUID
	CustomerID uuid.UUID
}

// RiderVerifyDeliveryCode is the drop-off handover: the customer shows the
// delivery code (it is on their order detail while the food is with the
// rider) and the rider holding the assignment enters it, exactly as the
// restaurant enters the rider's pickup code. Sets delivery_verified_at and
// moves the order to DELIVERED.
//
// Only the ACTIVE partner who holds assignmentID may verify it (anyone else
// gets pgx.ErrNoRows), only after pickup, and only while fewer than
// MaxDeliveryCodeAttempts wrong codes were entered; a wrong code is counted
// and committed before the refusal is returned.
func (s *Store) RiderVerifyDeliveryCode(ctx context.Context, riderUserID, assignmentID uuid.UUID, code string) (*DeliveryVerification, error) {
	partner, err := s.GetDeliveryPartner(ctx, riderUserID)
	if err != nil {
		return nil, err
	}
	if partner.Status != "ACTIVE" {
		return nil, ErrDeliveryPartnerNotActive
	}
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	var storedCode, assignmentStatus, orderStatus string
	var failed int
	var v DeliveryVerification
	if err := tx.QueryRow(ctx, `
		SELECT COALESCE(da.delivery_code, ''), da.status::text, da.delivery_code_failed_attempts,
			o.id, o.user_id, o.status::text
		FROM food.delivery_assignments da
		JOIN food.orders o ON o.id = da.order_id
		WHERE da.id = $1 AND da.delivery_partner_id = $2
		FOR UPDATE OF da
	`, assignmentID, partner.ID).Scan(&storedCode, &assignmentStatus, &failed, &v.OrderID, &v.CustomerID, &orderStatus); err != nil {
		return nil, err
	}
	// Only after pickup: a leaked code cannot mark an order delivered early.
	if assignmentStatus != "PICKED_UP" && assignmentStatus != "ARRIVED_AT_CUSTOMER" {
		return nil, fmt.Errorf("%w: assignment is %s", ErrAssignmentNotReady, assignmentStatus)
	}
	if failed >= MaxDeliveryCodeAttempts {
		return nil, ErrDeliveryCodeLocked
	}
	if !codeMatches(storedCode, code) {
		if _, err := tx.Exec(ctx, `
			UPDATE food.delivery_assignments
			SET delivery_code_failed_attempts = delivery_code_failed_attempts + 1
			WHERE id = $1
		`, assignmentID); err != nil {
			return nil, err
		}
		if err := tx.Commit(ctx); err != nil {
			return nil, err
		}
		return nil, ErrDeliveryCodeInvalid
	}
	if _, err := tx.Exec(ctx, `
		UPDATE food.delivery_assignments
		SET delivery_verified_at = NOW(), delivered_at = NOW(), status = 'DELIVERED'
		WHERE id = $1
	`, assignmentID); err != nil {
		return nil, err
	}
	if orderStatus == orderstate.PickedUp {
		// The rider skipped "arrived at customer"; record the hop honestly.
		if err := transitionOrderTx(ctx, tx, OrderTransition{
			OrderID: v.OrderID, From: orderstate.PickedUp, To: orderstate.OutForDelivery,
			Actor: orderstate.ActorSystem, Reason: "delivery code verified before arrival was marked",
		}); err != nil {
			return nil, err
		}
		orderStatus = orderstate.OutForDelivery
	}
	if err := transitionOrderTx(ctx, tx, OrderTransition{
		OrderID: v.OrderID, From: orderStatus, To: orderstate.Delivered,
		Actor: orderstate.ActorDeliveryPartner, ChangedBy: &riderUserID, Reason: "delivery code verified",
	}); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return &v, nil
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
