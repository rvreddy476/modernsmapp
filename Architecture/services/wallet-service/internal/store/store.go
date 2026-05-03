// Package store provides PostgreSQL access for wallet-service.
//
// BC-of-PPI MODEL: AtPost holds NO customer funds. The partner bank holds the
// PPI; the rows below are a *mirror* + audit log so the UX can render quickly
// and so we have a local source for cross-service merchant-pay debits. The
// nightly reconciler (cmd/reconciler) compares this mirror against the
// partner bank's settlement file and flags discrepancies.
package store

import (
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Store wraps a pgxpool with per-aggregate methods (balances, transactions,
// idempotency, kyc, recipients, settlements).
type Store struct {
	db *pgxpool.Pool
}

// New returns a Store backed by the given pool.
func New(db *pgxpool.Pool) *Store {
	return &Store{db: db}
}

// DB exposes the underlying pool for transactional flows in the service layer
// (e.g. the send-saga starts a tx that touches both balance + transaction in
// one atomic step).
func (s *Store) DB() *pgxpool.Pool { return s.db }

// --- Domain types ----------------------------------------------------------

// KYCTier enumerates the wallet KYC levels. Limits per tier are enforced by
// service.balance + service.send.
type KYCTier string

const (
	KYCMinimal  KYCTier = "minimal"
	KYCFull     KYCTier = "full"
	KYCEnhanced KYCTier = "enhanced"
)

// MonthlyLimitForTier returns the per-month spend cap (paise) for a tier.
// minimal = 10 000 INR; full = 2 lakh INR; enhanced = 5 lakh INR.
func MonthlyLimitForTier(tier KYCTier) int64 {
	switch tier {
	case KYCFull:
		return 20000000 // 2 lakh INR
	case KYCEnhanced:
		return 50000000 // 5 lakh INR
	default:
		return 1000000 // 10 000 INR (minimal KYC)
	}
}

// Balance is the consumer-facing balance row. Truth lives at the partner bank.
type Balance struct {
	UserID            uuid.UUID `json:"user_id"`
	BankAccountRef    string    `json:"bank_account_ref"`
	AvailablePaise    int64     `json:"available_paise"`
	PendingInPaise    int64     `json:"pending_in_paise"`
	PendingOutPaise   int64     `json:"pending_out_paise"`
	KYCTier           KYCTier   `json:"kyc_tier"`
	MonthlyLimitPaise int64     `json:"monthly_limit_paise"`
	IsFrozen          bool      `json:"is_frozen"`
	FrozenReason      *string   `json:"frozen_reason,omitempty"`
	LastSyncedAt      time.Time `json:"last_synced_at"`
	CreatedAt         time.Time `json:"created_at"`
	UpdatedAt         time.Time `json:"updated_at"`
}

// Transaction is one row in wallet.transactions. Statuses progress
// pending -> succeeded | failed | reversed | pending_invite.
type Transaction struct {
	ID                 uuid.UUID  `json:"id"`
	UserID             uuid.UUID  `json:"user_id"`
	Type               string     `json:"type"`
	Direction          string     `json:"direction"`
	AmountPaise        int64      `json:"amount_paise"`
	CounterpartyUserID *uuid.UUID `json:"counterparty_user_id,omitempty"`
	CounterpartyPhone  *string    `json:"counterparty_phone,omitempty"`
	CounterpartyLabel  *string    `json:"counterparty_label,omitempty"`
	MerchantService    *string    `json:"merchant_service,omitempty"`
	MerchantRef        *string    `json:"merchant_ref,omitempty"`
	Status             string     `json:"status"`
	BankTxnRef         *string    `json:"bank_txn_ref,omitempty"`
	UPITxnRef          *string    `json:"upi_txn_ref,omitempty"`
	FailureReason      *string    `json:"failure_reason,omitempty"`
	IdempotencyKey     *string    `json:"idempotency_key,omitempty"`
	CreatedAt          time.Time  `json:"created_at"`
	SettledAt          *time.Time `json:"settled_at,omitempty"`
}

// IdempotencyRecord deduplicates a payment-touching API call. Every top-up,
// send, and merchant-pay carries a key (client-supplied or server-generated);
// service-layer checks this table FIRST and returns the cached response when
// the same key reappears within 24h.
type IdempotencyRecord struct {
	Key           string     `json:"key"`
	UserID        uuid.UUID  `json:"user_id"`
	Operation     string     `json:"operation"`
	TransactionID *uuid.UUID `json:"transaction_id,omitempty"`
	ResponseBody  []byte     `json:"response_body,omitempty"`
	CreatedAt     time.Time  `json:"created_at"`
	ExpiresAt     time.Time  `json:"expires_at"`
}

// KYCRecord is the wallet.kyc_records row. DPDP: aadhaar_number is NEVER
// stored — only the partner-supplied opaque digilocker_ref. PAN is masked to
// last 4 digits. Address proofs are media references.
type KYCRecord struct {
	UserID           uuid.UUID  `json:"user_id"`
	Tier             KYCTier    `json:"tier"`
	AadhaarStatus    *string    `json:"aadhaar_status,omitempty"`
	DigiLockerRef    *string    `json:"digilocker_ref,omitempty"`
	PANStatus        *string    `json:"pan_status,omitempty"`
	PANMasked        *string    `json:"pan_masked,omitempty"`
	AddressProofRef  *string    `json:"address_proof_ref,omitempty"`
	SubmittedAt      *time.Time `json:"submitted_at,omitempty"`
	VerifiedAt       *time.Time `json:"verified_at,omitempty"`
	RejectionReason  *string    `json:"rejection_reason,omitempty"`
}

// Recipient is one entry in the user's "frequent recipients" list. The
// composite PK (user_id, recipient_user_id|recipient_phone) keeps in-AtPost
// users separate from external phone-only contacts.
type Recipient struct {
	UserID          uuid.UUID  `json:"user_id"`
	RecipientUserID *uuid.UUID `json:"recipient_user_id,omitempty"`
	RecipientPhone  *string    `json:"recipient_phone,omitempty"`
	Label           *string    `json:"label,omitempty"`
	LastSentAt      *time.Time `json:"last_sent_at,omitempty"`
	SendCount       int        `json:"send_count"`
}

// Settlement is a partner-bank daily reconciliation row. cmd/reconciler
// inserts one per processed settlement file.
type Settlement struct {
	ID                uuid.UUID  `json:"id"`
	SettlementDate    time.Time  `json:"settlement_date"`
	SettlementFileRef *string    `json:"settlement_file_ref,omitempty"`
	TotalPaise        int64      `json:"total_paise"`
	TransactionCount  int        `json:"transaction_count"`
	Status            string     `json:"status"`
	Discrepancies     []byte     `json:"discrepancies,omitempty"`
	ReconciledAt      *time.Time `json:"reconciled_at,omitempty"`
	CreatedAt         time.Time  `json:"created_at"`
}
