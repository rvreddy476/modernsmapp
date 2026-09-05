package postgres

// Commerce P0 durability primitives.
//
// Everything in this file exists because the previous code performed a money
// effect and its record-keeping as separate, independently-failing steps:
//
//   - the webhook inbox row was inserted, and only THEN was the status
//     applied and the Kafka event published, so a crash in between turned the
//     PSP's retry into a silent 200 with no effect ever recorded (A3, R-1);
//   - the intent was marked refunded BEFORE the provider was asked to refund
//     it, and a provider failure was logged and swallowed, so the ledger
//     claimed money had moved when it had not (A6, LB-8);
//   - events were written straight to Kafka after the DB commit, so a broker
//     outage lost a captured payment permanently (LB-7, R-2).
//
// The fix in every case is the same shape: the effect and its outbox row
// commit in ONE transaction, and anything that talks to a network happens
// strictly after that commit, driven by a durable row that can be retried.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/atpost/payments-service/internal/gateway"
	"github.com/atpost/shared/events"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// ─── Errors ──────────────────────────────────────────────────────────

var (
	// ErrDuplicateEvent means the provider event was already recorded. The
	// caller acknowledges the webhook without re-applying anything.
	ErrDuplicateEvent = errors.New("payments: provider event already processed")
	// ErrBlankEventID rejects an empty dedupe key. R-5: Razorpay sends the
	// event id in the `x-razorpay-event-id` HEADER, and the old code read a
	// non-existent body field, so the key was usually "". The first event
	// then occupied that key and every later payment looked like a
	// duplicate and was acknowledged without recording its money.
	ErrBlankEventID = errors.New("payments: provider event id is required")
	// ErrIntentNotFound is returned when no intent matches.
	ErrIntentNotFound = errors.New("payments: intent not found")
	// ErrRefundExceedsRemaining guards the cap atomically, counting money
	// already reserved by in-flight refund commands.
	ErrRefundExceedsRemaining = errors.New("payments: refund exceeds refundable balance")
	// ErrNotOwnerDomain means the calling service does not own this intent.
	ErrNotOwnerDomain = errors.New("payments: intent belongs to another domain")
	// ErrWebhookAmountMismatch means a signature-valid provider event named
	// an amount or currency that is not the intent's.
	//
	// B2. This check used to run AFTER ApplyWebhookAtomically had committed
	// the terminal status and its outbox row, so a webhook carrying
	// AmountMinor=1 against an intent of 1,000,000 published
	// `payment.succeeded` before anything compared the two. Commerce then
	// consumed the intent's amount from that event and marked a ₹10,000
	// order paid on a ₹0.01 capture. The comparison is now inside the same
	// transaction and precedes both writes, so a mismatch produces no
	// terminal state and no event at all.
	ErrWebhookAmountMismatch = errors.New("payments: provider event amount does not match the intent")
	// ErrIdempotencyFingerprint means an idempotency key was reused for a
	// materially different request (N4). `idempotency_key` is unique across
	// every domain sharing this service, so an unfingerprinted conflict both
	// disclosed another domain's intent and let a new request's amount drive
	// provider order creation against the old row.
	ErrIdempotencyFingerprint = errors.New("payments: idempotency key reused for a different request")
	// ErrProviderOrderConflict means an intent already holds a DIFFERENT
	// provider order reference (N3). Two PSP orders exist for one local
	// intent; that is a reconciliation break, not something to overwrite.
	ErrProviderOrderConflict = errors.New("payments: intent already holds a different provider order")
	// ErrAmbiguousRefundTarget means a refund event's identifiers resolve to
	// more than one intent, or to two different intents (N5). Crediting an
	// arbitrary one refunds the wrong customer and leaves the right one
	// unreconciled, so this is refused rather than tie-broken.
	ErrAmbiguousRefundTarget = errors.New("payments: refund event does not identify exactly one intent")
)

// intentIDsByRef runs a locking lookup and collects the matches, so the
// caller can distinguish "one", "none" and "more than one" (N5). A query
// that answers with LIMIT 1 cannot tell the third case from the first.
func intentIDsByRef(ctx context.Context, tx pgx.Tx, query string, args ...any) ([]uuid.UUID, error) {
	rows, err := tx.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// ─── Outbox ──────────────────────────────────────────────────────────

// enqueueOutboxTx writes an event inside the caller's transaction.
//
// LB-7. This is the only way payments emits anything now. Writing to Kafka
// directly after a commit means a broker outage silently drops the event and
// the DB has no record that it was ever owed; the outbox makes the debt
// durable and the publisher retries until it is paid.
func enqueueOutboxTx(ctx context.Context, tx pgx.Tx, eventType, partitionKey string, actor *string, payload any) error {
	data, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("payments: marshal %s payload: %w", eventType, err)
	}
	env := events.EventEnvelope{
		EventID:     uuid.NewString(),
		EventType:   eventType,
		OccurredAt:  time.Now().UTC(),
		ActorUserID: actor,
		Payload:     data,
	}
	body, err := json.Marshal(env)
	if err != nil {
		return fmt.Errorf("payments: marshal %s envelope: %w", eventType, err)
	}
	_, err = tx.Exec(ctx,
		`INSERT INTO payments.outbox_events (event_type, partition_key, payload)
		 VALUES ($1, $2, $3)`,
		eventType, partitionKey, body)
	return err
}

// ─── Webhook inbox + effect + outbox, atomically ─────────────────────

// WebhookEffect describes what a verified provider event should change.
type WebhookEffect struct {
	Provider          string
	EventID           string
	EventType         string
	ProviderOrderID   string
	ProviderPaymentID string

	// NewStatus is the intent status this event drives, e.g. "succeeded"
	// or "failed". Empty means the event is recorded but changes nothing.
	NewStatus string

	// AmountMinor and Currency are what the PROVIDER says was captured.
	//
	// B2: these are verified against the intent inside the transaction,
	// before any terminal write. A zero AmountMinor means the provider event
	// carried no amount; for a state-changing event that is itself a
	// refusal, because an unverifiable capture must not become
	// `payment.succeeded`.
	AmountMinor int64
	Currency    string
}

// ApplyWebhookAtomically records the provider event, applies the status
// transition and enqueues the domain event in a single transaction.
//
// A3. If any step fails the whole thing rolls back, so the provider's retry
// finds no inbox row and is processed again — which is exactly what we want,
// because "acknowledged but not applied" is the state that loses money. The
// duplicate case returns ErrDuplicateEvent so the handler can answer 200
// without doing the work twice.
func (s *Store) ApplyWebhookAtomically(ctx context.Context, e WebhookEffect) (*PaymentIntent, error) {
	if e.EventID == "" {
		return nil, ErrBlankEventID
	}
	if e.Provider == "" {
		e.Provider = "razorpay"
	}

	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	tag, err := tx.Exec(ctx,
		`INSERT INTO payments.provider_events
		     (provider, event_id, event_type, provider_order_id, provider_payment_id)
		 VALUES ($1,$2,$3,NULLIF($4,''),NULLIF($5,''))
		 ON CONFLICT (provider, event_id) DO NOTHING`,
		e.Provider, e.EventID, e.EventType, e.ProviderOrderID, e.ProviderPaymentID)
	if err != nil {
		return nil, fmt.Errorf("payments: record provider event: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return nil, ErrDuplicateEvent
	}

	if e.NewStatus == "" {
		// Recorded, nothing to apply. Still commit so the dedupe holds.
		if err := tx.Commit(ctx); err != nil {
			return nil, err
		}
		return nil, nil
	}

	// C3-LB-1. Resolve to EXACTLY one intent, or refuse.
	//
	// idx_payment_intents_provider_order is a plain btree, not a unique
	// one, so nothing in the schema stops two intents carrying the same
	// provider order id. This lookup was a QueryRow, which answers with an
	// arbitrary row when several match — so a genuine, signature-verified
	// capture could settle whichever intent PostgreSQL happened to return
	// first, marking the wrong order paid and leaving the right one pending.
	//
	// The refund path already refuses this (N5, ErrAmbiguousRefundTarget);
	// the capture path did not. Same rule, same reason: an identifier that
	// does not identify exactly one thing has not identified anything.
	if e.ProviderOrderID != "" {
		ids, idErr := intentIDsByRef(ctx, tx,
			`SELECT id FROM payments.payment_intents
			  WHERE provider = $1 AND provider_order_id = $2
			  ORDER BY id`, e.Provider, e.ProviderOrderID)
		if idErr != nil {
			return nil, idErr
		}
		if len(ids) > 1 {
			return nil, fmt.Errorf(
				"%w: provider order %q resolves to %d intents; refusing to settle an arbitrary one",
				ErrAmbiguousRefundTarget, e.ProviderOrderID, len(ids))
		}
	}

	// Lock the intent so a concurrent verify/refund cannot interleave.
	var intent PaymentIntent
	err = tx.QueryRow(ctx,
		`SELECT id, payer_id, payee_id, reference_type, reference_id,
		        amount, COALESCE(amount_minor,0), currency, method, status,
		        COALESCE(provider_ref,''), COALESCE(upi_intent_url,''),
		        idempotency_key, COALESCE(refunded_amount_minor,0),
		        created_at, updated_at
		   FROM payments.payment_intents
		  WHERE provider = $1 AND provider_order_id = $2
		  FOR UPDATE`,
		e.Provider, e.ProviderOrderID).Scan(
		&intent.ID, &intent.PayerID, &intent.PayeeID, &intent.ReferenceType, &intent.ReferenceID,
		&intent.Amount, &intent.AmountMinorRaw, &intent.Currency, &intent.Method, &intent.Status,
		&intent.ProviderRef, &intent.UPIIntentURL, &intent.IdempotencyKey, &intent.RefundedAmountMinor,
		&intent.CreatedAt, &intent.UpdatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrIntentNotFound
		}
		return nil, err
	}

	if !transitionAllowed(intent.Status, e.NewStatus) {
		// Not an error: a late `captured` after a refund, or a repeat of a
		// terminal state. Commit the inbox row so the provider stops
		// retrying, and change nothing.
		if err := tx.Commit(ctx); err != nil {
			return nil, err
		}
		return &intent, nil
	}

	// ── B2. Amount and currency, verified BEFORE any terminal write ────
	//
	// This block used to live in the service, after this function had
	// already committed. Ordering is the entire fix: everything below writes
	// terminal state and an outbox row that commerce acts on, so the
	// comparison has to happen above it, inside this transaction, where a
	// failure rolls the inbox row back with it.
	//
	// Rolling the inbox row back is deliberate. The alternative — commit the
	// record, refuse the effect — makes the provider's retry a duplicate and
	// therefore a 200, which permanently buries a capture we never applied.
	// Rolling back means the provider keeps retrying, the mismatch keeps
	// alarming, and the intent stays non-terminal until a human or the
	// reconciler resolves it against the provider, which is the source of
	// truth for what was actually captured.
	//
	// A `succeeded` transition with no amount on the event is refused for
	// the same reason: an amount that cannot be compared has not been
	// verified, and "we could not check" must never resolve to "paid".
	if e.NewStatus == "succeeded" {
		// C3-LB-1: the same policy the service, the reconciler and the
		// recovery paths use — applied here, inside the transaction, where
		// a refusal rolls the inbox row back with it.
		//
		// Rolling the inbox row back is deliberate. The alternative — commit
		// the record, refuse the effect — makes the provider's retry a
		// duplicate and therefore a 200, which permanently buries a capture
		// we never applied. Rolling back means the provider keeps retrying,
		// the mismatch keeps alarming, and the intent stays non-terminal
		// until the reconciler resolves it against the provider.
		if err := gateway.VerifyProviderMoney(gateway.MoneyCheck{
			Operation:      "webhook " + e.EventID + " against intent " + intent.ID.String(),
			IdentifierKind: "provider payment id",
			Identifier:     firstNonBlank(e.ProviderPaymentID, e.ProviderOrderID),
			Provider:       gateway.Money{Minor: e.AmountMinor, Currency: e.Currency},
			Expected:       gateway.Money{Minor: intent.AmountMinor(), Currency: intent.Currency},
		}); err != nil {
			return nil, fmt.Errorf("%w: %s", ErrWebhookAmountMismatch, err)
		}
	}

	if _, err := tx.Exec(ctx,
		`UPDATE payments.payment_intents
		    SET status = $2,
		        provider_payment_id = COALESCE(NULLIF($3,''), provider_payment_id),
		        updated_at = NOW()
		  WHERE id = $1`,
		intent.ID, e.NewStatus, e.ProviderPaymentID); err != nil {
		return nil, err
	}
	if _, err := tx.Exec(ctx,
		`INSERT INTO payments.payment_audit_log (intent_id, event, old_status, new_status, metadata)
		 VALUES ($1,$2,$3,$4,$5)`,
		intent.ID, "provider_webhook", intent.Status, e.NewStatus,
		[]byte(fmt.Sprintf(`{"provider":%q,"event_id":%q}`, e.Provider, e.EventID))); err != nil {
		return nil, err
	}

	// ProviderRef stays the PSP ORDER id — the payment id is recorded in
	// its own column. Conflating the two is what let a cross-order replay
	// look valid to the old verify path.
	intent.Status = e.NewStatus

	domainEvent := ""
	switch e.NewStatus {
	case "succeeded":
		domainEvent = events.EventPaymentSucceeded
	case "failed":
		domainEvent = events.EventPaymentFailed
	}
	if domainEvent != "" {
		payer := intent.PayerID.String()
		if err := enqueueOutboxTx(ctx, tx, domainEvent, intent.ReferenceID.String(), &payer, intent); err != nil {
			return nil, fmt.Errorf("payments: enqueue %s: %w", domainEvent, err)
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return &intent, nil
}

// transitionAllowed mirrors the state machine already enforced in
// UpdateStatus. Terminal states never step backwards, so a late
// `payment.captured` after `refunded` cannot resurrect a refunded payment.
func transitionAllowed(from, to string) bool {
	if from == to {
		return false
	}
	switch from {
	case "pending", "processing":
		return to == "succeeded" || to == "failed" || to == "processing"
	case "succeeded":
		return to == "refunded" || to == "partially_refunded" || to == "disputed"
	case "partially_refunded":
		return to == "refunded" || to == "disputed"
	default:
		return false
	}
}

// ─── Durable refund commands ─────────────────────────────────────────

// RefundCommand is a refund that has been accepted and persisted but not
// necessarily settled with the provider yet.
type RefundCommand struct {
	ID                     uuid.UUID
	IntentID               uuid.UUID
	AmountMinor            int64
	Currency               string
	Reason                 string
	ProviderIdempotencyKey string
	Status                 string
	Provider               string
	ProviderRefundID       string
	Attempts               int
	LastError              string
	NextAttemptAt          time.Time
	RequestedBy            string
	CreatedAt              time.Time
	SettledAt              *time.Time
}

// CreateRefundCommand durably reserves a refund BEFORE any provider call.
//
// A6/LB-8. The old order of operations was: mark the intent refunded, then
// ask Razorpay, then swallow the error. This inverts it. The reservation is
// what makes the cap correct under concurrency — two simultaneous refunds
// each used to see the full remaining balance, because neither had committed
// yet; now the first one's reservation is visible to the second.
//
// The provider idempotency key is supplied by the caller and is unique, so a
// retried request (a double-tapped cancel, a redelivered command) returns the
// existing command instead of creating a second one.
func (s *Store) CreateRefundCommand(
	ctx context.Context,
	intentID uuid.UUID,
	amountMinor int64,
	reason, providerIdemKey, requestedBy, ownerDomain string,
) (*RefundCommand, bool, error) {
	if providerIdemKey == "" {
		return nil, false, fmt.Errorf("payments: provider idempotency key is required")
	}
	if amountMinor <= 0 {
		return nil, false, fmt.Errorf("payments: refund amount must be positive")
	}

	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, false, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	// Existing command for this key? Return it verbatim — idempotent.
	if existing, err := getRefundCommandByKeyTx(ctx, tx, providerIdemKey); err == nil && existing != nil {
		if err := tx.Commit(ctx); err != nil {
			return nil, false, err
		}
		return existing, false, nil
	} else if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return nil, false, err
	}

	var (
		status                            string
		amount, refunded, reserved        int64
		domain, provider, providerOrderID string
		method                            string
	)
	err = tx.QueryRow(ctx,
		`SELECT status, COALESCE(amount_minor,0), COALESCE(refunded_amount_minor,0),
		        COALESCE(refund_reserved_minor,0), COALESCE(owner_domain,''),
		        COALESCE(provider,'razorpay'), COALESCE(provider_order_id, COALESCE(provider_ref,'')),
		        method
		   FROM payments.payment_intents WHERE id = $1 FOR UPDATE`,
		intentID).Scan(&status, &amount, &refunded, &reserved, &domain, &provider, &providerOrderID, &method)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, false, ErrIntentNotFound
		}
		return nil, false, err
	}

	// D4/A2: authority is per-domain. A bare intent UUID must never grant
	// refund rights to whichever service happens to know it.
	//
	// B4: this fails CLOSED now. The previous condition required BOTH sides
	// to be non-empty before it would refuse, so a caller with no identity,
	// or an intent whose owner stamp had failed, satisfied it trivially and
	// the refund proceeded. Refunds move real money out of the platform;
	// "we could not establish authority" must resolve to no.
	if ownerDomain == "" {
		return nil, false, fmt.Errorf("%w: caller has no verified service identity", ErrNotOwnerDomain)
	}
	if domain == "" {
		return nil, false, fmt.Errorf("%w: intent %s has no owner domain", ErrNotOwnerDomain, intentID)
	}
	if domain != ownerDomain {
		return nil, false, ErrNotOwnerDomain
	}
	if status != "succeeded" && status != "partially_refunded" {
		return nil, false, fmt.Errorf("payments: cannot refund an intent in status %s", status)
	}
	if refunded+reserved+amountMinor > amount {
		return nil, false, ErrRefundExceedsRemaining
	}

	cmd := &RefundCommand{
		ID:                     uuid.New(),
		IntentID:               intentID,
		AmountMinor:            amountMinor,
		Currency:               "INR",
		Reason:                 reason,
		ProviderIdempotencyKey: providerIdemKey,
		Status:                 "pending",
		Provider:               provider,
		RequestedBy:            requestedBy,
		NextAttemptAt:          time.Now(),
		CreatedAt:              time.Now(),
	}
	if _, err := tx.Exec(ctx,
		`INSERT INTO payments.refund_commands
		    (id, intent_id, amount_minor, currency, reason, provider_idempotency_key,
		     status, provider, requested_by, next_attempt_at)
		 VALUES ($1,$2,$3,$4,$5,$6,'pending',$7,$8,NOW())`,
		cmd.ID, cmd.IntentID, cmd.AmountMinor, cmd.Currency, cmd.Reason,
		cmd.ProviderIdempotencyKey, cmd.Provider, cmd.RequestedBy); err != nil {
		return nil, false, err
	}

	if _, err := tx.Exec(ctx,
		`UPDATE payments.payment_intents
		    SET refund_reserved_minor = COALESCE(refund_reserved_minor,0) + $2,
		        updated_at = NOW()
		  WHERE id = $1`, intentID, amountMinor); err != nil {
		return nil, false, err
	}

	if _, err := tx.Exec(ctx,
		`INSERT INTO payments.payment_audit_log (intent_id, event, old_status, new_status, metadata)
		 VALUES ($1,'refund_command_created',$2,$2,$3)`,
		intentID, status,
		[]byte(fmt.Sprintf(`{"command_id":%q,"amount_minor":%d,"requested_by":%q}`,
			cmd.ID, amountMinor, requestedBy))); err != nil {
		return nil, false, err
	}

	// Tell the domain a refund is now owed. `refund_pending` is a promise
	// the reconciliation worker is accountable for; it is NOT "refunded".
	payer := requestedBy
	if err := enqueueOutboxTx(ctx, tx, "payment.refund_pending", intentID.String(), &payer, map[string]any{
		"intent_id":    intentID,
		"command_id":   cmd.ID,
		"amount_minor": amountMinor,
		"reason":       reason,
	}); err != nil {
		return nil, false, err
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, false, err
	}
	return cmd, true, nil
}

func getRefundCommandByKeyTx(ctx context.Context, tx pgx.Tx, key string) (*RefundCommand, error) {
	var c RefundCommand
	err := tx.QueryRow(ctx,
		`SELECT id, intent_id, amount_minor, currency, COALESCE(reason,''),
		        provider_idempotency_key, status, provider, COALESCE(provider_refund_id,''),
		        attempts, COALESCE(last_error,''), next_attempt_at, requested_by, created_at, settled_at
		   FROM payments.refund_commands WHERE provider_idempotency_key = $1`, key).
		Scan(&c.ID, &c.IntentID, &c.AmountMinor, &c.Currency, &c.Reason,
			&c.ProviderIdempotencyKey, &c.Status, &c.Provider, &c.ProviderRefundID,
			&c.Attempts, &c.LastError, &c.NextAttemptAt, &c.RequestedBy, &c.CreatedAt, &c.SettledAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return &c, nil
}

// GetRefundCommand fetches one command by id.
func (s *Store) GetRefundCommand(ctx context.Context, id uuid.UUID) (*RefundCommand, error) {
	var c RefundCommand
	err := s.db.QueryRow(ctx,
		`SELECT id, intent_id, amount_minor, currency, COALESCE(reason,''),
		        provider_idempotency_key, status, provider, COALESCE(provider_refund_id,''),
		        attempts, COALESCE(last_error,''), next_attempt_at, requested_by, created_at, settled_at
		   FROM payments.refund_commands WHERE id = $1`, id).
		Scan(&c.ID, &c.IntentID, &c.AmountMinor, &c.Currency, &c.Reason,
			&c.ProviderIdempotencyKey, &c.Status, &c.Provider, &c.ProviderRefundID,
			&c.Attempts, &c.LastError, &c.NextAttemptAt, &c.RequestedBy, &c.CreatedAt, &c.SettledAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return &c, nil
}

// ClaimDueRefundCommands locks and returns commands ready for a provider
// attempt. SKIP LOCKED lets several worker replicas share the queue without
// two of them submitting the same refund.
func (s *Store) ClaimDueRefundCommands(ctx context.Context, limit int) ([]RefundCommand, error) {
	rows, err := s.db.Query(ctx,
		`UPDATE payments.refund_commands
		    SET attempts = attempts + 1,
		        next_attempt_at = NOW() + (LEAST(attempts + 1, 8) * INTERVAL '30 seconds'),
		        updated_at = NOW()
		  WHERE id IN (
		        SELECT id FROM payments.refund_commands
		         WHERE status IN ('pending','submitted')
		           AND next_attempt_at <= NOW()
		         ORDER BY next_attempt_at
		         FOR UPDATE SKIP LOCKED
		         LIMIT $1)
		RETURNING id, intent_id, amount_minor, currency, COALESCE(reason,''),
		          provider_idempotency_key, status, provider, COALESCE(provider_refund_id,''),
		          attempts, COALESCE(last_error,''), next_attempt_at, requested_by, created_at, settled_at`,
		limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []RefundCommand
	for rows.Next() {
		var c RefundCommand
		if err := rows.Scan(&c.ID, &c.IntentID, &c.AmountMinor, &c.Currency, &c.Reason,
			&c.ProviderIdempotencyKey, &c.Status, &c.Provider, &c.ProviderRefundID,
			&c.Attempts, &c.LastError, &c.NextAttemptAt, &c.RequestedBy, &c.CreatedAt, &c.SettledAt); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// MarkRefundSubmitted records that the provider accepted the refund. The
// money is not settled yet — the refund webhook does that.
func (s *Store) MarkRefundSubmitted(ctx context.Context, id uuid.UUID, providerRefundID string) error {
	_, err := s.db.Exec(ctx,
		`UPDATE payments.refund_commands
		    SET status = 'submitted', provider_refund_id = $2, last_error = NULL, updated_at = NOW()
		  WHERE id = $1 AND status IN ('pending','submitted')`, id, providerRefundID)
	return err
}

// MarkRefundAttemptFailed records a transient provider failure. The command
// stays claimable, so the retry worker tries again — the failure is never
// swallowed the way the old code swallowed it.
func (s *Store) MarkRefundAttemptFailed(ctx context.Context, id uuid.UUID, reason string, terminal bool) error {
	status := "pending"
	if terminal {
		// needs_attention, not failed-and-forgotten: a refund we could not
		// place is money we still owe, and it must page someone.
		status = "needs_attention"
	}
	_, err := s.db.Exec(ctx,
		`UPDATE payments.refund_commands
		    SET status = $3, last_error = $2, updated_at = NOW()
		  WHERE id = $1 AND status IN ('pending','submitted')`, id, reason, status)
	return err
}

// ApplyProviderRefund settles a refund on a verified provider signal.
//
// Idempotent on (provider, provider_refund_id) so a redelivered refund
// webhook, or the same refund re-emitted under a new event id, credits the
// ledger exactly once.
func (s *Store) ApplyProviderRefund(
	ctx context.Context,
	provider, providerRefundID string,
	intentID uuid.UUID,
	amountMinor int64,
	eventCurrency string,
) (applied bool, newStatus string, err error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return false, "", err
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	applied, newStatus, err = applyProviderRefundTx(ctx, tx, provider, providerRefundID, intentID, amountMinor, eventCurrency)
	if err != nil {
		return false, "", err
	}
	if err := tx.Commit(ctx); err != nil {
		return false, "", err
	}
	return applied, newStatus, nil
}

// applyProviderRefundTx is the refund ledger effect, as a participant in the
// CALLER'S transaction.
//
// B3. It was extracted so the refund-webhook path can commit the provider
// inbox row and this effect together. Previously the webhook handler ran
// ApplyWebhookAtomically (which committed the inbox) and then called
// ApplyProviderRefund (a second, independent transaction). A crash or a
// failed intent lookup between the two left the provider event marked seen
// with the refund never applied — and the provider's redelivery then took
// the duplicate path and was answered 200. Money had left the PSP and the
// local ledger never recorded it.
func applyProviderRefundTx(
	ctx context.Context,
	tx pgx.Tx,
	provider, providerRefundID string,
	intentID uuid.UUID,
	amountMinor int64,
	eventCurrency string,
) (applied bool, newStatus string, err error) {
	tag, err := tx.Exec(ctx,
		`INSERT INTO payments.provider_refunds_applied
		     (provider, provider_refund_id, intent_id, amount_minor)
		 VALUES ($1,$2,$3,$4)
		 ON CONFLICT (provider, provider_refund_id) DO NOTHING`,
		provider, providerRefundID, intentID, amountMinor)
	if err != nil {
		return false, "", err
	}
	if tag.RowsAffected() == 0 {
		// Already credited under this provider refund id. The caller commits
		// so the inbox row (if any) still lands.
		return false, "", nil
	}

	var amount, refunded, reserved int64
	var intentCurrency, refType, refID string
	if err := tx.QueryRow(ctx,
		`SELECT COALESCE(amount_minor,0), COALESCE(refunded_amount_minor,0), COALESCE(refund_reserved_minor,0),
		        currency, COALESCE(reference_type,''), COALESCE(reference_id::text,'')
		   FROM payments.payment_intents WHERE id = $1 FOR UPDATE`, intentID).
		Scan(&amount, &refunded, &reserved, &intentCurrency, &refType, &refID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return false, "", ErrIntentNotFound
		}
		return false, "", err
	}

	// C3-LB-1. A refund is money-bearing and its currency was never compared
	// to anything. The amount was checked for positivity upstream and capped
	// below, but nothing asserted the event was denominated in the intent's
	// currency — so a refund of the right NUMBER in the wrong currency settled
	// against the wrong sum, and `defaultINR` used to guarantee the field was
	// never even blank.
	//
	// Deliberately NOT an equality check on the amount: a partial refund is
	// legitimately smaller than the intent. Only presence and denomination are
	// asserted here; the cap is enforced immediately below, under this lock.
	if strings.TrimSpace(eventCurrency) == "" {
		return false, "", fmt.Errorf(
			"%w: refund %s carried no currency for intent %s (intent is %s)",
			ErrWebhookAmountMismatch, providerRefundID, intentID, intentCurrency)
	}
	if strings.TrimSpace(intentCurrency) == "" {
		return false, "", fmt.Errorf(
			"%w: intent %s has no currency to compare refund %s against",
			ErrWebhookAmountMismatch, intentID, providerRefundID)
	}
	if !strings.EqualFold(strings.TrimSpace(eventCurrency), strings.TrimSpace(intentCurrency)) {
		return false, "", fmt.Errorf(
			"%w: refund %s is in %q, intent %s is in %q",
			ErrWebhookAmountMismatch, providerRefundID, eventCurrency, intentID, intentCurrency)
	}

	newRefunded := refunded + amountMinor
	if newRefunded > amount {
		// The provider settled more than the intent is worth. Never clamp
		// this: it is a genuine reconciliation break and must surface.
		return false, "", fmt.Errorf(
			"payments: provider refund %s would take refunded total to %d on an intent worth %d",
			providerRefundID, newRefunded, amount)
	}
	newStatus = "partially_refunded"
	if newRefunded >= amount {
		newStatus = "refunded"
	}
	// Release the reservation this refund consumed, floored at zero so a
	// provider-initiated refund we never reserved for cannot go negative.
	releaseBy := amountMinor
	if releaseBy > reserved {
		releaseBy = reserved
	}

	if _, err := tx.Exec(ctx,
		`UPDATE payments.payment_intents
		    SET refunded_amount_minor = $2,
		        refund_reserved_minor = COALESCE(refund_reserved_minor,0) - $3,
		        status = $4,
		        updated_at = NOW()
		  WHERE id = $1`, intentID, newRefunded, releaseBy, newStatus); err != nil {
		return false, "", err
	}

	if _, err := tx.Exec(ctx,
		`UPDATE payments.refund_commands
		    SET status = 'succeeded', settled_at = NOW(), updated_at = NOW(),
		        provider_refund_id = COALESCE(NULLIF(provider_refund_id,''), $2)
		  WHERE provider_refund_id = $2 OR (intent_id = $1 AND status = 'submitted' AND amount_minor = $3)`,
		intentID, providerRefundID, amountMinor); err != nil {
		return false, "", err
	}

	if _, err := tx.Exec(ctx,
		`INSERT INTO payments.payment_audit_log (intent_id, event, new_status, metadata)
		 VALUES ($1,'refund_settled',$2,$3)`,
		intentID, newStatus,
		[]byte(fmt.Sprintf(`{"provider":%q,"provider_refund_id":%q,"amount_minor":%d}`,
			provider, providerRefundID, amountMinor))); err != nil {
		return false, "", err
	}

	if err := enqueueOutboxTx(ctx, tx, events.EventPaymentRefunded, intentID.String(), nil, map[string]any{
		"id":                 intentID,
		"intent_id":          intentID,
		"provider":           provider,
		"provider_refund_id": providerRefundID,
		"amount_minor":       amountMinor,
		"status":             newStatus,
		// The intent's REFERENCE, without which the event cannot be
		// attributed to anything.
		//
		// This payload carried neither field, and commerce's consumer keys
		// its refund handler on exactly them: `applyRefund` parses
		// reference_id as an order id and returns nil — silently, because a
		// refund for another domain is a legitimate no-op — the moment the
		// parse fails. So EVERY refund event was dropped on the floor. The
		// order stayed `refund_pending` for ever with the money already
		// credited at the provider, and commerce's refund worker, which
		// waits for this event to settle its command, re-sent the same
		// refund every forty seconds.
		//
		// payment.succeeded has always carried them, which is why that half
		// of the loop worked and this half did not.
		"reference_type": refType,
		"reference_id":   refID,
	}); err != nil {
		return false, "", err
	}

	// No commit here: the caller owns the transaction, which is what lets
	// the webhook path bind the inbox row to this effect (B3).
	return true, newStatus, nil
}

// ApplyRefundWebhookAtomically records the provider refund event AND applies
// the refund ledger effect in ONE transaction.
//
// B3. This is the whole fix. The service used to do:
//
//	ApplyWebhookAtomically(...)   // transaction 1: inbox row, COMMITTED
//	GetIntentByProviderRef(...)   // may fail
//	ApplyProviderRefund(...)      // transaction 2: the money
//
// so any failure after the first commit produced "the provider event is
// recorded as seen, and the refund was never applied". The provider's
// redelivery then matched the inbox row, returned ErrDuplicateEvent, and the
// handler answered 200 — telling the PSP to stop retrying an event we never
// acted on.
//
// Intent resolution moved inside the transaction as well, and it accepts the
// provider PAYMENT id as a fallback. Razorpay's refund entity does not carry
// the order id; the old normaliser read it from a `payload.payment.entity`
// that a refund-only payload does not contain, so a legitimate refund
// resolved to an empty order id and entered exactly the loss sequence above.
func (s *Store) ApplyRefundWebhookAtomically(ctx context.Context, e WebhookEffect, providerRefundID string) (applied bool, newStatus string, err error) {
	if e.EventID == "" {
		return false, "", ErrBlankEventID
	}
	if providerRefundID == "" {
		return false, "", fmt.Errorf("payments: refund webhook has no provider refund id")
	}
	if e.Provider == "" {
		e.Provider = "razorpay"
	}

	tx, err := s.db.Begin(ctx)
	if err != nil {
		return false, "", err
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	tag, err := tx.Exec(ctx,
		`INSERT INTO payments.provider_events
		     (provider, event_id, event_type, provider_order_id, provider_payment_id)
		 VALUES ($1,$2,$3,NULLIF($4,''),NULLIF($5,''))
		 ON CONFLICT (provider, event_id) DO NOTHING`,
		e.Provider, e.EventID, e.EventType, e.ProviderOrderID, e.ProviderPaymentID)
	if err != nil {
		return false, "", fmt.Errorf("payments: record provider refund event: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return false, "", ErrDuplicateEvent
	}

	// ── N5. Attribution must be UNAMBIGUOUS ───────────────────────────
	//
	// This was an `OR` across the two identifiers with
	// `ORDER BY updated_at DESC LIMIT 1`. Three ways that credits the wrong
	// customer:
	//
	//   * the event carries an order id belonging to intent A and a payment
	//     id belonging to intent B. The OR matched both, and the sort picked
	//     whichever happened to be touched most recently.
	//   * `provider_ref` is a legacy column that historically held whatever
	//     the old code put there, so duplicates across rows are possible.
	//     The sort silently chose one.
	//   * two intents legitimately share an order id across a retry that
	//     created a second row before B6 closed that path.
	//
	// In every case the refund credited an arbitrary intent while the real
	// one stayed unreconciled — a customer refunded on paper who was not
	// refunded in fact, and another whose balance moved without cause.
	//
	// So: resolve each identifier SEPARATELY, require each to match at most
	// one row, and when both are present require them to agree. Ambiguity is
	// an error, never a tie-break. The transaction rolls back including the
	// inbox row, so the provider retries and this alarms.
	var byOrder, byPayment []uuid.UUID
	if e.ProviderOrderID != "" {
		byOrder, err = intentIDsByRef(ctx, tx,
			`SELECT id FROM payments.payment_intents
			  WHERE provider = $1 AND COALESCE(provider_order_id, provider_ref) = $2
			  FOR UPDATE`, e.Provider, e.ProviderOrderID)
		if err != nil {
			return false, "", err
		}
		if len(byOrder) > 1 {
			return false, "", fmt.Errorf(
				"%w: provider order %q matches %d intents; refusing to guess which to credit",
				ErrAmbiguousRefundTarget, e.ProviderOrderID, len(byOrder))
		}
	}
	if e.ProviderPaymentID != "" {
		byPayment, err = intentIDsByRef(ctx, tx,
			`SELECT id FROM payments.payment_intents
			  WHERE provider = $1 AND provider_payment_id = $2
			  FOR UPDATE`, e.Provider, e.ProviderPaymentID)
		if err != nil {
			return false, "", err
		}
		if len(byPayment) > 1 {
			return false, "", fmt.Errorf(
				"%w: provider payment %q matches %d intents; refusing to guess which to credit",
				ErrAmbiguousRefundTarget, e.ProviderPaymentID, len(byPayment))
		}
	}

	// When both identifiers resolve, they must name the SAME intent.
	if len(byOrder) == 1 && len(byPayment) == 1 && byOrder[0] != byPayment[0] {
		return false, "", fmt.Errorf(
			"%w: refund event %q names order %q (intent %s) and payment %q (intent %s) — "+
				"the identifiers disagree",
			ErrAmbiguousRefundTarget, e.EventID,
			e.ProviderOrderID, byOrder[0], e.ProviderPaymentID, byPayment[0])
	}

	var intentID uuid.UUID
	switch {
	case len(byOrder) == 1:
		intentID = byOrder[0]
	case len(byPayment) == 1:
		intentID = byPayment[0]
	default:
		// A refund we cannot attribute must roll the inbox row back so the
		// provider keeps retrying — never be acknowledged.
		return false, "", fmt.Errorf(
			"%w: refund event %q names provider order %q / payment %q",
			ErrIntentNotFound, e.EventID, e.ProviderOrderID, e.ProviderPaymentID)
	}

	// C3-LB-1: a refund is a money-bearing event and was reaching the ledger
	// with its currency never compared to anything. The refund amount was
	// checked for positivity in the service and capped in
	// applyProviderRefundTx, but nothing asserted the event was denominated
	// in the intent's currency — so a refund of the right NUMBER in the
	// wrong currency settled a refund command against the wrong sum.
	//
	// Note this is deliberately NOT an equality check against the intent
	// amount: a partial refund is legitimately smaller. Only the currency and
	// the presence of a positive amount are asserted here; the cap is
	// enforced below, under lock, against the refundable balance.
	applied, newStatus, err = applyProviderRefundTx(ctx, tx, e.Provider, providerRefundID, intentID, e.AmountMinor, e.Currency)
	if err != nil {
		return false, "", err
	}
	if err := tx.Commit(ctx); err != nil {
		return false, "", err
	}
	return applied, newStatus, nil
}

// ─── Reconciliation support ──────────────────────────────────────────

// StalePending returns intents stuck in a non-terminal state past `age`, for
// the reconciliation worker to resolve against the provider.
func (s *Store) StalePending(ctx context.Context, age time.Duration, limit int) ([]PaymentIntent, error) {
	rows, err := s.db.Query(ctx,
		`SELECT id, payer_id, payee_id, reference_type, reference_id,
		        amount, COALESCE(amount_minor,0), currency, method, status,
		        COALESCE(provider_order_id, COALESCE(provider_ref,'')), COALESCE(upi_intent_url,''),
		        idempotency_key, COALESCE(refunded_amount_minor,0), created_at, updated_at
		   FROM payments.payment_intents
		  WHERE status IN ('pending','processing')
		    AND created_at < NOW() - $1::interval
		  ORDER BY created_at
		  LIMIT $2`,
		fmt.Sprintf("%d seconds", int(age.Seconds())), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []PaymentIntent
	for rows.Next() {
		var p PaymentIntent
		if err := rows.Scan(&p.ID, &p.PayerID, &p.PayeeID, &p.ReferenceType, &p.ReferenceID,
			&p.Amount, &p.AmountMinorRaw, &p.Currency, &p.Method, &p.Status,
			&p.ProviderRef, &p.UPIIntentURL, &p.IdempotencyKey, &p.RefundedAmountMinor,
			&p.CreatedAt, &p.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// UnsettledRefundAge returns the age of the oldest refund that has not
// settled, for the `refund pending age` alarm. Zero means none outstanding.
func (s *Store) UnsettledRefundAge(ctx context.Context) (time.Duration, error) {
	var secs *float64
	err := s.db.QueryRow(ctx,
		`SELECT EXTRACT(EPOCH FROM (NOW() - MIN(created_at)))
		   FROM payments.refund_commands WHERE status <> 'succeeded'`).Scan(&secs)
	if err != nil {
		return 0, err
	}
	if secs == nil {
		return 0, nil
	}
	return time.Duration(*secs) * time.Second, nil
}

// SetOwnerDomain stamps the owning service on an intent at creation.
func (s *Store) SetOwnerDomain(ctx context.Context, id uuid.UUID, domain string) error {
	_, err := s.db.Exec(ctx,
		`UPDATE payments.payment_intents SET owner_domain = $2 WHERE id = $1`, id, domain)
	return err
}

// IntentProviderAndOrder returns the provider name and provider order id an
// intent was written with.
//
// Both are needed to address an intent the way ApplyWebhookAtomically does —
// it resolves on (provider, provider_order_id) — and neither is on the
// PaymentIntent struct. The provider in particular is NOT inferable from the
// running gateway: intents are stamped 'razorpay' by default even on a stub
// deployment, so a caller that assumed 'stub' looked up nothing and the
// settlement failed with "no intent for provider order".
func (s *Store) IntentProviderAndOrder(ctx context.Context, intentID uuid.UUID) (string, string, error) {
	var provider, order string
	err := s.db.QueryRow(ctx,
		`SELECT COALESCE(provider,'razorpay'),
		        COALESCE(NULLIF(provider_order_id,''), COALESCE(provider_ref,''))
		   FROM payments.payment_intents WHERE id = $1`, intentID).Scan(&provider, &order)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", "", ErrIntentNotFound
		}
		return "", "", err
	}
	return provider, order, nil
}

// SetProviderOrder attaches a PSP order reference to an existing intent.
//
// B6. Written only after the provider has actually returned an order id, and
// only onto an intent that has none — so a late/duplicate attach can never
// overwrite a reference the webhook path is already matching against. Both
// columns are set because the codebase reads `provider_order_id` with a
// COALESCE fallback to the older `provider_ref`.
// N3 — the affected-row count is the whole point of this function.
//
// It used to discard the CommandTag. A competing attach (two callers racing
// the same retry, or a webhook that already recorded a reference) updated
// zero rows, the function returned nil, and attachProviderOrder handed the
// caller a PSP order id the database does not hold — so the client was told
// to pay against an order that no local row references, and the webhook for
// it can never be attributed. It also silently masked a genuine second PSP
// order created by an ambiguous retry.
//
// Zero rows now means one of two things, and they are distinguished rather
// than merged: the intent already carries THIS reference (the retry
// converged — success, idempotent), or it carries a DIFFERENT one
// (ErrProviderOrderConflict — two PSP orders exist for one intent, which is
// a reconciliation break and must surface).
func (s *Store) SetProviderOrder(ctx context.Context, intentID uuid.UUID, providerOrderID string) error {
	if providerOrderID == "" {
		return fmt.Errorf("payments: refusing to attach an empty provider order id")
	}
	tag, err := s.db.Exec(ctx,
		`UPDATE payments.payment_intents
		    SET provider_ref      = $2,
		        provider_order_id = $2,
		        updated_at        = NOW()
		  WHERE id = $1
		    AND COALESCE(NULLIF(provider_order_id,''), NULLIF(provider_ref,'')) IS NULL`,
		intentID, providerOrderID)
	if err != nil {
		// A2. Once gated 999 has installed the uniqueness invariant, the
		// LOSER of a concurrent attach race lands here with SQLSTATE 23505
		// rather than winning a phantom.
		//
		// That is the whole point of the index: the application's own guard
		// counts matches in one statement and locks in another, and under
		// READ COMMITTED a duplicate can appear between them. This turns
		// "two intents quietly share a reference" into a stable, typed
		// refusal that the caller already knows how to handle — the intent
		// stays pending with no reference and the reconciler owns it.
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" &&
			pgErr.ConstraintName == "uq_payment_intents_provider_reference" {
			return fmt.Errorf(
				"%w: provider order %q is already attached to another intent (refusing to attach it to %s)",
				ErrProviderOrderConflict, providerOrderID, intentID)
		}
		return err
	}
	if tag.RowsAffected() == 1 {
		return nil
	}

	// Nothing was attached. Find out why.
	var existing string
	err = s.db.QueryRow(ctx,
		`SELECT COALESCE(NULLIF(provider_order_id,''), COALESCE(provider_ref,''))
		   FROM payments.payment_intents WHERE id = $1`, intentID).Scan(&existing)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrIntentNotFound
		}
		return err
	}
	if existing == providerOrderID {
		// The retry converged on the same provider order. This is the
		// success case the deterministic idempotency key exists to produce.
		return nil
	}
	return fmt.Errorf("%w: intent %s already holds provider order %q, refusing to attach %q",
		ErrProviderOrderConflict, intentID, existing, providerOrderID)
}

// IntentOwnerDomain reads the owning service for an authorization check.
func (s *Store) IntentOwnerDomain(ctx context.Context, id uuid.UUID) (string, error) {
	var d string
	err := s.db.QueryRow(ctx,
		`SELECT COALESCE(owner_domain,'') FROM payments.payment_intents WHERE id = $1`, id).Scan(&d)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", ErrIntentNotFound
		}
		return "", err
	}
	return d, nil
}

// firstNonBlank returns the first argument that is not blank.
//
// The webhook money check names an identifier, and which one is present
// depends on the event: a capture carries a payment id, an order-level event
// only the order id. Naming whichever exists keeps the refusal message
// actionable instead of reporting a blank field.
func firstNonBlank(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}
