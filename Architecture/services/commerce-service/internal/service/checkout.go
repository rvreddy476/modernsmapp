// Checkout and payment confirmation: placing the order, confirming a
// payment, the stub-provider guard and the order's initial state. Moved out
// of service.go unchanged.

package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/atpost/commerce-service/internal/money"
	"github.com/atpost/commerce-service/internal/store/postgres"
	"github.com/atpost/shared/events"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// ConfirmPaymentInput is the request body the customer-facing confirm
// endpoint accepts. The signature triple is what Razorpay returns on
// successful checkout; commerce-service does not trust any of these
// fields — they are forwarded to payments-service for HMAC verification
// against the stored intent.
type ConfirmPaymentInput struct {
	PaymentIntentID   uuid.UUID
	RazorpayOrderID   string
	RazorpayPaymentID string
	RazorpaySignature string
	AmountMinor       int64
	Gateway           string
}

type CheckoutInput struct {
	UserID         uuid.UUID
	AddressID      uuid.UUID
	PaymentMethod  string
	CouponCode     string
	GiftMessage    *string
	IdempotencyKey string

	// ─── Phase 5 — Optional B2B context ────────────────────────
	// When OrganizationID is set, the caller must be an active 'admin'
	// or 'buyer' member. The org's approval_threshold + credit_terms
	// are applied at order create time.
	OrganizationID *uuid.UUID
	PONumber       *string
	CostCenter     *string
	InvoiceEmail   *string
}

// Checkout commits the user's cart as an order. All pricing (line totals,
// coupon, shipping, tax, grand total) is computed by priceCart so a future
// quote API and any client-side display can never drift from the value
// actually persisted on the order.
func (s *Service) Checkout(ctx context.Context, in CheckoutInput) (*postgres.Order, error) {
	// H4 — idempotency dedup. When the caller supplies an Idempotency-Key
	// header and we already have an order for that (user, key) pair,
	// return it verbatim instead of double-creating + double-charging.
	// This must run before priceCart so a double-tap during a slow round
	// trip doesn't waste a pricing query.
	if in.IdempotencyKey != "" {
		if existing, err := s.store.GetOrderByIdempotencyKey(ctx, in.UserID, in.IdempotencyKey); err == nil && existing != nil {
			return existing, nil
		}
	}

	res, err := s.priceCart(ctx, in.UserID, in.PaymentMethod, in.CouponCode, true)
	if err != nil {
		return nil, err
	}

	idempKey := in.IdempotencyKey
	if idempKey == "" {
		idempKey = fmt.Sprintf("%s-%d", in.UserID, time.Now().UnixNano())
	}

	// COD orders skip the gateway: confirmed immediately, payment_status
	// stays "pending" (orders_payment_status_check allows
	// pending|processing|paid|failed|refund_*; there is no cod_pending —
	// downstream code distinguishes COD by reading payment_method='cod').
	addrSnapshot, _ := json.Marshal(map[string]any{"address_id": in.AddressID})
	pm := in.PaymentMethod
	isCOD := strings.EqualFold(pm, "cod")
	orderStatus, paymentStatus := checkoutInitialState(isCOD)

	// Phase 5 — B2B context: validate org membership, apply approval
	// threshold + credit terms. A buyer on an org with approval_threshold
	// set and order >= threshold can't pay yet — the order parks in
	// approval_status=pending and Status="awaiting_approval"; an approver
	// then green-lights it via ApproveOrgOrder.
	var (
		orgID           *uuid.UUID
		approvalStatus  *string
		creditTermsDays int
		paymentDueDate  *time.Time
	)
	if in.OrganizationID != nil {
		member, err := s.requireOrgRole(ctx, *in.OrganizationID, in.UserID, "admin", "buyer")
		if err != nil {
			return nil, fmt.Errorf("org checkout: %w", err)
		}
		_ = member // role already validated
		org, err := s.store.GetOrganizationByID(ctx, *in.OrganizationID)
		if err != nil || org == nil {
			return nil, ErrOrgNotFound
		}
		if org.Status != "active" {
			return nil, fmt.Errorf("organization not active")
		}
		orgID = &org.ID
		// Approval gate
		if org.ApprovalThreshold != nil && res.FinalAmount >= *org.ApprovalThreshold {
			s := "pending"
			approvalStatus = &s
			orderStatus = "awaiting_approval"
			paymentStatus = "pending"
		} else if in.OrganizationID != nil {
			s := "not_required"
			approvalStatus = &s
		}
		// Credit terms: net N days. Only applied when the org has terms
		// configured AND the buyer chose the credit payment method.
		if org.CreditTermsDays > 0 && strings.EqualFold(pm, "credit") {
			creditTermsDays = org.CreditTermsDays
			due := time.Now().AddDate(0, 0, org.CreditTermsDays)
			paymentDueDate = &due
			// Credit orders defer payment but the order is otherwise
			// confirmed once approval clears.
			if approvalStatus == nil || *approvalStatus != "pending" {
				orderStatus = "confirmed"
			}
		}
	}

	order := &postgres.Order{
		CustomerUserID:          in.UserID,
		Subtotal:                res.Subtotal,
		DiscountAmount:          0,
		ShippingCharges:         res.Shipping,
		TaxAmount:               res.Tax,
		CouponCode:              res.CouponCodePtr,
		CouponDiscount:          res.CouponDiscount,
		FinalAmount:             res.FinalAmount,
		CurrencyCode:            "INR",
		PaymentMethod:           &pm,
		PaymentStatus:           paymentStatus,
		DeliveryAddressID:       &in.AddressID,
		DeliveryAddressSnapshot: addrSnapshot,
		GiftMessage:             in.GiftMessage,
		Status:                  orderStatus,
		IdempotencyKey:          &idempKey,
		OrganizationID:          orgID,
		PONumber:                in.PONumber,
		CostCenter:              in.CostCenter,
		InvoiceEmail:            in.InvoiceEmail,
		ApprovalStatus:          approvalStatus,
		CreditTermsDays:         creditTermsDays,
		PaymentDueDate:          paymentDueDate,
	}

	if err := s.store.CreateOrder(ctx, order, res.OrderItems); err != nil {
		if in.IdempotencyKey != "" && isPostgresUniqueViolation(err) {
			if existing, lookupErr := s.store.GetOrderByIdempotencyKey(ctx, in.UserID, in.IdempotencyKey); lookupErr == nil && existing != nil {
				return existing, nil
			}
		}
		return nil, fmt.Errorf("create order: %w", err)
	}

	// Hard-reserve inventory (COD deducts immediately — no gateway step).
	for _, ci := range res.CartItems {
		if isCOD {
			if err := s.store.DeductStock(ctx, ci.VariantID, ci.Quantity, order.ID); err != nil {
				slog.Warn("failed to deduct stock for COD", "variant", ci.VariantID, "error", err)
			}
			continue
		}
		if err := s.store.ReserveStock(ctx, ci.VariantID, in.UserID, ci.Quantity, &order.ID, "order", 30*time.Minute); err != nil {
			slog.Warn("failed to reserve stock", "variant", ci.VariantID, "error", err)
		}
	}

	_ = s.store.ClearCart(ctx, res.Cart.ID)

	if res.CouponCodePtr != nil {
		if c, err := s.store.GetCouponByCode(ctx, *res.CouponCodePtr); err == nil {
			_ = s.store.IncrCouponUsage(ctx, c.ID, in.UserID, order.ID)
		}
	}

	buyerEmail, buyerName := s.resolveBuyer(ctx, in.UserID)
	var sellerEmail, sellerName string
	if len(res.OrderItems) > 0 {
		if seller, err := s.store.GetSellerByID(ctx, res.OrderItems[0].SellerID); err == nil {
			sellerEmail, sellerName = s.resolveSeller(seller)
		}
	}

	s.publish(ctx, "commerce.order.created", map[string]any{
		"order_id": order.ID, "user_id": in.UserID,
		"order_number": order.OrderNumber, "amount": order.FinalAmount,
		"payment_method": pm,
		"buyer_email":    buyerEmail,
		"buyer_name":     buyerName,
	})
	s.publish(ctx, events.EventCommerceSellerNewOrder, map[string]any{
		"order_id":     order.ID,
		"order_number": order.OrderNumber,
		"seller_id":    sellerIDOrNil(res.OrderItems),
		"amount":       order.FinalAmount,
		"seller_email": sellerEmail,
		"seller_name":  sellerName,
	})
	if isCOD {
		s.publish(ctx, events.EventCommerceOrderPaid, map[string]any{
			"order_id":       order.ID,
			"order_number":   order.OrderNumber,
			"user_id":        in.UserID,
			"amount":         order.FinalAmount,
			"payment_method": "cod",
			"buyer_email":    buyerEmail,
			"buyer_name":     buyerName,
		})
		// Phase 6.1 — durable enqueue replaces the fire-and-forget goroutine.
		s.EnqueueFulfillPaidOrder(ctx, order.ID)
	}
	return order, nil
}

func sellerIDOrNil(items []*postgres.OrderItem) *uuid.UUID {
	if len(items) == 0 {
		return nil
	}
	return &items[0].SellerID
}

// ConfirmPayment is the customer-facing confirm path invoked by the HTTP
// handler after Razorpay checkout returns. It is the safety-critical entry
// point: previously the handler trusted any (payment_id, gateway) pair the
// client sent and marked the order paid. Now we:
//
//  1. Require the actor to own the order.
//  2. Require the order to actually be payment_pending.
//  3. Forward the Razorpay signature triple to payments-service for HMAC
//     verification + amount check.
//  4. Refuse gateway=stub unless PAYMENTS_ALLOW_STUB is explicitly set.
//
// Idempotent — an already-paid order returns nil without re-running the
// fulfillment side effects.
func (s *Service) ConfirmPayment(ctx context.Context, orderID, actorID uuid.UUID, in ConfirmPaymentInput) error {
	order, err := s.getOrderForPayment(ctx, orderID)
	if err != nil {
		return err
	}
	if order.CustomerUserID != actorID {
		return ErrNotOrderOwner
	}
	if order.PaymentStatus == "paid" {
		return nil // idempotent: payment already applied
	}
	// v1 §5.3: this comparison read `order.PaymentStatus != "payment_pending"`,
	// testing the PAYMENT status against a value that only ever appears in
	// `orders.status` — and which the payment_status CHECK constraint does
	// not even permit. So every prepaid confirm returned
	// ErrOrderNotPaymentPending and the amount-verified branch below was
	// unreachable. The predicate is now postgres.OrderPayable — the same one
	// MarkOrderPaid enforces in SQL — so a confirm and the guard can never
	// disagree about which states accept money.
	if !postgres.OrderPayable(order.Status, order.PaymentStatus) {
		return fmt.Errorf("%w (status=%s payment_status=%s)", ErrOrderNotPaymentPending, order.Status, order.PaymentStatus)
	}

	gateway := in.Gateway
	if gateway == "" {
		gateway = "razorpay"
	}
	if gateway == "stub" && !s.stubGatewayAllowed() {
		return ErrStubGatewayInProd
	}
	if s.payments == nil {
		return ErrPaymentsClientMissing
	}

	// The intent a callback may be checked against is the one commerce
	// bound to the order at checkout — never one the client names. A client
	// that names a different intent is refused before payments is asked.
	intentID, err := s.orders.OrderPaymentIntentID(ctx, orderID)
	if err != nil || intentID == uuid.Nil {
		return ErrPaymentVerifyFailed
	}
	if in.PaymentIntentID != uuid.Nil && in.PaymentIntentID != intentID {
		slog.Warn("payment verify: client named an intent that is not the order's",
			"order_id", orderID, "bound_intent", intentID, "claimed_intent", in.PaymentIntentID)
		return ErrPaymentVerifyFailed
	}
	// The payable amount comes from `final_amount_minor`, which is what the
	// checkout wrote and what the payment intent was opened for. It used to
	// be ROUND(order.FinalAmount*100) — the NUMERIC mirror migration 007
	// stopped maintaining — so on every P0 order the expected amount was
	// ZERO, and a client that honestly reported what it paid was refused
	// with PAYMENT_AMOUNT_MISMATCH.
	expectedMinor := order.TotalMinor()
	if in.AmountMinor != 0 && in.AmountMinor != expectedMinor {
		return ErrPaymentAmountMismatch
	}

	// A1 / review R-3 — with a real gateway THIS PATH MARKS NOTHING PAID.
	//
	// It used to call VerifyIntent (which itself transitioned the intent to
	// `succeeded`) and then applyPaidStatus. That made a browser callback an
	// approval-capable evaluator: whatever the client handed back became
	// terminal order state as long as the HMAC checked out. A provider can
	// reverse a payment, or never capture it, after that callback — and we
	// would already have committed stock and started fulfilment.
	//
	// The callback is ADVISORY. A positive verdict tells the app the
	// redirect was genuine so it can leave the spinner and begin polling
	// GET /v1/commerce/orders/:orderId/payment/status. The order becomes
	// paid only when a signature-verified provider webhook reaches
	// Store.ApplyPaymentSucceeded, which re-checks amount, currency, payer
	// and intent against the order before committing any stock.
	verdict, err := s.payments.VerifyCallback(ctx, intentID,
		in.RazorpayOrderID, in.RazorpayPaymentID, in.RazorpaySignature,
		money.Paise(expectedMinor))
	if err != nil {
		slog.Warn("commerce: advisory callback verification failed",
			"order_id", orderID, "intent_id", intentID, "error", err)
		return ErrPaymentVerifyFailed
	}
	if verdict == nil || !verdict.Verified {
		return ErrPaymentVerifyFailed
	}
	// Bind the verified intent to this order + actor. payments-service
	// echoes the intent's reference/payer; an intent minted for another
	// order (or by another user) is refused even with a valid signature.
	// These checks can only make the verdict NEGATIVE.
	if verdict.ReferenceID != uuid.Nil && verdict.ReferenceID != orderID {
		slog.Warn("payment verify: intent references a different order",
			"order_id", orderID, "intent_id", intentID, "intent_ref", verdict.ReferenceID)
		return ErrPaymentVerifyFailed
	}
	if verdict.PayerID != uuid.Nil && verdict.PayerID != actorID {
		slog.Warn("payment verify: intent payer is not the order customer",
			"order_id", orderID, "intent_id", intentID)
		return ErrPaymentVerifyFailed
	}
	if verdict.AmountMinor != 0 && verdict.AmountMinor != expectedMinor {
		return ErrPaymentAmountMismatch
	}

	if gateway != "stub" {
		// Deliberately no state change: nil means "your callback looks
		// genuine", never "you have paid".
		return nil
	}

	// The stub gateway is the one case where the callback IS the last word:
	// no PSP exists to send a webhook, so nothing else can ever settle the
	// order.
	//
	// ─── AND THE FLAG ALONE IS NOT ENOUGH TO ESTABLISH THAT ──────────────
	//
	// PAYMENTS_ALLOW_STUB is a COMMERCE-side flag. It says what this service
	// was told, not what payments-service is actually running, and the two
	// can disagree — this dev stack currently does, with the flag on here
	// and real Razorpay test credentials over there. In that configuration a
	// webhook DOES exist, so treating a client callback as terminal would
	// restore exactly the authority A1/LB-3 removed: whoever holds a genuine
	// signature settles the order without the webhook's amount, currency,
	// payer and intent re-checks ever running.
	//
	// So the last question is put to payments-service itself. An intent
	// carries `client_session` if and only if a real Provider adapter is
	// configured there (payments' withClientSession attaches it from
	// h.provider). Its presence means a webhook is coming, and the callback
	// goes back to being advisory.
	//
	// Failing to ask is fatal rather than permissive: an unreachable
	// payments-service is not evidence that no provider exists.
	if err := s.assertNoRealProvider(ctx, intentID); err != nil {
		return err
	}

	// Same guarded transition the webhook consumer would take — one UPDATE,
	// converging with any concurrent caller, refusing a dead order.
	return s.applyPaidStatus(ctx, orderID, in.RazorpayPaymentID, gateway, &actorID, "customer")
}

// assertNoRealProvider refuses stub settlement when payments-service has a
// real PSP adapter configured.
//
// The signal is `client_session` on the intent: payments attaches it only
// when `h.provider != nil`, so it is present exactly when a provider exists
// that can sign — and therefore send — a webhook. Reading the fact off the
// intent rather than off our own environment means the two services cannot
// disagree about which settlement path is live.
func (s *Service) assertNoRealProvider(ctx context.Context, intentID uuid.UUID) error {
	intent, err := s.payments.GetIntent(ctx, intentID)
	if err != nil {
		slog.Error("commerce: refusing stub settlement — payments-service could not be asked "+
			"which provider is configured", "intent_id", intentID, "error", err)
		return ErrPaymentVerifyFailed
	}
	// payments-service in stub mode now names itself: it attaches a session
	// whose provider is "stub" (so a client can detect the stub by that
	// field rather than by sniffing the order handle). That session is the
	// stub's, not a provider's, and is the ONE value admitted here. Any
	// other provider name, or a session with no provider named at all, is
	// a real adapter and the webhook settles.
	if intent != nil && len(intent.ClientSession) > 0 && intent.ClientSession["provider"] != StubProviderName {
		slog.Warn("commerce: refusing stub settlement — payments-service has a real provider "+
			"configured, so the signature-verified webhook is the settlement path",
			"intent_id", intentID, "provider", intent.ClientSession["provider"])
		return ErrStubGatewayInProd
	}
	return nil
}

// StubProviderName is what payments-service's stub mode writes as
// client_session.provider. Only this value lets ConfirmPayment settle.
const StubProviderName = "stub"

// checkoutInitialState is the (status, payment_status) pair a fresh order
// is created in. Prepaid orders park in payment_pending/pending until the
// gateway confirms; COD orders are confirmed immediately with payment
// still pending (there is no cod_pending value — downstream code reads
// payment_method='cod'). The pair MUST satisfy postgres.OrderPayable, or
// ConfirmPayment / the payments consumer can never move the order to
// paid — that was the bug behind the always-409 confirm endpoint, which
// compared payment_status against the *order* status value
// "payment_pending". The unit test pins the two together.
func checkoutInitialState(isCOD bool) (orderStatus, paymentStatus string) {
	if isCOD {
		return "confirmed", "pending"
	}
	return "payment_pending", "pending"
}

// getOrderForPayment loads an order for the money path, mapping the
// store's "no rows" into ErrOrderNotFound (GetOrderByID returns a
// non-nil pointer alongside pgx.ErrNoRows).
func (s *Service) getOrderForPayment(ctx context.Context, orderID uuid.UUID) (*postgres.Order, error) {
	if s.orders == nil {
		return nil, fmt.Errorf("order store not configured")
	}
	order, err := s.orders.GetOrderByID(ctx, orderID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) || errors.Is(err, postgres.ErrOrderNotFound) {
			return nil, ErrOrderNotFound
		}
		return nil, err
	}
	if order == nil {
		return nil, ErrOrderNotFound
	}
	return order, nil
}
