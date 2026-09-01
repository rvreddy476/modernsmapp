package postgres

// Persisted shipping quotes.
//
// Amendment A4 / review R-4. Two P0 rules collided:
//
//	LB-14  no network call happens before the checkout commit
//	D7     Shiprocket owns serviceability and the delivery rate
//
// Both can only hold if the rate is fetched BEFORE the transaction and
// merely consumed inside it. The naive version of that — "quote first, then
// charge whatever the quote said" — is worse than the hardcoded ₹40 it
// replaces, because it is silently stale:
//
//	quote cart+address A -> ₹70
//	customer edits the address to a different state
//	checkout charges ₹70 for a delivery that costs ₹170
//
// So the quote is bound to every input that could change its validity, and
// checkout re-checks each binding under lock. A quote whose cart version,
// address content, seller or item set has moved is refused, and the client
// re-quotes. The hash of address CONTENT matters as much as the id: editing
// an address in place leaves the id unchanged.

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/atpost/commerce-service/internal/money"
	"github.com/atpost/commerce-service/internal/tax"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// QuoteTTL bounds staleness. Short enough that a courier's rate has not
// moved, long enough for a customer to finish a checkout screen.
const QuoteTTL = 15 * time.Minute

// ShippingQuote is a persisted, single-use delivery price.
type ShippingQuote struct {
	ID             uuid.UUID
	UserID         uuid.UUID
	CartID         uuid.UUID
	CartVersion    int64
	AddressID      uuid.UUID
	AddressHash    string
	SellerID       uuid.UUID
	ItemsHash      string
	TotalWeightG   int
	DestinationPin string
	ShippingMinor  money.Paise
	CODAvailable   bool
	CourierCode    string
	ExpiresAt      time.Time
	ConsumedAt     *time.Time

	// C3-LB-2: the complete price this quote represents, and the request it
	// was taken for. Checkout refuses a quote whose coupon or payment method
	// differs from the one being redeemed — those change the price, and
	// reusing the quote across them charges a number the buyer never saw.
	SubtotalMinor money.Paise
	DiscountMinor money.Paise
	TaxMinor      money.Paise
	TotalMinor    money.Paise
	CouponCode    string
	PaymentMethod string
}

// SaveQuote persists a quote obtained from the courier adapter.
func (s *Store) SaveQuote(ctx context.Context, q ShippingQuote, providerPayload any) (*ShippingQuote, error) {
	payload, err := json.Marshal(providerPayload)
	if err != nil {
		payload = []byte(`{}`)
	}
	q.ID = uuid.New()
	q.ExpiresAt = time.Now().Add(QuoteTTL)
	_, err = s.db.Exec(ctx, `
		INSERT INTO shipping_quotes (
			id, user_id, cart_id, cart_version, address_id, address_hash,
			seller_id, items_hash, total_weight_g, destination_pin,
			currency, shipping_minor, cod_available, courier_code,
			provider_payload, expires_at,
			subtotal_minor, discount_minor, tax_minor, total_minor,
			coupon_code, payment_method)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,'INR',$11,$12,$13,$14,$15,
		        $16,$17,$18,$19,NULLIF($20,''),NULLIF($21,''))`,
		q.ID, q.UserID, q.CartID, q.CartVersion, q.AddressID, q.AddressHash,
		q.SellerID, q.ItemsHash, q.TotalWeightG, q.DestinationPin,
		q.ShippingMinor.Int64(), q.CODAvailable, q.CourierCode, payload, q.ExpiresAt,
		q.SubtotalMinor.Int64(), q.DiscountMinor.Int64(), q.TaxMinor.Int64(), q.TotalMinor.Int64(),
		q.CouponCode, q.PaymentMethod)
	if err != nil {
		return nil, fmt.Errorf("quote: save: %w", err)
	}
	return &q, nil
}

// lockQuote reads a quote FOR UPDATE inside the checkout transaction, so two
// concurrent checkouts cannot both consume it.
func lockQuote(ctx context.Context, tx pgx.Tx, id, userID uuid.UUID) (*ShippingQuote, error) {
	var q ShippingQuote
	err := tx.QueryRow(ctx, `
		SELECT id, user_id, cart_id, cart_version, address_id, address_hash,
		       seller_id, items_hash, total_weight_g, destination_pin,
		       shipping_minor, cod_available, COALESCE(courier_code,''),
		       expires_at, consumed_at,
		       COALESCE(subtotal_minor,0), COALESCE(discount_minor,0),
		       COALESCE(tax_minor,0), COALESCE(total_minor,0),
		       COALESCE(coupon_code,''), COALESCE(payment_method,'')
		  FROM shipping_quotes
		 WHERE id = $1 AND user_id = $2
		 FOR UPDATE`, id, userID).Scan(
		&q.ID, &q.UserID, &q.CartID, &q.CartVersion, &q.AddressID, &q.AddressHash,
		&q.SellerID, &q.ItemsHash, &q.TotalWeightG, &q.DestinationPin,
		&q.ShippingMinor, &q.CODAvailable, &q.CourierCode,
		&q.ExpiresAt, &q.ConsumedAt,
		&q.SubtotalMinor, &q.DiscountMinor, &q.TaxMinor, &q.TotalMinor,
		&q.CouponCode, &q.PaymentMethod)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrQuoteMismatch
		}
		return nil, err
	}
	return &q, nil
}

// hashLines fingerprints the cart's item set.
//
// Ordered by variant id (lockCartLines guarantees that ordering), so the
// same cart always hashes the same way and a quantity change always changes
// the hash. This is what stops "quote 1 unit, check out 5".
func hashLines(lines []cartLine) string {
	h := sha256.New()
	for _, l := range lines {
		h.Write([]byte(l.VariantID.String()))
		h.Write([]byte{'x'})
		h.Write([]byte(strconv.Itoa(l.Quantity)))
		h.Write([]byte{'|'})
	}
	return base64.RawURLEncoding.EncodeToString(h.Sum(nil))
}

// HashCartItems is the exported form used when a quote is created, so the
// quote and the checkout compute the fingerprint identically.
func HashCartItems(variantIDs []uuid.UUID, quantities []int) string {
	lines := make([]cartLine, len(variantIDs))
	for i := range variantIDs {
		lines[i] = cartLine{VariantID: variantIDs[i], Quantity: quantities[i]}
	}
	return hashLines(lines)
}

// HashAddress fingerprints an address's CONTENT.
//
// Binding a quote to the address id alone would miss an in-place edit: same
// id, different destination, stale rate. The plaintext never leaves the
// caller — this takes the already-decrypted fields and returns a digest.
func HashAddress(line1, line2, city, state, postal string) string {
	h := sha256.New()
	for _, p := range []string{line1, line2, city, state, postal} {
		h.Write([]byte(p))
		h.Write([]byte{0})
	}
	return base64.RawURLEncoding.EncodeToString(h.Sum(nil))
}

// CartMeta is what a quote request needs to know about the current cart.
type CartMeta struct {
	CartID     uuid.UUID
	Version    int64
	SellerID   uuid.UUID
	ItemsHash  string
	WeightG    int
	VariantIDs []uuid.UUID
	Quantities []int
}

// CartMetaForQuote reads the cart's identity and weight for a quote request.
//
// Deliberately NOT inside the checkout transaction: it feeds the courier
// call, which happens before checkout begins.
func (s *Store) CartMetaForQuote(ctx context.Context, userID uuid.UUID) (*CartMeta, error) {
	var m CartMeta
	err := s.db.QueryRow(ctx,
		`SELECT id, version FROM carts WHERE user_id = $1`, userID).Scan(&m.CartID, &m.Version)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrCartEmpty
		}
		return nil, err
	}
	rows, err := s.db.Query(ctx, `
		SELECT ci.variant_id, ci.quantity, p.seller_id,
		       COALESCE(v.weight_grams, p.weight_grams, 0)
		  FROM cart_items ci
		  JOIN product_variants v ON v.id = ci.variant_id
		  JOIN products p ON p.id = v.product_id
		 WHERE ci.cart_id = $1
		 ORDER BY ci.variant_id`, m.CartID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var (
			vid    uuid.UUID
			qty    int
			seller uuid.UUID
			weight int
		)
		if err := rows.Scan(&vid, &qty, &seller, &weight); err != nil {
			return nil, err
		}
		if m.SellerID == uuid.Nil {
			m.SellerID = seller
		} else if m.SellerID != seller {
			// D2: the cart should never have reached this state, but a
			// quote for a mixed cart would be meaningless, so refuse.
			return nil, ErrMultipleSellers
		}
		m.VariantIDs = append(m.VariantIDs, vid)
		m.Quantities = append(m.Quantities, qty)
		m.WeightG += weight * qty
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(m.VariantIDs) == 0 {
		return nil, ErrCartEmpty
	}
	m.ItemsHash = HashCartItems(m.VariantIDs, m.Quantities)
	return &m, nil
}

// ─── C3-LB-2: the quote carries the whole price ──────────────────────

// QuotePricing is the complete, server-computed breakdown a buyer is asked to
// accept. Every field is in paise and GST is INCLUDED in Total, never added
// to it (D1).
type QuotePricing struct {
	SubtotalMinor money.Paise
	DiscountMinor money.Paise
	ShippingMinor money.Paise
	TaxMinor      money.Paise
	TotalMinor    money.Paise
	Currency      string
}

// QuotePricingInput is what the breakdown depends on. Everything here is also
// bound into the persisted quote, so checkout can refuse a quote taken under
// different assumptions.
type QuotePricingInput struct {
	UserID           uuid.UUID
	CartID           uuid.UUID
	ShippingMinor    money.Paise
	CouponCode       string
	SellerState      string
	DestinationState string
}

// PriceCartForQuote computes what checkout WILL charge, without charging it.
//
// C3-LB-2. This is the whole point of the pass: the buyer is asked to approve
// a total, so the server that will charge that total has to be the one that
// states it. The Android client used to derive it as `0 + shipping`, which
// disagreed with checkout on every non-empty cart.
//
// It runs the SAME helpers as the checkout transaction — lockCartLines,
// lockAndPriceLines, evaluateCoupon, tax.Compute — because a second
// implementation of "what does this cart cost" is a second answer waiting to
// happen, and the disagreement would surface as a PRICE_CHANGED the buyer
// cannot resolve.
//
// Three things it deliberately does NOT do:
//
//	claim coupon capacity  a quote is not a promise; holding a one-use code
//	                       for everyone who opens a checkout screen would
//	                       exhaust it on the first person to look
//	reserve stock          same reason, and LB-14 puts reservation inside the
//	                       checkout transaction
//	commit                 the transaction exists only to take the same row
//	                       locks checkout takes, so the two read identical
//	                       catalogue state; it is always rolled back
//
// So this price can still be stale by the time checkout runs. That is
// expected and handled: checkout recomputes under its own transaction and
// returns PRICE_CHANGED. What must never happen is the client inventing a
// number nobody computed.
func (s *Store) PriceCartForQuote(ctx context.Context, in QuotePricingInput) (*QuotePricing, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, err
	}
	// Always. Nothing here may persist.
	defer tx.Rollback(ctx) //nolint:errcheck

	lines, err := lockCartLines(ctx, tx, in.CartID)
	if err != nil {
		return nil, err
	}
	if len(lines) == 0 {
		return nil, ErrCartEmpty
	}

	priced, sellerID, err := lockAndPriceLines(ctx, tx, lines)
	if err != nil {
		return nil, err
	}

	taxLines := make([]tax.Line, len(priced))
	for i, pl := range priced {
		taxLines[i] = tax.Line{
			Ref:            pl.VariantID.String(),
			GrossInclusive: pl.UnitMinor.MulQty(pl.Quantity),
			Rate:           pl.RateBP,
		}
	}
	subtotal := subtotalOf(taxLines)

	discount := money.Zero
	if in.CouponCode != "" {
		d, cErr := previewCoupon(ctx, tx, in.CouponCode, in.UserID, sellerID, subtotal, priced)
		if cErr != nil {
			// Surfaced, not swallowed. A quote that silently drops an
			// unusable coupon shows a total the buyer did not ask for.
			return nil, cErr
		}
		discount = d
	}

	// Same refusal as checkout: a quote that guesses the tax shows the buyer
	// a total the order will not be invoiced at.
	if strings.TrimSpace(in.SellerState) == "" || strings.TrimSpace(in.DestinationState) == "" {
		return nil, fmt.Errorf("%w: seller state %q, destination state %q",
			ErrPlaceOfSupplyUnknown, in.SellerState, in.DestinationState)
	}
	interstate := !equalFoldState(in.SellerState, in.DestinationState)

	computed, err := tax.Compute(tax.Input{
		Lines:         taxLines,
		OrderDiscount: discount,
		Shipping:      in.ShippingMinor,
		Interstate:    interstate,
	})
	if err != nil {
		return nil, fmt.Errorf("quote: pricing: %w", err)
	}

	return &QuotePricing{
		SubtotalMinor: subtotal,
		DiscountMinor: discount,
		ShippingMinor: in.ShippingMinor,
		TaxMinor:      computed.TotalTax,
		TotalMinor:    computed.Total,
		Currency:      "INR",
	}, nil
}
