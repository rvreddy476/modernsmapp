// Premium purchases, entitlements and the payment inbox (lane P2).
//
// A pass or a Boost changes only in ApplyPremiumPaymentEvent, in the same
// transaction as the dating_payment_inbox row, from a payments-service event.
// The purchase route creates the purchase row and binds the payments intent;
// it never grants anything.
package store

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/atpost/dating-service/internal/payments"
	"github.com/atpost/shared/events"
	"github.com/atpost/shared/paymentevents"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

var (
	// ErrPurchaseNotFound: no such purchase for this user. Another user's
	// purchase is the same 404 as a missing one.
	ErrPurchaseNotFound = errors.New("not_found: purchase not found")
	// ErrIdempotencyKeyReused: the key already names a purchase of a different
	// product or method for this user. HTTP 409.
	ErrIdempotencyKeyReused = errors.New("idempotency key already used for a different purchase")
	// ErrPurchaseIntentConflict: a different payments intent is already bound.
	ErrPurchaseIntentConflict = errors.New("purchase is bound to a different payments intent")
)

// PremiumPurchase is one row of dating_premium_purchases.
type PremiumPurchase struct {
	ID              uuid.UUID  `json:"id"`
	UserID          uuid.UUID  `json:"-"`
	Product         string     `json:"product"`
	AmountMinor     int64      `json:"amount_minor"`
	Currency        string     `json:"currency"`
	Method          string     `json:"method"`
	IdempotencyKey  string     `json:"idempotency_key"`
	PaymentIntentID *uuid.UUID `json:"payment_intent_id,omitempty"`
	ProviderRef     *string    `json:"provider_ref,omitempty"`
	Status          string     `json:"status"`
	RefundedMinor   int64      `json:"refunded_minor"`
	PassExpiresAt   *time.Time `json:"pass_expires_at,omitempty"`
	PaidAt          *time.Time `json:"paid_at,omitempty"`
	FailedAt        *time.Time `json:"failed_at,omitempty"`
	RefundedAt      *time.Time `json:"refunded_at,omitempty"`
	CreatedAt       time.Time  `json:"created_at"`
	UpdatedAt       time.Time  `json:"updated_at"`
}

const purchaseColumns = `id, user_id, product, amount_minor, currency, method, idempotency_key,
        payment_intent_id, provider_ref, status, refunded_minor, pass_expires_at,
        paid_at, failed_at, refunded_at, created_at, updated_at`

func scanPurchase(row pgx.Row) (*PremiumPurchase, error) {
	p := &PremiumPurchase{}
	err := row.Scan(&p.ID, &p.UserID, &p.Product, &p.AmountMinor, &p.Currency, &p.Method, &p.IdempotencyKey,
		&p.PaymentIntentID, &p.ProviderRef, &p.Status, &p.RefundedMinor, &p.PassExpiresAt,
		&p.PaidAt, &p.FailedAt, &p.RefundedAt, &p.CreatedAt, &p.UpdatedAt)
	return p, err
}

// CreateOrGetPremiumPurchase inserts a `created` purchase priced from the
// catalogue product, or returns the one this user's idempotency key already
// names. created reports which. A key reused for another product or method is
// ErrIdempotencyKeyReused.
func (s *Store) CreateOrGetPremiumPurchase(ctx context.Context, userID uuid.UUID, product payments.Product, method, idempotencyKey string) (*PremiumPurchase, bool, error) {
	if userID == uuid.Nil {
		return nil, false, fmt.Errorf("invalid: user_id required")
	}
	p, err := scanPurchase(s.db.QueryRow(ctx, `
        INSERT INTO dating_premium_purchases (user_id, product, amount_minor, currency, method, idempotency_key)
        VALUES ($1, $2, $3, $4, $5, $6)
        ON CONFLICT (user_id, idempotency_key) DO NOTHING
        RETURNING `+purchaseColumns,
		userID, product.ID, product.AmountMinor, product.Currency, method, idempotencyKey))
	if err == nil {
		return p, true, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return nil, false, fmt.Errorf("create premium purchase: %w", err)
	}
	p, err = scanPurchase(s.db.QueryRow(ctx, `
        SELECT `+purchaseColumns+` FROM dating_premium_purchases
        WHERE user_id = $1 AND idempotency_key = $2`, userID, idempotencyKey))
	if err != nil {
		return nil, false, fmt.Errorf("read premium purchase by key: %w", err)
	}
	if p.Product != product.ID || p.Method != method {
		return nil, false, ErrIdempotencyKeyReused
	}
	return p, false, nil
}

// AttachPremiumIntent binds the payments intent to a purchase and moves it
// from created to confirming. Binding the same intent again is a no-op; a
// different intent is ErrPurchaseIntentConflict.
func (s *Store) AttachPremiumIntent(ctx context.Context, userID, purchaseID, intentID uuid.UUID, providerRef string) (*PremiumPurchase, error) {
	p, err := scanPurchase(s.db.QueryRow(ctx, `
        UPDATE dating_premium_purchases
        SET payment_intent_id = COALESCE(payment_intent_id, $3),
            provider_ref      = COALESCE(provider_ref, NULLIF($4, '')),
            status            = CASE WHEN status = 'created' THEN 'confirming' ELSE status END,
            updated_at        = now()
        WHERE id = $1 AND user_id = $2
          AND (payment_intent_id IS NULL OR payment_intent_id = $3)
        RETURNING `+purchaseColumns, purchaseID, userID, intentID, providerRef))
	if errors.Is(err, pgx.ErrNoRows) {
		if _, gerr := s.GetPremiumPurchaseForUser(ctx, userID, purchaseID); gerr != nil {
			return nil, gerr
		}
		return nil, ErrPurchaseIntentConflict
	}
	if err != nil {
		return nil, fmt.Errorf("attach premium intent: %w", err)
	}
	return p, nil
}

// GetPremiumPurchaseForUser reads one of the user's purchases.
func (s *Store) GetPremiumPurchaseForUser(ctx context.Context, userID, purchaseID uuid.UUID) (*PremiumPurchase, error) {
	p, err := scanPurchase(s.db.QueryRow(ctx, `
        SELECT `+purchaseColumns+` FROM dating_premium_purchases
        WHERE id = $1 AND user_id = $2`, purchaseID, userID))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrPurchaseNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get premium purchase: %w", err)
	}
	return p, nil
}

// ListPremiumPurchasesForUser returns the user's purchases, newest first
// (data export).
func (s *Store) ListPremiumPurchasesForUser(ctx context.Context, userID uuid.UUID) ([]*PremiumPurchase, error) {
	rows, err := s.db.Query(ctx, `
        SELECT `+purchaseColumns+` FROM dating_premium_purchases
        WHERE user_id = $1 ORDER BY created_at DESC`, userID)
	if err != nil {
		return nil, fmt.Errorf("list premium purchases: %w", err)
	}
	defer rows.Close()
	var out []*PremiumPurchase
	for rows.Next() {
		p, err := scanPurchase(rows)
		if err != nil {
			return nil, fmt.Errorf("scan premium purchase: %w", err)
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// PurchasePaymentState is what GET /purchases/:id/payment is derived from.
type PurchasePaymentState struct {
	PurchaseID     uuid.UUID
	Product        string
	Status         string
	AmountMinor    int64
	Currency       string
	CaptureApplied bool
	UpdatedAt      time.Time
}

// PremiumPurchasePayment reads a purchase's payment state for its own buyer.
// Another user's purchase is ErrPurchaseNotFound. CaptureApplied is true only
// when the inbox holds a payment.succeeded event for it with outcome granted.
func (s *Store) PremiumPurchasePayment(ctx context.Context, userID, purchaseID uuid.UUID) (*PurchasePaymentState, error) {
	st := &PurchasePaymentState{}
	err := s.db.QueryRow(ctx, `
        SELECT p.id, p.product, p.status, p.amount_minor, p.currency, p.updated_at,
               EXISTS (SELECT 1 FROM dating_payment_inbox i
                       WHERE i.purchase_id = p.id AND i.event_type = $3 AND i.outcome = $4)
        FROM dating_premium_purchases p
        WHERE p.id = $1 AND p.user_id = $2`,
		purchaseID, userID, events.EventPaymentSucceeded, string(payments.OutcomeGranted),
	).Scan(&st.PurchaseID, &st.Product, &st.Status, &st.AmountMinor, &st.Currency, &st.UpdatedAt, &st.CaptureApplied)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrPurchaseNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("premium purchase payment: %w", err)
	}
	return st, nil
}

// PremiumEntitlement is the user's current premium state.
type PremiumEntitlement struct {
	// PassExpiresAt is nil when the user never held a pass.
	PassExpiresAt *time.Time
	// PassActive is expires_at > now(), computed by the database.
	PassActive   bool
	PassProduct  string
	BoostBalance int
}

// GetPremiumEntitlement reads the pass and the Boost balance.
func (s *Store) GetPremiumEntitlement(ctx context.Context, userID uuid.UUID) (*PremiumEntitlement, error) {
	out := &PremiumEntitlement{}
	var product *string
	err := s.db.QueryRow(ctx, `
        SELECT s.expires_at, COALESCE(s.expires_at > now(), false), s.plan,
               COALESCE((SELECT balance FROM dating_boost_balances b WHERE b.user_id = $1), 0)
        FROM (SELECT 1) one
        LEFT JOIN dating_premium_subscriptions s ON s.user_id = $1`, userID,
	).Scan(&out.PassExpiresAt, &out.PassActive, &product, &out.BoostBalance)
	if err != nil {
		return nil, fmt.Errorf("get premium entitlement: %w", err)
	}
	if product != nil {
		out.PassProduct = *product
	}
	return out, nil
}

// ConsumeBoostToken spends one purchased Boost token. false when none is left.
func (s *Store) ConsumeBoostToken(ctx context.Context, userID uuid.UUID) (bool, error) {
	tag, err := s.db.Exec(ctx, `
        UPDATE dating_boost_balances SET balance = balance - 1, updated_at = now()
        WHERE user_id = $1 AND balance > 0`, userID)
	if err != nil {
		return false, fmt.Errorf("consume boost token: %w", err)
	}
	return tag.RowsAffected() == 1, nil
}

// ApplyPremiumPaymentEvent applies one payments-service event in ONE
// transaction:
//
//  1. INSERT the dating_payment_inbox row; a conflict on event_id means the
//     event was already applied -> OutcomeDuplicate, nothing else runs;
//  2. read the purchase FOR UPDATE;
//  3. payments.Decide;
//  4. apply the effect to the pass or Boost balance and the purchase;
//  5. record the outcome on the inbox row; COMMIT.
//
// Steps 1 and 2-5 are paymentevents.ApplyOnce over premiumInbox. A mismatch or
// an unknown purchase commits the inbox row with no effect, so it is recorded
// once and not retried. Any error rolls back the inbox row with the rest.
func (s *Store) ApplyPremiumPaymentEvent(ctx context.Context, ev payments.Event) (payments.Applied, error) {
	applied := payments.Applied{PurchaseID: ev.PurchaseID}
	if strings.TrimSpace(ev.EventID) == "" {
		return applied, paymentevents.ErrNoEventID
	}
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return applied, err
	}
	defer tx.Rollback(ctx)

	claim := paymentevents.Claim{
		EventID: ev.EventID, EventType: ev.EventType, IntentID: ev.IntentID,
		ReferenceID: ev.PurchaseID, AmountMinor: ev.AmountMinor, Currency: ev.Currency,
	}
	err = paymentevents.ApplyOnce(ctx, tx, premiumInbox{}, claim, func(ctx context.Context, tx pgx.Tx) error {
		return applyPremiumEffectTx(ctx, tx, ev, &applied)
	})
	switch {
	case errors.Is(err, paymentevents.ErrDuplicate):
		applied.Decision = payments.Decision{Outcome: payments.OutcomeDuplicate}
		return applied, nil
	case err != nil:
		return applied, err
	}
	return applied, tx.Commit(ctx)
}

// premiumInbox is dating_payment_inbox, the dedupe half of ApplyOnce.
type premiumInbox struct{}

func (premiumInbox) Claim(ctx context.Context, tx pgx.Tx, c paymentevents.Claim) (bool, error) {
	tag, err := tx.Exec(ctx, `
        INSERT INTO dating_payment_inbox (event_id, event_type, intent_id, purchase_id, amount_minor, currency)
        VALUES ($1, $2, NULLIF($3, ''), $4, $5, NULLIF($6, ''))
        ON CONFLICT (event_id) DO NOTHING`,
		c.EventID, c.EventType, c.IntentID, c.ReferenceID, c.AmountMinor, c.Currency)
	if err != nil {
		return false, fmt.Errorf("record payment event: %w", err)
	}
	return tag.RowsAffected() > 0, nil
}

func applyPremiumEffectTx(ctx context.Context, tx pgx.Tx, ev payments.Event, applied *payments.Applied) error {
	snap := payments.PurchaseSnapshot{PurchaseID: ev.PurchaseID}
	var grantedSeconds, revokedSeconds int64
	var boostGranted, boostRevoked bool
	err := tx.QueryRow(ctx, `
        SELECT user_id, product, status, amount_minor, currency, refunded_minor,
               COALESCE(payment_intent_id::text, ''), granted_seconds, revoked_seconds,
               boost_granted, boost_revoked
        FROM dating_premium_purchases
        WHERE id = $1
        FOR UPDATE`, ev.PurchaseID,
	).Scan(&snap.UserID, &snap.Product, &snap.Status, &snap.AmountMinor, &snap.Currency, &snap.RefundedMinor,
		&snap.IntentID, &grantedSeconds, &revokedSeconds, &boostGranted, &boostRevoked)
	if errors.Is(err, pgx.ErrNoRows) {
		applied.Decision = payments.Decision{Outcome: payments.OutcomePurchaseNotFound, Detail: "no such premium purchase"}
		return recordPremiumInboxOutcomeTx(ctx, tx, ev.EventID, applied.Decision)
	}
	if err != nil {
		return fmt.Errorf("read purchase for payment event: %w", err)
	}
	applied.UserID = snap.UserID
	applied.Product = snap.Product

	product, ok := payments.LookupProduct(snap.Product)
	if !ok {
		return fmt.Errorf("purchase %s names unknown product %q", snap.PurchaseID, snap.Product)
	}

	d := payments.Decide(snap, ev)
	switch d.Effect {
	case payments.EffectGrant:
		err = grantTx(ctx, tx, snap, product, applied)
	case payments.EffectMarkFailed:
		_, err = tx.Exec(ctx, `
            UPDATE dating_premium_purchases SET status = 'failed', failed_at = now(), updated_at = now()
            WHERE id = $1`, snap.PurchaseID)
	case payments.EffectRevoke, payments.EffectPartialRevoke:
		refundedAfter := snap.RefundedMinor + ev.AmountMinor
		if refundedAfter > snap.AmountMinor {
			refundedAfter = snap.AmountMinor
		}
		full := d.Effect == payments.EffectRevoke
		status := payments.StatusPartiallyRefunded
		if full {
			status = payments.StatusRefunded
			refundedAfter = snap.AmountMinor
		}
		switch product.Kind {
		case payments.KindPass:
			target := grantedSeconds
			if !full {
				target = payments.ProRataRevokeSeconds(grantedSeconds, snap.AmountMinor, refundedAfter)
			}
			if target < revokedSeconds {
				target = revokedSeconds
			}
			err = revokePassTx(ctx, tx, snap.UserID, target-revokedSeconds, applied)
			if err == nil {
				_, err = tx.Exec(ctx, `
                    UPDATE dating_premium_purchases
                    SET status = $2, refunded_minor = $3, revoked_seconds = $4,
                        refunded_at = now(), updated_at = now()
                    WHERE id = $1`, snap.PurchaseID, status, refundedAfter, target)
			}
		case payments.KindBoost:
			// A partial refund leaves the token alone; a full refund takes back
			// an unused token. A token already spent cannot be taken back, and
			// the inbox detail says so.
			revoke := full && boostGranted && !boostRevoked
			if revoke {
				tag, xerr := tx.Exec(ctx, `
                    UPDATE dating_boost_balances SET balance = balance - 1, updated_at = now()
                    WHERE user_id = $1 AND balance > 0`, snap.UserID)
				if xerr != nil {
					err = xerr
					break
				}
				if tag.RowsAffected() == 0 {
					revoke = false
					d.Detail = "boost token already used; nothing to take back"
				}
			}
			if err == nil {
				_, err = tx.Exec(ctx, `
                    UPDATE dating_premium_purchases
                    SET status = $2, refunded_minor = $3, boost_revoked = boost_revoked OR $4,
                        refunded_at = now(), updated_at = now()
                    WHERE id = $1`, snap.PurchaseID, status, refundedAfter, revoke)
			}
		}
	}
	applied.Decision = d
	if err != nil {
		return fmt.Errorf("apply %s (%s): %w", ev.EventType, d.Outcome, err)
	}
	return recordPremiumInboxOutcomeTx(ctx, tx, ev.EventID, d)
}

// grantTx: a pass extends expires_at to GREATEST(expires_at, now()) +
// duration; a Boost adds one token.
func grantTx(ctx context.Context, tx pgx.Tx, snap payments.PurchaseSnapshot, product payments.Product, applied *payments.Applied) error {
	switch product.Kind {
	case payments.KindPass:
		seconds := product.PassSeconds()
		var expires time.Time
		if err := tx.QueryRow(ctx, `
            INSERT INTO dating_premium_subscriptions (user_id, plan, plan_id, started_at, expires_at, source, auto_renew)
            VALUES ($1, $2, $2, now(), now() + $3::bigint * interval '1 second', 'payments', false)
            ON CONFLICT (user_id) DO UPDATE
            SET plan         = EXCLUDED.plan,
                plan_id      = EXCLUDED.plan_id,
                started_at   = CASE WHEN dating_premium_subscriptions.expires_at > now()
                                    THEN dating_premium_subscriptions.started_at ELSE now() END,
                expires_at   = GREATEST(dating_premium_subscriptions.expires_at, now()) + $3::bigint * interval '1 second',
                source       = 'payments',
                auto_renew   = false,
                cancelled_at = NULL
            RETURNING expires_at`, snap.UserID, product.ID, seconds).Scan(&expires); err != nil {
			return fmt.Errorf("extend pass: %w", err)
		}
		applied.PassExpiresAt = &expires
		_, err := tx.Exec(ctx, `
            UPDATE dating_premium_purchases
            SET status = 'paid', paid_at = now(), granted_seconds = $2, pass_expires_at = $3, updated_at = now()
            WHERE id = $1`, snap.PurchaseID, seconds, expires)
		return err
	case payments.KindBoost:
		if _, err := tx.Exec(ctx, `
            INSERT INTO dating_boost_balances (user_id, balance) VALUES ($1, 1)
            ON CONFLICT (user_id) DO UPDATE
            SET balance = dating_boost_balances.balance + 1, updated_at = now()`, snap.UserID); err != nil {
			return fmt.Errorf("add boost token: %w", err)
		}
		_, err := tx.Exec(ctx, `
            UPDATE dating_premium_purchases
            SET status = 'paid', paid_at = now(), boost_granted = true, updated_at = now()
            WHERE id = $1`, snap.PurchaseID)
		return err
	}
	return fmt.Errorf("grant: unknown product kind %q", product.Kind)
}

// revokePassTx takes seconds off the user's pass expiry.
func revokePassTx(ctx context.Context, tx pgx.Tx, userID uuid.UUID, seconds int64, applied *payments.Applied) error {
	if seconds <= 0 {
		return nil
	}
	var expires time.Time
	err := tx.QueryRow(ctx, `
        UPDATE dating_premium_subscriptions
        SET expires_at = expires_at - $2::bigint * interval '1 second'
        WHERE user_id = $1
        RETURNING expires_at`, userID, seconds).Scan(&expires)
	if errors.Is(err, pgx.ErrNoRows) {
		// No entitlement row (purged, or never granted): nothing to shorten.
		return nil
	}
	if err != nil {
		return fmt.Errorf("revoke pass time: %w", err)
	}
	applied.PassExpiresAt = &expires
	return nil
}

func recordPremiumInboxOutcomeTx(ctx context.Context, tx pgx.Tx, eventID string, d payments.Decision) error {
	_, err := tx.Exec(ctx, `
        UPDATE dating_payment_inbox SET outcome = $2, detail = NULLIF($3, '') WHERE event_id = $1`,
		eventID, string(d.Outcome), d.Detail)
	if err != nil {
		return fmt.Errorf("record payment event outcome: %w", err)
	}
	return nil
}

// PremiumExpiryReminder is one claimed "expiring soon" reminder.
type PremiumExpiryReminder struct {
	PurchaseID uuid.UUID
	UserID     uuid.UUID
	Product    string
	ExpiresAt  time.Time
}

// ClaimPremiumExpiryReminders claims, for every user whose pass expires within
// `within` (and has not expired), the latest paid pass purchase whose reminder
// has not been sent, stamping expiry_reminder_sent_at. Each purchase is claimed
// at most once across replicas: the UPDATE re-checks the NULL under its row
// lock. A later pass bought after the reminder gets its own.
func (s *Store) ClaimPremiumExpiryReminders(ctx context.Context, within time.Duration, limit int) ([]PremiumExpiryReminder, error) {
	if limit <= 0 {
		limit = 200
	}
	rows, err := s.db.Query(ctx, `
        WITH latest AS (
            SELECT DISTINCT ON (p.user_id) p.id, p.user_id, p.product, s.expires_at
            FROM dating_premium_purchases p
            JOIN dating_premium_subscriptions s ON s.user_id = p.user_id
            WHERE p.product <> 'boost'
              AND p.status IN ('paid', 'partially_refunded')
              AND s.expires_at > now()
              AND s.expires_at <= now() + $1::bigint * interval '1 second'
            ORDER BY p.user_id, p.paid_at DESC
        ), due AS (
            SELECT * FROM latest
            WHERE id IN (SELECT id FROM dating_premium_purchases WHERE expiry_reminder_sent_at IS NULL)
            LIMIT $2
        )
        UPDATE dating_premium_purchases p
        SET expiry_reminder_sent_at = now()
        FROM due
        WHERE p.id = due.id AND p.expiry_reminder_sent_at IS NULL
        RETURNING p.id, p.user_id, p.product, due.expires_at`, int64(within/time.Second), limit)
	if err != nil {
		return nil, fmt.Errorf("claim premium expiry reminders: %w", err)
	}
	defer rows.Close()
	var out []PremiumExpiryReminder
	for rows.Next() {
		var r PremiumExpiryReminder
		if err := rows.Scan(&r.PurchaseID, &r.UserID, &r.Product, &r.ExpiresAt); err != nil {
			return nil, fmt.Errorf("scan premium expiry reminder: %w", err)
		}
		out = append(out, r)
	}
	return out, rows.Err()
}
