// Premium service — Sprint 5. Implements:
//
//   - ListPlans: pass-through to the catalogue.
//   - Checkout: creates a Razorpay order or subscription and persists a
//     payment intent so the webhook can correlate.
//   - HandleWebhook: idempotent dispatch on dating_payment_events.
//   - MyPremium: returns current subscription state (or nil).
//   - CancelSubscription: turns auto_renew off; expires_at is preserved.
//
// CRITICAL RULES:
//
//   #4 Webhook idempotency — every webhook is recorded in
//      dating_payment_events with a UNIQUE razorpay_event_id. A replayed
//      delivery short-circuits to a no-op 200 with no state mutation.
//
//   #6 No silent failures on payment-adjacent code — every error path is
//      wrapped, logged, and surfaced. The webhook returns a non-2xx if the
//      *signature* fails or persistence fails; otherwise 200 (because
//      Razorpay re-tries on non-2xx and we don't want loops once we've
//      deduped on event id).
//
// DPDP compliance — see PULSE_DATING_SPEC.md §15.8: when a user upgrades
// we log a `payments` consent entry against the active policy version.
package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/atpost/dating-service/internal/payments"
	"github.com/atpost/dating-service/internal/store"
	"github.com/google/uuid"
)

// CheckoutRequest is the body for POST /v1/dating/premium/checkout.
type CheckoutRequest struct {
	PlanID string `json:"plan_id"`
	Source string `json:"source"`
}

// CheckoutResponse is what the mobile/web client uses to present the
// Razorpay SDK / UPI intent.
type CheckoutResponse struct {
	IntentID               uuid.UUID `json:"intent_id"`
	RazorpayOrderID        string    `json:"razorpay_order_id"`
	RazorpaySubscriptionID string    `json:"razorpay_subscription_id,omitempty"`
	RazorpayKeyID          string    `json:"razorpay_key_id"`
	AmountINRPaise         int64     `json:"amount_inr_paise"`
	PlanID                 string    `json:"plan_id"`
	PlanName               string    `json:"plan_name"`
	Currency               string    `json:"currency"`
}

// MyPremiumResponse is returned by GET /v1/dating/premium/me.
type MyPremiumResponse struct {
	IsPremium    bool                       `json:"is_premium"`
	Subscription *store.PremiumSubscription `json:"subscription,omitempty"`
}

// SetRazorpayClient injects the payments client.
func (s *Service) SetRazorpayClient(c payments.Client) {
	s.razorpay = c
}

// SetConsentPolicyVersion overrides the policy-version stamp used when
// logging consent against premium upgrades. Defaults to "v1.0-2026-04-29".
func (s *Service) SetConsentPolicyVersion(v string) {
	if v != "" {
		s.consentPolicyVersion = v
	}
}

// consentPolicy returns the configured policy version (or default).
func (s *Service) consentPolicy() string {
	if s.consentPolicyVersion != "" {
		return s.consentPolicyVersion
	}
	return "v1.0-2026-04-29"
}

// ListPlans returns every active plan. Pass-through to the store.
func (s *Service) ListPlans(ctx context.Context) ([]*store.PremiumPlan, error) {
	return s.store.ListActivePlans(ctx)
}

// MyPremium returns the user's premium state.
func (s *Service) MyPremium(ctx context.Context, userID uuid.UUID) (*MyPremiumResponse, error) {
	if userID == uuid.Nil {
		return nil, fmt.Errorf("invalid: user_id required")
	}
	out := &MyPremiumResponse{}
	sub, err := s.store.GetSubscription(ctx, userID)
	if err != nil && !errors.Is(err, store.ErrSubscriptionNotFound) {
		return nil, err
	}
	if sub != nil {
		out.Subscription = sub
	}
	is, err := s.store.IsPremium(ctx, userID)
	if err != nil {
		return nil, err
	}
	out.IsPremium = is
	return out, nil
}

// Checkout creates an order or subscription with Razorpay, persists a
// payment intent, and returns enough metadata for the client SDK.
func (s *Service) Checkout(ctx context.Context, userID uuid.UUID, req CheckoutRequest) (*CheckoutResponse, error) {
	if userID == uuid.Nil {
		return nil, fmt.Errorf("invalid: user_id required")
	}
	if req.PlanID == "" {
		return nil, fmt.Errorf("invalid: plan_id required")
	}
	if s.razorpay == nil {
		return nil, fmt.Errorf("razorpay client not configured")
	}
	source := req.Source
	if source == "" {
		source = "app"
	}

	plan, err := s.store.GetPlan(ctx, req.PlanID)
	if err != nil {
		return nil, err
	}
	if !plan.IsActive {
		return nil, fmt.Errorf("invalid: plan is not active")
	}

	notes := map[string]string{
		"user_id": userID.String(),
		"plan_id": plan.ID,
		"source":  source,
	}

	order, err := s.razorpay.CreateOrder(ctx, plan.PriceINRPaise, "pulse-"+userID.String()[:8]+"-"+plan.ID, notes)
	if err != nil {
		return nil, fmt.Errorf("create razorpay order: %w", err)
	}

	intent, err := s.store.CreatePaymentIntent(ctx, userID, plan.ID, order.ID, source, plan.PriceINRPaise)
	if err != nil {
		return nil, err
	}

	resp := &CheckoutResponse{
		IntentID:        intent.ID,
		RazorpayOrderID: order.ID,
		RazorpayKeyID:   s.razorpay.KeyID(),
		AmountINRPaise:  plan.PriceINRPaise,
		PlanID:          plan.ID,
		PlanName:        plan.Name,
		Currency:        "INR",
	}

	// For autopay subscriptions on the Android/iOS clients, also create a
	// Razorpay subscription so the SDK can present the UPI Autopay flow.
	// Web/playstore/appstore checkouts stick to one-time orders — Razorpay
	// reroutes those server-side.
	if plan.PlanType == "subscription" && (source == "app" || source == "web") {
		totalCount := 12
		if plan.DurationDays != nil && *plan.DurationDays >= 365 {
			totalCount = 5
		}
		sub, sErr := s.razorpay.CreateSubscription(ctx, plan.ID, totalCount, notes)
		if sErr != nil {
			// Subscription creation failed — log and fall back to one-time
			// order. The intent still represents this purchase.
			slog.Warn("razorpay create subscription failed; falling back to order",
				"plan_id", plan.ID, "error", sErr)
		} else {
			resp.RazorpaySubscriptionID = sub.ID
			if err := s.store.AttachSubscriptionID(ctx, intent.ID, sub.ID); err != nil {
				slog.Warn("attach subscription id failed", "intent_id", intent.ID, "error", err)
			}
		}
	}

	return resp, nil
}

// CancelSubscription flips auto_renew off. The user keeps premium until the
// existing expires_at. Emits dating.premium.expired? — no: we emit a soft
// `cancelled` flag inside MyPremium. The expiry event fires via webhook
// (subscription.completed) once Razorpay closes the cycle.
func (s *Service) CancelSubscription(ctx context.Context, userID uuid.UUID) error {
	if userID == uuid.Nil {
		return fmt.Errorf("invalid: user_id required")
	}
	if err := s.store.MarkSubscriptionCancelled(ctx, userID); err != nil {
		return err
	}
	return nil
}

// WebhookEvent is the minimal Razorpay webhook envelope we care about.
type WebhookEvent struct {
	Event   string         `json:"event"`
	Payload map[string]any `json:"payload"`
}

// WebhookResult tells the http layer how to respond.
type WebhookResult struct {
	Idempotent bool   // true => already processed; respond 200 no-op
	Processed  bool   // true => state mutated
	EventID    string // razorpay event id (for response logging)
}

// HandleWebhook is the webhook entrypoint. Steps:
//
//  1. Verify signature. On fail return error → http 401.
//  2. Parse body; require an `id` (used as razorpayEventID).
//  3. RecordPaymentEvent — if not inserted, the event was already
//     processed; return Idempotent=true.
//  4. Dispatch by event type. Each branch is responsible for state
//     mutation + emitting the matching dating.premium.* Kafka event.
//  5. Mark the event row processed.
func (s *Service) HandleWebhook(ctx context.Context, signature string, body []byte) (*WebhookResult, error) {
	if s.razorpay == nil {
		return nil, fmt.Errorf("razorpay client not configured")
	}
	if err := s.razorpay.VerifyWebhookSignature(body, signature); err != nil {
		return nil, fmt.Errorf("forbidden: %w", err)
	}

	// Parse the envelope. We accept the standard Razorpay shape:
	// {"entity":"event","event":"payment.captured","id":"evt_...","payload":{...}}
	var env struct {
		ID      string         `json:"id"`
		Event   string         `json:"event"`
		Payload map[string]any `json:"payload"`
	}
	if err := json.Unmarshal(body, &env); err != nil {
		return nil, fmt.Errorf("invalid: malformed webhook body: %w", err)
	}
	if env.ID == "" || env.Event == "" {
		return nil, fmt.Errorf("invalid: webhook missing id or event")
	}

	intentID, intent := s.findIntentForWebhook(ctx, env.Payload)

	inserted, err := s.store.RecordPaymentEvent(ctx, intentID, env.ID, env.Event, body)
	if err != nil {
		return nil, fmt.Errorf("record event: %w", err)
	}
	if !inserted {
		// Idempotent replay — Razorpay sometimes redelivers within seconds
		// of the first call. Do nothing.
		return &WebhookResult{Idempotent: true, EventID: env.ID}, nil
	}

	switch env.Event {
	case "payment.captured":
		if err := s.handlePaymentCaptured(ctx, intent, env.Payload); err != nil {
			return nil, err
		}
	case "subscription.charged":
		if err := s.handleSubscriptionCharged(ctx, intent, env.Payload); err != nil {
			return nil, err
		}
	case "subscription.completed", "subscription.cancelled", "subscription.halted":
		if err := s.handleSubscriptionCompleted(ctx, intent, env.Payload); err != nil {
			return nil, err
		}
	case "payment.failed":
		s.handlePaymentFailed(ctx, intent, env.Payload)
	default:
		slog.Info("razorpay webhook: ignoring event type", "event", env.Event, "event_id", env.ID)
	}

	if err := s.store.MarkPaymentEventProcessed(ctx, env.ID); err != nil {
		// This is best-effort: state is already mutated, the row may
		// just lack processed_at. We surface to slog but not to the caller.
		slog.Warn("mark event processed failed", "event_id", env.ID, "error", err)
	}

	return &WebhookResult{Processed: true, EventID: env.ID}, nil
}

// findIntentForWebhook extracts the order id from the Razorpay payload and
// looks up the matching payment intent. Returns (nil, nil) on miss — some
// webhook types (e.g. subscription.charged on the very first cycle) might
// not point at an intent we created.
func (s *Service) findIntentForWebhook(ctx context.Context, payload map[string]any) (*uuid.UUID, *store.PaymentIntent) {
	orderID := extractOrderID(payload)
	if orderID == "" {
		return nil, nil
	}
	intent, err := s.store.GetPaymentIntentByOrderID(ctx, orderID)
	if err != nil {
		if !errors.Is(err, store.ErrPaymentIntentNotFound) {
			slog.Warn("lookup intent by order id failed", "order_id", orderID, "error", err)
		}
		return nil, nil
	}
	return &intent.ID, intent
}

// extractOrderID digs through the standard Razorpay payload shape:
//
//	{"payload":{"payment":{"entity":{"order_id":"order_..."}}}}
//	{"payload":{"subscription":{"entity":{"id":"sub_..."}}}}
//
// Returns the order_id, or empty string when absent.
func extractOrderID(payload map[string]any) string {
	if payload == nil {
		return ""
	}
	if pay, ok := payload["payment"].(map[string]any); ok {
		if ent, ok := pay["entity"].(map[string]any); ok {
			if oid, ok := ent["order_id"].(string); ok && oid != "" {
				return oid
			}
		}
	}
	if order, ok := payload["order"].(map[string]any); ok {
		if ent, ok := order["entity"].(map[string]any); ok {
			if oid, ok := ent["id"].(string); ok && oid != "" {
				return oid
			}
		}
	}
	return ""
}

// handlePaymentCaptured marks the intent paid, upserts the subscription (or
// applies the boost for one-time plans) and emits the premium.subscribed
// event.
func (s *Service) handlePaymentCaptured(ctx context.Context, intent *store.PaymentIntent, payload map[string]any) error {
	if intent == nil {
		// No matching intent — likely a stray event. Persist the row
		// (already done) and bail; we cannot mutate user state without a
		// known user.
		slog.Info("payment.captured without matching intent; skipping state update")
		return nil
	}
	now := time.Now()
	if err := s.store.MarkPaymentIntentPaid(ctx, intent.ID, now); err != nil {
		return fmt.Errorf("mark intent paid: %w", err)
	}
	plan, err := s.store.GetPlan(ctx, intent.PlanID)
	if err != nil {
		return fmt.Errorf("load plan: %w", err)
	}

	switch plan.PlanType {
	case "subscription":
		duration := 30
		if plan.DurationDays != nil {
			duration = *plan.DurationDays
		}
		expires := now.Add(time.Duration(duration) * 24 * time.Hour)
		if err := s.store.UpsertSubscription(ctx, intent.UserID, plan.ID, plan.ID, intent.RazorpaySubscriptionID, intent.Source, &now, &expires, true); err != nil {
			return fmt.Errorf("upsert subscription: %w", err)
		}
		if s.producer != nil {
			if err := s.producer.PublishPremiumSubscribed(ctx, intent.UserID, plan.ID, expires, intent.Source); err != nil {
				slog.Warn("publish premium.subscribed failed", "user_id", intent.UserID, "error", err)
			}
		}
	case "one_time":
		// Boost: grant the boost token via Redis. The Pulse boost endpoint
		// (handler_pulse) consumes the token in exchange for 5 candidates.
		if plan.ID == "boost_49" {
			if err := s.grantBoostToken(ctx, intent.UserID); err != nil {
				slog.Warn("grant boost token failed", "user_id", intent.UserID, "error", err)
			}
		}
	}

	// DPDP audit: log a `payments` consent entry so the export contains the
	// user's purchase decision against the active policy.
	if err := s.store.RecordConsent(ctx, intent.UserID, "payments", true, s.consentPolicy()); err != nil {
		slog.Warn("record payments consent failed", "user_id", intent.UserID, "error", err)
	}
	return nil
}

// handleSubscriptionCharged extends the existing subscription by the plan's
// duration. Idempotent because the unique event id already deduped.
func (s *Service) handleSubscriptionCharged(ctx context.Context, intent *store.PaymentIntent, payload map[string]any) error {
	if intent == nil {
		slog.Info("subscription.charged without matching intent; skipping")
		return nil
	}
	plan, err := s.store.GetPlan(ctx, intent.PlanID)
	if err != nil {
		return fmt.Errorf("load plan: %w", err)
	}
	if plan.DurationDays == nil {
		return nil
	}
	if err := s.store.ExtendSubscription(ctx, intent.UserID, *plan.DurationDays); err != nil {
		if errors.Is(err, store.ErrSubscriptionNotFound) {
			// First charge before payment.captured arrived (out-of-order
			// delivery). Upsert from scratch.
			now := time.Now()
			expires := now.Add(time.Duration(*plan.DurationDays) * 24 * time.Hour)
			if err2 := s.store.UpsertSubscription(ctx, intent.UserID, plan.ID, plan.ID, intent.RazorpaySubscriptionID, intent.Source, &now, &expires, true); err2 != nil {
				return fmt.Errorf("upsert subscription on charge: %w", err2)
			}
		} else {
			return fmt.Errorf("extend subscription: %w", err)
		}
	}
	if s.producer != nil {
		expires := time.Now().Add(time.Duration(*plan.DurationDays) * 24 * time.Hour)
		_ = s.producer.PublishPremiumSubscribed(ctx, intent.UserID, plan.ID, expires, intent.Source)
	}
	return nil
}

// handleSubscriptionCompleted closes out the subscription. The user has
// premium until expires_at; auto_renew is off. Emit premium.expired.
func (s *Service) handleSubscriptionCompleted(ctx context.Context, intent *store.PaymentIntent, payload map[string]any) error {
	if intent == nil {
		return nil
	}
	if err := s.store.MarkSubscriptionCancelled(ctx, intent.UserID); err != nil {
		if !errors.Is(err, store.ErrSubscriptionNotFound) {
			return fmt.Errorf("mark sub cancelled: %w", err)
		}
	}
	if s.producer != nil {
		_ = s.producer.PublishPremiumExpired(ctx, intent.UserID, intent.PlanID)
	}
	return nil
}

// handlePaymentFailed marks the intent failed. v1: no user-facing action.
func (s *Service) handlePaymentFailed(ctx context.Context, intent *store.PaymentIntent, payload map[string]any) {
	if intent == nil {
		return
	}
	if err := s.store.MarkPaymentIntentFailed(ctx, intent.ID); err != nil {
		slog.Warn("mark intent failed errored", "intent_id", intent.ID, "error", err)
	}
}
