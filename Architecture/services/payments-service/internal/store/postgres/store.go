package postgres

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ErrNotRefundable is returned when a payment intent is not in a refundable state.
var ErrNotRefundable = errors.New("payment intent is not in a refundable state")

// ErrPaymentNotFound is returned when a payment intent does not exist.
var ErrPaymentNotFound = errors.New("payment intent not found")

type Store struct {
	db *pgxpool.Pool
}

func New(db *pgxpool.Pool) *Store {
	return &Store{db: db}
}

type PaymentIntent struct {
	ID            uuid.UUID `json:"id"`
	PayerID       uuid.UUID `json:"payer_id"`
	PayeeID       uuid.UUID `json:"payee_id"`
	ReferenceType string    `json:"reference_type"`
	ReferenceID   uuid.UUID `json:"reference_id"`
	// Amount is the legacy rupees-major float column. Audit P7-deep
	// migration: AmountMinorRaw (the new BIGINT paise column) is the
	// source of truth; this field is kept as a deprecated mirror for one
	// release cycle so analytics readers + external consumers keep
	// working. New write paths set both. Read paths SELECT both — the
	// AmountMinor() getter prefers the new column and falls back to
	// math.Round(Amount*100) only when the new column is zero (legacy
	// pre-migration rows).
	//
	// Deprecated: prefer AmountMinorRaw / AmountMinor(). Drops in the
	// follow-up migration once readers cut over.
	Amount float64 `json:"amount"`
	// AmountMinorRaw is the paise-minor int64 source of truth (audit
	// P7-deep). Marshalled as "amount_minor" on the wire so commerce-
	// service / external HTTP callers can read the int64 form directly.
	// Reads scan both columns; writes carry both for the one-release
	// dual-write window.
	AmountMinorRaw      int64  `json:"amount_minor"`
	Currency            string `json:"currency"`
	Method              string `json:"method"`
	Status              string `json:"status"`
	ProviderRef         string `json:"provider_ref,omitempty"`
	UPIIntentURL        string `json:"upi_intent_url,omitempty"`
	IdempotencyKey      string `json:"idempotency_key"`
	RefundedAmountMinor int64  `json:"refunded_amount_minor"`
	// OwnerDomain is the service identity that created this intent.
	//
	// B4: it is written by the INSERT in CreateIntent, in the same statement
	// as the intent itself. It used to be a separate best-effort UPDATE
	// issued after the 201 had already been decided, so a transient failure
	// produced a live intent with an EMPTY owner — and every ownership check
	// treated empty as "anyone may act on this".
	OwnerDomain string    `json:"owner_domain,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// AmountMinor returns the intent amount in paise-minor int64.
//
// Audit P7-deep: the BIGINT amount_minor column is now the source of
// truth. When it's set (post-migration / freshly inserted rows) we
// return it verbatim — no float math involved, so the legacy float64
// precision-loss path is closed at the read boundary. Legacy rows that
// pre-date the migration (AmountMinorRaw == 0 but Amount > 0) fall
// back to math.Round(Amount*100); this is the same conversion the
// backfill SQL runs, so the two paths agree.
//
// Pin behaviour with the rupeesToPaise pin in the service test plus the
// new TestGetIntent_LegacyFloatFallback test.
func (p *PaymentIntent) AmountMinor() int64 {
	if p.AmountMinorRaw > 0 {
		return p.AmountMinorRaw
	}
	return int64(math.Round(p.Amount * 100))
}

type AuditEntry struct {
	ID        int64     `json:"id"`
	IntentID  uuid.UUID `json:"intent_id"`
	Event     string    `json:"event"`
	OldStatus string    `json:"old_status,omitempty"`
	NewStatus string    `json:"new_status,omitempty"`
	ActorID   uuid.UUID `json:"actor_id,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}

// CreateIntentResult is returned by CreateIntent, carrying the intent and
// a flag indicating whether the record already existed (idempotent replay).
type CreateIntentResult struct {
	Intent      *PaymentIntent
	WasExisting bool // true if idempotency_key already existed
}

// CreateIntent creates a new payment intent. Idempotent on idempotency_key.
// If the key already exists the existing row is returned and WasExisting is set to true.
func (s *Store) CreateIntent(ctx context.Context, in PaymentIntent) (*CreateIntentResult, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	// Audit P7-deep: dual-write the paise-minor + rupees-major columns.
	// The caller's preferred source-of-truth is AmountMinorRaw; if only
	// the legacy Amount float was set the service layer fills
	// AmountMinorRaw in via rupeesToPaise before we get here. Both
	// columns land in the same row so analytics readers that still scan
	// `amount` keep working through the deprecation window.
	// B4: owner_domain is written HERE, in the same statement as the intent.
	// B6: `inserted` is derived from xmax rather than from the row's age.
	// The old discriminator was `time.Since(created_at) > time.Second`,
	// which reported a genuine retry arriving inside one second as a NEW
	// intent — so a duplicate submission ran the whole post-create path
	// (hold creation, `payment.initiated`) a second time against the same
	// row. With ON CONFLICT DO UPDATE, a freshly inserted row has xmax = 0
	// and a conflicting row does not; that is exact, not a timing guess.
	// N4: the REQUESTED tuple, captured before the Scan overwrites `in`
	// with whatever the database actually holds under this key.
	want := struct {
		owner, refType, currency, method string
		refID, payer, payee              uuid.UUID
		amountMinor                      int64
	}{
		owner: in.OwnerDomain, refType: in.ReferenceType, currency: in.Currency,
		method: in.Method, refID: in.ReferenceID, payer: in.PayerID,
		payee: in.PayeeID, amountMinor: in.AmountMinorRaw,
	}

	var inserted bool
	err = tx.QueryRow(ctx,
		`INSERT INTO payments.payment_intents
		    (payer_id, payee_id, reference_type, reference_id, amount, amount_minor, currency, method, status, provider_ref, idempotency_key, owner_domain, metadata)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, 'pending', $9, $10, NULLIF($11,''), $12)
		 ON CONFLICT (idempotency_key)
		 DO UPDATE SET updated_at = payments.payment_intents.updated_at
		 RETURNING id, payer_id, payee_id, reference_type, reference_id, amount, COALESCE(amount_minor, 0),
		           currency, method, status, idempotency_key, COALESCE(provider_ref,''),
		           COALESCE(owner_domain,''), created_at, updated_at, (xmax = 0)`,
		in.PayerID, in.PayeeID, in.ReferenceType, in.ReferenceID,
		in.Amount, in.AmountMinorRaw, in.Currency, in.Method, in.ProviderRef, in.IdempotencyKey, in.OwnerDomain, "{}",
	).Scan(&in.ID, &in.PayerID, &in.PayeeID, &in.ReferenceType, &in.ReferenceID,
		&in.Amount, &in.AmountMinorRaw, &in.Currency, &in.Method, &in.Status, &in.IdempotencyKey,
		&in.ProviderRef, &in.OwnerDomain, &in.CreatedAt, &in.UpdatedAt, &inserted)
	if err != nil {
		return nil, err
	}

	// ── N4. The idempotency key is fingerprinted ──────────────────────
	//
	// `idempotency_key` is a GLOBAL unique index across every domain that
	// shares payments-service. The conflict path returned the existing row
	// and compared nothing, so reusing a key — by collision, by a client
	// that derives keys badly, or deliberately — did two things:
	//
	//   1. disclosed another domain's intent to the caller, since the row
	//      comes back in full and B4's ownership checks guard the /intents
	//      routes rather than this constructor;
	//   2. fed the NEW request's amount into provider order creation for the
	//      OLD row, because InitiatePayment attaches a provider order using
	//      the amount it was called with when the existing row has no
	//      provider reference yet.
	//
	// A key that names a different request is not a retry. Commerce already
	// takes this position on its own checkout key (ErrIdempotencyConflict,
	// M-7); this is the same rule at the payments boundary, and it is
	// enforced before the caller can reach the PSP.
	if !inserted {
		switch {
		// MRC-3 — the owner comparison is EXACT and fails closed on either
		// blank side.
		//
		// It used to read `want.owner != "" && in.OwnerDomain != "" && ...`,
		// so a conflict against a row with an empty `owner_domain` was
		// accepted: a caller with a valid identity inherited an intent whose
		// authority owner is unknown, and could then drive it. That is the
		// exact ownerless state B4 refuses everywhere else — ownsIntent and
		// CreateRefundCommand both reject an empty stored owner — so
		// accepting it here was the one door left open on the same wall.
		//
		// `owner_domain` is still a nullable column and migration 007's
		// backfill covers ordinary rows but cannot guarantee every one, so
		// the blank case is reachable rather than theoretical.
		case want.owner == "":
			return nil, fmt.Errorf("%w: caller has no verified service identity",
				ErrIdempotencyFingerprint)
		case in.OwnerDomain == "":
			return nil, fmt.Errorf("%w: key belongs to an intent with no owner domain; "+
				"its authority cannot be established", ErrIdempotencyFingerprint)
		case want.owner != in.OwnerDomain:
			return nil, fmt.Errorf("%w: key belongs to domain %q, caller is %q",
				ErrIdempotencyFingerprint, in.OwnerDomain, want.owner)
		case want.refType != in.ReferenceType || want.refID != in.ReferenceID:
			return nil, fmt.Errorf("%w: key names %s/%s, request is %s/%s",
				ErrIdempotencyFingerprint, in.ReferenceType, in.ReferenceID, want.refType, want.refID)
		case want.payer != in.PayerID || want.payee != in.PayeeID:
			return nil, fmt.Errorf("%w: key belongs to a different payer/payee pair",
				ErrIdempotencyFingerprint)
		case want.amountMinor != in.AmountMinorRaw:
			return nil, fmt.Errorf("%w: key was created for %d minor, request is %d minor",
				ErrIdempotencyFingerprint, in.AmountMinorRaw, want.amountMinor)
		case !strings.EqualFold(want.currency, in.Currency):
			return nil, fmt.Errorf("%w: key was created in %s, request is in %s",
				ErrIdempotencyFingerprint, in.Currency, want.currency)
		case want.method != in.Method:
			return nil, fmt.Errorf("%w: key was created for method %q, request is %q",
				ErrIdempotencyFingerprint, in.Method, want.method)
		}
	}

	result := &CreateIntentResult{
		Intent:      &in,
		WasExisting: !inserted,
	}

	// Only write audit entry for genuinely new intents
	if !result.WasExisting {
		_, err = tx.Exec(ctx,
			`INSERT INTO payments.payment_audit_log (intent_id, event, new_status, actor_id) VALUES ($1,'initiated','pending',$2)`,
			in.ID, in.PayerID,
		)
		if err != nil {
			return nil, err
		}
	}

	return result, tx.Commit(ctx)
}

// GetIntent fetches a payment intent by ID.
//
// Audit P7-deep: SELECTs the new amount_minor BIGINT alongside the
// deprecated `amount` NUMERIC so AmountMinor() can prefer the source-
// of-truth column. COALESCE on amount_minor handles legacy rows that
// pre-date the migration (the backfill UPDATE in setup.sql + 006
// covers most, but a transactional insert mid-deploy could still slip
// a NULL through before the NOT NULL constraint lands).
func (s *Store) GetIntent(ctx context.Context, id uuid.UUID) (*PaymentIntent, error) {
	var p PaymentIntent
	err := s.db.QueryRow(ctx,
		`SELECT id, payer_id, payee_id, reference_type, reference_id, amount, COALESCE(amount_minor, 0),
		        currency, method, status,
		        COALESCE(provider_ref,''), COALESCE(upi_intent_url,''), idempotency_key,
		        COALESCE(refunded_amount_minor, 0), created_at, updated_at
		 FROM payments.payment_intents WHERE id = $1`,
		id,
	).Scan(&p.ID, &p.PayerID, &p.PayeeID, &p.ReferenceType, &p.ReferenceID,
		&p.Amount, &p.AmountMinorRaw, &p.Currency, &p.Method, &p.Status, &p.ProviderRef, &p.UPIIntentURL,
		&p.IdempotencyKey, &p.RefundedAmountMinor, &p.CreatedAt, &p.UpdatedAt)
	return &p, err
}

// GetIntentByProviderRef is used by the webhook-publish path so events
// can carry the full intent shape (reference_type / reference_id) that
// commerce-service consumers need. Returns nil + ErrPaymentNotFound
// when the provider hasn't been wired up against any intent yet.
func (s *Store) GetIntentByProviderRef(ctx context.Context, providerRef string) (*PaymentIntent, error) {
	if providerRef == "" {
		return nil, ErrPaymentNotFound
	}
	var p PaymentIntent
	err := s.db.QueryRow(ctx,
		`SELECT id, payer_id, payee_id, reference_type, reference_id, amount, COALESCE(amount_minor, 0),
		        currency, method, status,
		        COALESCE(provider_ref,''), COALESCE(upi_intent_url,''), idempotency_key,
		        COALESCE(refunded_amount_minor, 0), created_at, updated_at
		 FROM payments.payment_intents WHERE provider_ref = $1
		 ORDER BY updated_at DESC LIMIT 1`,
		providerRef,
	).Scan(&p.ID, &p.PayerID, &p.PayeeID, &p.ReferenceType, &p.ReferenceID,
		&p.Amount, &p.AmountMinorRaw, &p.Currency, &p.Method, &p.Status, &p.ProviderRef, &p.UPIIntentURL,
		&p.IdempotencyKey, &p.RefundedAmountMinor, &p.CreatedAt, &p.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return &p, nil
}

// UpdateStatus atomically updates status and writes an audit entry.
func (s *Store) UpdateStatus(ctx context.Context, id uuid.UUID, oldStatus, newStatus, providerRef string, actorID uuid.UUID) error {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	result, err := tx.Exec(ctx,
		`UPDATE payments.payment_intents
		 SET status = $1, provider_ref = COALESCE(NULLIF($2,''), provider_ref), updated_at = NOW()
		 WHERE id = $3 AND status = $4`,
		newStatus, providerRef, id, oldStatus,
	)
	if err != nil {
		return err
	}
	if result.RowsAffected() == 0 {
		return ErrPaymentNotFound
	}

	_, err = tx.Exec(ctx,
		`INSERT INTO payments.payment_audit_log (intent_id, event, old_status, new_status, actor_id) VALUES ($1,'status_changed',$2,$3,$4)`,
		id, oldStatus, newStatus, actorID,
	)
	if err != nil {
		return err
	}

	return tx.Commit(ctx)
}

// InitiateRefund atomically transitions a payment intent from 'succeeded' to 'refunded'.
// Returns ErrNotRefundable if the intent is not found or not in 'succeeded' status.
//
// Deprecated: superseded by ApplyRefund, which accepts a paise-minor
// refund amount and bookkeeps the partial-refund running total. Kept on
// the type only for the (unlikely) external caller; the in-tree service
// path uses ApplyRefund.
func (s *Store) InitiateRefund(ctx context.Context, intentID uuid.UUID) error {
	result, err := s.db.Exec(ctx,
		`UPDATE payments.payment_intents
		 SET status = 'refunded', updated_at = NOW()
		 WHERE id = $1 AND status = 'succeeded'`,
		intentID,
	)
	if err != nil {
		return err
	}
	if result.RowsAffected() == 0 {
		return ErrNotRefundable
	}
	return nil
}

// ApplyRefund atomically books a refund of `amountMinor` paise against
// the intent. Audit P6 + P7:
//
//   - amountMinor must satisfy 0 < amountMinor <= remaining (caller
//     pre-validates; this method enforces again at the DB layer with
//     a WHERE clause so concurrent refunds can't oversubscribe).
//   - The new total = refunded_amount_minor + amountMinor.
//   - If new total >= intent_amount_minor, status flips to 'refunded';
//     otherwise to 'partially_refunded'.
//
// The status transition is checked against validStatusTransitions
// (allowed: succeeded → refunded/partially_refunded, partially_refunded
// → refunded/partially_refunded). The audit row records the running
// refunded_amount_minor for traceability.
//
// Returns ErrNotRefundable when the row isn't in a refundable state or
// when the requested amount exceeds the remaining refundable balance
// (the WHERE clause filters both).
func (s *Store) ApplyRefund(ctx context.Context, intentID uuid.UUID, amountMinor, intentAmountMinor int64, actorID uuid.UUID) (newStatus string, newRefundedMinor int64, err error) {
	if amountMinor <= 0 {
		return "", 0, fmt.Errorf("refund amount must be positive")
	}
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return "", 0, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	// Atomic update: only succeed when (a) intent is in a refundable
	// state, (b) the running total stays <= the intent amount. The CASE
	// flips status to 'refunded' when fully covered, otherwise
	// 'partially_refunded'. RETURNING gives us the new status + running
	// total so the service can publish the right event without a re-read.
	err = tx.QueryRow(ctx, `
		UPDATE payments.payment_intents
		   SET refunded_amount_minor = refunded_amount_minor + $2,
		       status = CASE
		                   WHEN refunded_amount_minor + $2 >= $3 THEN 'refunded'
		                   ELSE 'partially_refunded'
		                END,
		       updated_at = NOW()
		 WHERE id = $1
		   AND status IN ('succeeded', 'partially_refunded')
		   AND refunded_amount_minor + $2 <= $3
		 RETURNING status, refunded_amount_minor
	`, intentID, amountMinor, intentAmountMinor).Scan(&newStatus, &newRefundedMinor)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", 0, ErrNotRefundable
		}
		return "", 0, err
	}

	_, err = tx.Exec(ctx,
		`INSERT INTO payments.payment_audit_log (intent_id, event, old_status, new_status, actor_id, metadata)
		 VALUES ($1, 'refund_applied', 'succeeded', $2, $3, jsonb_build_object('amount_minor', $4::bigint, 'refunded_total_minor', $5::bigint))`,
		intentID, newStatus, actorID, amountMinor, newRefundedMinor,
	)
	if err != nil {
		return "", 0, err
	}
	if err := tx.Commit(ctx); err != nil {
		return "", 0, err
	}
	return newStatus, newRefundedMinor, nil
}

// RecordRefundIfFresh inserts a row into payments.refunds_applied if
// the refund_provider_ref hasn't been seen before. Returns true when
// the insert actually happened (caller proceeds to ApplyRefund) and
// false when ON CONFLICT short-circuited (caller skips — the refund
// was already applied by an earlier webhook delivery).
//
// This is the refund-level idempotency layer. The webhook_events
// dedup catches identical event_ids, but Razorpay can re-deliver the
// same refund with a fresh event_id (manual replay, queue re-issue),
// so we key on the refund id itself.
func (s *Store) RecordRefundIfFresh(ctx context.Context, refundProviderRef string, intentID uuid.UUID, amountMinor int64) (bool, error) {
	if refundProviderRef == "" {
		return false, fmt.Errorf("refund_provider_ref must be non-empty")
	}
	if amountMinor <= 0 {
		return false, fmt.Errorf("amount_minor must be positive")
	}
	tag, err := s.db.Exec(ctx,
		`INSERT INTO payments.refunds_applied (refund_provider_ref, intent_id, amount_minor)
		 VALUES ($1, $2, $3)
		 ON CONFLICT (refund_provider_ref) DO NOTHING`,
		refundProviderRef, intentID, amountMinor,
	)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() == 1, nil
}

// ListByReference returns payment intents for a given reference, capped
// at 100 most-recent rows. HP4: prior version had no LIMIT — an order
// with many retried/failed intent attempts could pull an unbounded set
// into memory and back through the API envelope. Callers want the
// latest attempts anyway (status-display + refund-locator); 100 is
// well past any real-world tail.
func (s *Store) ListByReference(ctx context.Context, refType string, refID uuid.UUID) ([]PaymentIntent, error) {
	rows, err := s.db.Query(ctx,
		`SELECT id, payer_id, payee_id, reference_type, reference_id, amount, COALESCE(amount_minor, 0),
		        currency, method, status,
		        COALESCE(provider_ref,''), COALESCE(upi_intent_url,''), idempotency_key,
		        COALESCE(refunded_amount_minor, 0), created_at, updated_at
		 FROM payments.payment_intents
		 WHERE reference_type = $1 AND reference_id = $2
		 ORDER BY created_at DESC
		 LIMIT 100`,
		refType, refID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var intents []PaymentIntent
	for rows.Next() {
		var p PaymentIntent
		if err := rows.Scan(&p.ID, &p.PayerID, &p.PayeeID, &p.ReferenceType, &p.ReferenceID,
			&p.Amount, &p.AmountMinorRaw, &p.Currency, &p.Method, &p.Status, &p.ProviderRef, &p.UPIIntentURL,
			&p.IdempotencyKey, &p.RefundedAmountMinor, &p.CreatedAt, &p.UpdatedAt); err != nil {
			return nil, err
		}
		intents = append(intents, p)
	}
	return intents, rows.Err()
}

// validStatusTransitions encodes the allowed state-machine edges for
// payment_intents. Audit P2: previously UpdateStatusByProviderRef did
// an unconditional UPDATE — a late payment.captured webhook could flip
// `refunded` back to `succeeded`, and a duplicate refund.processed
// could flip `succeeded` → `refunded` → `succeeded` via reordering.
//
// `pending` is the initial state; terminal states (failed, refunded,
// cancelled) accept no further transitions. `succeeded` only moves
// forward to refunded.
var validStatusTransitions = map[string]map[string]bool{
	"pending": {
		"processing": true, "succeeded": true, "failed": true, "cancelled": true,
	},
	"processing": {
		"succeeded": true, "failed": true,
	},
	"succeeded": {
		// Audit P6: partial refunds open a `partially_refunded` middle
		// state. From there a follow-up refund can push to `refunded`
		// (full) or stay partial by accumulating in refunded_amount_minor.
		"refunded": true, "partially_refunded": true,
	},
	"partially_refunded": {
		"refunded": true, "partially_refunded": true,
	},
	"failed":    {},
	"refunded":  {},
	"cancelled": {},
}

// ErrInvalidStatusTransition is returned when a status update would
// violate the payment-intent state machine.
var ErrInvalidStatusTransition = errors.New("invalid payment status transition")

// UpdateStatusByProviderRef updates the status of an intent matched by
// its provider_ref (gateway order ID) and returns the updated row.
// Returns ErrPaymentNotFound when no intent matches,
// ErrInvalidStatusTransition when the requested transition is forbidden
// by the state machine. The transition is applied atomically inside a
// single UPDATE so two concurrent webhook retries can't both succeed.
//
// The UPDATE replaces provider_ref with paymentID (the Razorpay payment
// id — what gateway refunds must address), so the row can no longer be
// found by the incoming providerRef afterwards. That is why the updated
// row is RETURNED here rather than re-read by the caller: the old
// re-read-by-provider_ref always missed and the resulting event lost
// its reference_type/reference_id.
func (s *Store) UpdateStatusByProviderRef(ctx context.Context, providerRef, newStatus, paymentID string) (*PaymentIntent, error) {
	// Build the allowed-current-status list for newStatus.
	allowedCurrent := make([]string, 0, 4)
	for from, edges := range validStatusTransitions {
		if edges[newStatus] {
			allowedCurrent = append(allowedCurrent, from)
		}
	}
	if len(allowedCurrent) == 0 {
		return nil, fmt.Errorf("%w: unknown target status %q", ErrInvalidStatusTransition, newStatus)
	}

	var p PaymentIntent
	err := s.db.QueryRow(ctx, `
		UPDATE payments.payment_intents
		SET status = $1,
		    provider_ref = CASE WHEN $2 <> '' THEN $2 ELSE provider_ref END,
		    updated_at = NOW()
		WHERE provider_ref = $3 AND status = ANY($4)
		RETURNING id, payer_id, payee_id, reference_type, reference_id, amount, COALESCE(amount_minor, 0),
		          currency, method, status,
		          COALESCE(provider_ref,''), COALESCE(upi_intent_url,''), idempotency_key,
		          COALESCE(refunded_amount_minor, 0), created_at, updated_at
	`, newStatus, paymentID, providerRef, allowedCurrent).Scan(
		&p.ID, &p.PayerID, &p.PayeeID, &p.ReferenceType, &p.ReferenceID,
		&p.Amount, &p.AmountMinorRaw, &p.Currency, &p.Method, &p.Status, &p.ProviderRef, &p.UPIIntentURL,
		&p.IdempotencyKey, &p.RefundedAmountMinor, &p.CreatedAt, &p.UpdatedAt)
	if err == nil {
		return &p, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return nil, err
	}
	// Distinguish "no such intent" from "invalid transition" so the
	// caller (handler) can return the right error to the caller.
	var existsCount int
	_ = s.db.QueryRow(ctx,
		`SELECT COUNT(*) FROM payments.payment_intents WHERE provider_ref = $1`,
		providerRef).Scan(&existsCount)
	if existsCount == 0 {
		return nil, ErrPaymentNotFound
	}
	return nil, ErrInvalidStatusTransition
}

// RecordWebhookEventIfNew inserts a row into payments.webhook_events
// returning true iff the event_id is new. Audit P3: Razorpay retries
// webhooks aggressively; previously every retry re-ran the status
// update and re-published a Kafka event. Now the handler can short-
// circuit duplicates with a single SELECT-INSERT.
func (s *Store) RecordWebhookEventIfNew(ctx context.Context, eventID, eventType, providerRef string) (bool, error) {
	if eventID == "" {
		// No event_id in the payload → can't dedup. Caller treats as new.
		return true, nil
	}
	cmd, err := s.db.Exec(ctx, `
		INSERT INTO payments.webhook_events (event_id, event_type, provider_ref, received_at)
		VALUES ($1, $2, $3, NOW())
		ON CONFLICT (event_id) DO NOTHING
	`, eventID, eventType, providerRef)
	if err != nil {
		return false, err
	}
	return cmd.RowsAffected() == 1, nil
}

// CreateHold creates a payment hold record for an escrow payment.
func (s *Store) CreateHold(ctx context.Context, intentID uuid.UUID, amount int64, currency, condition string) error {
	_, err := s.db.Exec(ctx, `
		INSERT INTO payments.payment_holds (payment_intent_id, hold_amount, currency, release_condition)
		VALUES ($1, $2, $3, $4)
	`, intentID, amount, currency, condition)
	return err
}

// ReleaseHold marks a payment hold as released.
func (s *Store) ReleaseHold(ctx context.Context, intentID uuid.UUID, releasedBy string) error {
	result, err := s.db.Exec(ctx, `
		UPDATE payments.payment_holds
		SET released_at = NOW(), released_by = $2
		WHERE payment_intent_id = $1 AND released_at IS NULL
	`, intentID, releasedBy)
	if err != nil {
		return err
	}
	if n := result.RowsAffected(); n == 0 {
		return fmt.Errorf("no active hold found for intent %s", intentID)
	}
	return nil
}

// ensure pgx import is used
var _ pgx.Tx

// EnqueueOutboxEvent inserts one already-enveloped event into
// payments.outbox_events. shared/outbox.Publisher (started in
// cmd/server/main.go with DBSchema "payments") drains the table to Kafka
// with at-least-once delivery. Replaces the previous direct
// kafka.Writer.WriteMessages on the request path, which dropped
// payment.succeeded on any broker blip and left the order unpaid forever.
func (s *Store) EnqueueOutboxEvent(ctx context.Context, eventType, partitionKey string, payload []byte) error {
	_, err := s.db.Exec(ctx, `
		INSERT INTO payments.outbox_events (event_type, partition_key, payload)
		VALUES ($1, $2, $3)`,
		eventType, partitionKey, payload)
	return err
}
