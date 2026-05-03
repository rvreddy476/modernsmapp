// Package store provides PostgreSQL access for bill-pay-service.
//
// BBPS MODEL (Phase 2 D2): Setu is the BBPS aggregator (rail). AtPost is the
// consumer-facing biller. Tables in this schema cache catalog data sourced
// from Setu (categories, providers, mobile plans), persist user state
// (accounts, reminders, scheduled payments), and record the audit log of
// every payment touched (payments, idempotency, bills).
//
// DPDP NOTE: Customer account identifiers (electricity consumer numbers,
// mobile numbers, vehicle numbers) ARE stored — they are required to fetch
// and pay bills. They MUST NOT be logged in plain text. See store/payments.go
// for masking helpers used by callers.
package store

import (
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Store wraps a pgxpool with per-aggregate methods (providers, accounts,
// payments, idempotency, reminders, scheduled, mobile_plans).
type Store struct {
	db *pgxpool.Pool
}

// New returns a Store backed by the given pool.
func New(db *pgxpool.Pool) *Store {
	return &Store{db: db}
}

// DB exposes the underlying pool for transactional flows in the service layer.
func (s *Store) DB() *pgxpool.Pool { return s.db }

// --- Domain types ---------------------------------------------------------

// Category is a bill category (mobile_postpaid, electricity, ...). Static
// catalog seeded at bootstrap.
type Category struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Icon      string `json:"icon"`
	SortOrder int    `json:"sort_order"`
	IsActive  bool   `json:"is_active"`
}

// Provider is a single BBPS biller exposed by Setu (e.g. "BSES Rajdhani"
// under category=electricity, restricted to states=['DL']).
type Provider struct {
	ID                 uuid.UUID `json:"id"`
	SetuBillerID       string    `json:"setu_biller_id"`
	CategoryID         string    `json:"category_id"`
	Name               string    `json:"name"`
	ShortName          *string   `json:"short_name,omitempty"`
	LogoURL            *string   `json:"logo_url,omitempty"`
	States             []string  `json:"states"`
	CustomerParams     []byte    `json:"customer_params"` // raw JSONB
	BillFetchSupported bool      `json:"bill_fetch_supported"`
	IsActive           bool      `json:"is_active"`
	LastSyncedAt       time.Time `json:"last_synced_at"`
}

// Account is a user-saved bill identifier (e.g. their consumer-number at
// BSES). Soft-deleted via deleted_at — UI must filter on the deleted_at IS
// NULL clause we apply in queries.
type Account struct {
	ID              uuid.UUID  `json:"id"`
	UserID          uuid.UUID  `json:"user_id"`
	ProviderID      uuid.UUID  `json:"provider_id"`
	Identifier      string     `json:"identifier"`
	ExtraParams     []byte     `json:"extra_params"`
	Label           string     `json:"label"`
	IsDefault       bool       `json:"is_default"`
	AutopayEnabled  bool       `json:"autopay_enabled"`
	CreatedAt       time.Time  `json:"created_at"`
	DeletedAt       *time.Time `json:"deleted_at,omitempty"`
}

// Bill is a cached snapshot of the latest bill returned by Setu for an
// account. Status flips to 'paid' once a payment for the bill_id succeeds.
type Bill struct {
	ID              uuid.UUID  `json:"id"`
	AccountID       uuid.UUID  `json:"account_id"`
	BillAmountPaise int64      `json:"bill_amount_paise"`
	BillPeriodStart *time.Time `json:"bill_period_start,omitempty"`
	BillPeriodEnd   *time.Time `json:"bill_period_end,omitempty"`
	BillDueDate     *time.Time `json:"bill_due_date,omitempty"`
	BillNumber      *string    `json:"bill_number,omitempty"`
	CustomerName    *string    `json:"customer_name,omitempty"`
	SetuBillRef     *string    `json:"setu_bill_ref,omitempty"`
	Status          string     `json:"status"`
	FetchedAt       time.Time  `json:"fetched_at"`
	PaidAt          *time.Time `json:"paid_at,omitempty"`
	PaymentID       *uuid.UUID `json:"payment_id,omitempty"`
}

// Payment is one row in billpay.payments. Status progresses
// initiated -> submitted -> succeeded | failed | refunded.
type Payment struct {
	ID              uuid.UUID  `json:"id"`
	UserID          uuid.UUID  `json:"user_id"`
	AccountID       *uuid.UUID `json:"account_id,omitempty"`
	ProviderID      uuid.UUID  `json:"provider_id"`
	AmountPaise     int64      `json:"amount_paise"`
	FeePaise        int64      `json:"fee_paise"`
	PaymentMethod   string     `json:"payment_method"`
	WalletTxnID     *uuid.UUID `json:"wallet_txn_id,omitempty"`
	UPITxnRef       *string    `json:"upi_txn_ref,omitempty"`
	SetuPaymentRef  *string    `json:"setu_payment_ref,omitempty"`
	Status          string     `json:"status"`
	FailureReason   *string    `json:"failure_reason,omitempty"`
	ReceiptNumber   *string    `json:"receipt_number,omitempty"`
	BillID          *uuid.UUID `json:"bill_id,omitempty"`
	IdempotencyKey  *string    `json:"idempotency_key,omitempty"`
	CreatedAt       time.Time  `json:"created_at"`
	SettledAt       *time.Time `json:"settled_at,omitempty"`
}

// IdempotencyRecord deduplicates a payment-touching API call. Every Pay /
// RechargeMobile call carries a key (client-supplied); the service-layer
// checks this table FIRST and returns the cached response when the same key
// reappears within 24h.
type IdempotencyRecord struct {
	Key          string     `json:"key"`
	UserID       uuid.UUID  `json:"user_id"`
	PaymentID    *uuid.UUID `json:"payment_id,omitempty"`
	ResponseBody []byte     `json:"response_body,omitempty"`
	CreatedAt    time.Time  `json:"created_at"`
	ExpiresAt    time.Time  `json:"expires_at"`
}

// Reminder is one configurable bill reminder per account.
type Reminder struct {
	ID            uuid.UUID  `json:"id"`
	AccountID     uuid.UUID  `json:"account_id"`
	UserID        uuid.UUID  `json:"user_id"`
	DaysBeforeDue int        `json:"days_before_due"`
	Channels      []string   `json:"channels"`
	IsActive      bool       `json:"is_active"`
	LastSentAt    *time.Time `json:"last_sent_at,omitempty"`
	CreatedAt     time.Time  `json:"created_at"`
}

// ScheduledPayment is one user-configured recurring or one-off future bill-pay.
type ScheduledPayment struct {
	ID            uuid.UUID  `json:"id"`
	UserID        uuid.UUID  `json:"user_id"`
	AccountID     uuid.UUID  `json:"account_id"`
	AmountPaise   *int64     `json:"amount_paise,omitempty"` // NULL = "pay full bill amount"
	PaymentMethod string     `json:"payment_method"`
	ScheduleKind  string     `json:"schedule_kind"`
	NextRunDate   time.Time  `json:"next_run_date"`
	LastRunAt     *time.Time `json:"last_run_at,omitempty"`
	IsActive      bool       `json:"is_active"`
	CreatedAt     time.Time  `json:"created_at"`
}

// MobilePlan is a single recharge plan cached from Setu.
type MobilePlan struct {
	ID              uuid.UUID `json:"id"`
	Operator        string    `json:"operator"`
	Circle          string    `json:"circle"`
	PlanAmountPaise int64     `json:"plan_amount_paise"`
	ValidityDays    *int      `json:"validity_days,omitempty"`
	DataGBPerDay    *float64  `json:"data_gb_per_day,omitempty"`
	TalktimePaise   *int64    `json:"talktime_paise,omitempty"`
	SMSCountPerDay  *int      `json:"sms_count_per_day,omitempty"`
	Description     *string   `json:"description,omitempty"`
	Category        *string   `json:"category,omitempty"`
	IsActive        bool      `json:"is_active"`
	LastSyncedAt    time.Time `json:"last_synced_at"`
}

// MaskIdentifier returns the last 4 chars of an account identifier with
// the rest replaced by 'X'. Used in error messages and audit logs to keep
// DPDP compliant. e.g. "9876543210" -> "XXXXXX3210".
func MaskIdentifier(s string) string {
	if len(s) <= 4 {
		return "XXXX"
	}
	mask := make([]byte, len(s))
	for i := range mask {
		if i < len(s)-4 {
			mask[i] = 'X'
		} else {
			mask[i] = s[i]
		}
	}
	return string(mask)
}
