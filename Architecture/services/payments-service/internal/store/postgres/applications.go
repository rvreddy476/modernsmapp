package postgres

// Applications: the registry, and the per-application view of payments and
// refunds (migration 010).
//
// An application is a PRODUCT key (mstore, feast, …). Every intent carries one,
// and every row derived from an intent copies it at insert: refund_commands,
// refund_required, provider_refunds_applied, refunds_applied, payment_holds. A
// `channel` (momentum_android, web, …) is optional and says which installed app
// the payment came through; it is never the application.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/atpost/shared/paymentsclient"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// Application statuses.
const (
	ApplicationStatusActive   = "active"
	ApplicationStatusDisabled = "disabled"
)

var (
	// ErrApplicationNotFound means the key is not in the registry.
	ErrApplicationNotFound = errors.New("payments: application is not registered")
	// ErrApplicationRequired refuses a new payment row with no application.
	ErrApplicationRequired = errors.New("payments: application_id is required")
	// ErrApplicationMismatch means a request names a different application from
	// the intent it acts on, or the intent has none to compare against.
	ErrApplicationMismatch = errors.New("payments: application_id does not match the intent's application")
	// ErrInvalidChannel refuses a channel that is not a well-formed key.
	ErrInvalidChannel = errors.New("payments: channel must match ^[a-z][a-z0-9_]{1,31}$")
)

// ValidApplicationKey reports whether k is a well-formed application key, by the
// same rule the shared client applies before sending one.
func ValidApplicationKey(k string) bool { return paymentsclient.ValidateApplicationID(k) == nil }

// ValidChannel reports whether c is a well-formed channel. The empty string is
// "no channel" and is valid.
func ValidChannel(c string) bool { return c == "" || ValidApplicationKey(c) }

// Application is one registry entry.
type Application struct {
	Key                 string          `json:"key"`
	DisplayName         string          `json:"display_name"`
	Status              string          `json:"status"`
	MerchantDisplayName string          `json:"merchant_display_name"`
	EnabledMethods      []string        `json:"enabled_methods"`
	Settings            json.RawMessage `json:"settings"`
	CreatedAt           time.Time       `json:"created_at"`
	UpdatedAt           time.Time       `json:"updated_at"`
}

// Active reports whether the application accepts new payments.
func (a *Application) Active() bool { return a != nil && a.Status == ApplicationStatusActive }

// MethodEnabled reports whether the application accepts method.
func (a *Application) MethodEnabled(method string) bool {
	if a == nil {
		return false
	}
	for _, m := range a.EnabledMethods {
		if m == method {
			return true
		}
	}
	return false
}

const applicationColumns = `key, display_name, status, merchant_display_name, enabled_methods,
	settings::text, created_at, updated_at`

func scanApplication(row pgx.Row) (*Application, error) {
	var (
		a        Application
		settings string
	)
	if err := row.Scan(&a.Key, &a.DisplayName, &a.Status, &a.MerchantDisplayName, &a.EnabledMethods,
		&settings, &a.CreatedAt, &a.UpdatedAt); err != nil {
		return nil, err
	}
	if a.EnabledMethods == nil {
		a.EnabledMethods = []string{}
	}
	a.Settings = json.RawMessage(settings)
	return &a, nil
}

// GetApplication reads one registry entry.
func (s *Store) GetApplication(ctx context.Context, key string) (*Application, error) {
	a, err := scanApplication(s.db.QueryRow(ctx,
		`SELECT `+applicationColumns+` FROM payments.applications WHERE key = $1`, key))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrApplicationNotFound
	}
	return a, err
}

// ListApplications reads the whole registry, ordered by key.
func (s *Store) ListApplications(ctx context.Context) ([]Application, error) {
	rows, err := s.db.Query(ctx, `SELECT `+applicationColumns+` FROM payments.applications ORDER BY key`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Application{}
	for rows.Next() {
		a, err := scanApplication(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *a)
	}
	return out, rows.Err()
}

// PutApplicationInput creates or replaces one registry entry.
type PutApplicationInput struct {
	Key                 string
	DisplayName         string
	Status              string
	MerchantDisplayName string
	EnabledMethods      []string
	// Settings is a JSON object; nil means {}.
	Settings json.RawMessage
	// OperatorID and Credential are recorded on the audit row.
	OperatorID string
	Credential string
}

// ApplicationWrite is the outcome of PutApplication.
type ApplicationWrite struct {
	Application Application `json:"application"`
	// Created is true when this call inserted the entry.
	Created bool `json:"created"`
	// Changed is false for a replay that matched the stored entry; nothing was
	// written and no audit row was added.
	Changed bool `json:"changed"`
}

// NormalizeMethods sorts and de-duplicates a method list, so a replay that
// names the same methods in another order is recognised as a replay.
func NormalizeMethods(methods []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(methods))
	for _, m := range methods {
		if !seen[m] {
			seen[m] = true
			out = append(out, m)
		}
	}
	sort.Strings(out)
	return out
}

// PutApplication creates or replaces a registry entry, and writes one audit row
// when, and only when, something changed. Disabling an application changes this
// row only: existing payments and refunds are never touched.
func (s *Store) PutApplication(ctx context.Context, in PutApplicationInput) (*ApplicationWrite, error) {
	if !ValidApplicationKey(in.Key) {
		return nil, fmt.Errorf("payments: application key %q is not well formed", in.Key)
	}
	settings := "{}"
	if len(in.Settings) > 0 {
		settings = string(in.Settings)
	}
	methods := NormalizeMethods(in.EnabledMethods)

	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	var before []byte
	err = tx.QueryRow(ctx,
		`SELECT to_jsonb(a) FROM payments.applications a WHERE key = $1 FOR UPDATE`, in.Key).Scan(&before)
	created := false
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		tag, err := tx.Exec(ctx,
			`INSERT INTO payments.applications
			     (key, display_name, status, merchant_display_name, enabled_methods, settings)
			 VALUES ($1,$2,$3,$4,$5::text[],$6::jsonb)
			 ON CONFLICT (key) DO NOTHING`,
			in.Key, in.DisplayName, in.Status, in.MerchantDisplayName, methods, settings)
		if err != nil {
			return nil, err
		}
		if tag.RowsAffected() == 1 {
			created = true
		} else if err := tx.QueryRow(ctx,
			// A concurrent PUT created it first; continue as an update of that row.
			`SELECT to_jsonb(a) FROM payments.applications a WHERE key = $1 FOR UPDATE`, in.Key).Scan(&before); err != nil {
			return nil, err
		}
	case err != nil:
		return nil, err
	}

	changed := created
	if !created {
		tag, err := tx.Exec(ctx,
			`UPDATE payments.applications
			    SET display_name = $2, status = $3, merchant_display_name = $4,
			        enabled_methods = $5::text[], settings = $6::jsonb, updated_at = NOW()
			  WHERE key = $1
			    AND (display_name, status, merchant_display_name, enabled_methods, settings)
			        IS DISTINCT FROM ($2, $3, $4, $5::text[], $6::jsonb)`,
			in.Key, in.DisplayName, in.Status, in.MerchantDisplayName, methods, settings)
		if err != nil {
			return nil, err
		}
		changed = tag.RowsAffected() == 1
	}

	app, err := scanApplication(tx.QueryRow(ctx,
		`SELECT `+applicationColumns+` FROM payments.applications WHERE key = $1`, in.Key))
	if err != nil {
		return nil, err
	}
	if changed {
		action := "updated"
		var beforeArg any = before
		if created {
			action, beforeArg = "created", nil
		}
		if _, err := tx.Exec(ctx,
			`INSERT INTO payments.application_audit_log
			     (application_key, action, operator_id, credential, before, after)
			 SELECT $1, $2, $3, $4, $5::jsonb, to_jsonb(a) FROM payments.applications a WHERE a.key = $1`,
			in.Key, action, in.OperatorID, in.Credential, beforeArg); err != nil {
			return nil, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return &ApplicationWrite{Application: *app, Created: created, Changed: changed}, nil
}

// isApplicationFKViolation reports a write that named an application the
// registry does not hold.
func isApplicationFKViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23503" &&
		strings.HasPrefix(pgErr.ConstraintName, "fk_") && strings.HasSuffix(pgErr.ConstraintName, "_application")
}

// ─── Per-application transactions ────────────────────────────────────

// Transaction types.
const (
	TransactionPayment = "payment"
	TransactionRefund  = "refund"
)

// Transaction is one payment (an intent) or one refund (a refund command) of an
// application.
type Transaction struct {
	Type          string    `json:"type"`
	ID            uuid.UUID `json:"id"`
	IntentID      uuid.UUID `json:"intent_id"`
	ApplicationID string    `json:"application_id"`
	Channel       string    `json:"channel,omitempty"`
	ReferenceType string    `json:"reference_type"`
	ReferenceID   string    `json:"reference_id"`
	PayerID       string    `json:"payer_id"`
	AmountMinor   int64     `json:"amount_minor"`
	Currency      string    `json:"currency"`
	Method        string    `json:"method,omitempty"`
	// Status is the intent's status for a payment, the command's for a refund.
	Status            string `json:"status"`
	Provider          string `json:"provider"`
	ProviderOrderID   string `json:"provider_order_id,omitempty"`
	ProviderPaymentID string `json:"provider_payment_id,omitempty"`
	ProviderRefundID  string `json:"provider_refund_id,omitempty"`
	// RefundedAmountMinor is set on payments only.
	RefundedAmountMinor int64 `json:"refunded_amount_minor,omitempty"`
	// Reason is set on refunds only.
	Reason    string    `json:"reason,omitempty"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// TransactionFilter narrows ListApplicationTransactions.
type TransactionFilter struct {
	// ApplicationID is required.
	ApplicationID string
	// Type is "", TransactionPayment or TransactionRefund ("" = both).
	Type string
	// Status filters on the intent status (payments) or the command status
	// (refunds). "" = every status.
	Status string
	// OwnerDomain restricts to intents that service owns. "" = every domain.
	OwnerDomain string
	Limit       int
	// After is the last row of the previous page (newest first).
	After *RefundCursor
}

// ListApplicationTransactions returns an application's payments and refunds,
// newest first, and the cursor for the next page (nil on the last page).
func (s *Store) ListApplicationTransactions(ctx context.Context, f TransactionFilter) ([]Transaction, *RefundCursor, error) {
	if !ValidApplicationKey(f.ApplicationID) {
		return nil, nil, ErrApplicationRequired
	}
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
		`SELECT type, id, intent_id, application_id, channel, reference_type, reference_id, payer_id,
		        amount_minor, currency, method, status, provider, provider_order_id, provider_payment_id,
		        provider_refund_id, refunded_amount_minor, reason, created_at, updated_at
		   FROM (
		        SELECT 'payment'::text AS type, i.id, i.id AS intent_id, i.application_id,
		               COALESCE(i.channel,'') AS channel, i.reference_type,
		               i.reference_id::text AS reference_id, i.payer_id::text AS payer_id,
		               COALESCE(i.amount_minor,0) AS amount_minor, i.currency, i.method, i.status,
		               COALESCE(i.provider,'razorpay') AS provider,
		               COALESCE(NULLIF(i.provider_order_id,''), COALESCE(i.provider_ref,'')) AS provider_order_id,
		               COALESCE(i.provider_payment_id,'') AS provider_payment_id,
		               ''::text AS provider_refund_id,
		               COALESCE(i.refunded_amount_minor,0) AS refunded_amount_minor,
		               ''::text AS reason, i.created_at, i.updated_at
		          FROM payments.payment_intents i
		         WHERE $2::text IN ('', 'payment')
		           AND i.application_id = $1::text
		           AND ($3::text = '' OR i.status = $3::text)
		           AND ($4::text = '' OR i.owner_domain = $4::text)
		           AND ($5::timestamptz IS NULL OR (i.created_at, i.id) < ($5::timestamptz, $6::uuid))
		        UNION ALL
		        SELECT 'refund'::text, c.id, c.intent_id, c.application_id,
		               COALESCE(i.channel,''), COALESCE(i.reference_type,''),
		               COALESCE(i.reference_id::text,''), COALESCE(i.payer_id::text,''),
		               c.amount_minor, c.currency, COALESCE(i.method,''), c.status, c.provider,
		               COALESCE(NULLIF(i.provider_order_id,''), COALESCE(i.provider_ref,'')),
		               COALESCE(i.provider_payment_id,''), COALESCE(c.provider_refund_id,''),
		               0::bigint, COALESCE(c.reason,''), c.created_at, c.updated_at
		          FROM payments.refund_commands c
		          JOIN payments.payment_intents i ON i.id = c.intent_id
		         WHERE $2::text IN ('', 'refund')
		           AND c.application_id = $1::text
		           AND ($3::text = '' OR c.status = $3::text)
		           AND ($4::text = '' OR i.owner_domain = $4::text)
		           AND ($5::timestamptz IS NULL OR (c.created_at, c.id) < ($5::timestamptz, $6::uuid))
		        ) t
		  ORDER BY created_at DESC, id DESC
		  LIMIT $7`,
		f.ApplicationID, f.Type, f.Status, f.OwnerDomain, afterAt, afterID, f.Limit+1)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	out := make([]Transaction, 0, f.Limit)
	for rows.Next() {
		var t Transaction
		if err := rows.Scan(&t.Type, &t.ID, &t.IntentID, &t.ApplicationID, &t.Channel, &t.ReferenceType,
			&t.ReferenceID, &t.PayerID, &t.AmountMinor, &t.Currency, &t.Method, &t.Status, &t.Provider,
			&t.ProviderOrderID, &t.ProviderPaymentID, &t.ProviderRefundID, &t.RefundedAmountMinor,
			&t.Reason, &t.CreatedAt, &t.UpdatedAt); err != nil {
			return nil, nil, err
		}
		out = append(out, t)
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

// UnmappedApplicationCounts reports, per table, how many rows have no
// application_id. main.go logs it at boot; gated 998 refuses while any is > 0.
func (s *Store) UnmappedApplicationCounts(ctx context.Context) (map[string]int64, error) {
	out := map[string]int64{}
	for _, tbl := range []string{"payment_intents", "refund_commands", "refund_required",
		"provider_refunds_applied", "refunds_applied", "payment_holds"} {
		var n int64
		if err := s.db.QueryRow(ctx,
			`SELECT count(*) FROM payments.`+tbl+` WHERE application_id IS NULL`).Scan(&n); err != nil {
			return nil, err
		}
		out[tbl] = n
	}
	return out, nil
}
