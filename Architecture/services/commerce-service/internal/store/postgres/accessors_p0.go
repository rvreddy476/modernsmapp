package postgres

// Reads the P0 checkout path needs.
//
// These are deliberately narrow: each returns exactly the fact its caller
// needs and nothing else. The alternative — loading a fat model and picking
// a field off it — is how `Order.FinalAmount` (a float64 rupee value) ended
// up being read by code that then multiplied it by 100 in four different
// places with three different rounding behaviours.

import (
	"context"
	"errors"

	"github.com/atpost/commerce-service/internal/money"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// AddressRow is a customer address as stored, with both the plaintext
// columns (during the dual-write window) and the ciphertext ones.
type AddressRow struct {
	ID     uuid.UUID
	UserID uuid.UUID

	// Plaintext columns. Present only until migration 013's contraction
	// removes them; the service prefers the ciphertext when it exists.
	ContactName  string
	Phone        string
	AddressLine1 string
	AddressLine2 string
	Landmark     string

	ContactNameEnc  []byte
	PhoneEnc        []byte
	AddressLine1Enc []byte
	AddressLine2Enc []byte
	LandmarkEnc     []byte
	KeyVersion      int

	// Never encrypted: serviceability and the interstate GST determination
	// need these in a WHERE clause, and none identifies a person alone.
	City       string
	State      string
	PostalCode string
	Country    string
}

// GetAddressRow loads one address. Ownership is checked by the CALLER —
// this returns the row so the caller can compare and produce the right
// error, rather than conflating "not found" with "not yours".
func (s *Store) GetAddressRow(ctx context.Context, id uuid.UUID) (*AddressRow, error) {
	var a AddressRow
	err := s.db.QueryRow(ctx, `
		SELECT id, user_id,
		       COALESCE(contact_name,''), COALESCE(phone,''),
		       COALESCE(address_line_1,''), COALESCE(address_line_2,''), COALESCE(landmark,''),
		       contact_name_enc, phone_enc, address_line_1_enc, address_line_2_enc, landmark_enc,
		       COALESCE(pii_key_version,0),
		       city, state, postal_code, COALESCE(country,'IN')
		  FROM customer_addresses WHERE id = $1`, id).Scan(
		&a.ID, &a.UserID,
		&a.ContactName, &a.Phone, &a.AddressLine1, &a.AddressLine2, &a.Landmark,
		&a.ContactNameEnc, &a.PhoneEnc, &a.AddressLine1Enc, &a.AddressLine2Enc, &a.LandmarkEnc,
		&a.KeyVersion,
		&a.City, &a.State, &a.PostalCode, &a.Country)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrAddressNotOwned
		}
		return nil, err
	}
	return &a, nil
}

// SellerStateForCart returns the place of supply of the cart's seller.
//
// It decides CGST+SGST versus IGST. D2 guarantees one seller per cart, so
// there is exactly one answer; a cart that somehow holds two sellers is a
// data error and is reported rather than guessed at.
//
// ─── WHY THE PICKUP ADDRESS AND NOT `sellers.state` ─────────────────────
//
// This read `sellers.state` alone, and every quote from a seller onboarded
// through the app failed with ErrPlaceOfSupplyUnknown.
//
// `sellers.state` is written by exactly two paths: the legacy
// `POST /sellers/onboard` insert, and `PUT /onboarding/step/basic`. The P0
// app calls neither — its journey is start → seller/address → payout →
// documents → submit — so the column stays empty and the seller half of the
// place-of-supply comparison was never populated. The address the seller DID
// give went to `seller_addresses`, which this query did not look at.
//
// The give-away is that the two halves of one address were read from two
// different tables: SellerPickupPin (a dozen lines below) already resolves
// the postcode from `seller_addresses`, which is why the courier call
// succeeded on the same request whose tax determination failed. The origin
// of a shipment and the place of supply for its GST are the same address, so
// they are now read the same way — same table, same address types, same
// ordering, same fallback.
//
// The fallback to `sellers.state` is for sellers onboarded through the older
// wizard, whose registered state is the only one they have. Pickup wins when
// both exist: it is where the goods actually move from.
func (s *Store) SellerStateForCart(ctx context.Context, userID uuid.UUID) (string, error) {
	rows, err := s.db.Query(ctx, `
		SELECT DISTINCT COALESCE(NULLIF(addr.state, ''), NULLIF(sel.state, ''), '')
		  FROM cart_items ci
		  JOIN carts c ON c.id = ci.cart_id
		  JOIN products p ON p.id = ci.product_id
		  JOIN sellers sel ON sel.id = p.seller_id
		  LEFT JOIN LATERAL (
		      SELECT sa.state
		        FROM seller_addresses sa
		       WHERE sa.seller_id = sel.id
		         AND sa.address_type IN ('pickup','warehouse','business')
		       ORDER BY (sa.address_type = 'pickup') DESC, sa.is_default DESC
		       LIMIT 1
		  ) addr ON TRUE
		 WHERE c.user_id = $1`, userID)
	if err != nil {
		return "", err
	}
	defer rows.Close()
	var states []string
	for rows.Next() {
		var st string
		if err := rows.Scan(&st); err != nil {
			return "", err
		}
		states = append(states, st)
	}
	if err := rows.Err(); err != nil {
		return "", err
	}
	switch len(states) {
	case 0:
		return "", ErrCartEmpty
	case 1:
		return states[0], nil
	default:
		return "", ErrMultipleSellers
	}
}

// SellerForOrder returns the seller an order belongs to.
//
// D4: this is `sellers.id`, and it is what becomes the payment payee. It is
// NOT a user id — the two namespaces were being compared directly in several
// authorization checks (M-9), which both denied legitimate sellers and would
// have opened cross-seller access under a naive fix.
func (s *Store) SellerForOrder(ctx context.Context, orderID uuid.UUID) (uuid.UUID, error) {
	rows, err := s.db.Query(ctx,
		`SELECT DISTINCT seller_id FROM order_items WHERE order_id = $1`, orderID)
	if err != nil {
		return uuid.Nil, err
	}
	defer rows.Close()
	var ids []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return uuid.Nil, err
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return uuid.Nil, err
	}
	switch len(ids) {
	case 0:
		return uuid.Nil, ErrOrderNotFoundP0
	case 1:
		return ids[0], nil
	default:
		return uuid.Nil, ErrMultipleSellers
	}
}

// OrderTotalMinor returns the authoritative payable amount in paise.
//
// LB-4: this is the ONLY number that may be sent to payments. The client
// never proposes an amount, and every verification compares against this.
func (s *Store) OrderTotalMinor(ctx context.Context, orderID uuid.UUID) (money.Paise, error) {
	var v money.Paise
	err := s.db.QueryRow(ctx,
		`SELECT COALESCE(final_amount_minor, 0) FROM orders WHERE id = $1`, orderID).Scan(&v)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return 0, ErrOrderNotFoundP0
		}
		return 0, err
	}
	if v <= 0 {
		// A zero payable would open a ₹0 intent, which is both meaningless
		// and a plausible symptom of the money migration having missed a
		// write path. Refuse rather than charge nothing.
		return 0, errors.New("order has no positive minor total; refusing to open a payment")
	}
	return v, nil
}

// SellerPickupPin returns the seller's pickup pincode for a rate quote.
func (s *Store) SellerPickupPin(ctx context.Context, sellerID uuid.UUID) (string, error) {
	var pin string
	err := s.db.QueryRow(ctx, `
		SELECT COALESCE(postal_code,'')
		  FROM seller_addresses
		 WHERE seller_id = $1 AND address_type IN ('pickup','warehouse','business')
		 ORDER BY (address_type = 'pickup') DESC, is_default DESC
		 LIMIT 1`, sellerID).Scan(&pin)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			// Fall back to the seller's own registered postcode.
			if err2 := s.db.QueryRow(ctx,
				`SELECT COALESCE(postal_code,'') FROM sellers WHERE id = $1`, sellerID).Scan(&pin); err2 == nil {
				return pin, nil
			}
			return "", nil
		}
		return "", err
	}
	return pin, nil
}

// SellerIDForUser resolves a user to their seller profile.
//
// M-9. Several write paths compared `sellers.id` directly against an
// authenticated user UUID — two different namespaces, so a legitimate seller
// was denied, and "fixing" it by comparing the other way would have let one
// seller act for another. Every seller-scoped check now goes through this
// one resolution.
func (s *Store) SellerIDForUser(ctx context.Context, userID uuid.UUID) (uuid.UUID, error) {
	var id uuid.UUID
	err := s.db.QueryRow(ctx,
		`SELECT id FROM sellers WHERE user_id = $1`, userID).Scan(&id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return uuid.Nil, ErrNotOrderOwnerP0
		}
		return uuid.Nil, err
	}
	return id, nil
}

// SellerOwnsOrder reports whether a USER's seller profile owns any line of
// an order. Used to gate the seller fulfilment surface.
func (s *Store) SellerOwnsOrder(ctx context.Context, userID, orderID uuid.UUID) (bool, error) {
	sellerID, err := s.SellerIDForUser(ctx, userID)
	if err != nil {
		return false, err
	}
	var n int
	if err := s.db.QueryRow(ctx,
		`SELECT count(*) FROM order_items WHERE order_id = $1 AND seller_id = $2`,
		orderID, sellerID).Scan(&n); err != nil {
		return false, err
	}
	return n > 0, nil
}

// CartSellerID returns the single seller a cart is bound to, or uuid.Nil for
// an empty cart. D2: add-to-cart uses this to refuse a second seller.
func (s *Store) CartSellerID(ctx context.Context, userID uuid.UUID) (uuid.UUID, error) {
	var id *uuid.UUID
	err := s.db.QueryRow(ctx, `
		SELECT DISTINCT p.seller_id
		  FROM cart_items ci
		  JOIN carts c ON c.id = ci.cart_id
		  JOIN products p ON p.id = ci.product_id
		 WHERE c.user_id = $1
		 LIMIT 1`, userID).Scan(&id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return uuid.Nil, nil
		}
		return uuid.Nil, err
	}
	if id == nil {
		return uuid.Nil, nil
	}
	return *id, nil
}

// ProductSaleEligibility reports whether a product may be sold right now.
//
// LB-17 / v1 §5.6. AddToCart checked only the VARIANT's status, and
// priceCart checked neither, so a product could be bought while
// `approval_status = 'pending'` or after an admin rejected it — the entire
// moderation queue was advisory. This is the add-to-cart guard; the
// authoritative re-check happens inside the checkout transaction, because a
// product can be rejected in between.
func (s *Store) ProductSaleEligibility(ctx context.Context, variantID uuid.UUID) (sellerID uuid.UUID, ok bool, err error) {
	var pStatus, pApproval, vStatus string
	err = s.db.QueryRow(ctx, `
		SELECT p.seller_id, p.status, p.approval_status, v.status
		  FROM product_variants v
		  JOIN products p ON p.id = v.product_id
		 WHERE v.id = $1`, variantID).Scan(&sellerID, &pStatus, &pApproval, &vStatus)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return uuid.Nil, false, ErrProductUnavailable
		}
		return uuid.Nil, false, err
	}
	return sellerID, pStatus == "active" && pApproval == "approved" && vStatus == "active", nil
}

// UnpublishedOutboxCount feeds the outbox-backlog gauge.
//
// A rising backlog means domain events are not reaching consumers:
// notifications stop, search goes stale, and — most importantly — the
// payment events that drive order state stop flowing.
func (s *Store) UnpublishedOutboxCount(ctx context.Context) (int64, error) {
	var n int64
	err := s.db.QueryRow(ctx,
		`SELECT count(*) FROM outbox_events WHERE published_at IS NULL`).Scan(&n)
	return n, err
}
