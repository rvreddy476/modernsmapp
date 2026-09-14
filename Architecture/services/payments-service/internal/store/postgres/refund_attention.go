package postgres

// Parked refunds: announced once, listed for an operator, resolved once.
//
// The refund worker parks a command it can never complete in needs_attention.
// Three things follow from that, and each is bound to the row it describes:
//
//   - ParkRefundCommand writes the park AND the payment.refund_failed outbox
//     row in one transaction, guarded by the status transition, so a command is
//     announced exactly once — a second park of the same command matches no
//     row and writes nothing.
//   - ListRefundsNeedingAttention is the operator's view, keyset-paged so a
//     page that is resolved while it is being read does not skip the next one.
//   - ResolveRefundCommand moves a parked command to `resolved` exactly once.
//     A replay returns the stored resolution and writes nothing.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/atpost/shared/events"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// EventPaymentRefundFailed announces that a refund command was parked: the
// refund was NOT placed and the money is still owed. See the contract on
// refundFailedPayload.
const EventPaymentRefundFailed = "payment.refund_failed"

// Refund command statuses written by this file.
const (
	RefundStatusNeedsAttention = "needs_attention"
	RefundStatusResolved       = "resolved"
)

// Operator resolutions for a parked refund.
const (
	// ResolutionRefundedManually: the money was returned outside the provider
	// integration. The refund ledger is credited and payment.refunded is
	// published once, marked manual.
	ResolutionRefundedManually = "refunded_manually"
	// ResolutionWrittenOff: the money will not be returned. Nothing is published.
	ResolutionWrittenOff = "written_off"
	// ResolutionTestData: the payment never existed at a provider (a dev seed's
	// simulated capture). Nothing is published.
	ResolutionTestData = "test_data"
)

// manualRefundProvider namespaces a manual resolution in
// provider_refunds_applied, so it can never collide with a provider's refund id.
const manualRefundProvider = "manual"

var (
	ErrRefundCommandNotFound = errors.New("payments: refund command not found")
	// ErrRefundCommandNotParked means the command is not in needs_attention
	// (still being worked, or settled) and so is not an operator's to resolve.
	ErrRefundCommandNotParked = errors.New("payments: refund command is not in needs_attention")
	ErrInvalidResolution      = errors.New("payments: resolution must be refunded_manually, written_off or test_data")
	// ErrManualRefundRefused means the intent cannot absorb the manual refund
	// (not in a refundable status, a different currency, or over its amount).
	ErrManualRefundRefused = errors.New("payments: a manual refund cannot be applied to this intent")
)

// IsValidResolution reports whether r is one of the three resolutions.
func IsValidResolution(r string) bool {
	switch r {
	case ResolutionRefundedManually, ResolutionWrittenOff, ResolutionTestData:
		return true
	}
	return false
}

const maxParkReasonRunes = 500

// refundFailedPayload is the payment.refund_failed contract. It mirrors
// payment.refunded (`id` and `intent_id` are the intent; reference_type and
// reference_id attribute it to the owning domain's order) and adds what an
// owning domain or an operator needs to act:
//
//	command_id    the refund command that was parked
//	amount_minor  the refund's amount, integer minor units
//	currency      ISO 4217
//	reason_code   closed vocabulary (obs.RefundReasonCodes)
//	reason        the REDACTED reason stored in last_error: status, provider
//	              error code/reason and a bounded description — never a raw
//	              provider body, a credential or a signature
//	status          always "needs_attention"
//	application_id  the product the refund belongs to (migration 010)
//
// It is published once per command, in the transaction that parks it.
func refundFailedPayload(intentID, commandID uuid.UUID, refType, refID, provider string,
	amountMinor int64, currency, code, reason, applicationID string) map[string]any {
	return map[string]any{
		"id":             intentID,
		"intent_id":      intentID,
		"command_id":     commandID,
		"reference_type": refType,
		"reference_id":   refID,
		"provider":       provider,
		"amount_minor":   amountMinor,
		"currency":       currency,
		"reason_code":    code,
		"reason":         reason,
		"status":         RefundStatusNeedsAttention,
		"application_id": applicationID,
	}
}

// ParkRefundCommand moves a pending or submitted command to needs_attention
// and enqueues payment.refund_failed in the SAME transaction. It reports
// whether this call parked it; false means the command was already parked,
// settled or resolved, and nothing was written.
func (s *Store) ParkRefundCommand(ctx context.Context, id uuid.UUID, code, reason string) (bool, error) {
	reason = boundRunes(reason, maxParkReasonRunes)

	tx, err := s.db.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	var (
		intentID           uuid.UUID
		amount             int64
		currency, provider string
	)
	err = tx.QueryRow(ctx,
		`UPDATE payments.refund_commands
		    SET status = 'needs_attention', last_error = $2, failure_code = $3, updated_at = NOW()
		  WHERE id = $1 AND status IN ('pending','submitted')
		  RETURNING intent_id, amount_minor, currency, provider`,
		id, reason, code).Scan(&intentID, &amount, &currency, &provider)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("payments: park refund command %s: %w", id, err)
	}

	var refType, refID, appID string
	err = tx.QueryRow(ctx,
		`SELECT COALESCE(reference_type,''), COALESCE(reference_id::text,''), COALESCE(application_id,'')
		   FROM payments.payment_intents WHERE id = $1`, intentID).Scan(&refType, &refID, &appID)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return false, err
	}

	if err := enqueueOutboxTx(ctx, tx, EventPaymentRefundFailed, intentID.String(), nil,
		refundFailedPayload(intentID, id, refType, refID, provider, amount, currency, code, reason, appID)); err != nil {
		return false, fmt.Errorf("payments: enqueue %s: %w", EventPaymentRefundFailed, err)
	}
	if err := tx.Commit(ctx); err != nil {
		return false, err
	}
	return true, nil
}

// CountRefundsNeedingAttention is the alarm gauge's value.
func (s *Store) CountRefundsNeedingAttention(ctx context.Context) (int64, error) {
	var n int64
	err := s.db.QueryRow(ctx,
		`SELECT count(*) FROM payments.refund_commands WHERE status = 'needs_attention'`).Scan(&n)
	return n, err
}

// NeedsAttentionRefund is one parked command as an operator sees it.
type NeedsAttentionRefund struct {
	ID                uuid.UUID `json:"id"`
	IntentID          uuid.UUID `json:"intent_id"`
	ReferenceType     string    `json:"reference_type"`
	ReferenceID       string    `json:"reference_id"`
	PayerID           string    `json:"payer_id"`
	AmountMinor       int64     `json:"amount_minor"`
	Currency          string    `json:"currency"`
	ReasonCode        string    `json:"reason_code"`
	Reason            string    `json:"reason"`
	Attempts          int       `json:"attempts"`
	Provider          string    `json:"provider"`
	ProviderOrderID   string    `json:"provider_order_id,omitempty"`
	ProviderPaymentID string    `json:"provider_payment_id,omitempty"`
	RequestedBy       string    `json:"requested_by"`
	ApplicationID     string    `json:"application_id,omitempty"`
	CreatedAt         time.Time `json:"created_at"`
	UpdatedAt         time.Time `json:"updated_at"`
}

// RefundCursor is a keyset position: the last row of the previous page.
type RefundCursor struct {
	CreatedAt time.Time
	ID        uuid.UUID
}

// NeedsAttentionFilter narrows the operator list.
type NeedsAttentionFilter struct {
	Limit         int
	ReferenceType string // "" = every reference type
	OwnerDomain   string // "" = every domain (legacy internal-key caller)
	ApplicationID string // "" = every application
	After         *RefundCursor
}

// ListRefundsNeedingAttention returns parked commands oldest first, and the
// cursor for the next page (nil on the last page).
func (s *Store) ListRefundsNeedingAttention(ctx context.Context, f NeedsAttentionFilter) ([]NeedsAttentionRefund, *RefundCursor, error) {
	if f.Limit <= 0 {
		f.Limit = 50
	}
	var afterAt *time.Time
	afterID := uuid.Nil
	if f.After != nil {
		t := f.After.CreatedAt
		afterAt, afterID = &t, f.After.ID
	}
	rows, err := s.db.Query(ctx,
		`SELECT c.id, c.intent_id, COALESCE(i.reference_type,''), COALESCE(i.reference_id::text,''),
		        COALESCE(i.payer_id::text,''), c.amount_minor, c.currency,
		        COALESCE(NULLIF(c.failure_code,''),'unclassified'), COALESCE(c.last_error,''),
		        c.attempts, c.provider,
		        COALESCE(NULLIF(i.provider_order_id,''), COALESCE(i.provider_ref,'')),
		        COALESCE(i.provider_payment_id,''), c.requested_by, COALESCE(c.application_id,''),
		        c.created_at, c.updated_at
		   FROM payments.refund_commands c
		   JOIN payments.payment_intents i ON i.id = c.intent_id
		  WHERE c.status = 'needs_attention'
		    AND ($1::text = '' OR i.reference_type = $1::text)
		    AND ($2::text = '' OR i.owner_domain = $2::text)
		    AND ($6::text = '' OR c.application_id = $6::text)
		    AND ($3::timestamptz IS NULL OR (c.created_at, c.id) > ($3::timestamptz, $4::uuid))
		  ORDER BY c.created_at, c.id
		  LIMIT $5`,
		f.ReferenceType, f.OwnerDomain, afterAt, afterID, f.Limit+1, f.ApplicationID)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	out := make([]NeedsAttentionRefund, 0, f.Limit)
	for rows.Next() {
		var r NeedsAttentionRefund
		if err := rows.Scan(&r.ID, &r.IntentID, &r.ReferenceType, &r.ReferenceID, &r.PayerID,
			&r.AmountMinor, &r.Currency, &r.ReasonCode, &r.Reason, &r.Attempts, &r.Provider,
			&r.ProviderOrderID, &r.ProviderPaymentID, &r.RequestedBy, &r.ApplicationID, &r.CreatedAt, &r.UpdatedAt); err != nil {
			return nil, nil, err
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, nil, err
	}
	if len(out) <= f.Limit {
		return out, nil, nil
	}
	out = out[:f.Limit]
	last := out[len(out)-1]
	return out, &RefundCursor{CreatedAt: last.CreatedAt, ID: last.ID}, nil
}

// ResolveRefundInput is an operator's resolution of one parked command.
type ResolveRefundInput struct {
	CommandID  uuid.UUID
	Resolution string
	Note       string
	// OperatorID is who resolved it, recorded on the command and the audit row.
	OperatorID string
	// Credential is how the caller authenticated ("internal_key" or
	// "service_token:<issuer>"), recorded on the audit row.
	Credential string
	// OwnerDomain restricts the command to intents that domain owns. "" is a
	// legacy internal-key caller, which is not domain-scoped.
	OwnerDomain string
}

// RefundResolution is the stored outcome of a resolution.
type RefundResolution struct {
	CommandID   uuid.UUID `json:"command_id"`
	IntentID    uuid.UUID `json:"intent_id"`
	Status      string    `json:"status"`
	Resolution  string    `json:"resolution"`
	Note        string    `json:"note"`
	ResolvedBy  string    `json:"resolved_by"`
	ResolvedAt  time.Time `json:"resolved_at"`
	AmountMinor int64     `json:"amount_minor"`
	Currency    string    `json:"currency"`
	// Replayed is true when the command was already resolved and this call
	// returned the stored resolution without writing anything.
	Replayed bool `json:"replayed"`
	// RefundEventEmitted is true only on the call that published
	// payment.refunded (a first refunded_manually resolution).
	RefundEventEmitted bool `json:"refund_event_emitted"`
}

// ResolveRefundCommand resolves a parked command exactly once.
//
// Lock order is intent, then command — the same order the refund webhook takes
// (it locks the intent to attribute a refund, then settles commands) — so the
// two cannot deadlock.
func (s *Store) ResolveRefundCommand(ctx context.Context, in ResolveRefundInput) (*RefundResolution, error) {
	if !IsValidResolution(in.Resolution) {
		return nil, fmt.Errorf("%w (got %q)", ErrInvalidResolution, in.Resolution)
	}

	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	var intentID uuid.UUID
	err = tx.QueryRow(ctx, `SELECT intent_id FROM payments.refund_commands WHERE id = $1`, in.CommandID).Scan(&intentID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrRefundCommandNotFound
	}
	if err != nil {
		return nil, err
	}

	var (
		intentStatus, intentCurrency, owner, refType, refID, provider, appID string
		intentAmount, refunded, reserved                                     int64
	)
	err = tx.QueryRow(ctx,
		`SELECT status, COALESCE(amount_minor,0), COALESCE(refunded_amount_minor,0),
		        COALESCE(refund_reserved_minor,0), COALESCE(currency,''), COALESCE(owner_domain,''),
		        COALESCE(reference_type,''), COALESCE(reference_id::text,''), COALESCE(provider,'razorpay'),
		        COALESCE(application_id,'')
		   FROM payments.payment_intents WHERE id = $1 FOR UPDATE`, intentID).
		Scan(&intentStatus, &intentAmount, &refunded, &reserved, &intentCurrency, &owner, &refType, &refID, &provider, &appID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrRefundCommandNotFound
	}
	if err != nil {
		return nil, err
	}
	if in.OwnerDomain != "" && owner != in.OwnerDomain {
		// Not yours reads as absent, as on every other owner-scoped route.
		return nil, ErrRefundCommandNotFound
	}

	var (
		status, currency, resolution, note, resolvedBy string
		amount                                         int64
		resolvedAt                                     *time.Time
	)
	err = tx.QueryRow(ctx,
		`SELECT status, amount_minor, currency, COALESCE(resolution,''), COALESCE(resolution_note,''),
		        COALESCE(resolved_by,''), resolved_at
		   FROM payments.refund_commands WHERE id = $1 AND intent_id = $2 FOR UPDATE`,
		in.CommandID, intentID).Scan(&status, &amount, &currency, &resolution, &note, &resolvedBy, &resolvedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrRefundCommandNotFound
	}
	if err != nil {
		return nil, err
	}

	out := &RefundResolution{
		CommandID: in.CommandID, IntentID: intentID, Status: status,
		AmountMinor: amount, Currency: currency,
	}
	switch status {
	case RefundStatusResolved:
		out.Resolution, out.Note, out.ResolvedBy, out.Replayed = resolution, note, resolvedBy, true
		if resolvedAt != nil {
			out.ResolvedAt = *resolvedAt
		}
		return out, tx.Commit(ctx)
	case RefundStatusNeedsAttention:
	default:
		return nil, fmt.Errorf("%w (status %s)", ErrRefundCommandNotParked, status)
	}

	var at time.Time
	if err := tx.QueryRow(ctx,
		`UPDATE payments.refund_commands
		    SET status = 'resolved', resolution = $2, resolution_note = $3, resolved_by = $4,
		        resolved_at = NOW(), updated_at = NOW(),
		        settled_at = CASE WHEN $2 = 'refunded_manually' THEN NOW() ELSE settled_at END
		  WHERE id = $1 AND status = 'needs_attention'
		  RETURNING resolved_at`,
		in.CommandID, in.Resolution, in.Note, in.OperatorID).Scan(&at); err != nil {
		return nil, fmt.Errorf("payments: resolve refund command %s: %w", in.CommandID, err)
	}

	// The command's reservation is released either way: nothing is in flight
	// for it any more. Floored at the reservation actually held.
	release := amount
	if release > reserved {
		release = reserved
	}
	newIntentStatus := intentStatus

	if in.Resolution == ResolutionRefundedManually {
		if intentStatus != "succeeded" && intentStatus != "partially_refunded" {
			return nil, fmt.Errorf("%w: intent %s is %s", ErrManualRefundRefused, intentID, intentStatus)
		}
		if !strings.EqualFold(strings.TrimSpace(currency), strings.TrimSpace(intentCurrency)) {
			return nil, fmt.Errorf("%w: command is in %q, intent %s is in %q",
				ErrManualRefundRefused, currency, intentID, intentCurrency)
		}
		newRefunded := refunded + amount
		if newRefunded > intentAmount {
			return nil, fmt.Errorf("%w: refunded total would be %d on an intent worth %d",
				ErrManualRefundRefused, newRefunded, intentAmount)
		}
		newIntentStatus = "partially_refunded"
		if newRefunded >= intentAmount {
			newIntentStatus = "refunded"
		}

		// Second guard behind the status transition: one ledger row per
		// command, keyed in a namespace no provider refund id can occupy.
		manualID := "manual:" + in.CommandID.String()
		tag, err := tx.Exec(ctx,
			`INSERT INTO payments.provider_refunds_applied
			     (provider, provider_refund_id, command_id, intent_id, amount_minor, application_id)
			 VALUES ($1,$2,$3,$4,$5,NULLIF($6,''))
			 ON CONFLICT (provider, provider_refund_id) DO NOTHING`,
			manualRefundProvider, manualID, in.CommandID, intentID, amount, appID)
		if err != nil {
			return nil, err
		}
		if tag.RowsAffected() == 0 {
			return nil, fmt.Errorf("payments: manual refund for command %s is already on the ledger", in.CommandID)
		}
		if _, err := tx.Exec(ctx,
			`UPDATE payments.payment_intents
			    SET refunded_amount_minor = $2,
			        refund_reserved_minor = COALESCE(refund_reserved_minor,0) - $3,
			        status = $4, updated_at = NOW()
			  WHERE id = $1`, intentID, newRefunded, release, newIntentStatus); err != nil {
			return nil, err
		}
		if err := enqueueOutboxTx(ctx, tx, events.EventPaymentRefunded, intentID.String(), nil, map[string]any{
			"id":                 intentID,
			"intent_id":          intentID,
			"provider":           provider,
			"provider_refund_id": manualID,
			"amount_minor":       amount,
			"status":             newIntentStatus,
			"reference_type":     refType,
			"reference_id":       refID,
			"command_id":         in.CommandID,
			"application_id":     appID,
			// manual: no provider refund exists. The money was returned outside
			// the provider integration and an operator recorded it.
			"manual": true,
		}); err != nil {
			return nil, fmt.Errorf("payments: enqueue manual %s: %w", events.EventPaymentRefunded, err)
		}
		out.RefundEventEmitted = true
	} else if release > 0 {
		if _, err := tx.Exec(ctx,
			`UPDATE payments.payment_intents
			    SET refund_reserved_minor = COALESCE(refund_reserved_minor,0) - $2, updated_at = NOW()
			  WHERE id = $1`, intentID, release); err != nil {
			return nil, err
		}
	}

	meta, err := json.Marshal(map[string]any{
		"command_id":   in.CommandID,
		"resolution":   in.Resolution,
		"operator_id":  in.OperatorID,
		"credential":   in.Credential,
		"note":         in.Note,
		"amount_minor": amount,
	})
	if err != nil {
		return nil, err
	}
	if _, err := tx.Exec(ctx,
		`INSERT INTO payments.payment_audit_log (intent_id, event, old_status, new_status, metadata)
		 VALUES ($1,'refund_command_resolved',$2,$3,$4)`,
		intentID, intentStatus, newIntentStatus, meta); err != nil {
		return nil, err
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	out.Status = RefundStatusResolved
	out.Resolution, out.Note, out.ResolvedBy, out.ResolvedAt = in.Resolution, in.Note, in.OperatorID, at
	return out, nil
}

func boundRunes(s string, max int) string {
	if r := []rune(s); len(r) > max {
		return string(r[:max]) + "…"
	}
	return s
}
