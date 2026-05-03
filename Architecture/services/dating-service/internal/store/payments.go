// Payments store — Sprint 5. See PULSE_DATING_SPEC.md §14 (premium tier).
//
// CRITICAL RULES #4 (idempotent webhooks): RecordPaymentEvent uses ON
// CONFLICT (razorpay_event_id) DO NOTHING. A retried delivery returns
// inserted=false and the caller short-circuits to a 200 with no state
// mutation.
//
// CRITICAL RULES #6 (no silent failures): every error path here is wrapped
// with %w and surfaced. The service layer is the only place that may swallow
// — and only via slog.Warn for non-payment-affecting telemetry hops.
package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// PremiumPlan is one row of dating_premium_plans.
type PremiumPlan struct {
	ID            string    `json:"id"`
	PlanType      string    `json:"plan_type"`
	Name          string    `json:"name"`
	PriceINRPaise int64     `json:"price_inr_paise"`
	DurationDays  *int      `json:"duration_days,omitempty"`
	Description   string    `json:"description,omitempty"`
	IsActive      bool      `json:"is_active"`
	CreatedAt     time.Time `json:"created_at"`
}

// PaymentIntent is one row of dating_payment_intents.
type PaymentIntent struct {
	ID                     uuid.UUID  `json:"id"`
	UserID                 uuid.UUID  `json:"user_id"`
	PlanID                 string     `json:"plan_id"`
	AmountINRPaise         int64      `json:"amount_inr_paise"`
	RazorpayOrderID        string     `json:"razorpay_order_id"`
	RazorpaySubscriptionID *string    `json:"razorpay_subscription_id,omitempty"`
	Status                 string     `json:"status"`
	Source                 string     `json:"source"`
	CreatedAt              time.Time  `json:"created_at"`
	PaidAt                 *time.Time `json:"paid_at,omitempty"`
}

// PremiumSubscription is the user-facing subscription state.
type PremiumSubscription struct {
	UserID                 uuid.UUID  `json:"user_id"`
	Plan                   string     `json:"plan"`
	PlanID                 *string    `json:"plan_id,omitempty"`
	RazorpaySubscriptionID *string    `json:"razorpay_subscription_id,omitempty"`
	StartedAt              time.Time  `json:"started_at"`
	ExpiresAt              *time.Time `json:"expires_at,omitempty"`
	Source                 *string    `json:"source,omitempty"`
	AutoRenew              bool       `json:"auto_renew"`
	CancelledAt            *time.Time `json:"cancelled_at,omitempty"`
}

// ErrPlanNotFound / ErrPaymentIntentNotFound mirror the package's
// not_found-prefix convention so respondServiceError lifts them to 404.
var (
	ErrPlanNotFound          = errors.New("not_found: plan not found")
	ErrPaymentIntentNotFound = errors.New("not_found: payment intent not found")
	ErrSubscriptionNotFound  = errors.New("not_found: subscription not found")
)

// SeedPremiumPlans upserts the canonical plan catalogue. Called from main.go
// on boot. Idempotent: re-running keeps the existing rows in sync.
//
// monthly_399      Pulse Premium Monthly  ₹399  / 30 days
// quarterly_999    Pulse Premium Quarterly ₹999 / 90 days
// yearly_2499      Pulse Premium Yearly   ₹2499 / 365 days
// boost_49         Pulse Boost            ₹49   / one-time
func (s *Store) SeedPremiumPlans(ctx context.Context) error {
	plans := []PremiumPlan{
		{ID: "monthly_399", PlanType: "subscription", Name: "Pulse Premium Monthly", PriceINRPaise: 39900, DurationDays: ptrInt(30), Description: "30 days of premium features."},
		{ID: "quarterly_999", PlanType: "subscription", Name: "Pulse Premium Quarterly", PriceINRPaise: 99900, DurationDays: ptrInt(90), Description: "90 days of premium features."},
		{ID: "yearly_2499", PlanType: "subscription", Name: "Pulse Premium Yearly", PriceINRPaise: 249900, DurationDays: ptrInt(365), Description: "365 days of premium features."},
		{ID: "boost_49", PlanType: "one_time", Name: "Pulse Boost", PriceINRPaise: 4900, DurationDays: nil, Description: "Single Pulse Boost — 5 extra candidates today."},
	}
	for _, p := range plans {
		_, err := s.db.Exec(ctx, `
            INSERT INTO dating_premium_plans (id, plan_type, name, price_inr_paise, duration_days, description, is_active)
            VALUES ($1, $2, $3, $4, $5, $6, true)
            ON CONFLICT (id) DO UPDATE
                SET plan_type       = EXCLUDED.plan_type,
                    name            = EXCLUDED.name,
                    price_inr_paise = EXCLUDED.price_inr_paise,
                    duration_days   = EXCLUDED.duration_days,
                    description     = EXCLUDED.description,
                    is_active       = true`,
			p.ID, p.PlanType, p.Name, p.PriceINRPaise, p.DurationDays, p.Description)
		if err != nil {
			return fmt.Errorf("seed plan %s: %w", p.ID, err)
		}
	}
	return nil
}

func ptrInt(i int) *int { return &i }

// ListActivePlans returns every active plan ordered by price ascending.
func (s *Store) ListActivePlans(ctx context.Context) ([]*PremiumPlan, error) {
	rows, err := s.db.Query(ctx, `
        SELECT id, plan_type, name, price_inr_paise, duration_days, description, is_active, created_at
        FROM dating_premium_plans
        WHERE is_active = true
        ORDER BY price_inr_paise ASC`)
	if err != nil {
		return nil, fmt.Errorf("list active plans: %w", err)
	}
	defer rows.Close()
	out := make([]*PremiumPlan, 0, 4)
	for rows.Next() {
		p := &PremiumPlan{}
		var desc *string
		if err := rows.Scan(&p.ID, &p.PlanType, &p.Name, &p.PriceINRPaise, &p.DurationDays, &desc, &p.IsActive, &p.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan plan: %w", err)
		}
		if desc != nil {
			p.Description = *desc
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// GetPlan fetches one plan or returns ErrPlanNotFound.
func (s *Store) GetPlan(ctx context.Context, id string) (*PremiumPlan, error) {
	row := s.db.QueryRow(ctx, `
        SELECT id, plan_type, name, price_inr_paise, duration_days, description, is_active, created_at
        FROM dating_premium_plans
        WHERE id = $1`, id)
	p := &PremiumPlan{}
	var desc *string
	if err := row.Scan(&p.ID, &p.PlanType, &p.Name, &p.PriceINRPaise, &p.DurationDays, &desc, &p.IsActive, &p.CreatedAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrPlanNotFound
		}
		return nil, fmt.Errorf("get plan: %w", err)
	}
	if desc != nil {
		p.Description = *desc
	}
	return p, nil
}

// CreatePaymentIntent inserts a `created` row and returns it. razorpayOrderID
// is the unique idempotency anchor — a duplicate from a retried Checkout
// call returns ErrPaymentIntentDuplicate.
func (s *Store) CreatePaymentIntent(ctx context.Context, userID uuid.UUID, planID, razorpayOrderID, source string, amountPaise int64) (*PaymentIntent, error) {
	if userID == uuid.Nil {
		return nil, fmt.Errorf("invalid: user_id required")
	}
	if planID == "" {
		return nil, fmt.Errorf("invalid: plan_id required")
	}
	if razorpayOrderID == "" {
		return nil, fmt.Errorf("invalid: razorpay_order_id required")
	}
	if amountPaise <= 0 {
		return nil, fmt.Errorf("invalid: amount must be positive")
	}
	if source == "" {
		source = "app"
	}
	out := &PaymentIntent{}
	err := s.db.QueryRow(ctx, `
        INSERT INTO dating_payment_intents (user_id, plan_id, amount_inr_paise, razorpay_order_id, source, status)
        VALUES ($1, $2, $3, $4, $5, 'created')
        RETURNING id, user_id, plan_id, amount_inr_paise, razorpay_order_id,
                  razorpay_subscription_id, status, source, created_at, paid_at`,
		userID, planID, amountPaise, razorpayOrderID, source,
	).Scan(&out.ID, &out.UserID, &out.PlanID, &out.AmountINRPaise, &out.RazorpayOrderID,
		&out.RazorpaySubscriptionID, &out.Status, &out.Source, &out.CreatedAt, &out.PaidAt)
	if err != nil {
		return nil, fmt.Errorf("create payment intent: %w", err)
	}
	return out, nil
}

// AttachSubscriptionID stamps the razorpay_subscription_id onto an intent.
func (s *Store) AttachSubscriptionID(ctx context.Context, intentID uuid.UUID, subID string) error {
	tag, err := s.db.Exec(ctx, `
        UPDATE dating_payment_intents
        SET razorpay_subscription_id = $2
        WHERE id = $1`, intentID, subID)
	if err != nil {
		return fmt.Errorf("attach sub id: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrPaymentIntentNotFound
	}
	return nil
}

// GetPaymentIntent fetches by id.
func (s *Store) GetPaymentIntent(ctx context.Context, id uuid.UUID) (*PaymentIntent, error) {
	row := s.db.QueryRow(ctx, `
        SELECT id, user_id, plan_id, amount_inr_paise, razorpay_order_id,
               razorpay_subscription_id, status, source, created_at, paid_at
        FROM dating_payment_intents
        WHERE id = $1`, id)
	out := &PaymentIntent{}
	if err := row.Scan(&out.ID, &out.UserID, &out.PlanID, &out.AmountINRPaise, &out.RazorpayOrderID,
		&out.RazorpaySubscriptionID, &out.Status, &out.Source, &out.CreatedAt, &out.PaidAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrPaymentIntentNotFound
		}
		return nil, fmt.Errorf("get payment intent: %w", err)
	}
	return out, nil
}

// GetPaymentIntentByOrderID fetches by Razorpay order id (the webhook key).
func (s *Store) GetPaymentIntentByOrderID(ctx context.Context, orderID string) (*PaymentIntent, error) {
	row := s.db.QueryRow(ctx, `
        SELECT id, user_id, plan_id, amount_inr_paise, razorpay_order_id,
               razorpay_subscription_id, status, source, created_at, paid_at
        FROM dating_payment_intents
        WHERE razorpay_order_id = $1`, orderID)
	out := &PaymentIntent{}
	if err := row.Scan(&out.ID, &out.UserID, &out.PlanID, &out.AmountINRPaise, &out.RazorpayOrderID,
		&out.RazorpaySubscriptionID, &out.Status, &out.Source, &out.CreatedAt, &out.PaidAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrPaymentIntentNotFound
		}
		return nil, fmt.Errorf("get payment intent by order: %w", err)
	}
	return out, nil
}

// MarkPaymentIntentPaid flips status='paid' and stamps paid_at. Idempotent —
// a second call updates nothing and returns nil (the row is already paid).
func (s *Store) MarkPaymentIntentPaid(ctx context.Context, intentID uuid.UUID, paidAt time.Time) error {
	if paidAt.IsZero() {
		paidAt = time.Now()
	}
	tag, err := s.db.Exec(ctx, `
        UPDATE dating_payment_intents
        SET status = 'paid', paid_at = COALESCE(paid_at, $2)
        WHERE id = $1 AND status != 'paid'`, intentID, paidAt)
	if err != nil {
		return fmt.Errorf("mark intent paid: %w", err)
	}
	_ = tag
	return nil
}

// MarkPaymentIntentFailed flips status='failed' (terminal). Idempotent.
func (s *Store) MarkPaymentIntentFailed(ctx context.Context, intentID uuid.UUID) error {
	_, err := s.db.Exec(ctx, `
        UPDATE dating_payment_intents
        SET status = 'failed'
        WHERE id = $1 AND status NOT IN ('paid','failed','cancelled')`, intentID)
	if err != nil {
		return fmt.Errorf("mark intent failed: %w", err)
	}
	return nil
}

// RecordPaymentEvent inserts the webhook delivery into dating_payment_events.
// Returns (inserted, error). When inserted=false the caller MUST short-
// circuit the webhook to a no-op 200 — the event was already processed.
func (s *Store) RecordPaymentEvent(ctx context.Context, intentID *uuid.UUID, razorpayEventID, eventType string, payload []byte) (bool, error) {
	if razorpayEventID == "" {
		return false, fmt.Errorf("invalid: razorpay_event_id required")
	}
	if eventType == "" {
		return false, fmt.Errorf("invalid: event_type required")
	}
	if len(payload) == 0 {
		// jsonb NOT NULL — store an empty object rather than fail.
		payload = []byte(`{}`)
	} else if !json.Valid(payload) {
		return false, fmt.Errorf("invalid: payload must be valid JSON")
	}
	tag, err := s.db.Exec(ctx, `
        INSERT INTO dating_payment_events (payment_intent_id, razorpay_event_id, event_type, payload)
        VALUES ($1, $2, $3, $4::jsonb)
        ON CONFLICT (razorpay_event_id) DO NOTHING`,
		intentID, razorpayEventID, eventType, payload)
	if err != nil {
		return false, fmt.Errorf("record payment event: %w", err)
	}
	return tag.RowsAffected() > 0, nil
}

// MarkPaymentEventProcessed stamps processed_at on the event row. Best-
// effort: a missing row is not an error (the unique index already gated us).
func (s *Store) MarkPaymentEventProcessed(ctx context.Context, razorpayEventID string) error {
	_, err := s.db.Exec(ctx, `
        UPDATE dating_payment_events
        SET processed_at = now()
        WHERE razorpay_event_id = $1 AND processed_at IS NULL`, razorpayEventID)
	if err != nil {
		return fmt.Errorf("mark event processed: %w", err)
	}
	return nil
}

// UpsertSubscription writes the subscription row. Mirrors the spec §14
// behaviour: a re-subscription extends expires_at; a charge re-uses the same
// row and rolls expires_at forward.
func (s *Store) UpsertSubscription(ctx context.Context, userID uuid.UUID, planID, plan string, razorpaySubscriptionID *string, source string, startedAt, expiresAt *time.Time, autoRenew bool) error {
	if userID == uuid.Nil {
		return fmt.Errorf("invalid: user_id required")
	}
	if plan == "" {
		plan = planID
	}
	start := time.Now()
	if startedAt != nil && !startedAt.IsZero() {
		start = *startedAt
	}
	_, err := s.db.Exec(ctx, `
        INSERT INTO dating_premium_subscriptions
            (user_id, plan, plan_id, razorpay_subscription_id, started_at, expires_at, source, auto_renew, cancelled_at)
        VALUES ($1, $2, $3, $4, $5, $6, $7, $8, NULL)
        ON CONFLICT (user_id) DO UPDATE
            SET plan                     = EXCLUDED.plan,
                plan_id                  = EXCLUDED.plan_id,
                razorpay_subscription_id = COALESCE(EXCLUDED.razorpay_subscription_id, dating_premium_subscriptions.razorpay_subscription_id),
                expires_at               = GREATEST(
                    COALESCE(dating_premium_subscriptions.expires_at, EXCLUDED.expires_at),
                    EXCLUDED.expires_at
                ),
                source                   = COALESCE(EXCLUDED.source, dating_premium_subscriptions.source),
                auto_renew               = EXCLUDED.auto_renew,
                cancelled_at             = NULL`,
		userID, plan, planID, razorpaySubscriptionID, start, expiresAt, source, autoRenew)
	if err != nil {
		return fmt.Errorf("upsert subscription: %w", err)
	}
	return nil
}

// ExtendSubscription pushes expires_at forward by `days` days. Used by
// subscription.charged webhook redeliveries (spec §14 autopay).
func (s *Store) ExtendSubscription(ctx context.Context, userID uuid.UUID, days int) error {
	if days <= 0 {
		return fmt.Errorf("invalid: days must be positive")
	}
	tag, err := s.db.Exec(ctx, fmt.Sprintf(`
        UPDATE dating_premium_subscriptions
        SET expires_at = COALESCE(expires_at, now()) + INTERVAL '%d days',
            cancelled_at = NULL
        WHERE user_id = $1`, days), userID)
	if err != nil {
		return fmt.Errorf("extend subscription: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrSubscriptionNotFound
	}
	return nil
}

// MarkSubscriptionCancelled stamps cancelled_at and sets auto_renew=false.
// expires_at is left unchanged — the user keeps premium until then.
func (s *Store) MarkSubscriptionCancelled(ctx context.Context, userID uuid.UUID) error {
	tag, err := s.db.Exec(ctx, `
        UPDATE dating_premium_subscriptions
        SET cancelled_at = COALESCE(cancelled_at, now()),
            auto_renew   = false
        WHERE user_id = $1`, userID)
	if err != nil {
		return fmt.Errorf("mark sub cancelled: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrSubscriptionNotFound
	}
	return nil
}

// GetSubscription returns the user's subscription state or ErrSubscriptionNotFound.
func (s *Store) GetSubscription(ctx context.Context, userID uuid.UUID) (*PremiumSubscription, error) {
	row := s.db.QueryRow(ctx, `
        SELECT user_id, plan, plan_id, razorpay_subscription_id, started_at, expires_at,
               source, auto_renew, cancelled_at
        FROM dating_premium_subscriptions
        WHERE user_id = $1`, userID)
	out := &PremiumSubscription{}
	if err := row.Scan(&out.UserID, &out.Plan, &out.PlanID, &out.RazorpaySubscriptionID,
		&out.StartedAt, &out.ExpiresAt, &out.Source, &out.AutoRenew, &out.CancelledAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrSubscriptionNotFound
		}
		return nil, fmt.Errorf("get subscription: %w", err)
	}
	return out, nil
}

// ConsentEntry is one row of dating_consent_log.
type ConsentEntry struct {
	ID            uuid.UUID `json:"id"`
	UserID        uuid.UUID `json:"user_id"`
	ConsentType   string    `json:"consent_type"`
	Granted       bool      `json:"granted"`
	PolicyVersion string    `json:"policy_version"`
	CreatedAt     time.Time `json:"created_at"`
}

// RecordConsent appends a row to the consent registry. Append-only: a user
// who toggles Echoes off then on again has two rows. The DPDP audit trail
// requires both entries.
func (s *Store) RecordConsent(ctx context.Context, userID uuid.UUID, consentType string, granted bool, policyVersion string) error {
	if userID == uuid.Nil {
		return fmt.Errorf("invalid: user_id required")
	}
	if consentType == "" {
		return fmt.Errorf("invalid: consent_type required")
	}
	if policyVersion == "" {
		return fmt.Errorf("invalid: policy_version required")
	}
	if _, err := s.db.Exec(ctx, `
        INSERT INTO dating_consent_log (user_id, consent_type, granted, policy_version)
        VALUES ($1, $2, $3, $4)`, userID, consentType, granted, policyVersion); err != nil {
		return fmt.Errorf("record consent: %w", err)
	}
	return nil
}

// ListConsentForUser returns the user's consent history, newest first.
func (s *Store) ListConsentForUser(ctx context.Context, userID uuid.UUID) ([]*ConsentEntry, error) {
	rows, err := s.db.Query(ctx, `
        SELECT id, user_id, consent_type, granted, policy_version, created_at
        FROM dating_consent_log
        WHERE user_id = $1
        ORDER BY created_at DESC`, userID)
	if err != nil {
		return nil, fmt.Errorf("list consent: %w", err)
	}
	defer rows.Close()
	out := make([]*ConsentEntry, 0, 4)
	for rows.Next() {
		e := &ConsentEntry{}
		if err := rows.Scan(&e.ID, &e.UserID, &e.ConsentType, &e.Granted, &e.PolicyVersion, &e.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan consent: %w", err)
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// ListPaymentIntentsForUser returns the user's payment history, newest first.
// Used by the data-exporter (DPDP §15.8).
func (s *Store) ListPaymentIntentsForUser(ctx context.Context, userID uuid.UUID) ([]*PaymentIntent, error) {
	rows, err := s.db.Query(ctx, `
        SELECT id, user_id, plan_id, amount_inr_paise, razorpay_order_id,
               razorpay_subscription_id, status, source, created_at, paid_at
        FROM dating_payment_intents
        WHERE user_id = $1
        ORDER BY created_at DESC`, userID)
	if err != nil {
		return nil, fmt.Errorf("list payment intents: %w", err)
	}
	defer rows.Close()
	out := make([]*PaymentIntent, 0, 4)
	for rows.Next() {
		p := &PaymentIntent{}
		if err := rows.Scan(&p.ID, &p.UserID, &p.PlanID, &p.AmountINRPaise, &p.RazorpayOrderID,
			&p.RazorpaySubscriptionID, &p.Status, &p.Source, &p.CreatedAt, &p.PaidAt); err != nil {
			return nil, fmt.Errorf("scan intent: %w", err)
		}
		out = append(out, p)
	}
	return out, rows.Err()
}
