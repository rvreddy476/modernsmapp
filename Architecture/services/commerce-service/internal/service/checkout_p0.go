package service

// The P0 checkout orchestration.
//
// The division of labour here is the whole point of A4 and LB-14:
//
//	BEFORE the transaction   anything that touches a network — the courier
//	                         rate, KMS decryption of the address
//	INSIDE the transaction   every read and write that decides money or
//	                         stock, committing together or not at all
//	AFTER the transaction    opening the payment, against an order that
//	                         already exists and already holds its stock
//
// The old code interleaved all three, which is why a courier timeout could
// leave a half-made order and a reservation failure could leave a confirmed
// one with no stock behind it.

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/atpost/commerce-service/internal/courier"
	"github.com/atpost/commerce-service/internal/money"
	"github.com/atpost/commerce-service/internal/payments"
	"github.com/atpost/commerce-service/internal/pii"
	"github.com/atpost/commerce-service/internal/store/postgres"
	"github.com/atpost/shared/paymentmethod"
	"github.com/google/uuid"
)

// ─── Quote (A4, step 1: outside the transaction) ─────────────────────

// QuoteInputP0 asks the server to price the caller's current cart.
//
// C3-LB-2: it is no longer only a delivery-price request. The coupon and the
// payment method travel with it because both change the total, and the quote
// is bound to them so checkout can refuse a quote taken under different terms.
type QuoteInputP0 struct {
	UserID    uuid.UUID
	AddressID uuid.UUID
	// CouponCode is optional. When present the quote prices the discount
	// WITHOUT claiming coupon capacity — see previewCoupon.
	CouponCode string
	// PaymentMethod must be a launch method; the quote is bound to it.
	PaymentMethod string
}

// QuoteResult is the COMPLETE price the buyer is asked to accept, and the
// only source of figures the checkout screen may render.
//
// C3-LB-2. This used to carry a delivery charge and nothing else, so the
// Android screen had no total to show and computed `0 + shipping` — which it
// then submitted as expected_total_minor, guaranteeing PRICE_CHANGED on every
// non-empty cart. A buyer accepts a TOTAL; only the server that will charge
// it may state it.
//
// GST is INCLUDED in TotalMinor (D1: catalogue prices are GST-inclusive).
// TaxMinor is the portion already inside it, published so the screen can say
// "includes ₹X GST". A client that ADDS it charges tax twice.
type QuoteResult struct {
	QuoteID uuid.UUID `json:"quote_id"`

	SubtotalMinor money.Paise `json:"subtotal_minor"`
	DiscountMinor money.Paise `json:"discount_minor"`
	ShippingMinor money.Paise `json:"shipping_minor"`
	TaxMinor      money.Paise `json:"tax_minor"`
	TotalMinor    money.Paise `json:"total_minor"`
	Currency      string      `json:"currency"`

	CourierCode string    `json:"courier_code"`
	ExpiresAt   time.Time `json:"expires_at"`
	Serviceable bool      `json:"serviceable"`
	Reason      string    `json:"reason,omitempty"`
}

// PrepareQuote calls the courier and persists the result.
//
// R-4: LB-14 forbids a network call before the checkout commit, and D7 makes
// Shiprocket the authority on the rate. Both hold only if the rate is
// fetched here and merely CONSUMED inside the transaction. The quote is
// bound to the cart version, the address content, the seller and the item
// set, so a change to any of them invalidates it rather than silently
// charging yesterday's price for today's delivery.
func (s *Service) PrepareQuote(ctx context.Context, in QuoteInputP0) (*QuoteResult, error) {
	meta, err := s.store.CartMetaForQuote(ctx, in.UserID)
	if err != nil {
		return nil, err
	}

	addr, err := s.loadAddress(ctx, in.UserID, in.AddressID)
	if err != nil {
		return nil, err
	}

	if s.courier == nil {
		// D7 / M-10: production must not fall back to a made-up rate. The
		// old code's ₹40-flat constant is exactly the "fake dependency in
		// production" pattern the review called out.
		return nil, fmt.Errorf("commerce: no courier configured; refusing to quote a delivery price")
	}

	res, err := s.courier.CheckServiceability(ctx, courier.ServiceabilityRequest{
		PickupPincode: s.sellerPickupPin(ctx, meta.SellerID),
		DropPincode:   addr.PostalCode,
		// The adapter's contract is kilograms. Grams are the storage unit,
		// so the conversion happens once, here, at the boundary — and it is
		// a WEIGHT, not money, so a float is legitimate.
		WeightKg:      float64(meta.WeightG) / 1000.0,
		PaymentMethod: "prepaid", // A5: prepaid only, never "cod"
	})
	if err != nil {
		// 503, not 500. The carrier is a third party, and it being
		// unreachable or refusing our credentials is not a defect in the
		// buyer's request — it is retryable, and it must read that way so
		// the client offers "try again" rather than "something went wrong".
		//
		// Found while re-running the launch journey: a Shiprocket
		// credential failure surfaced as a bare INTERNAL_ERROR, and the
		// only way to learn that the quote was down (rather than broken)
		// was to read the server log.
		return nil, fmt.Errorf("%w: %v", ErrCourierUnavailable, err)
	}
	if !res.Serviceable {
		return &QuoteResult{Serviceable: false, Reason: res.Reason}, nil
	}

	// B8 — defence in depth, one layer above the adapter. The Shiprocket
	// adapter now refuses to return a serviceable-but-unpriced result, but
	// this is the line that actually persists ShippingChargeMinor into the
	// quote checkout consumes, so it refuses too. Any future adapter that
	// answers "yes, and I have no idea what it costs" fails here rather than
	// silently shipping free.
	//
	// This mirrors the courier-not-configured refusal above: production must
	// never invent a delivery price, and zero is a price.
	if res.ShippingChargeMinor <= 0 {
		return nil, fmt.Errorf(
			"commerce: courier %q reported %s→%s serviceable but returned no delivery rate; "+
				"refusing to quote zero shipping",
			res.Courier, s.sellerPickupPin(ctx, meta.SellerID), addr.PostalCode)
	}

	// ── C3-LB-2: price the WHOLE cart, not just the delivery ──────────
	//
	// The client is about to be shown a total and asked to approve it, so the
	// server computes that total here, using the same helpers the checkout
	// transaction uses. This runs AFTER the courier call because it needs the
	// shipping charge, and it takes no network of its own.
	sellerState, err := s.store.SellerStateForCart(ctx, in.UserID)
	if err != nil {
		return nil, err
	}
	pricing, err := s.store.PriceCartForQuote(ctx, postgres.QuotePricingInput{
		UserID:           in.UserID,
		CartID:           meta.CartID,
		ShippingMinor:    money.Paise(res.ShippingChargeMinor),
		CouponCode:       in.CouponCode,
		SellerState:      sellerState,
		DestinationState: addr.State,
	})
	if err != nil {
		return nil, err
	}

	q, err := s.store.SaveQuote(ctx, postgres.ShippingQuote{
		UserID:         in.UserID,
		CartID:         meta.CartID,
		CartVersion:    meta.Version,
		AddressID:      in.AddressID,
		AddressHash:    postgres.HashAddress(addr.AddressLine1, addr.AddressLine2, addr.City, addr.State, addr.PostalCode),
		SellerID:       meta.SellerID,
		ItemsHash:      meta.ItemsHash,
		TotalWeightG:   meta.WeightG,
		DestinationPin: addr.PostalCode,
		ShippingMinor:  money.Paise(res.ShippingChargeMinor),
		CODAvailable:   false, // A5
		CourierCode:    res.Courier,

		SubtotalMinor: pricing.SubtotalMinor,
		DiscountMinor: pricing.DiscountMinor,
		TaxMinor:      pricing.TaxMinor,
		TotalMinor:    pricing.TotalMinor,
		CouponCode:    in.CouponCode,
		PaymentMethod: in.PaymentMethod,
	}, res)
	if err != nil {
		return nil, err
	}
	return &QuoteResult{
		QuoteID:       q.ID,
		SubtotalMinor: pricing.SubtotalMinor,
		DiscountMinor: pricing.DiscountMinor,
		ShippingMinor: pricing.ShippingMinor,
		TaxMinor:      pricing.TaxMinor,
		TotalMinor:    pricing.TotalMinor,
		Currency:      pricing.Currency,
		CourierCode:   q.CourierCode,
		ExpiresAt:     q.ExpiresAt,
		Serviceable:   true,
	}, nil
}

// ─── Checkout (LB-14) ────────────────────────────────────────────────

// CheckoutInputP0 is the request. It carries no amount: the client cannot
// propose what its own order costs.
type CheckoutInputP0 struct {
	UserID         uuid.UUID
	AddressID      uuid.UUID
	QuoteID        uuid.UUID
	IdempotencyKey string
	CouponCode     string
	PaymentMethod  string
	TermsVersion   string
	// ExpectedTotalMinor is what the customer was last shown. A mismatch
	// produces a typed price-changed response rather than a silent charge
	// of a different number.
	ExpectedTotalMinor money.Paise
}

// CheckoutOutputP0 is the created order plus the handle the client needs to
// pay for it.
type CheckoutOutputP0 struct {
	OrderID       uuid.UUID         `json:"order_id"`
	OrderNumber   string            `json:"order_number"`
	TotalMinor    money.Paise       `json:"total_minor"`
	TaxMinor      money.Paise       `json:"tax_minor"`
	ShippingMinor money.Paise       `json:"shipping_minor"`
	Currency      string            `json:"currency"`
	IntentID      *uuid.UUID        `json:"payment_intent_id,omitempty"`
	ClientSession map[string]string `json:"client_session,omitempty"`
}

// CheckoutP0 creates one order.
func (s *Service) CheckoutP0(ctx context.Context, in CheckoutInputP0) (*CheckoutOutputP0, error) {
	// LB-15: the key is required. The old code fabricated
	// "userID-<nanotime>" when it was missing, which is a key that can
	// never match a retry and therefore dedupes nothing.
	if strings.TrimSpace(in.IdempotencyKey) == "" {
		return nil, fmt.Errorf("commerce: Idempotency-Key is required for checkout")
	}
	// C3-LB-3: the launch vocabulary, refused here as well as at the edge, in
	// the store and by the database CHECK.
	//
	// This used to be `+"`"+`method == "" || EqualFold(method, "cod")`+"`"+`, which let
	// net_banking, wallet, emi, bnpl and any typo straight through — and the
	// gated CHECK still permitted net_banking, so the order committed and held
	// stock before payments refused the intent.
	if err := paymentmethod.Validate(in.PaymentMethod); err != nil {
		if strings.EqualFold(strings.TrimSpace(in.PaymentMethod), "cod") {
			// Keep the specific COD error: the client renders dedicated copy
			// for it, and A5 is a named launch decision rather than a typo.
			return nil, postgres.ErrCODNotSupported
		}
		return nil, err
	}

	// KMS decryption happens BEFORE the transaction — it is a network call.
	addr, err := s.loadAddress(ctx, in.UserID, in.AddressID)
	if err != nil {
		return nil, err
	}
	// B4. The order's delivery-address snapshot.
	//
	// `delivery_address_snapshot` is a plaintext JSON copy of the customer's
	// full address, written on every order. It exists so an order can be
	// fulfilled and invoiced against the address as it was, independently of
	// later edits — a real requirement, and the reason the encrypted copy in
	// `delivery_address_snapshot_enc` is not simply a duplicate.
	//
	// After cutover the plaintext copy is reduced to its ROUTING fields only.
	// City, state, postal code and country stay legible because the courier
	// integration, the GST place-of-supply determination and the shipping
	// quote all read them directly in SQL, and none of them identifies a
	// person. The name, phone, street and landmark travel only in the sealed
	// blob.
	sealed, keyVer, err := s.sealSnapshot(ctx, *addr)
	if err != nil {
		return nil, err
	}
	snapshotSource := *addr
	if !s.piiCutover.WritesPlaintext() {
		if keyVer <= 0 || len(sealed) == 0 {
			return nil, fmt.Errorf(
				"commerce: refusing to place an order whose address snapshot could not be sealed")
		}
		snapshotSource = pii.Address{
			City: addr.City, State: addr.State,
			PostalCode: addr.PostalCode, Country: addr.Country,
		}
	}
	snapshot, err := json.Marshal(snapshotSource)
	if err != nil {
		return nil, err
	}

	// The seller's place of supply is NOT resolved here. It used to be, and
	// because finding the seller means reading the cart, it returned
	// ErrCartEmpty on any retry that arrived after a successful checkout had
	// cleared the cart — the exact case Idempotency-Key exists for. The
	// store's replay path (advisory lock, then lookup by key) never ran, so a
	// client retrying a timed-out request was told its cart was empty instead
	// of being handed the order it had already placed. The store now resolves
	// it inside the transaction, after the replay.
	res, err := s.store.Checkout(ctx, postgres.CheckoutParams{
		UserID:             in.UserID,
		AddressID:          in.AddressID,
		QuoteID:            in.QuoteID,
		IdempotencyKey:     in.IdempotencyKey,
		RequestFingerprint: fingerprint(in, addr),
		CouponCode:         in.CouponCode,
		PaymentMethod:      in.PaymentMethod,
		TermsVersion:       in.TermsVersion,
		ExpectedTotalMinor: in.ExpectedTotalMinor,
		AddressSnapshot:    snapshot,
		SealedSnapshot:     sealed,
		SnapshotKeyVer:     keyVer,
		DestinationState:   addr.State,
		DestinationPin:     addr.PostalCode,
		ActorType:          "customer",
	})
	if err != nil {
		return nil, err
	}

	out := &CheckoutOutputP0{
		OrderID:       res.OrderID,
		OrderNumber:   res.OrderNumber,
		TotalMinor:    res.TotalMinor,
		TaxMinor:      res.TaxMinor,
		ShippingMinor: res.ShipMinor,
		Currency:      "INR",
	}
	if res.Reused {
		// An idempotent retry. The order already exists, and so does its
		// intent; return the existing handle rather than opening a second.
		if id, err := s.store.OrderPaymentIntentID(ctx, res.OrderID); err == nil && id != uuid.Nil {
			out.IntentID = &id
		}
		return out, nil
	}

	// ── AFTER the commit: open the payment (LB-4) ─────────────────────
	//
	// The order exists and its stock is held, so a failure here is
	// recoverable — the customer retries payment, or the reservation
	// expires and the order terminates. Nothing is left half-written.
	intent, err := s.openPaymentIntent(ctx, res.OrderID, in.UserID, res.TotalMinor, in.PaymentMethod)
	if err != nil {
		slog.Warn("commerce: order created but payment could not be opened",
			"order_id", res.OrderID, "error", err)
		return out, nil // the client retries via POST /orders/:id/payment/intent
	}
	out.IntentID = &intent.ID
	return out, nil
}

// openPaymentIntent asks payments for a payable, with the amount authored
// here from the order we own.
func (s *Service) openPaymentIntent(ctx context.Context, orderID, userID uuid.UUID, total money.Paise, method string) (*payments.Intent, error) {
	if s.payments == nil {
		return nil, ErrPaymentsClientMissing
	}
	sellerID, err := s.store.SellerForOrder(ctx, orderID)
	if err != nil {
		return nil, err
	}
	intent, err := s.payments.CreateIntent(ctx, payments.CreateIntentInput{
		OrderID:     orderID,
		PayerID:     userID,
		PayeeID:     sellerID, // D4: sellers.id, typed, never a user id
		AmountMinor: total,
		Method:      method,
	})
	if err != nil {
		return nil, err
	}
	if err := s.store.BindPaymentIntent(ctx, orderID, intent.ID); err != nil {
		return nil, err
	}
	return intent, nil
}

// OpenPaymentForOrder is the client-facing retry of the payment leg.
//
// This is the endpoint that replaces the removed public
// POST /v1/payments/intents. The client names an ORDER, not an amount.
func (s *Service) OpenPaymentForOrder(ctx context.Context, orderID, userID uuid.UUID) (*payments.Intent, error) {
	order, err := s.store.GetOrderByID(ctx, orderID)
	if err != nil || order == nil {
		return nil, ErrOrderNotFound
	}
	if order.CustomerUserID != userID {
		return nil, ErrNotOrderOwner
	}
	if order.Status != "payment_pending" {
		return nil, ErrOrderNotPaymentPending
	}
	if existing, err := s.store.OrderPaymentIntentID(ctx, orderID); err == nil && existing != uuid.Nil {
		return s.payments.GetIntent(ctx, existing)
	}
	total, err := s.store.OrderTotalMinor(ctx, orderID)
	if err != nil {
		return nil, err
	}
	method := "upi"
	if order.PaymentMethod != nil {
		method = *order.PaymentMethod
	}
	return s.openPaymentIntent(ctx, orderID, userID, total, method)
}

// PaymentStatus reports the authoritative payment state for an order.
//
// A1: this is what the app polls. The redirect is never proof, so the client
// shows "confirming" until this says otherwise.
type PaymentStatus struct {
	OrderID       uuid.UUID `json:"order_id"`
	OrderStatus   string    `json:"order_status"`
	PaymentStatus string    `json:"payment_status"`
	// ProviderStatus is what payments last knew. Advisory: the order's own
	// status is what the app should act on.
	ProviderStatus string `json:"provider_status,omitempty"`
}

func (s *Service) PaymentStatusForOrder(ctx context.Context, orderID, userID uuid.UUID) (*PaymentStatus, error) {
	order, err := s.store.GetOrderByID(ctx, orderID)
	if err != nil || order == nil {
		return nil, ErrOrderNotFound
	}
	if order.CustomerUserID != userID {
		return nil, ErrNotOrderOwner
	}
	out := &PaymentStatus{
		OrderID:       orderID,
		OrderStatus:   order.Status,
		PaymentStatus: order.PaymentStatus,
	}
	if id, err := s.store.OrderPaymentIntentID(ctx, orderID); err == nil && id != uuid.Nil && s.payments != nil {
		if intent, err := s.payments.GetIntent(ctx, id); err == nil && intent != nil {
			out.ProviderStatus = intent.Status
		}
	}
	return out, nil
}

// ─── Address loading (LB-18, LB-24) ──────────────────────────────────

func (s *Service) loadAddress(ctx context.Context, userID, addressID uuid.UUID) (*pii.Address, error) {
	row, err := s.store.GetAddressRow(ctx, addressID)
	if err != nil {
		return nil, err
	}
	if row.UserID != userID {
		// The old Checkout never loaded the address at all, let alone
		// checked ownership; it stored whatever id it was handed.
		return nil, postgres.ErrAddressNotOwned
	}
	if s.pii == nil {
		return nil, fmt.Errorf("commerce: PII cipher is not configured")
	}
	// B4/B5. A row with no ciphertext may be served from plaintext ONLY
	// during the dual-write window.
	//
	// After cutover this fallback is closed. Every row is supposed to carry
	// ciphertext by then, so one that does not is a defect — and serving it
	// silently would hide exactly the failure the cutover exists to remove,
	// right up until the gated scrub cleared the plaintext and the address
	// became unrecoverable. Failing here surfaces it while the data is still
	// there.
	if len(row.ContactNameEnc) == 0 {
		if !s.piiCutover.AllowsPlaintextRead() {
			return nil, fmt.Errorf(
				"commerce: address %s has no ciphertext and the PII cutover is complete; "+
					"refusing to read identifying plaintext (the backfill did not cover this row)",
				row.ID)
		}
		if row.ContactName == "" {
			return nil, fmt.Errorf("commerce: address %s has neither ciphertext nor plaintext", row.ID)
		}
		return &pii.Address{
			ContactName: row.ContactName, Phone: row.Phone,
			AddressLine1: row.AddressLine1, AddressLine2: row.AddressLine2,
			Landmark: row.Landmark, City: row.City, State: row.State,
			PostalCode: row.PostalCode, Country: row.Country,
		}, nil
	}
	return s.pii.OpenAddress(ctx, pii.ScopeProfile, pii.Sealed{
		ContactName:  row.ContactNameEnc,
		Phone:        row.PhoneEnc,
		AddressLine1: row.AddressLine1Enc,
		AddressLine2: row.AddressLine2Enc,
		Landmark:     row.LandmarkEnc,
	}, row.City, row.State, row.PostalCode, row.Country)
}

func (s *Service) sealSnapshot(ctx context.Context, a pii.Address) ([]byte, int, error) {
	if s.pii == nil {
		return nil, 0, nil
	}
	blob, err := json.Marshal(a)
	if err != nil {
		return nil, 0, err
	}
	// The order snapshot uses its own key scope, so profile-address
	// shredding cannot destroy an invoice record (review §5-D8).
	return s.pii.Seal(ctx, pii.ScopeOrderSnapshot, string(blob))
}

// fingerprint canonicalises the request so a retry with the same
// Idempotency-Key but different content is a conflict rather than a silent
// replay of the old order (M-7).
func fingerprint(in CheckoutInputP0, addr *pii.Address) string {
	h := sha256.New()
	for _, p := range []string{
		in.UserID.String(), in.AddressID.String(), in.QuoteID.String(),
		strings.ToLower(in.CouponCode), strings.ToLower(in.PaymentMethod),
		addr.PostalCode, addr.AddressLine1,
	} {
		h.Write([]byte(p))
		h.Write([]byte{0})
	}
	return base64.RawURLEncoding.EncodeToString(h.Sum(nil))
}

func (s *Service) sellerPickupPin(ctx context.Context, sellerID uuid.UUID) string {
	pin, err := s.store.SellerPickupPin(ctx, sellerID)
	if err != nil || pin == "" {
		return ""
	}
	return pin
}

// ─── Workers ─────────────────────────────────────────────────────────

// RunRefundWorker delivers durable refund commands to payments.
//
// LB-8. The old cancel path called payments inline and swallowed the error.
// Here a failure leaves the command claimable, and a permanent failure parks
// it in `needs_attention` — which is an alarm, not a shrug.
func (s *Service) RunRefundWorker(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		interval = 20 * time.Second
	}
	t := time.NewTicker(interval)
	defer t.Stop()
	slog.Info("commerce: refund worker started", "interval", interval)
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			cmds, err := s.store.ClaimDueRefundCommands(ctx, 25)
			if err != nil {
				slog.Warn("commerce: claim refund commands failed", "error", err)
				continue
			}
			for _, c := range cmds {
				s.deliverRefund(ctx, c)
			}
		}
	}
}

func (s *Service) deliverRefund(ctx context.Context, c postgres.RefundCommand) {
	if s.payments == nil {
		_ = s.store.MarkRefundFailed(ctx, c.ID, "payments client not configured", false)
		return
	}
	intentID := c.IntentID
	if intentID == "" {
		if id, err := s.store.OrderPaymentIntentID(ctx, c.OrderID); err == nil && id != uuid.Nil {
			intentID = id.String()
		}
	}
	if intentID == "" {
		// Nothing was ever captured. Park it visibly rather than retrying
		// against an intent that does not exist.
		_ = s.store.MarkRefundFailed(ctx, c.ID, "no payment intent for this order", true)
		return
	}
	iid, err := uuid.Parse(intentID)
	if err != nil {
		_ = s.store.MarkRefundFailed(ctx, c.ID, "malformed intent id", true)
		return
	}
	// A6: the SAME deterministic key on every attempt.
	if _, err := s.payments.Refund(ctx, iid, c.AmountMinor, c.Reason, c.IdempotencyKey); err != nil {
		terminal := errors.Is(err, payments.ErrRefused)
		slog.Warn("commerce: refund delivery failed",
			"command_id", c.ID, "order_id", c.OrderID, "terminal", terminal, "error", err)
		_ = s.store.MarkRefundFailed(ctx, c.ID, err.Error(), terminal)
		return
	}
	_ = s.store.MarkRefundSubmitted(ctx, c.ID)
	slog.Info("commerce: refund submitted to payments", "command_id", c.ID, "order_id", c.OrderID)
}

// RunReservationExpiry terminally expires unpaid orders whose hold lapsed.
//
// LB-22 / M-5. The old sweeper released the hold and left the order
// payment_pending, so a late capture could still apply against stock that
// had already been sold to someone else.
func (s *Service) RunReservationExpiry(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		interval = time.Minute
	}
	t := time.NewTicker(interval)
	defer t.Stop()
	slog.Info("commerce: reservation expiry worker started", "interval", interval)
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			n, err := s.store.ExpireStaleOrders(ctx, 200)
			if err != nil {
				slog.Warn("commerce: expiry sweep failed", "error", err)
				continue
			}
			if n > 0 {
				slog.Info("commerce: expired stale orders", "count", n)
			}
		}
	}
}
