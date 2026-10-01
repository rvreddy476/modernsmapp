package postgres

// The bank-offer registry (migration 014) and the store half of the offer
// capture rule. The rule itself is gateway.MatchOfferCapture; this file only
// reads the registry terms it needs and writes the registry.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/bits"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/atpost/payments-service/internal/gateway"
	"github.com/atpost/shared/events"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

var (
	// ErrOfferNotFound means no registry offer has this id (or it belongs to
	// an application the caller is confined away from).
	ErrOfferNotFound = errors.New("payments: offer not found")
	// ErrOfferExists means the provider offer id is already registered.
	ErrOfferExists = errors.New("payments: this provider offer is already registered")
	// ErrInvalidOffer wraps a field the registry refuses.
	ErrInvalidOffer = errors.New("payments: invalid offer")
	// ErrCaptureBelowIntent marks an amount mismatch where the provider
	// captured LESS than the intent, in the intent's currency. It is ALWAYS
	// also ErrWebhookAmountMismatch, with the same message as before offers
	// existed; it only lets the service know an offer might explain it.
	ErrCaptureBelowIntent = errors.New("payments: provider captured less than the intent")
)

// captureBelowIntent is the mismatch error for a lower capture: identical
// text and identical ErrWebhookAmountMismatch identity to the error every
// mismatch has always returned, plus errors.Is(ErrCaptureBelowIntent).
type captureBelowIntent struct{ err error }

func (e captureBelowIntent) Error() string        { return e.err.Error() }
func (e captureBelowIntent) Unwrap() error        { return e.err }
func (e captureBelowIntent) Is(target error) bool { return target == ErrCaptureBelowIntent }

// Offer field vocabularies.
var (
	offerPaymentMethods = map[string]bool{"card": true, "upi": true, "any": true}
	offerFundedBy       = map[string]bool{"bank": true, "merchant": true}
)

// PaymentOffer is one registry offer.
type PaymentOffer struct {
	ID               uuid.UUID  `json:"id"`
	Application      string     `json:"application"`
	Provider         string     `json:"provider"`
	ProviderOfferID  string     `json:"provider_offer_id"`
	Title            string     `json:"title"`
	Description      string     `json:"description"`
	PaymentMethod    string     `json:"payment_method"`
	DiscountType     string     `json:"discount_type"`
	DiscountValue    int64      `json:"discount_value"`
	MaxDiscountMinor *int64     `json:"max_discount_minor"`
	MinAmountMinor   int64      `json:"min_amount_minor"`
	FundedBy         string     `json:"funded_by"`
	StartsAt         time.Time  `json:"starts_at"`
	EndsAt           *time.Time `json:"ends_at"`
	Active           bool       `json:"active"`
	CreatedBy        string     `json:"created_by"`
	CreatedAt        time.Time  `json:"created_at"`
	UpdatedAt        time.Time  `json:"updated_at"`
}

const offerColumns = `id, application, provider, provider_offer_id, title, description, payment_method,
	discount_type, discount_value, max_discount_minor, min_amount_minor, funded_by, starts_at, ends_at,
	active, created_by, created_at, updated_at`

func scanOffer(row pgx.Row, o *PaymentOffer) error {
	return row.Scan(&o.ID, &o.Application, &o.Provider, &o.ProviderOfferID, &o.Title, &o.Description,
		&o.PaymentMethod, &o.DiscountType, &o.DiscountValue, &o.MaxDiscountMinor, &o.MinAmountMinor,
		&o.FundedBy, &o.StartsAt, &o.EndsAt, &o.Active, &o.CreatedBy, &o.CreatedAt, &o.UpdatedAt)
}

// ValidateOffer checks every mutable field the way the table's CHECKs do, so
// the API answers 400 with a reason instead of a constraint name.
func ValidateOffer(o PaymentOffer) error {
	bad := func(format string, args ...any) error {
		return fmt.Errorf("%w: %s", ErrInvalidOffer, fmt.Sprintf(format, args...))
	}
	if n := utf8.RuneCountInString(strings.TrimSpace(o.Title)); n < 1 || n > 120 {
		return bad("title must be 1..120 characters")
	}
	if utf8.RuneCountInString(o.Description) > 1000 {
		return bad("description must be at most 1000 characters")
	}
	if !offerPaymentMethods[o.PaymentMethod] {
		return bad("payment_method must be card, upi or any")
	}
	switch o.DiscountType {
	case gateway.OfferDiscountPercentage:
		if o.DiscountValue < 1 || o.DiscountValue > gateway.OfferBasisPoints {
			return bad("a percentage discount_value is basis points, 1..10000")
		}
	case gateway.OfferDiscountFlat:
		if o.DiscountValue < 1 {
			return bad("a flat discount_value is paise and must be positive")
		}
	default:
		return bad("discount_type must be percentage or flat")
	}
	if o.MaxDiscountMinor != nil && *o.MaxDiscountMinor < 1 {
		return bad("max_discount_minor must be positive when set")
	}
	if o.MinAmountMinor < 0 {
		return bad("min_amount_minor must not be negative")
	}
	if !offerFundedBy[o.FundedBy] {
		return bad("funded_by must be bank or merchant")
	}
	if o.EndsAt != nil && !o.EndsAt.After(o.StartsAt) {
		return bad("ends_at must be after starts_at")
	}
	return nil
}

// ValidProviderOfferID is the registry's provider offer id rule
// (chk_payment_offers_provider_offer_id): Razorpay's `offer_…`.
func ValidProviderOfferID(id string) bool {
	rest, ok := strings.CutPrefix(id, "offer_")
	if !ok || len(rest) < 6 || len(rest) > 40 {
		return false
	}
	for _, r := range rest {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9') {
			return false
		}
	}
	return true
}

// OfferWrite carries who is writing, for the change log.
type OfferWrite struct {
	OperatorID string
	Credential string
}

// CreateOffer registers an offer and writes its first change-log snapshot, in
// one transaction.
func (s *Store) CreateOffer(ctx context.Context, o PaymentOffer, w OfferWrite) (*PaymentOffer, error) {
	if !ValidApplicationKey(o.Application) {
		return nil, fmt.Errorf("%w: application is not well formed", ErrInvalidOffer)
	}
	if o.Provider == "" {
		o.Provider = "razorpay"
	}
	if o.Provider != "razorpay" {
		return nil, fmt.Errorf("%w: provider must be razorpay", ErrInvalidOffer)
	}
	if !ValidProviderOfferID(o.ProviderOfferID) {
		return nil, fmt.Errorf("%w: provider_offer_id must look like offer_XXXX", ErrInvalidOffer)
	}
	if strings.TrimSpace(w.OperatorID) == "" || strings.TrimSpace(w.Credential) == "" {
		return nil, fmt.Errorf("%w: the writer is not identified", ErrInvalidOffer)
	}
	o.Title = strings.TrimSpace(o.Title)
	if err := ValidateOffer(o); err != nil {
		return nil, err
	}

	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	var out PaymentOffer
	err = scanOffer(tx.QueryRow(ctx,
		`INSERT INTO payments.payment_offers
		     (application, provider, provider_offer_id, title, description, payment_method, discount_type,
		      discount_value, max_discount_minor, min_amount_minor, funded_by, starts_at, ends_at, active, created_by)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15)
		 RETURNING `+offerColumns,
		o.Application, o.Provider, o.ProviderOfferID, o.Title, o.Description, o.PaymentMethod, o.DiscountType,
		o.DiscountValue, o.MaxDiscountMinor, o.MinAmountMinor, o.FundedBy, o.StartsAt, o.EndsAt, o.Active,
		w.OperatorID), &out)
	if err != nil {
		var pgErr *pgconn.PgError
		switch {
		case errors.As(err, &pgErr) && pgErr.Code == "23505" && pgErr.ConstraintName == "uq_payment_offers_provider_offer":
			return nil, ErrOfferExists
		case isApplicationFKViolation(err):
			return nil, fmt.Errorf("%w: %q", ErrApplicationNotFound, o.Application)
		}
		return nil, err
	}
	if err := insertOfferChangeTx(ctx, tx, &out, "created", w); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return &out, nil
}

// OfferPatch is a partial edit. A nil field is unchanged. ClearMaxDiscount and
// ClearEndsAt remove those limits.
type OfferPatch struct {
	Title            *string
	Description      *string
	PaymentMethod    *string
	DiscountType     *string
	DiscountValue    *int64
	MaxDiscountMinor *int64
	ClearMaxDiscount bool
	MinAmountMinor   *int64
	FundedBy         *string
	StartsAt         *time.Time
	// ClearStartsAt removes a scheduled start: the offer counts from when it
	// was registered.
	ClearStartsAt bool
	EndsAt        *time.Time
	ClearEndsAt   bool
	Active        *bool
}

// UpdateOffer applies a patch under the row lock and appends a snapshot when
// anything changed. application confines an app-scoped admin: an offer of
// another application reads as absent. Returns the offer and whether it
// changed.
func (s *Store) UpdateOffer(ctx context.Context, id uuid.UUID, application string, p OfferPatch, w OfferWrite) (*PaymentOffer, bool, error) {
	if strings.TrimSpace(w.OperatorID) == "" || strings.TrimSpace(w.Credential) == "" {
		return nil, false, fmt.Errorf("%w: the writer is not identified", ErrInvalidOffer)
	}
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, false, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	var cur PaymentOffer
	err = scanOffer(tx.QueryRow(ctx,
		`SELECT `+offerColumns+` FROM payments.payment_offers WHERE id = $1 FOR UPDATE`, id), &cur)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, false, ErrOfferNotFound
	}
	if err != nil {
		return nil, false, err
	}
	if application != "" && cur.Application != application {
		return nil, false, ErrOfferNotFound
	}

	next := cur
	if p.Title != nil {
		next.Title = strings.TrimSpace(*p.Title)
	}
	if p.Description != nil {
		next.Description = *p.Description
	}
	if p.PaymentMethod != nil {
		next.PaymentMethod = *p.PaymentMethod
	}
	if p.DiscountType != nil {
		next.DiscountType = *p.DiscountType
	}
	if p.DiscountValue != nil {
		next.DiscountValue = *p.DiscountValue
	}
	if p.ClearMaxDiscount {
		next.MaxDiscountMinor = nil
	} else if p.MaxDiscountMinor != nil {
		v := *p.MaxDiscountMinor
		next.MaxDiscountMinor = &v
	}
	if p.MinAmountMinor != nil {
		next.MinAmountMinor = *p.MinAmountMinor
	}
	if p.FundedBy != nil {
		next.FundedBy = *p.FundedBy
	}
	if p.ClearStartsAt {
		next.StartsAt = cur.CreatedAt
	} else if p.StartsAt != nil {
		next.StartsAt = *p.StartsAt
	}
	if p.ClearEndsAt {
		next.EndsAt = nil
	} else if p.EndsAt != nil {
		v := *p.EndsAt
		next.EndsAt = &v
	}
	if p.Active != nil {
		next.Active = *p.Active
	}
	if err := ValidateOffer(next); err != nil {
		return nil, false, err
	}
	if offerTermsEqual(cur, next) {
		if err := tx.Commit(ctx); err != nil {
			return nil, false, err
		}
		return &cur, false, nil
	}

	var out PaymentOffer
	err = scanOffer(tx.QueryRow(ctx,
		`UPDATE payments.payment_offers
		    SET title = $2, description = $3, payment_method = $4, discount_type = $5, discount_value = $6,
		        max_discount_minor = $7, min_amount_minor = $8, funded_by = $9, starts_at = $10, ends_at = $11,
		        active = $12, updated_at = NOW()
		  WHERE id = $1
		 RETURNING `+offerColumns,
		id, next.Title, next.Description, next.PaymentMethod, next.DiscountType, next.DiscountValue,
		next.MaxDiscountMinor, next.MinAmountMinor, next.FundedBy, next.StartsAt, next.EndsAt, next.Active), &out)
	if err != nil {
		return nil, false, err
	}
	if err := insertOfferChangeTx(ctx, tx, &out, "updated", w); err != nil {
		return nil, false, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, false, err
	}
	return &out, true, nil
}

func offerTermsEqual(a, b PaymentOffer) bool {
	eqI := func(x, y *int64) bool { return (x == nil && y == nil) || (x != nil && y != nil && *x == *y) }
	eqT := func(x, y *time.Time) bool { return (x == nil && y == nil) || (x != nil && y != nil && x.Equal(*y)) }
	return a.Title == b.Title && a.Description == b.Description && a.PaymentMethod == b.PaymentMethod &&
		a.DiscountType == b.DiscountType && a.DiscountValue == b.DiscountValue &&
		eqI(a.MaxDiscountMinor, b.MaxDiscountMinor) && a.MinAmountMinor == b.MinAmountMinor &&
		a.FundedBy == b.FundedBy && a.StartsAt.Equal(b.StartsAt) && eqT(a.EndsAt, b.EndsAt) && a.Active == b.Active
}

// insertOfferChangeTx appends the full post-change snapshot. changed_at is the
// row's updated_at, so the log and the row agree on when the terms changed.
func insertOfferChangeTx(ctx context.Context, tx pgx.Tx, o *PaymentOffer, action string, w OfferWrite) error {
	_, err := tx.Exec(ctx,
		`INSERT INTO payments.payment_offer_changes
		     (offer_id, action, operator_id, credential, title, description, payment_method, discount_type,
		      discount_value, max_discount_minor, min_amount_minor, funded_by, starts_at, ends_at, active, changed_at)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16)`,
		o.ID, action, w.OperatorID, w.Credential, o.Title, o.Description, o.PaymentMethod, o.DiscountType,
		o.DiscountValue, o.MaxDiscountMinor, o.MinAmountMinor, o.FundedBy, o.StartsAt, o.EndsAt, o.Active, o.UpdatedAt)
	if err != nil {
		return fmt.Errorf("payments: append offer change: %w", err)
	}
	return nil
}

// OfferChange is one change-log row.
type OfferChange struct {
	ID         int64     `json:"id"`
	OfferID    uuid.UUID `json:"offer_id"`
	Action     string    `json:"action"`
	OperatorID string    `json:"operator_id"`
	Credential string    `json:"credential"`
	Active     bool      `json:"active"`
	ChangedAt  time.Time `json:"changed_at"`
}

// OfferChanges lists an offer's change log, oldest first.
func (s *Store) OfferChanges(ctx context.Context, offerID uuid.UUID) ([]OfferChange, error) {
	rows, err := s.db.Query(ctx,
		`SELECT id, offer_id, action, operator_id, credential, active, changed_at
		   FROM payments.payment_offer_changes WHERE offer_id = $1 ORDER BY changed_at, id`, offerID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []OfferChange
	for rows.Next() {
		var c OfferChange
		if err := rows.Scan(&c.ID, &c.OfferID, &c.Action, &c.OperatorID, &c.Credential, &c.Active, &c.ChangedAt); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// ListOffers reads the registry, newest first. application "" means every
// application.
func (s *Store) ListOffers(ctx context.Context, application string) ([]PaymentOffer, error) {
	rows, err := s.db.Query(ctx,
		`SELECT `+offerColumns+` FROM payments.payment_offers
		  WHERE ($1 = '' OR application = $1)
		  ORDER BY created_at DESC, id`, application)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return collectOffers(rows)
}

// ApplicableOffers is what a buyer may use now: the application's offers for
// this provider that are active and inside their window at `at`, and — when
// amountMinor > 0 — whose minimum the amount meets. Oldest first, so the
// Create Order `offers` array is stable across retries.
func (s *Store) ApplicableOffers(ctx context.Context, application, provider string, amountMinor int64, at time.Time) ([]PaymentOffer, error) {
	rows, err := s.db.Query(ctx,
		`SELECT `+offerColumns+` FROM payments.payment_offers
		  WHERE application = $1 AND provider = $2 AND active
		    AND starts_at <= $3 AND (ends_at IS NULL OR ends_at > $3)
		    AND ($4 <= 0 OR min_amount_minor <= $4)
		  ORDER BY created_at, id`, application, provider, at, amountMinor)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return collectOffers(rows)
}

func collectOffers(rows pgx.Rows) ([]PaymentOffer, error) {
	out := []PaymentOffer{}
	for rows.Next() {
		var o PaymentOffer
		if err := scanOffer(rows, &o); err != nil {
			return nil, err
		}
		out = append(out, o)
	}
	return out, rows.Err()
}

// ─── The store half of the capture rule ──────────────────────────────

// OfferEvidence is what the PROVIDER says about a lower capture, fetched
// server-side (gateway.OfferFetcher) and verified against the event before it
// is handed here.
type OfferEvidence struct {
	ProviderOfferIDs []string
	PaidAt           time.Time
}

// offerTermsAtTx reads, inside the caller's transaction, the registry terms in
// force at `at` for the named provider offers — of ANY application, so the rule
// can tell "not ours" from "another application's".
func offerTermsAtTx(ctx context.Context, tx pgx.Tx, provider string, providerOfferIDs []string, at time.Time) ([]gateway.OfferTerms, error) {
	if len(providerOfferIDs) == 0 {
		return nil, nil
	}
	rows, err := tx.Query(ctx,
		`SELECT o.id::text, o.application, o.provider, o.provider_offer_id,
		        c.id IS NOT NULL, COALESCE(c.title,''), COALESCE(c.funded_by,''), COALESCE(c.active, FALSE),
		        COALESCE(c.starts_at, o.starts_at), c.ends_at, COALESCE(c.discount_type,''),
		        COALESCE(c.discount_value,0), c.max_discount_minor, COALESCE(c.min_amount_minor,0)
		   FROM payments.payment_offers o
		   LEFT JOIN LATERAL (
		        SELECT * FROM payments.payment_offer_changes ch
		         WHERE ch.offer_id = o.id AND ch.changed_at <= $3
		         ORDER BY ch.changed_at DESC, ch.id DESC
		         LIMIT 1) c ON TRUE
		  WHERE o.provider = $1 AND o.provider_offer_id = ANY($2)`, provider, providerOfferIDs, at)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []gateway.OfferTerms
	for rows.Next() {
		var t gateway.OfferTerms
		if err := rows.Scan(&t.OfferID, &t.Application, &t.Provider, &t.ProviderOfferID,
			&t.Known, &t.Title, &t.FundedBy, &t.Active, &t.StartsAt, &t.EndsAt, &t.DiscountType,
			&t.DiscountValue, &t.MaxDiscountMinor, &t.MinAmountMinor); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// matchOfferCaptureTx is the ONE call site of gateway.MatchOfferCapture. The
// webhook and the reconciler both reach it through ApplyWebhookAtomically.
func matchOfferCaptureTx(ctx context.Context, tx pgx.Tx, intent *PaymentIntent, e WebhookEffect) (gateway.OfferMatch, error) {
	terms, err := offerTermsAtTx(ctx, tx, e.Provider, e.Offer.ProviderOfferIDs, e.Offer.PaidAt)
	if err != nil {
		return gateway.OfferMatch{}, err
	}
	return gateway.MatchOfferCapture(gateway.OfferCapture{
		Operation:        "offer capture " + e.EventID + " against intent " + intent.ID.String(),
		Identifier:       e.ProviderPaymentID,
		Application:      intent.ApplicationID,
		Provider:         e.Provider,
		Intent:           gateway.Money{Minor: intent.AmountMinor(), Currency: intent.Currency},
		Captured:         gateway.Money{Minor: e.AmountMinor, Currency: e.Currency},
		PaidAt:           e.Offer.PaidAt,
		ProviderOfferIDs: e.Offer.ProviderOfferIDs,
		Registry:         terms,
	})
}

// recordOfferCaptureTx writes what an accepted offer capture changed, in the
// capture's own transaction: the money actually captured, our offer, the
// discount, and an audit row naming the provider's offer and the cap.
func recordOfferCaptureTx(ctx context.Context, tx pgx.Tx, intent *PaymentIntent, e WebhookEffect, m gateway.OfferMatch) error {
	if _, err := tx.Exec(ctx,
		`UPDATE payments.payment_intents
		    SET captured_minor = $2, offer_id = $3::uuid, offer_discount_minor = $4
		  WHERE id = $1`,
		intent.ID, e.AmountMinor, m.Terms.OfferID, m.DiscountMinor); err != nil {
		return fmt.Errorf("payments: record offer capture on intent %s: %w", intent.ID, err)
	}
	meta, err := json.Marshal(map[string]any{
		"event_id":             e.EventID,
		"provider":             e.Provider,
		"provider_payment_id":  e.ProviderPaymentID,
		"offer_id":             m.Terms.OfferID,
		"provider_offer_id":    m.Terms.ProviderOfferID,
		"intent_minor":         intent.AmountMinor(),
		"captured_minor":       e.AmountMinor,
		"offer_discount_minor": m.DiscountMinor,
		"allowed_minor":        m.AllowedMinor,
		"paid_at":              e.Offer.PaidAt.UTC().Format(time.RFC3339),
	})
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx,
		`INSERT INTO payments.payment_audit_log (intent_id, event, old_status, new_status, metadata)
		 VALUES ($1,'offer_capture_accepted',$2,$3,$4)`,
		intent.ID, intent.Status, e.NewStatus, meta)
	return err
}

// ─── Refunds of an offer payment ─────────────────────────────────────

// ErrOfferRefundRoundsToZero refuses a partial refund so small that its
// prorated money is less than one paise.
var ErrOfferRefundRoundsToZero = errors.New("payments: refund of an offer payment rounds to zero")

// ErrOfferRefundExceedsCaptured refuses a refund that would return more money
// than the offer payment captured.
var ErrOfferRefundExceedsCaptured = errors.New("payments: refund would return more than the offer payment captured")

// OfferRefundMoney is the money a refund of X (ORDER VALUE) returns on an offer
// payment that captured `captured` of `intent`:
//
//   - the refund that completes the order value (committedOrder + X = intent)
//     returns everything left of the capture, so a full refund returns exactly
//     `captured` and a run of partials ends at exactly `captured`;
//   - any other refund returns floor(X × captured / intent);
//   - never more than captured − committedMoney.
//
// committedOrder is order value already refunded or reserved; committedMoney is
// money already returned or reserved by open commands.
func OfferRefundMoney(intent, captured, committedOrder, committedMoney, x int64) (int64, error) {
	if intent <= 0 || captured <= 0 || captured > intent || x <= 0 {
		return 0, fmt.Errorf("%w: intent %d, captured %d, refund %d", ErrInvalidOffer, intent, captured, x)
	}
	left := captured - committedMoney
	if left <= 0 {
		return 0, ErrOfferRefundExceedsCaptured
	}
	var money int64
	if committedOrder+x >= intent {
		money = left
	} else {
		money = mulDivFloor(x, captured, intent)
		if money > left {
			money = left
		}
	}
	if money <= 0 {
		return 0, fmt.Errorf("%w: %d of order value is %d paise of the %d captured", ErrOfferRefundRoundsToZero, x, money, captured)
	}
	return money, nil
}

// mulDivFloor is floor(a × b / c) for non-negative a, b and positive c whose
// quotient fits in 64 bits (every caller has a ≤ c or b ≤ c), computed in 128
// bits so the product cannot overflow.
func mulDivFloor(a, b, c int64) int64 {
	q, _ := mulDiv(a, b, c)
	return int64(q)
}

// mulDivCeil is ceil(a × b / c) under the same conditions.
func mulDivCeil(a, b, c int64) int64 {
	q, rem := mulDiv(a, b, c)
	if rem != 0 {
		q++
	}
	return int64(q)
}

func mulDiv(a, b, c int64) (uint64, uint64) {
	hi, lo := bits.Mul64(uint64(a), uint64(b))
	return bits.Div64(hi, lo, uint64(c))
}

// offerRefundMoneyTx computes OfferRefundMoney under the intent's row lock,
// counting money reserved by commands still in flight (pending, submitted, or
// parked in needs_attention — the same set whose order value is reserved).
func offerRefundMoneyTx(ctx context.Context, tx pgx.Tx, intentID uuid.UUID, intent, captured int64, refundedCaptured *int64, committedOrder, x int64) (int64, error) {
	var reservedMoney int64
	if err := tx.QueryRow(ctx,
		`SELECT COALESCE(SUM(COALESCE(provider_amount_minor, amount_minor)),0)
		   FROM payments.refund_commands
		  WHERE intent_id = $1 AND status IN ('pending','submitted','needs_attention')`,
		intentID).Scan(&reservedMoney); err != nil {
		return 0, err
	}
	var returned int64
	if refundedCaptured != nil {
		returned = *refundedCaptured
	}
	return OfferRefundMoney(intent, captured, committedOrder, returned+reservedMoney, x)
}

// offerRefundSettlement is how a provider refund of an offer payment is
// credited: in order value (what refunded_amount_minor and commerce count) and
// in money (refunded_captured_minor).
type offerRefundSettlement struct {
	orderValue int64
	commandID  *uuid.UUID
}

// settleOfferRefundTx works out the order value a provider refund of `money`
// on an offer payment stands for. The command it settles is preferred — the
// one bound to this provider refund id, else the single open command whose
// prorated money is exactly this refund — and its money must equal what the
// provider refunded. A refund no command explains (a dashboard refund) is
// credited as the rest of the order value when it returns the rest of the
// capture, and otherwise as ceil(money × intent / captured), never more than
// is left.
func settleOfferRefundTx(ctx context.Context, tx pgx.Tx, intentID uuid.UUID, providerRefundID string, money, intent, refundedOrder, captured, refundedCaptured int64) (offerRefundSettlement, error) {
	if refundedCaptured+money > captured {
		return offerRefundSettlement{}, fmt.Errorf("%w: refund %s of %d on top of %d returned, captured %d",
			ErrOfferRefundExceedsCaptured, providerRefundID, money, refundedCaptured, captured)
	}
	type cand struct {
		id          uuid.UUID
		orderValue  int64
		moneyAmount *int64
	}
	pick := func(query string, args ...any) ([]cand, error) {
		rows, err := tx.Query(ctx, query, args...)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		var out []cand
		for rows.Next() {
			var c cand
			if err := rows.Scan(&c.id, &c.orderValue, &c.moneyAmount); err != nil {
				return nil, err
			}
			out = append(out, c)
		}
		return out, rows.Err()
	}
	cs, err := pick(`SELECT id, amount_minor, provider_amount_minor FROM payments.refund_commands
	                  WHERE intent_id = $1 AND provider_refund_id = $2`, intentID, providerRefundID)
	if err != nil {
		return offerRefundSettlement{}, err
	}
	if len(cs) == 0 {
		cs, err = pick(`SELECT id, amount_minor, provider_amount_minor FROM payments.refund_commands
		                 WHERE intent_id = $1 AND status IN ('pending','submitted')
		                   AND COALESCE(provider_refund_id,'') = ''
		                   AND COALESCE(provider_amount_minor, amount_minor) = $2`, intentID, money)
		if err != nil {
			return offerRefundSettlement{}, err
		}
		if len(cs) > 1 {
			cs = nil // ambiguous: credit by proportion, settle no command by guess
		}
	}
	if len(cs) == 1 {
		c := cs[0]
		want := c.orderValue
		if c.moneyAmount != nil {
			want = *c.moneyAmount
		}
		if want != money {
			return offerRefundSettlement{}, fmt.Errorf("%w: provider refund %s returned %d, command %s asked for %d",
				ErrWebhookAmountMismatch, providerRefundID, money, c.id, want)
		}
		id := c.id
		return offerRefundSettlement{orderValue: c.orderValue, commandID: &id}, nil
	}
	remainingOrder := intent - refundedOrder
	if remainingOrder <= 0 {
		return offerRefundSettlement{}, fmt.Errorf("%w: refund %s on an intent with no order value left",
			ErrOfferRefundExceedsCaptured, providerRefundID)
	}
	if refundedCaptured+money == captured {
		return offerRefundSettlement{orderValue: remainingOrder}, nil
	}
	ov := mulDivCeil(money, intent, captured)
	if ov > remainingOrder {
		ov = remainingOrder
	}
	return offerRefundSettlement{orderValue: ov}, nil
}

type offerRefundApply struct {
	provider, providerRefundID string
	intentID                   uuid.UUID
	money                      int64 // what the provider refunded
	amount, refunded, reserved int64 // order value: intent, refunded, reserved
	captured, refundedCaptured int64 // money: captured, already returned
	refType, refID, appID      string
}

// applyOfferProviderRefundTx is applyProviderRefundTx for a bank-offer payment,
// inside the same transaction (the provider_refunds_applied dedupe row is
// already written by the caller). It credits the order value the refund stands
// for to refunded_amount_minor and the money to refunded_captured_minor, and
// publishes payment.refunded with both.
func applyOfferProviderRefundTx(ctx context.Context, tx pgx.Tx, a offerRefundApply) (bool, string, error) {
	st, err := settleOfferRefundTx(ctx, tx, a.intentID, a.providerRefundID, a.money, a.amount, a.refunded, a.captured, a.refundedCaptured)
	if err != nil {
		return false, "", err
	}
	newRefunded := a.refunded + st.orderValue
	if newRefunded > a.amount {
		return false, "", fmt.Errorf(
			"payments: provider refund %s would take refunded total to %d on an intent worth %d",
			a.providerRefundID, newRefunded, a.amount)
	}
	newReturned := a.refundedCaptured + a.money
	newStatus := "partially_refunded"
	if newRefunded >= a.amount {
		newStatus = "refunded"
	}
	releaseBy := st.orderValue
	if releaseBy > a.reserved {
		releaseBy = a.reserved
	}
	if _, err := tx.Exec(ctx,
		`UPDATE payments.payment_intents
		    SET refunded_amount_minor = $2,
		        refund_reserved_minor = COALESCE(refund_reserved_minor,0) - $3,
		        refunded_captured_minor = $4,
		        status = $5,
		        updated_at = NOW()
		  WHERE id = $1`, a.intentID, newRefunded, releaseBy, newReturned, newStatus); err != nil {
		return false, "", err
	}
	if _, err := tx.Exec(ctx,
		`UPDATE payments.provider_refunds_applied SET order_value_minor = $3
		  WHERE provider = $1 AND provider_refund_id = $2`,
		a.provider, a.providerRefundID, st.orderValue); err != nil {
		return false, "", err
	}
	if st.commandID != nil {
		_, err = tx.Exec(ctx,
			`UPDATE payments.refund_commands
			    SET status = 'succeeded', settled_at = NOW(), updated_at = NOW(),
			        provider_refund_id = COALESCE(NULLIF(provider_refund_id,''), $2)
			  WHERE id = $1`, *st.commandID, a.providerRefundID)
	} else {
		_, err = tx.Exec(ctx,
			`UPDATE payments.refund_commands
			    SET status = 'succeeded', settled_at = NOW(), updated_at = NOW()
			  WHERE provider_refund_id = $1`, a.providerRefundID)
	}
	if err != nil {
		return false, "", err
	}
	meta, err := json.Marshal(map[string]any{
		"provider": a.provider, "provider_refund_id": a.providerRefundID,
		"amount_minor": st.orderValue, "amount_returned_minor": a.money,
	})
	if err != nil {
		return false, "", err
	}
	if _, err := tx.Exec(ctx,
		`INSERT INTO payments.payment_audit_log (intent_id, event, new_status, metadata)
		 VALUES ($1,'refund_settled',$2,$3)`, a.intentID, newStatus, meta); err != nil {
		return false, "", err
	}
	if err := enqueueOutboxTx(ctx, tx, events.EventPaymentRefunded, a.intentID.String(), nil, map[string]any{
		"id":                 a.intentID,
		"intent_id":          a.intentID,
		"provider":           a.provider,
		"provider_refund_id": a.providerRefundID,
		// ORDER VALUE, as commerce requested and counts it.
		"amount_minor": st.orderValue,
		// The money that actually went back to the customer.
		"amount_returned_minor": a.money,
		"status":                newStatus,
		"reference_type":        a.refType,
		"reference_id":          a.refID,
		"application_id":        a.appID,
	}); err != nil {
		return false, "", err
	}
	return true, newStatus, nil
}

// offerSucceededPayload is payment.succeeded for an offer capture: the intent
// exactly as every payment.succeeded carries it — amount_minor stays the ORDER
// VALUE, so commerce's full-amount check is unchanged — plus what the offer
// did. A payment without an offer never uses this type.
type offerSucceededPayload struct {
	*PaymentIntent
	CapturedMinor      int64  `json:"captured_minor"`
	OfferID            string `json:"offer_id"`
	OfferTitle         string `json:"offer_title"`
	OfferDiscountMinor int64  `json:"offer_discount_minor"`
	OfferFundedBy      string `json:"offer_funded_by"`
}
