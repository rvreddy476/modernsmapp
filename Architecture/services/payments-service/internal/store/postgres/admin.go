package postgres

// Reads and the one registry write behind the admin console (admin-service).
//
// Every read takes an optional application_id. "" means every application; a
// named one confines the result to rows whose application_id is that key, so a
// row of another application reads as absent. admin-service passes it for an
// admin whose role is scoped to one application.
//
// Nothing here moves money. The refund resolution the console can take goes
// through ResolveRefundCommand (refund_attention.go) with its existing checks.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// AdminIntent is one payment intent as the console sees it. It carries no
// client-session material (UPI intent URL) and no idempotency key.
type AdminIntent struct {
	ID                  uuid.UUID `json:"id"`
	ApplicationID       string    `json:"application_id"`
	Channel             string    `json:"channel,omitempty"`
	OwnerDomain         string    `json:"owner_domain"`
	ReferenceType       string    `json:"reference_type"`
	ReferenceID         uuid.UUID `json:"reference_id"`
	PayerID             uuid.UUID `json:"payer_id"`
	PayeeID             uuid.UUID `json:"payee_id"`
	AmountMinor         int64     `json:"amount_minor"`
	Currency            string    `json:"currency"`
	Method              string    `json:"method"`
	Status              string    `json:"status"`
	Provider            string    `json:"provider"`
	ProviderOrderID     string    `json:"provider_order_id,omitempty"`
	ProviderPaymentID   string    `json:"provider_payment_id,omitempty"`
	RefundedAmountMinor int64     `json:"refunded_amount_minor"`
	RefundReservedMinor int64     `json:"refund_reserved_minor"`
	CreatedAt           time.Time `json:"created_at"`
	UpdatedAt           time.Time `json:"updated_at"`
}

// AdminRefund is one refund command as the console sees it.
type AdminRefund struct {
	ID               uuid.UUID  `json:"id"`
	IntentID         uuid.UUID  `json:"intent_id"`
	ApplicationID    string     `json:"application_id"`
	ReferenceType    string     `json:"reference_type"`
	ReferenceID      string     `json:"reference_id"`
	AmountMinor      int64      `json:"amount_minor"`
	Currency         string     `json:"currency"`
	Reason           string     `json:"reason,omitempty"`
	Status           string     `json:"status"`
	Provider         string     `json:"provider"`
	ProviderRefundID string     `json:"provider_refund_id,omitempty"`
	Attempts         int        `json:"attempts"`
	FailureCode      string     `json:"failure_code,omitempty"`
	LastError        string     `json:"last_error,omitempty"`
	RequestedBy      string     `json:"requested_by"`
	Resolution       string     `json:"resolution,omitempty"`
	ResolutionNote   string     `json:"resolution_note,omitempty"`
	ResolvedBy       string     `json:"resolved_by,omitempty"`
	ResolvedAt       *time.Time `json:"resolved_at,omitempty"`
	SettledAt        *time.Time `json:"settled_at,omitempty"`
	CreatedAt        time.Time  `json:"created_at"`
	UpdatedAt        time.Time  `json:"updated_at"`
}

// AdminIntentDetail is an intent and its refund commands, newest first.
type AdminIntentDetail struct {
	Intent  AdminIntent   `json:"intent"`
	Refunds []AdminRefund `json:"refunds"`
}

const adminIntentColumns = `i.id, COALESCE(i.application_id,''), COALESCE(i.channel,''), COALESCE(i.owner_domain,''),
	i.reference_type, i.reference_id, i.payer_id, i.payee_id, COALESCE(i.amount_minor,0), i.currency, i.method,
	i.status, COALESCE(i.provider,'razorpay'), COALESCE(NULLIF(i.provider_order_id,''), COALESCE(i.provider_ref,'')),
	COALESCE(i.provider_payment_id,''), COALESCE(i.refunded_amount_minor,0), COALESCE(i.refund_reserved_minor,0),
	i.created_at, i.updated_at`

func scanAdminIntent(row pgx.Row) (*AdminIntent, error) {
	var a AdminIntent
	if err := row.Scan(&a.ID, &a.ApplicationID, &a.Channel, &a.OwnerDomain, &a.ReferenceType, &a.ReferenceID,
		&a.PayerID, &a.PayeeID, &a.AmountMinor, &a.Currency, &a.Method, &a.Status, &a.Provider,
		&a.ProviderOrderID, &a.ProviderPaymentID, &a.RefundedAmountMinor, &a.RefundReservedMinor,
		&a.CreatedAt, &a.UpdatedAt); err != nil {
		return nil, err
	}
	return &a, nil
}

const adminRefundColumns = `c.id, c.intent_id, COALESCE(c.application_id,''), COALESCE(i.reference_type,''),
	COALESCE(i.reference_id::text,''), c.amount_minor, c.currency, COALESCE(c.reason,''), c.status, c.provider,
	COALESCE(c.provider_refund_id,''), c.attempts, COALESCE(c.failure_code,''), COALESCE(c.last_error,''),
	c.requested_by, COALESCE(c.resolution,''), COALESCE(c.resolution_note,''), COALESCE(c.resolved_by,''),
	c.resolved_at, c.settled_at, c.created_at, c.updated_at`

func scanAdminRefund(row pgx.Row) (*AdminRefund, error) {
	var r AdminRefund
	if err := row.Scan(&r.ID, &r.IntentID, &r.ApplicationID, &r.ReferenceType, &r.ReferenceID, &r.AmountMinor,
		&r.Currency, &r.Reason, &r.Status, &r.Provider, &r.ProviderRefundID, &r.Attempts, &r.FailureCode,
		&r.LastError, &r.RequestedBy, &r.Resolution, &r.ResolutionNote, &r.ResolvedBy, &r.ResolvedAt,
		&r.SettledAt, &r.CreatedAt, &r.UpdatedAt); err != nil {
		return nil, err
	}
	return &r, nil
}

// AdminGetIntent reads one intent and its refund commands. An intent of
// another application than applicationID ("" = any) is ErrIntentNotFound.
func (s *Store) AdminGetIntent(ctx context.Context, id uuid.UUID, applicationID string) (*AdminIntentDetail, error) {
	intent, err := scanAdminIntent(s.db.QueryRow(ctx,
		`SELECT `+adminIntentColumns+` FROM payments.payment_intents i
		  WHERE i.id = $1 AND ($2::text = '' OR i.application_id = $2::text)`, id, applicationID))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrIntentNotFound
	}
	if err != nil {
		return nil, err
	}
	rows, err := s.db.Query(ctx,
		`SELECT `+adminRefundColumns+` FROM payments.refund_commands c
		   JOIN payments.payment_intents i ON i.id = c.intent_id
		  WHERE c.intent_id = $1 ORDER BY c.created_at DESC, c.id DESC LIMIT 100`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := &AdminIntentDetail{Intent: *intent, Refunds: []AdminRefund{}}
	for rows.Next() {
		r, err := scanAdminRefund(rows)
		if err != nil {
			return nil, err
		}
		out.Refunds = append(out.Refunds, *r)
	}
	return out, rows.Err()
}

// AdminIntentFilter narrows AdminListIntents. Every field is optional.
type AdminIntentFilter struct {
	ApplicationID string
	ReferenceType string
	ReferenceID   *uuid.UUID
	// ProviderRef matches the provider order id or the provider payment id.
	ProviderRef string
	Status      string
	Limit       int
	// After is the last row of the previous page (newest first).
	After *RefundCursor
}

// AdminListIntents pages intents newest first, and the next cursor (nil on the
// last page).
func (s *Store) AdminListIntents(ctx context.Context, f AdminIntentFilter) ([]AdminIntent, *RefundCursor, error) {
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
		`SELECT `+adminIntentColumns+` FROM payments.payment_intents i
		  WHERE ($1::text = '' OR i.application_id = $1::text)
		    AND ($2::text = '' OR i.reference_type = $2::text)
		    AND ($3::uuid IS NULL OR i.reference_id = $3::uuid)
		    AND ($4::text = '' OR i.provider_order_id = $4::text OR i.provider_ref = $4::text
		         OR i.provider_payment_id = $4::text)
		    AND ($5::text = '' OR i.status = $5::text)
		    AND ($6::timestamptz IS NULL OR (i.created_at, i.id) < ($6::timestamptz, $7::uuid))
		  ORDER BY i.created_at DESC, i.id DESC
		  LIMIT $8`,
		f.ApplicationID, f.ReferenceType, f.ReferenceID, f.ProviderRef, f.Status, afterAt, afterID, f.Limit+1)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	out := make([]AdminIntent, 0, f.Limit)
	for rows.Next() {
		a, err := scanAdminIntent(rows)
		if err != nil {
			return nil, nil, err
		}
		out = append(out, *a)
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

// AdminGetRefund reads one refund command. A command of another application
// than applicationID ("" = any) is ErrRefundCommandNotFound.
func (s *Store) AdminGetRefund(ctx context.Context, id uuid.UUID, applicationID string) (*AdminRefund, error) {
	r, err := scanAdminRefund(s.db.QueryRow(ctx,
		`SELECT `+adminRefundColumns+` FROM payments.refund_commands c
		   JOIN payments.payment_intents i ON i.id = c.intent_id
		  WHERE c.id = $1 AND ($2::text = '' OR c.application_id = $2::text)`, id, applicationID))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrRefundCommandNotFound
	}
	return r, err
}

// ─── Reconciliation status ───────────────────────────────────────────

// StuckIntent is an intent still pending or processing past the reconciler's
// pending age.
type StuckIntent struct {
	ID              uuid.UUID `json:"id"`
	ApplicationID   string    `json:"application_id"`
	Status          string    `json:"status"`
	AmountMinor     int64     `json:"amount_minor"`
	Currency        string    `json:"currency"`
	ProviderOrderID string    `json:"provider_order_id,omitempty"`
	// StubReference is true for a stub-gateway order the reconciler never
	// sends to a provider (dev leftovers); they are listed, not reconciled.
	StubReference bool      `json:"stub_reference"`
	CreatedAt     time.Time `json:"created_at"`
	AgeSeconds    int64     `json:"age_seconds"`
}

// OpenRefundRequired is a late capture on an already-terminal intent, recorded
// in payments.refund_required and not refunded automatically.
type OpenRefundRequired struct {
	ID                int64     `json:"id"`
	IntentID          uuid.UUID `json:"intent_id"`
	ApplicationID     string    `json:"application_id"`
	Provider          string    `json:"provider"`
	ProviderPaymentID string    `json:"provider_payment_id"`
	AmountMinor       int64     `json:"amount_minor"`
	Currency          string    `json:"currency"`
	Reason            string    `json:"reason"`
	DetectedAt        time.Time `json:"detected_at"`
}

// Reconciliation is what the reconciler and the refund worker have left for a
// person: the database state they flag. The reconciler itself keeps no flags
// of its own beyond logs, so this is derived from the rows.
type Reconciliation struct {
	StuckIntentsCount            int64                `json:"stuck_intents_count"`
	StuckIntents                 []StuckIntent        `json:"stuck_intents"`
	RefundRequiredOpenCount      int64                `json:"refund_required_open_count"`
	RefundRequiredOpen           []OpenRefundRequired `json:"refund_required_open"`
	RefundsNeedingAttention      int64                `json:"refunds_needing_attention"`
	RefundsRetryingAfterFailure  int64                `json:"refunds_retrying_after_failure"`
	OldestUnsettledRefundSeconds int64                `json:"oldest_unsettled_refund_seconds"`
	PendingAgeSeconds            int64                `json:"pending_age_seconds"`
}

// AdminReconciliation reads the reconciliation status, oldest first, at most
// limit rows per list.
func (s *Store) AdminReconciliation(ctx context.Context, applicationID string, pendingAge time.Duration, limit int) (*Reconciliation, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	age := fmt.Sprintf("%d seconds", int64(pendingAge.Seconds()))
	out := &Reconciliation{StuckIntents: []StuckIntent{}, RefundRequiredOpen: []OpenRefundRequired{},
		PendingAgeSeconds: int64(pendingAge.Seconds())}

	if err := s.db.QueryRow(ctx,
		`SELECT
		   (SELECT count(*) FROM payments.payment_intents
		     WHERE status IN ('pending','processing') AND created_at < NOW() - $2::interval
		       AND ($1::text = '' OR application_id = $1::text)),
		   (SELECT count(*) FROM payments.refund_required
		     WHERE resolved_at IS NULL AND ($1::text = '' OR application_id = $1::text)),
		   (SELECT count(*) FROM payments.refund_commands
		     WHERE status = 'needs_attention' AND ($1::text = '' OR application_id = $1::text)),
		   (SELECT count(*) FROM payments.refund_commands
		     WHERE status = 'pending' AND last_error IS NOT NULL AND ($1::text = '' OR application_id = $1::text)),
		   (SELECT COALESCE(EXTRACT(EPOCH FROM (NOW() - MIN(created_at)))::bigint, 0) FROM payments.refund_commands
		     WHERE status NOT IN ('succeeded','resolved') AND ($1::text = '' OR application_id = $1::text))`,
		applicationID, age).Scan(&out.StuckIntentsCount, &out.RefundRequiredOpenCount, &out.RefundsNeedingAttention,
		&out.RefundsRetryingAfterFailure, &out.OldestUnsettledRefundSeconds); err != nil {
		return nil, err
	}

	rows, err := s.db.Query(ctx,
		`SELECT id, COALESCE(application_id,''), status, COALESCE(amount_minor,0), currency,
		        COALESCE(NULLIF(provider_order_id,''), COALESCE(provider_ref,'')),
		        COALESCE(NULLIF(provider_order_id,''), provider_ref, '') LIKE $4,
		        created_at, EXTRACT(EPOCH FROM (NOW() - created_at))::bigint
		   FROM payments.payment_intents
		  WHERE status IN ('pending','processing') AND created_at < NOW() - $2::interval
		    AND ($1::text = '' OR application_id = $1::text)
		  ORDER BY created_at, id LIMIT $3`, applicationID, age, limit, stubOrderRefPattern)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var si StuckIntent
		if err := rows.Scan(&si.ID, &si.ApplicationID, &si.Status, &si.AmountMinor, &si.Currency,
			&si.ProviderOrderID, &si.StubReference, &si.CreatedAt, &si.AgeSeconds); err != nil {
			rows.Close()
			return nil, err
		}
		out.StuckIntents = append(out.StuckIntents, si)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}

	rows, err = s.db.Query(ctx,
		`SELECT id, intent_id, COALESCE(application_id,''), provider, provider_payment_id, amount_minor,
		        COALESCE(currency,''), reason, detected_at
		   FROM payments.refund_required
		  WHERE resolved_at IS NULL AND ($1::text = '' OR application_id = $1::text)
		  ORDER BY detected_at, id LIMIT $2`, applicationID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var rr OpenRefundRequired
		if err := rows.Scan(&rr.ID, &rr.IntentID, &rr.ApplicationID, &rr.Provider, &rr.ProviderPaymentID,
			&rr.AmountMinor, &rr.Currency, &rr.Reason, &rr.DetectedAt); err != nil {
			return nil, err
		}
		out.RefundRequiredOpen = append(out.RefundRequiredOpen, rr)
	}
	return out, rows.Err()
}

// ─── Stats ───────────────────────────────────────────────────────────

// AdminStatCounts is one application's (or the total's) dashboard counts.
// Money is integer paise.
type AdminStatCounts struct {
	RefundsNeedingAttention int64 `json:"refunds_needing_attention"`
	FailedPayments24h       int64 `json:"failed_payments_24h"`
	RefundFailedAlertsOpen  int64 `json:"refund_failed_alerts_open"`
	StuckIntents            int64 `json:"stuck_intents"`
	CapturedTodayMinor      int64 `json:"captured_today_minor"`
}

// AdminStats is the stats route's body.
type AdminStats struct {
	// Applications is keyed by application key; rows with no application_id
	// are counted under "unattributed".
	Applications map[string]AdminStatCounts `json:"applications"`
	Total        AdminStatCounts            `json:"total"`
	// DayStartsAt is the start of "today" for captured_today_minor (IST).
	DayStartsAt       time.Time `json:"day_starts_at"`
	PendingAgeSeconds int64     `json:"pending_age_seconds"`
}

// UnattributedApplication labels stats rows without an application_id.
const UnattributedApplication = "unattributed"

// AdminStats counts, per application and in total:
//
//	refunds_needing_attention  refund commands parked in needs_attention
//	failed_payments_24h        intents that are failed and last changed in 24 h
//	refund_failed_alerts_open  refund commands whose last attempt failed and
//	                           that the worker is still retrying (pending with
//	                           last_error); a parked one counts above instead
//	stuck_intents              intents pending/processing past pendingAge
//	captured_today_minor       amount of intents that became succeeded today
//	                           (Asia/Kolkata), by their audit row
//
// When applicationID is set only that application is counted.
func (s *Store) AdminStats(ctx context.Context, applicationID string, pendingAge time.Duration) (*AdminStats, error) {
	out := &AdminStats{Applications: map[string]AdminStatCounts{}, PendingAgeSeconds: int64(pendingAge.Seconds())}
	apps, err := s.ListApplications(ctx)
	if err != nil {
		return nil, err
	}
	for _, a := range apps {
		if applicationID == "" || a.Key == applicationID {
			out.Applications[a.Key] = AdminStatCounts{}
		}
	}
	if err := s.db.QueryRow(ctx,
		`SELECT (date_trunc('day', NOW() AT TIME ZONE 'Asia/Kolkata') AT TIME ZONE 'Asia/Kolkata')`).
		Scan(&out.DayStartsAt); err != nil {
		return nil, err
	}
	rows, err := s.db.Query(ctx,
		`SELECT COALESCE(app, ''), metric, SUM(n)::bigint FROM (
		   SELECT application_id AS app, 'refunds_needing_attention' AS metric, count(*) AS n
		     FROM payments.refund_commands WHERE status = 'needs_attention' GROUP BY application_id
		   UNION ALL
		   SELECT application_id, 'failed_payments_24h', count(*)
		     FROM payments.payment_intents
		    WHERE status = 'failed' AND updated_at >= NOW() - INTERVAL '24 hours' GROUP BY application_id
		   UNION ALL
		   SELECT application_id, 'refund_failed_alerts_open', count(*)
		     FROM payments.refund_commands WHERE status = 'pending' AND last_error IS NOT NULL GROUP BY application_id
		   UNION ALL
		   SELECT application_id, 'stuck_intents', count(*)
		     FROM payments.payment_intents
		    WHERE status IN ('pending','processing') AND created_at < NOW() - $2::interval GROUP BY application_id
		   UNION ALL
		   SELECT i.application_id, 'captured_today_minor', COALESCE(SUM(COALESCE(i.amount_minor,0)),0)
		     FROM payments.payment_intents i
		    WHERE EXISTS (SELECT 1 FROM payments.payment_audit_log a
		                   WHERE a.intent_id = i.id AND a.new_status = 'succeeded'
		                     AND a.old_status IS DISTINCT FROM 'succeeded' AND a.created_at >= $3)
		    GROUP BY i.application_id
		 ) m
		 WHERE $1::text = '' OR app = $1::text
		 GROUP BY 1, 2`,
		applicationID, fmt.Sprintf("%d seconds", int64(pendingAge.Seconds())), out.DayStartsAt)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var (
			app, metric string
			n           int64
		)
		if err := rows.Scan(&app, &metric, &n); err != nil {
			return nil, err
		}
		if app == "" {
			app = UnattributedApplication
		}
		c := out.Applications[app]
		for _, target := range []*AdminStatCounts{&c, &out.Total} {
			switch metric {
			case "refunds_needing_attention":
				target.RefundsNeedingAttention += n
			case "failed_payments_24h":
				target.FailedPayments24h += n
			case "refund_failed_alerts_open":
				target.RefundFailedAlertsOpen += n
			case "stuck_intents":
				target.StuckIntents += n
			case "captured_today_minor":
				target.CapturedTodayMinor += n
			}
		}
		out.Applications[app] = c
	}
	return out, rows.Err()
}

// ─── Audit reads ─────────────────────────────────────────────────────

// PaymentAuditEntry is one payment_audit_log row with its intent's application.
type PaymentAuditEntry struct {
	ID            int64           `json:"id"`
	IntentID      uuid.UUID       `json:"intent_id"`
	ApplicationID string          `json:"application_id"`
	Event         string          `json:"event"`
	OldStatus     string          `json:"old_status,omitempty"`
	NewStatus     string          `json:"new_status,omitempty"`
	ActorID       *uuid.UUID      `json:"actor_id,omitempty"`
	Metadata      json.RawMessage `json:"metadata,omitempty"`
	CreatedAt     time.Time       `json:"created_at"`
}

// PaymentAuditFilter narrows AdminPaymentAudit. BeforeID pages (id DESC).
type PaymentAuditFilter struct {
	ApplicationID string
	IntentID      *uuid.UUID
	Event         string
	BeforeID      int64
	Limit         int
}

// AdminPaymentAudit pages payment_audit_log newest first. The next page starts
// before the last id returned; 0 means there is none.
func (s *Store) AdminPaymentAudit(ctx context.Context, f PaymentAuditFilter) ([]PaymentAuditEntry, int64, error) {
	if f.Limit <= 0 {
		f.Limit = 50
	}
	rows, err := s.db.Query(ctx,
		`SELECT a.id, a.intent_id, COALESCE(i.application_id,''), a.event, COALESCE(a.old_status,''),
		        COALESCE(a.new_status,''), a.actor_id, COALESCE(a.metadata::text,''), a.created_at
		   FROM payments.payment_audit_log a
		   JOIN payments.payment_intents i ON i.id = a.intent_id
		  WHERE ($1::text = '' OR i.application_id = $1::text)
		    AND ($2::uuid IS NULL OR a.intent_id = $2::uuid)
		    AND ($3::text = '' OR a.event = $3::text)
		    AND ($4::bigint = 0 OR a.id < $4::bigint)
		  ORDER BY a.id DESC LIMIT $5`,
		f.ApplicationID, f.IntentID, f.Event, f.BeforeID, f.Limit+1)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	out := make([]PaymentAuditEntry, 0, f.Limit)
	for rows.Next() {
		var (
			e    PaymentAuditEntry
			meta string
		)
		if err := rows.Scan(&e.ID, &e.IntentID, &e.ApplicationID, &e.Event, &e.OldStatus, &e.NewStatus,
			&e.ActorID, &meta, &e.CreatedAt); err != nil {
			return nil, 0, err
		}
		if meta != "" {
			e.Metadata = json.RawMessage(meta)
		}
		out = append(out, e)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}
	if len(out) <= f.Limit {
		return out, 0, nil
	}
	out = out[:f.Limit]
	return out, out[len(out)-1].ID, nil
}

// ApplicationAuditEntry is one application_audit_log row.
type ApplicationAuditEntry struct {
	ID             int64           `json:"id"`
	ApplicationKey string          `json:"application_id"`
	Action         string          `json:"action"`
	OperatorID     string          `json:"operator_id"`
	Credential     string          `json:"credential"`
	Before         json.RawMessage `json:"before,omitempty"`
	After          json.RawMessage `json:"after"`
	CreatedAt      time.Time       `json:"created_at"`
}

// AdminApplicationAudit pages application_audit_log newest first.
func (s *Store) AdminApplicationAudit(ctx context.Context, applicationID string, beforeID int64, limit int) ([]ApplicationAuditEntry, int64, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := s.db.Query(ctx,
		`SELECT id, application_key, action, operator_id, credential, COALESCE(before::text,''), after::text, created_at
		   FROM payments.application_audit_log
		  WHERE ($1::text = '' OR application_key = $1::text)
		    AND ($2::bigint = 0 OR id < $2::bigint)
		  ORDER BY id DESC LIMIT $3`, applicationID, beforeID, limit+1)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	out := make([]ApplicationAuditEntry, 0, limit)
	for rows.Next() {
		var (
			e             ApplicationAuditEntry
			before, after string
		)
		if err := rows.Scan(&e.ID, &e.ApplicationKey, &e.Action, &e.OperatorID, &e.Credential, &before, &after, &e.CreatedAt); err != nil {
			return nil, 0, err
		}
		if before != "" {
			e.Before = json.RawMessage(before)
		}
		e.After = json.RawMessage(after)
		out = append(out, e)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}
	if len(out) <= limit {
		return out, 0, nil
	}
	out = out[:limit]
	return out, out[len(out)-1].ID, nil
}

// ─── Registry presentation update ────────────────────────────────────

// ApplicationPresentationInput changes how an application is shown and which
// methods it accepts. A nil field is left as it is. Status is deliberately not
// here: disabling an application stays on the operator PUT route.
type ApplicationPresentationInput struct {
	Key                 string
	DisplayName         *string
	MerchantDisplayName *string
	// EnabledMethods nil = unchanged; otherwise the full new list (non-empty).
	EnabledMethods []string
	OperatorID     string
	Credential     string
}

// UpdateApplicationPresentation updates an existing entry and writes one
// application_audit_log row, in the same transaction, when something changed.
// A missing entry is ErrApplicationNotFound; nothing is created.
func (s *Store) UpdateApplicationPresentation(ctx context.Context, in ApplicationPresentationInput) (*ApplicationWrite, error) {
	if !ValidApplicationKey(in.Key) {
		return nil, ErrApplicationNotFound
	}
	var methods any
	if in.EnabledMethods != nil {
		methods = NormalizeMethods(in.EnabledMethods)
	}

	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	var before []byte
	err = tx.QueryRow(ctx,
		`SELECT to_jsonb(a) FROM payments.applications a WHERE key = $1 FOR UPDATE`, in.Key).Scan(&before)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrApplicationNotFound
	}
	if err != nil {
		return nil, err
	}
	tag, err := tx.Exec(ctx,
		`UPDATE payments.applications
		    SET display_name = COALESCE($2::text, display_name),
		        merchant_display_name = COALESCE($3::text, merchant_display_name),
		        enabled_methods = COALESCE($4::text[], enabled_methods),
		        updated_at = NOW()
		  WHERE key = $1
		    AND (display_name, merchant_display_name, enabled_methods)
		        IS DISTINCT FROM (COALESCE($2::text, display_name), COALESCE($3::text, merchant_display_name),
		                          COALESCE($4::text[], enabled_methods))`,
		in.Key, in.DisplayName, in.MerchantDisplayName, methods)
	if err != nil {
		return nil, err
	}
	changed := tag.RowsAffected() == 1
	if changed {
		if _, err := tx.Exec(ctx,
			`INSERT INTO payments.application_audit_log
			     (application_key, action, operator_id, credential, before, after)
			 SELECT $1, 'updated', $2, $3, $4::jsonb, to_jsonb(a) FROM payments.applications a WHERE a.key = $1`,
			in.Key, in.OperatorID, in.Credential, before); err != nil {
			return nil, err
		}
	}
	app, err := scanApplication(tx.QueryRow(ctx,
		`SELECT `+applicationColumns+` FROM payments.applications WHERE key = $1`, in.Key))
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return &ApplicationWrite{Application: *app, Changed: changed}, nil
}
