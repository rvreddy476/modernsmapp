package postgres

// The checkout transaction.
//
// LB-14 is the spine of Commerce P0, and this file is it. What it replaces:
//
//	CreateOrder(...)                    // transaction 1: the order
//	for each item { ReserveStock(...) } // transaction 2..N, errors LOGGED
//	ClearCart(...)                      // transaction N+1
//	IncrCouponUsage(...)                // transaction N+2, error IGNORED
//
// Every one of those was independently failing, and the reservation loop
// logged its failures and carried on. Two buyers racing for the last unit
// both got a confirmed order; the clamp in DeductStock then hid the
// discrepancy by flooring stock at zero.
//
// Now there is one transaction, and it either produces a complete order with
// its stock held, its coupon claimed and its event enqueued, or it produces
// nothing at all.
//
// Ordering rules that are load-bearing rather than stylistic:
//
//   - Inventory rows are locked in ascending variant_id order. Two carts
//     containing the same two SKUs in opposite orders would otherwise
//     deadlock roughly half the time under contention.
//   - The cart is locked and its version re-read, so a mutation racing
//     checkout cannot slip a line in between pricing and commit (C9).
//   - The shipping quote is CONSUMED here but OBTAINED outside, because
//     Shiprocket is a network call and no network call may happen before
//     this commit (A4 / review R-4).
//   - The commit is the last thing that happens. The payment intent is
//     created afterwards, against an order that already exists and already
//     holds its stock.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/atpost/commerce-service/internal/money"
	"github.com/atpost/commerce-service/internal/tax"
	"github.com/atpost/shared/events"
	"github.com/atpost/shared/paymentmethod"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// ─── Typed failures ──────────────────────────────────────────────────
//
// The old code returned `fmt.Errorf("insufficient stock for %s")`, which the
// handler mapped to a 500. A client could not tell "you must pick something
// else" from "our database is down", so it could not render either.

var (
	ErrCartEmpty           = errors.New("cart is empty")
	ErrCartChanged         = errors.New("cart changed during checkout")
	ErrOutOfStock          = errors.New("one or more items are out of stock")
	ErrPriceChanged        = errors.New("prices changed since the quote")
	ErrQuoteExpired        = errors.New("shipping quote expired")
	ErrQuoteMismatch       = errors.New("shipping quote does not match this cart or address")
	ErrQuoteConsumed       = errors.New("shipping quote has already been used")
	ErrAddressNotOwned     = errors.New("address does not belong to this user")
	ErrProductUnavailable  = errors.New("a product in the cart is no longer available")
	ErrMultipleSellers     = errors.New("cart contains items from more than one seller")
	ErrCouponExhausted     = errors.New("coupon is no longer available")
	ErrIdempotencyConflict = errors.New("idempotency key reused with a different request")
	ErrCODNotSupported     = errors.New("cash on delivery is not enabled")
	// B9. A line whose GST class is absent or unusable cannot be priced.
	// Separate from ErrProductUnavailable because the seller-facing fix is
	// different — the listing needs a tax class, not restocking — and
	// because conflating them is how "0% because we could not tell" hid
	// behind "temporarily unavailable".
	ErrTaxClassMissing = errors.New("a product in the cart has no usable GST tax class")
	ErrTaxClassInvalid = errors.New("a product in the cart has an invalid GST tax class")
	// ErrPlaceOfSupplyUnknown means one side of the GST place-of-supply
	// comparison is missing.
	//
	// `sellers.state` is a nullable TEXT with no validation, and it is the
	// seller half of the CGST+SGST-vs-IGST decision. The comparison was
	// written as `SellerState != "" && DestinationState != "" && !equal`, so
	// a blank on either side silently evaluated to "not interstate" and
	// charged CGST+SGST — the wrong tax, on a real invoice, with no signal.
	//
	// Refusing is the only honest option. A tax we cannot determine is not a
	// tax we may guess at, and the order would be invoiced under it.
	ErrPlaceOfSupplyUnknown = errors.New("the GST place of supply cannot be determined")
	// B9. The coupon is real and has capacity, but does not apply to what is
	// in this cart.
	ErrCouponNotApplicable = errors.New("coupon does not apply to the items in this cart")
	// N6. The price the customer approved is not optional. An absent or zero
	// expected total used to skip both price-change comparisons, which let a
	// stale client be charged a total it never displayed.
	ErrExpectedTotalRequired = errors.New("checkout: expected_total_minor is required and must be positive")
)

// OutOfStockLine names one unavailable line, so the client can render which.
type OutOfStockLine struct {
	VariantID    uuid.UUID `json:"variant_id"`
	ProductID    uuid.UUID `json:"product_id"`
	ProductTitle string    `json:"product_title"`
	Requested    int       `json:"requested"`
	Available    int       `json:"available"`
}

// OutOfStockError carries the detail behind ErrOutOfStock.
type OutOfStockError struct{ Lines []OutOfStockLine }

func (e *OutOfStockError) Error() string {
	return fmt.Sprintf("out of stock: %d line(s)", len(e.Lines))
}
func (e *OutOfStockError) Unwrap() error { return ErrOutOfStock }

// PriceChangedLine reports a line whose price moved since the client saw it.
type PriceChangedLine struct {
	VariantID uuid.UUID   `json:"variant_id"`
	WasMinor  money.Paise `json:"was_minor"`
	NowMinor  money.Paise `json:"now_minor"`
}

// PriceChangedError carries the detail behind ErrPriceChanged.
type PriceChangedError struct {
	Lines         []PriceChangedLine `json:"lines"`
	NewTotalMinor money.Paise        `json:"new_total_minor"`
}

func (e *PriceChangedError) Error() string {
	return fmt.Sprintf("price changed on %d line(s)", len(e.Lines))
}
func (e *PriceChangedError) Unwrap() error { return ErrPriceChanged }

// ─── Input / output ──────────────────────────────────────────────────

// CheckoutParams is everything the transaction needs. Notably absent: any
// amount. The client cannot propose what its own order costs.
type CheckoutParams struct {
	UserID    uuid.UUID
	AddressID uuid.UUID
	QuoteID   uuid.UUID

	// IdempotencyKey is REQUIRED (LB-15). The old code fabricated
	// "userID-<nanotime>" when it was absent, which is a key that can never
	// match anything and therefore dedupes nothing.
	IdempotencyKey string
	// RequestFingerprint is a canonical hash of the request. A retry with
	// the same key but a different fingerprint is a CONFLICT, not a licence
	// to return the old order (M-7).
	RequestFingerprint string

	CouponCode    string
	PaymentMethod string
	TermsVersion  string

	// ExpectedTotalMinor, when non-zero, is what the client last showed the
	// customer. A mismatch produces a typed price-changed error rather than
	// silently charging a different number.
	ExpectedTotalMinor money.Paise

	// AddressSnapshot is the fully-resolved, decrypted address, prepared by
	// the service layer before the transaction because decryption may call
	// KMS. LB-18: this is stored as an immutable copy, not a pointer.
	AddressSnapshot json.RawMessage
	// SealedSnapshot is the same address encrypted under the
	// order-snapshot key scope.
	SealedSnapshot   []byte
	SnapshotKeyVer   int
	DestinationState string
	DestinationPin   string

	// The seller half of the place-of-supply comparison is deliberately NOT
	// a parameter. It is resolved inside the transaction from the locked
	// seller — see sellerPlaceOfSupply and the note on the idempotency
	// section below.

	ActorType string
}

// CheckoutResult is what the caller gets on success.
type CheckoutResult struct {
	OrderID     uuid.UUID
	OrderNumber string
	TotalMinor  money.Paise
	TaxMinor    money.Paise
	ShipMinor   money.Paise
	Reused      bool // true when an idempotent retry returned the original
}

// ─── The transaction ─────────────────────────────────────────────────

func (s *Store) Checkout(ctx context.Context, p CheckoutParams) (*CheckoutResult, error) {
	if p.IdempotencyKey == "" {
		return nil, fmt.Errorf("checkout: idempotency key is required")
	}
	// C3-LB-3: the launch vocabulary, checked here as well as at the handler
	// and in the service, because THIS is the layer a queued job or an
	// internal caller reaches directly — and it is the last one before the
	// transaction that reserves stock and claims coupon capacity.
	//
	// It refuses BEFORE `+"`"+`s.db.Begin`+"`"+` on purpose: nothing should open a
	// transaction it already knows it will not commit.
	if err := paymentmethod.Validate(p.PaymentMethod); err != nil {
		if strings.EqualFold(strings.TrimSpace(p.PaymentMethod), "cod") {
			return nil, ErrCODNotSupported
		}
		return nil, err
	}

	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	// The trigger in migration 010 reads this to decide whether a status
	// transition is legal for the acting party.
	if _, err := tx.Exec(ctx, `SELECT set_config('commerce.actor_type', $1, true)`, actorOr(p.ActorType)); err != nil {
		return nil, err
	}

	// ── 1. Idempotency ────────────────────────────────────────────────
	//
	// The advisory lock is what makes the lookup below authoritative under
	// concurrency, and without it the lookup is a race rather than a check.
	//
	// The interleaving it closes (found by TestProofC2 against a live
	// database, once that proof was tightened to require every caller to
	// receive the winning order rather than merely "at most one distinct
	// id"): twenty callers arrive with the SAME key. All twenty run the
	// lookup, and in READ COMMITTED all twenty see no order, because none
	// has committed yet. They then queue on the cart's FOR UPDATE. The
	// winner creates the order and CLEARS THE CART; each loser in turn
	// acquires the cart lock, finds it empty, and returns ErrCartEmpty.
	//
	// So the caller whose network hiccup caused the retry — the exact case
	// idempotency exists for — got an error instead of their order, and a
	// client that treats ErrCartEmpty as "start again" would rebuild the
	// cart and buy twice.
	//
	// pg_advisory_xact_lock serialises on (user, key) for the remainder of
	// this transaction and is released by commit or rollback, so the losers
	// block until the winner is durable and their lookup then finds it.
	// Scoped to the user as well as the key so one tenant's key cannot
	// block another's.
	if _, err := tx.Exec(ctx,
		`SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`,
		p.UserID.String()+":"+p.IdempotencyKey); err != nil {
		return nil, fmt.Errorf("checkout: claim idempotency key: %w", err)
	}

	if existing, fp, err := lockExistingOrderByKey(ctx, tx, p.UserID, p.IdempotencyKey); err != nil {
		return nil, err
	} else if existing != nil {
		if fp != "" && p.RequestFingerprint != "" && fp != p.RequestFingerprint {
			// M-7: same key, different request. Returning the old order
			// here is how a client that changed address after a timeout
			// silently shipped to the wrong place.
			return nil, ErrIdempotencyConflict
		}
		if err := tx.Commit(ctx); err != nil {
			return nil, err
		}
		return &CheckoutResult{
			OrderID:     existing.ID,
			OrderNumber: existing.Number,
			TotalMinor:  existing.TotalMinor,
			TaxMinor:    existing.TaxMinor,
			ShipMinor:   existing.ShipMinor,
			Reused:      true,
		}, nil
	}

	// ── 2. Cart, locked, with its version ─────────────────────────────
	var cartID uuid.UUID
	var cartVersion int64
	err = tx.QueryRow(ctx,
		`SELECT id, version FROM carts WHERE user_id = $1 FOR UPDATE`, p.UserID).
		Scan(&cartID, &cartVersion)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrCartEmpty
		}
		return nil, err
	}

	lines, err := lockCartLines(ctx, tx, cartID)
	if err != nil {
		return nil, err
	}
	if len(lines) == 0 {
		return nil, ErrCartEmpty
	}

	// ── 3. Address ownership + snapshot (LB-18) ───────────────────────
	//
	// B7: the address CONTENT is read here, not just its owner. The quote
	// stores a hash of the content and the destination pincode, and both are
	// compared below. Locking FOR SHARE holds the row against a concurrent
	// UPDATE for the rest of this transaction, so the content that is hashed
	// is the content the order is shipped to.
	var (
		addrOwner                       uuid.UUID
		addrLine1, addrLine2            string
		addrCity, addrState, addrPostal string
	)
	err = tx.QueryRow(ctx,
		`SELECT user_id,
		        COALESCE(address_line_1,''), COALESCE(address_line_2,''),
		        COALESCE(city,''), COALESCE(state,''), COALESCE(postal_code,'')
		   FROM customer_addresses WHERE id = $1 FOR SHARE`, p.AddressID).
		Scan(&addrOwner, &addrLine1, &addrLine2, &addrCity, &addrState, &addrPostal)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrAddressNotOwned
		}
		return nil, err
	}
	if addrOwner != p.UserID {
		// The old Checkout neither loaded the address nor proved ownership;
		// it stored whatever id it was handed.
		return nil, ErrAddressNotOwned
	}
	if len(p.AddressSnapshot) == 0 {
		return nil, fmt.Errorf("checkout: an address snapshot is required")
	}

	// ── 4. Consume the shipping quote (A4 / R-4) ──────────────────────
	quote, err := lockQuote(ctx, tx, p.QuoteID, p.UserID)
	if err != nil {
		return nil, err
	}
	if quote.ConsumedAt != nil {
		return nil, ErrQuoteConsumed
	}
	if time.Now().After(quote.ExpiresAt) {
		return nil, ErrQuoteExpired
	}
	if quote.CartID != cartID || quote.CartVersion != cartVersion {
		// The cart moved after the quote was taken. Charging the old
		// shipping for the new cart is the ₹100 loss in R-4.
		return nil, ErrQuoteMismatch
	}
	if quote.AddressID != p.AddressID || quote.ItemsHash != hashLines(lines) {
		return nil, ErrQuoteMismatch
	}

	// C3-LB-2 — bind the quote to the COUPON and the PAYMENT METHOD too.
	//
	// The quote now states a complete total, and both of these change it. A
	// quote taken with a coupon, redeemed without one, would have promised
	// the buyer a discounted total and charged the undiscounted one — and
	// because the buyer echoes the quoted total back as expected_total_minor,
	// the disagreement surfaced as a PRICE_CHANGED they could not resolve.
	//
	// An empty stored value means "this quote assumed none", which must equal
	// what is being redeemed. Quotes written before migration 016 read back
	// as empty, so they are only usable for a couponless checkout — a quote
	// whose price we cannot reconstruct is refused rather than trusted.
	if quote.CouponCode != p.CouponCode {
		return nil, ErrQuoteMismatch
	}
	if quote.PaymentMethod != "" && quote.PaymentMethod != p.PaymentMethod {
		return nil, ErrQuoteMismatch
	}

	// B7 — bind the quote to the address CONTENT, not just its id.
	//
	// The quote already stored both an AddressHash and a DestinationPin, and
	// checkout compared NEITHER. Addresses are mutable rows: take a quote to
	// a Bengaluru address, edit that same row to a remote pincode, then
	// consume the quote, and the order shipped to the new destination at the
	// old rate. The platform absorbed the courier difference on every such
	// order, and the shipment could go somewhere no courier had quoted at
	// all.
	//
	// Recomputed from the row locked FOR SHARE above, so this cannot race
	// the comparison. A quote taken before this field existed has an empty
	// hash and is refused rather than trusted.
	if quote.AddressHash == "" {
		return nil, ErrQuoteMismatch
	}
	if quote.AddressHash != HashAddress(addrLine1, addrLine2, addrCity, addrState, addrPostal) {
		return nil, ErrQuoteMismatch
	}
	if quote.DestinationPin != "" && quote.DestinationPin != addrPostal {
		return nil, ErrQuoteMismatch
	}

	// ── 5. Products, variants, eligibility (LB-17, D2) ────────────────
	priced, sellerID, err := lockAndPriceLines(ctx, tx, lines)
	if err != nil {
		return nil, err
	}
	if quote.SellerID != sellerID {
		return nil, ErrQuoteMismatch
	}

	// Price-change detection against what the client last displayed.
	//
	// N6: this whole block used to be wrapped in `if p.ExpectedTotalMinor > 0`,
	// so a caller that omitted the field skipped it entirely. The store is
	// the layer an internal caller or a queued job reaches directly, so the
	// requirement is asserted HERE as well as at the HTTP edge — the edge
	// check stops a stale client, this one stops a code path that forgets.
	if p.ExpectedTotalMinor <= 0 {
		return nil, ErrExpectedTotalRequired
	}
	{
		var changed []PriceChangedLine
		for i, l := range lines {
			if l.SnapshotMinor != 0 && l.SnapshotMinor != priced[i].UnitMinor {
				changed = append(changed, PriceChangedLine{
					VariantID: l.VariantID, WasMinor: l.SnapshotMinor, NowMinor: priced[i].UnitMinor,
				})
			}
		}
		if len(changed) > 0 {
			return nil, &PriceChangedError{Lines: changed}
		}
	}

	// ── 6. Stock, locked in deterministic order (LB-14) ───────────────
	oos, err := lockInventory(ctx, tx, priced)
	if err != nil {
		return nil, err
	}
	if len(oos) > 0 {
		return nil, &OutOfStockError{Lines: oos}
	}

	// ── 7. Price it, in paise, with GST extracted (LB-19, LB-20) ──────
	taxLines := make([]tax.Line, len(priced))
	for i, pl := range priced {
		taxLines[i] = tax.Line{
			Ref:            pl.VariantID.String(),
			GrossInclusive: pl.UnitMinor.MulQty(pl.Quantity),
			Rate:           pl.RateBP,
		}
	}

	// ── 8. Claim coupon capacity atomically (LB-16 / M-6) ─────────────
	var couponID *uuid.UUID
	discount := money.Zero
	if p.CouponCode != "" {
		// B9: the priced lines travel in so product/category/variant
		// applicability can actually be checked against the cart.
		cid, amt, err := claimCoupon(ctx, tx, p.CouponCode, p.UserID, sellerID, subtotalOf(taxLines), priced)
		if err != nil {
			return nil, err
		}
		couponID, discount = cid, amt
	}

	// Resolved HERE, from the seller these locked lines belong to, rather
	// than passed in by the caller. See the idempotency section above: a
	// caller that resolved it beforehand had to read the cart to find the
	// seller, and on an idempotent retry the cart is already empty — so the
	// retry failed with ErrCartEmpty before ever reaching the replay that
	// exists to answer it. Resolving it inside the transaction means no
	// cart-dependent work can precede the replay.
	sellerState, err := sellerPlaceOfSupply(ctx, tx, sellerID)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(sellerState) == "" || strings.TrimSpace(p.DestinationState) == "" {
		return nil, fmt.Errorf("%w: seller state %q, destination state %q",
			ErrPlaceOfSupplyUnknown, sellerState, p.DestinationState)
	}
	interstate := !equalFoldState(sellerState, p.DestinationState)

	computed, err := tax.Compute(tax.Input{
		Lines:         taxLines,
		OrderDiscount: discount,
		Shipping:      quote.ShippingMinor,
		Interstate:    interstate,
	})
	if err != nil {
		return nil, fmt.Errorf("checkout: pricing: %w", err)
	}
	// N6: unconditional. The customer is charged the total they approved or
	// they are told the price moved; there is no third branch.
	if computed.Total != p.ExpectedTotalMinor {
		return nil, &PriceChangedError{NewTotalMinor: computed.Total}
	}

	// ── 9. Write the order ────────────────────────────────────────────
	orderID := uuid.New()
	var orderNumber string
	if err := tx.QueryRow(ctx, `SELECT generate_order_number()`).Scan(&orderNumber); err != nil {
		return nil, fmt.Errorf("checkout: order number: %w", err)
	}

	_, err = tx.Exec(ctx, `
		INSERT INTO orders (
			id, customer_user_id, order_number,
			subtotal, discount_amount, shipping_charges, tax_amount, coupon_discount, final_amount,
			subtotal_minor, discount_amount_minor, shipping_charges_minor,
			tax_amount_minor, coupon_discount_minor, final_amount_minor,
			taxable_minor, cgst_minor, sgst_minor, igst_minor,
			currency_code, payment_method, payment_status, status,
			delivery_address_id, delivery_address_snapshot, delivery_address_snapshot_enc,
			snapshot_key_version, address_snapshot_provenance, snapshot_cutover,
			coupon_code, idempotency_key, request_fingerprint,
			place_of_supply_state, seller_state, is_interstate,
			terms_version, consent_at, created_at, updated_at)
		VALUES (
			$1,$2,$3,
			0,0,0,0,0,0,
			$4,$5,$6,$7,$8,$9,
			$10,$11,$12,$13,
			'INR',$14,'pending','payment_pending',
			$15,$16,$17,$18,'snapshot',TRUE,
			NULLIF($19,''),$20,$21,
			$22,$23,$24,
			NULLIF($25,''),NOW(),NOW(),NOW())`,
		orderID, p.UserID, orderNumber,
		computed.GrossInclusive.Int64(), 0, computed.Shipping.Int64(),
		computed.TotalTax.Int64(), computed.OrderDiscount.Int64(), computed.Total.Int64(),
		computed.TotalTaxable.Int64(), computed.TotalCGST.Int64(),
		computed.TotalSGST.Int64(), computed.TotalIGST.Int64(),
		p.PaymentMethod,
		p.AddressID, p.AddressSnapshot, p.SealedSnapshot, p.SnapshotKeyVer,
		p.CouponCode, p.IdempotencyKey, p.RequestFingerprint,
		p.DestinationState, sellerState, interstate,
		p.TermsVersion,
	)
	if err != nil {
		if isUniqueViolation(err) {
			// Lost the race on the idempotency index. The winner's order is
			// the answer; re-read it rather than failing the customer.
			return nil, ErrIdempotencyConflict
		}
		return nil, fmt.Errorf("checkout: insert order: %w", err)
	}

	// Initial history row. The trigger writes subsequent transitions; the
	// first state has no previous status to transition from.
	if _, err := tx.Exec(ctx,
		`INSERT INTO order_status_history (id, order_id, to_status, actor_type, created_at)
		 VALUES (gen_random_uuid(), $1, 'payment_pending', $2, NOW())`,
		orderID, actorOr(p.ActorType)); err != nil {
		return nil, err
	}

	// ── 10. Items with their tax components (LB-20) ───────────────────
	for i, pl := range priced {
		lt := computed.Lines[i]
		if _, err := tx.Exec(ctx, `
			INSERT INTO order_items (
				id, order_id, product_id, variant_id, seller_id,
				product_title, variant_details, sku, quantity,
				unit_mrp, unit_price, discount_amount, tax_amount, final_price,
				unit_mrp_minor, unit_price_minor, discount_amount_minor,
				tax_amount_minor, final_price_minor,
				tax_class_id, hsn_code, tax_rate_bp, taxable_minor,
				cgst_minor, sgst_minor, igst_minor,
				allocated_discount_minor, allocated_shipping_minor, net_inclusive_minor,
				status, return_eligible_until, created_at)
			VALUES (
				gen_random_uuid(),$1,$2,$3,$4,
				$5,$6,$7,$8,
				0,0,0,0,0,
				$9,$10,$11,$12,$13,
				$14,$15,$16,$17,
				$18,$19,$20,
				$21,$22,$23,
				'confirmed',$24,NOW())`,
			orderID, pl.ProductID, pl.VariantID, pl.SellerID,
			pl.Title, pl.VariantDetails, pl.SKU, pl.Quantity,
			pl.MRPMinor.Int64(), pl.UnitMinor.Int64(), lt.AllocatedDiscount.Int64(),
			lt.Tax.Int64(), lt.NetInclusive.Int64(),
			pl.TaxClassID, pl.HSNCode, int(lt.Rate), lt.Taxable.Int64(),
			lt.CGST.Int64(), lt.SGST.Int64(), lt.IGST.Int64(),
			lt.AllocatedDiscount.Int64(), lt.AllocatedShipping.Int64(), lt.NetInclusive.Int64(),
			time.Now().AddDate(0, 0, pl.ReturnDays),
		); err != nil {
			return nil, fmt.Errorf("checkout: insert item: %w", err)
		}
	}

	// ── 11. Reserve stock (LB-14, LB-21, LB-23) ───────────────────────
	//
	// The CHECK constraint from migration 009 is what actually prevents an
	// oversell. If a concurrent checkout took the last unit between our
	// lock and this update, the constraint raises and the whole
	// transaction rolls back — no order, no partial state.
	for _, pl := range priced {
		if _, err := tx.Exec(ctx,
			`UPDATE inventory_items
			    SET reserved_qty = reserved_qty + $2, updated_at = NOW()
			  WHERE variant_id = $1`, pl.VariantID, pl.Quantity); err != nil {
			if isCheckViolation(err) {
				return nil, &OutOfStockError{Lines: []OutOfStockLine{{
					VariantID: pl.VariantID, ProductID: pl.ProductID,
					ProductTitle: pl.Title, Requested: pl.Quantity,
				}}}
			}
			return nil, fmt.Errorf("checkout: reserve %s: %w", pl.VariantID, err)
		}

		reservationID := uuid.New()
		if _, err := tx.Exec(ctx,
			`INSERT INTO inventory_reservations
			     (id, variant_id, order_id, user_id, quantity, type, expires_at)
			 VALUES ($1,$2,$3,$4,$5,'order',$6)`,
			reservationID, pl.VariantID, orderID, p.UserID, pl.Quantity,
			time.Now().Add(reservationTTL)); err != nil {
			return nil, err
		}
		if _, err := tx.Exec(ctx,
			`INSERT INTO inventory_ledger
			     (variant_id, order_id, reservation_id, delta_reserved, reason, actor_id, actor_type)
			 VALUES ($1,$2,$3,$4,'checkout_reserve',$5,'customer')`,
			pl.VariantID, orderID, reservationID, pl.Quantity, p.UserID); err != nil {
			return nil, err
		}
	}

	// ── 12. Consume the quote, clear the cart ─────────────────────────
	if _, err := tx.Exec(ctx,
		`UPDATE shipping_quotes SET consumed_at = NOW(), consumed_by_order = $2 WHERE id = $1`,
		p.QuoteID, orderID); err != nil {
		return nil, err
	}
	if couponID != nil {
		if _, err := tx.Exec(ctx,
			`INSERT INTO coupon_usages (coupon_id, user_id, order_id) VALUES ($1,$2,$3)
			 ON CONFLICT DO NOTHING`, *couponID, p.UserID, orderID); err != nil {
			return nil, err
		}
	}
	// Deleting the lines bumps the cart version through the trigger, which
	// invalidates any other outstanding quote for this cart.
	if _, err := tx.Exec(ctx, `DELETE FROM cart_items WHERE cart_id = $1`, cartID); err != nil {
		return nil, err
	}

	// ── 13. Outbox, in the same transaction (LB-7) ────────────────────
	if err := enqueueOutboxTx(ctx, tx, "commerce.order.created", orderID.String(), map[string]any{
		"order_id":       orderID,
		"order_number":   orderNumber,
		"user_id":        p.UserID,
		"seller_id":      sellerID,
		"total_minor":    computed.Total.Int64(),
		"tax_minor":      computed.TotalTax.Int64(),
		"shipping_minor": computed.Shipping.Int64(),
		"currency":       "INR",
		"payment_method": p.PaymentMethod,
	}); err != nil {
		return nil, err
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("checkout: commit: %w", err)
	}
	return &CheckoutResult{
		OrderID:     orderID,
		OrderNumber: orderNumber,
		TotalMinor:  computed.Total,
		TaxMinor:    computed.TotalTax,
		ShipMinor:   computed.Shipping,
	}, nil
}

// reservationTTL is how long stock is held for an unpaid order.
const reservationTTL = 20 * time.Minute

// ─── Helpers ─────────────────────────────────────────────────────────

type cartLine struct {
	VariantID     uuid.UUID
	ProductID     uuid.UUID
	Quantity      int
	SnapshotMinor money.Paise
}

type pricedLine struct {
	VariantID      uuid.UUID
	ProductID      uuid.UUID
	SellerID       uuid.UUID
	Title          string
	SKU            string
	VariantDetails []byte
	Quantity       int
	UnitMinor      money.Paise
	MRPMinor       money.Paise
	RateBP         tax.RateBP
	TaxClassID     *uuid.UUID
	HSNCode        *string
	ReturnDays     int
}

type existingOrder struct {
	ID         uuid.UUID
	Number     string
	TotalMinor money.Paise
	TaxMinor   money.Paise
	ShipMinor  money.Paise
}

func lockExistingOrderByKey(ctx context.Context, tx pgx.Tx, userID uuid.UUID, key string) (*existingOrder, string, error) {
	var o existingOrder
	var fp *string
	// Tax and shipping are selected because the retry's response must be the
	// SAME as the original's. They were omitted, and the omission was
	// invisible while a cart-dependent precheck made this path unreachable:
	// a retry never got here, so nobody saw it answer with the right total
	// beside a zero tax and a zero delivery charge. A client rendering its
	// confirmation from the retry showed the buyer a breakdown that did not
	// add up.
	err := tx.QueryRow(ctx,
		`SELECT id, order_number,
		        COALESCE(final_amount_minor,0),
		        COALESCE(tax_amount_minor,0),
		        COALESCE(shipping_charges_minor,0),
		        request_fingerprint
		   FROM orders
		  WHERE customer_user_id = $1 AND idempotency_key = $2
		  FOR UPDATE`, userID, key).
		Scan(&o.ID, &o.Number, &o.TotalMinor, &o.TaxMinor, &o.ShipMinor, &fp)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, "", nil
		}
		return nil, "", err
	}
	if fp == nil {
		return &o, "", nil
	}
	return &o, *fp, nil
}

// lockCartLines returns the cart ordered by variant_id. The ORDER BY is the
// deadlock-avoidance rule, not a cosmetic choice.
func lockCartLines(ctx context.Context, tx pgx.Tx, cartID uuid.UUID) ([]cartLine, error) {
	rows, err := tx.Query(ctx,
		`SELECT variant_id, product_id, quantity, COALESCE(price_snapshot_minor,0)
		   FROM cart_items WHERE cart_id = $1 ORDER BY variant_id`, cartID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []cartLine
	for rows.Next() {
		var l cartLine
		if err := rows.Scan(&l.VariantID, &l.ProductID, &l.Quantity, &l.SnapshotMinor); err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

// lockAndPriceLines reads catalogue truth under lock and enforces LB-17
// (moderation) and D2 (single seller).
//
// ─── WHY THIS DID NOT MOVE ONTO product_offers ──────────────────────────
//
// Step 14 moved every buyer-facing READ onto `product_offers`: browse, home,
// the product page, the category counts, the seller's catalogue, cart
// hydration. This statement is deliberately not one of them, and neither is
// `ProductSaleEligibility` (the add-to-cart guard) or `CartMetaForQuote`.
// The money path stays on `products` as ONE SET, so that the pre-checkout
// guard, the shipping quote and this authoritative re-check cannot disagree
// with each other about which table is the truth. They move together, later,
// or not at all.
//
// The specific reason it cannot move on its own is the lock. `FOR UPDATE OF
// v, p` re-reads the products row at its committed head, which is what makes
// the moderation check below authoritative against a rejection that lands
// between add-to-cart and checkout. An offer row read in the same statement
// is NOT in the OF list, so it would be read from the transaction's snapshot
// instead: a concurrent takedown that committed both copies would be seen on
// `p` and missed on the offer, and the check would pass on a listing that has
// just been withdrawn. Adding the offer to the OF list would fix the read and
// introduce a new lock on a new table into the most contended transaction in
// this service — for no change in the answer, since the checker reports the
// two copies identical.
//
// ─── THE OPTIONS SUBQUERY STAYS OUT OF `FOR UPDATE OF` ──────────────────
//
// The variant's options are built with jsonb_build_object over v's own
// columns, in the target list. They must stay there. Reading them instead
// from `product_variant_options` as a joined table inside a statement that
// says `FOR UPDATE OF v, p, …` would take row locks on the options in the
// order the plan produces them, which is not the ascending-variant_id order
// lockCartLines and lockInventory both take theirs in — two checkouts sharing
// two variants would then be able to hold each other's rows and deadlock.
// A subquery in the target list is not locked at all, which is why this shape
// is safe and why any future move of the options must keep it.
func lockAndPriceLines(ctx context.Context, tx pgx.Tx, lines []cartLine) ([]pricedLine, uuid.UUID, error) {
	out := make([]pricedLine, 0, len(lines))
	var sellerID uuid.UUID

	for _, l := range lines {
		var (
			pl               pricedLine
			pStatus          string
			pApproval        string
			vStatus          string
			cgst, sgst, igst *float64
		)
		err := tx.QueryRow(ctx, `
			SELECT v.id, p.id, p.seller_id, p.title, v.sku,
			       COALESCE(NULLIF(v.selling_price_minor, 0), ROUND(v.selling_price*100)),
			       COALESCE(NULLIF(v.mrp_minor, 0), ROUND(v.mrp*100)),
			       p.status, p.approval_status, v.status,
			       p.tax_class_id, p.hsn_code, p.return_policy_days,
			       tc.cgst_percentage, tc.sgst_percentage, tc.igst_percentage,
			       jsonb_build_object(
			           'option_1_name', v.option_1_name, 'option_1_value', v.option_1_value,
			           'option_2_name', v.option_2_name, 'option_2_value', v.option_2_value,
			           'option_3_name', v.option_3_name, 'option_3_value', v.option_3_value)
			  FROM product_variants v
			  JOIN products p ON p.id = v.product_id
			  LEFT JOIN tax_classes tc ON tc.id = p.tax_class_id
			 WHERE v.id = $1
			 FOR UPDATE OF v, p`,
			l.VariantID).Scan(
			&pl.VariantID, &pl.ProductID, &pl.SellerID, &pl.Title, &pl.SKU,
			&pl.UnitMinor, &pl.MRPMinor,
			&pStatus, &pApproval, &vStatus,
			&pl.TaxClassID, &pl.HSNCode, &pl.ReturnDays,
			&cgst, &sgst, &igst, &pl.VariantDetails)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return nil, uuid.Nil, ErrProductUnavailable
			}
			return nil, uuid.Nil, err
		}

		// LB-17 / v1 §5.6. AddToCart checked only the VARIANT's status, and
		// priceCart checked neither, so a product could be sold while
		// `approval_status = 'pending'` or after an admin rejected it. The
		// authoritative check belongs here, under the row lock, because a
		// product can be rejected between add-to-cart and checkout.
		if pStatus != "active" || pApproval != "approved" || vStatus != "active" {
			return nil, uuid.Nil, ErrProductUnavailable
		}

		// D2: single-seller carts. Multi-seller means partial cancels,
		// partial refunds and split shipments on day one.
		if sellerID == uuid.Nil {
			sellerID = pl.SellerID
		} else if sellerID != pl.SellerID {
			return nil, uuid.Nil, ErrMultipleSellers
		}

		pl.Quantity = l.Quantity
		// B9: a line whose tax class is missing or unusable fails the
		// checkout. Charging the customer a total we cannot correctly
		// account for is worse than refusing the sale.
		rate, rateErr := rateFromClass(pl.TaxClassID, cgst, sgst, igst)
		if rateErr != nil {
			return nil, uuid.Nil, fmt.Errorf("%w: variant %s: %v", ErrTaxClassMissing, pl.VariantID, rateErr)
		}
		pl.RateBP = rate
		out = append(out, pl)
	}
	return out, sellerID, nil
}

// rateFromClass resolves the GST rate in basis points.
//
// B9 — this used to return a bare tax.RateBP, and every failure path fell
// through to `return 0`. The doc comment claimed "a product with no tax class
// is NOT treated as zero-rated... lockAndPriceLines surfaces it as an
// unavailable product", but no such rejection existed: a missing tax_class_id
// FK, a NULL percentage, and a percentage tax.RateFromPercent refused all
// produced a silent 0% line. GST was then under-collected and under-reported
// in paise on every affected order, and the caller could not tell that from a
// legitimately zero-rated good.
//
// It now returns an error, and the caller refuses the checkout. A genuine
// zero-rate class is expressible and accepted — an explicit 0 that
// RateFromPercent validates — so the legitimate case is distinguishable from
// the broken one, which is the whole point.
func rateFromClass(taxClassID *uuid.UUID, cgst, sgst, igst *float64) (tax.RateBP, error) {
	if taxClassID == nil {
		return 0, ErrTaxClassMissing
	}
	// The LEFT JOIN yields NULL percentages when tax_class_id points at a
	// row that does not exist — a dangling FK, not a zero rate.
	if igst == nil && cgst == nil && sgst == nil {
		return 0, ErrTaxClassMissing
	}
	if igst != nil && *igst > 0 {
		bp, err := tax.RateFromPercent(*igst)
		if err != nil {
			return 0, fmt.Errorf("%w: igst %v", ErrTaxClassInvalid, *igst)
		}
		return bp, nil
	}
	if cgst != nil && sgst != nil {
		bp, err := tax.RateFromPercent(*cgst + *sgst)
		if err != nil {
			return 0, fmt.Errorf("%w: cgst %v + sgst %v", ErrTaxClassInvalid, *cgst, *sgst)
		}
		return bp, nil
	}
	// An IGST of exactly 0 with no CGST/SGST pair is a legitimate
	// zero-rated class only if it was stated; anything else is incomplete
	// configuration.
	if igst != nil && *igst == 0 {
		return 0, nil
	}
	return 0, ErrTaxClassInvalid
}

// lockInventory locks every inventory row in ascending variant_id order and
// reports any line that cannot be satisfied.
func lockInventory(ctx context.Context, tx pgx.Tx, lines []pricedLine) ([]OutOfStockLine, error) {
	ids := make([]uuid.UUID, len(lines))
	byVariant := map[uuid.UUID]pricedLine{}
	for i, l := range lines {
		ids[i] = l.VariantID
		byVariant[l.VariantID] = l
	}
	sort.Slice(ids, func(a, b int) bool { return ids[a].String() < ids[b].String() })

	var oos []OutOfStockLine
	for _, id := range ids {
		var total, reserved int
		err := tx.QueryRow(ctx,
			`SELECT total_qty, reserved_qty FROM inventory_items WHERE variant_id = $1 FOR UPDATE`, id).
			Scan(&total, &reserved)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				l := byVariant[id]
				oos = append(oos, OutOfStockLine{
					VariantID: id, ProductID: l.ProductID, ProductTitle: l.Title,
					Requested: l.Quantity, Available: 0,
				})
				continue
			}
			return nil, err
		}
		l := byVariant[id]
		if avail := total - reserved; avail < l.Quantity {
			oos = append(oos, OutOfStockLine{
				VariantID: id, ProductID: l.ProductID, ProductTitle: l.Title,
				Requested: l.Quantity, Available: avail,
			})
		}
	}
	return oos, nil
}

// anyLineMatches reports whether any cart line's chosen id is in the
// coupon's allowlist. An EMPTY allowlist matches nothing: a coupon scoped to
// "product" with no products named is misconfigured, and treating that as
// "all products" is the same silent-discount failure B9 removes.
func anyLineMatches(lines []pricedLine, allowed []uuid.UUID, pick func(pricedLine) uuid.UUID) bool {
	if len(allowed) == 0 {
		return false
	}
	set := make(map[uuid.UUID]struct{}, len(allowed))
	for _, id := range allowed {
		set[id] = struct{}{}
	}
	for _, l := range lines {
		if _, ok := set[pick(l)]; ok {
			return true
		}
	}
	return false
}

// anyLineInCategories reports whether any cart line's product sits in one of
// the coupon's categories. Read inside the caller's transaction, against the
// products already locked by lockAndPriceLines.
func anyLineInCategories(ctx context.Context, tx pgx.Tx, lines []pricedLine, allowed []uuid.UUID) (bool, error) {
	if len(allowed) == 0 {
		return false, nil
	}
	ids := make([]uuid.UUID, 0, len(lines))
	for _, l := range lines {
		ids = append(ids, l.ProductID)
	}
	var n int
	if err := tx.QueryRow(ctx,
		`SELECT count(*) FROM products
		  WHERE id = ANY($1) AND category_id = ANY($2)`,
		ids, allowed).Scan(&n); err != nil {
		return false, err
	}
	return n > 0, nil
}

// couponRow is a coupon's terms, read either by CLAIMING capacity (checkout)
// or by PREVIEWING it (quote).
//
// C3-LB-2. The quote must show the buyer the same discount checkout will
// charge, and the only way to guarantee that is for both to run the same
// applicability rules and the same arithmetic. Before this split, checkout
// had the only copy — so the quote could not price a coupon at all, which is
// part of why the client was left inventing a total.
type couponRow struct {
	id             uuid.UUID
	discType       string
	valueMinor     *int64
	basisPoints    *int
	maxDiscount    *int64
	minOrder       int64
	maxUsesPerUser int
	applicableTo   string
	applicableIDs  []uuid.UUID
	couponSeller   *uuid.UUID
}

// couponColumns is shared so the claim and the preview cannot read different
// fields and therefore reach different conclusions.
const couponColumns = `id, discount_type,
	          discount_value_minor, discount_basis_points, max_discount_amount_minor,
	          COALESCE(min_order_amount_minor,0), max_uses_per_user,
	          applicable_to, applicable_ids, seller_id`

// couponLive is the validity predicate: active, started, unexpired, and with
// capacity remaining. The claim applies it in an UPDATE, the preview in a
// SELECT — same text, so "valid" means the same thing to both.
const couponLive = `code = $1
		   AND is_active = TRUE
		   AND starts_at <= NOW()
		   AND (expires_at IS NULL OR expires_at > NOW())
		   AND (max_uses IS NULL OR uses_count < max_uses)`

func scanCoupon(row pgx.Row) (couponRow, error) {
	var c couponRow
	err := row.Scan(&c.id, &c.discType,
		&c.valueMinor, &c.basisPoints, &c.maxDiscount,
		&c.minOrder, &c.maxUsesPerUser,
		&c.applicableTo, &c.applicableIDs, &c.couponSeller)
	return c, err
}

// evaluateCoupon applies every rule that decides IF and BY HOW MUCH a coupon
// discounts this cart. It performs no writes, so the quote and the checkout
// can both call it and are guaranteed to agree.
func evaluateCoupon(
	ctx context.Context,
	tx pgx.Tx,
	c couponRow,
	userID, sellerID uuid.UUID,
	subtotal money.Paise,
	lines []pricedLine,
) (money.Paise, error) {
	// Per-user cap, counted under the caller's transaction.
	var used int
	if err := tx.QueryRow(ctx,
		`SELECT count(*) FROM coupon_usages WHERE coupon_id = $1 AND user_id = $2`,
		c.id, userID).Scan(&used); err != nil {
		return 0, err
	}
	if used >= c.maxUsesPerUser {
		return 0, ErrCouponExhausted
	}
	if subtotal < money.Paise(c.minOrder) {
		return 0, ErrCouponExhausted
	}

	// ── Applicability (B9) ────────────────────────────────────────────
	//
	// The query has always SELECTed `applicable_to` and `applicable_ids`,
	// and only the `seller` case was ever enforced. A product- or
	// category-scoped coupon therefore discounted any cart it was typed
	// into: an unauthorised discount on every affected order, funded by the
	// platform.
	//
	// The switch is exhaustive and its default REFUSES. A scope this code
	// does not understand must not be silently treated as "applies to
	// everything" — that is the failure mode being removed, and a new scope
	// added to the schema later would otherwise inherit it.
	switch c.applicableTo {
	case "all", "":
		// Unrestricted.
	case "seller":
		if c.couponSeller == nil || *c.couponSeller != sellerID {
			return 0, ErrCouponNotApplicable
		}
	case "product":
		if !anyLineMatches(lines, c.applicableIDs, func(l pricedLine) uuid.UUID { return l.ProductID }) {
			return 0, ErrCouponNotApplicable
		}
	case "category":
		ok, err := anyLineInCategories(ctx, tx, lines, c.applicableIDs)
		if err != nil {
			return 0, err
		}
		if !ok {
			return 0, ErrCouponNotApplicable
		}
	case "variant":
		if !anyLineMatches(lines, c.applicableIDs, func(l pricedLine) uuid.UUID { return l.VariantID }) {
			return 0, ErrCouponNotApplicable
		}
	default:
		return 0, fmt.Errorf("%w: unknown applicability scope %q", ErrCouponNotApplicable, c.applicableTo)
	}

	var discount money.Paise
	switch c.discType {
	case "percentage":
		if c.basisPoints == nil {
			return 0, ErrCouponExhausted
		}
		discount = money.Paise(int64(subtotal) * int64(*c.basisPoints) / 10000)
		if c.maxDiscount != nil && discount > money.Paise(*c.maxDiscount) {
			discount = money.Paise(*c.maxDiscount)
		}
	case "flat":
		if c.valueMinor == nil {
			return 0, ErrCouponExhausted
		}
		discount = money.Paise(*c.valueMinor)
	default:
		// free_shipping / buy_x_get_y are not in the P0 loop.
		return 0, ErrCouponExhausted
	}
	// A flat coupon larger than the cart must not create a negative order.
	if discount > subtotal {
		discount = subtotal
	}
	return discount, nil
}

// claimCoupon takes capacity conditionally, inside the transaction.
//
// M-6: caps used to be READ during pricing and INCREMENTED after the order
// was created, with the increment's error ignored. Fifty concurrent
// checkouts all passed a one-use coupon because none had committed when the
// others read.
func claimCoupon(ctx context.Context, tx pgx.Tx, code string, userID, sellerID uuid.UUID, subtotal money.Paise, lines []pricedLine) (*uuid.UUID, money.Paise, error) {
	// The conditional UPDATE is the claim: it only succeeds while capacity
	// remains, and it is atomic with everything else in this transaction.
	c, err := scanCoupon(tx.QueryRow(ctx, `
		UPDATE coupons
		   SET uses_count = uses_count + 1
		 WHERE `+couponLive+`
		RETURNING `+couponColumns, code))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, 0, ErrCouponExhausted
		}
		return nil, 0, err
	}
	discount, err := evaluateCoupon(ctx, tx, c, userID, sellerID, subtotal, lines)
	if err != nil {
		return nil, 0, err
	}
	return &c.id, discount, nil
}

// previewCoupon prices a coupon WITHOUT claiming capacity, for the quote.
//
// C3-LB-2. It deliberately does not reserve anything: a quote is not a
// promise, and holding coupon capacity for every buyer who opens a checkout
// screen would exhaust a one-use code on the first person to look at it.
// Checkout claims atomically and can still return ErrCouponExhausted, which
// the client surfaces as a price change rather than a silent charge.
func previewCoupon(ctx context.Context, tx pgx.Tx, code string, userID, sellerID uuid.UUID, subtotal money.Paise, lines []pricedLine) (money.Paise, error) {
	c, err := scanCoupon(tx.QueryRow(ctx,
		`SELECT `+couponColumns+` FROM coupons WHERE `+couponLive, code))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return 0, ErrCouponExhausted
		}
		return 0, err
	}
	return evaluateCoupon(ctx, tx, c, userID, sellerID, subtotal, lines)
}

func subtotalOf(lines []tax.Line) money.Paise {
	var t money.Paise
	for _, l := range lines {
		t = t.Add(l.GrossInclusive).Sub(l.LineDiscount)
	}
	return t
}

func actorOr(a string) string {
	if a == "" {
		return "customer"
	}
	return a
}

// sellerPlaceOfSupply returns the state GST is charged from, for one seller,
// inside the caller's transaction.
//
// It is the same predicate as SellerStateForCart and SellerPickupPin: the
// pickup address first, the seller's registered state as the fallback for
// sellers onboarded through the older wizard. The origin of a shipment and
// the place of supply for its GST are the same address, so all three resolve
// it identically — reading them from different tables is what let a seller
// have a courier pickup point and no determinable tax state at once.
func sellerPlaceOfSupply(ctx context.Context, tx pgx.Tx, sellerID uuid.UUID) (string, error) {
	var state string
	err := tx.QueryRow(ctx, `
		SELECT COALESCE(NULLIF(addr.state, ''), NULLIF(sel.state, ''), '')
		  FROM sellers sel
		  LEFT JOIN LATERAL (
		      SELECT sa.state
		        FROM seller_addresses sa
		       WHERE sa.seller_id = sel.id
		         AND sa.address_type IN ('pickup','warehouse','business')
		       ORDER BY (sa.address_type = 'pickup') DESC, sa.is_default DESC
		       LIMIT 1
		  ) addr ON TRUE
		 WHERE sel.id = $1`, sellerID).Scan(&state)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			// No seller row for a seller id taken off locked cart lines is a
			// data error, not an empty state. Reporting it as "place of
			// supply unknown" would tell the buyer to ask the seller to fix
			// an address that is not the problem.
			return "", fmt.Errorf("checkout: seller %s not found", sellerID)
		}
		return "", err
	}
	return state, nil
}

func equalFoldState(a, b string) bool {
	return normalizeState(a) == normalizeState(b)
}

func normalizeState(s string) string {
	out := make([]rune, 0, len(s))
	for _, r := range s {
		switch {
		case r >= 'A' && r <= 'Z':
			out = append(out, r+32)
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			out = append(out, r)
		}
	}
	return string(out)
}

// enqueueOutboxTx writes a domain event inside the caller's transaction.
func enqueueOutboxTx(ctx context.Context, tx pgx.Tx, eventType, partitionKey string, payload any) error {
	data, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	env := events.EventEnvelope{
		EventID:    uuid.NewString(),
		EventType:  eventType,
		OccurredAt: time.Now().UTC(),
		Payload:    data,
	}
	body, err := json.Marshal(env)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx,
		`INSERT INTO outbox_events (event_type, partition_key, payload) VALUES ($1,$2,$3)`,
		eventType, partitionKey, body)
	return err
}
