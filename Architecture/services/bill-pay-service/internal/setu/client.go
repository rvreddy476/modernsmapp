// Package setu wraps the Setu BBPS aggregator integration.
//
// PHASE 2 D2: Setu is the BBPS aggregator (bill-network rail). AtPost is the
// consumer-facing biller. All actual bill fetches and payment submissions
// flow through Setu's REST API. Two implementations live here:
//
//   - MockClient — deterministic, used in tests and local-dev
//     (SETU_MODE=mock, the default).
//   - HTTPClient — production stub matching Setu's REST API shape
//     (https://prod.setu.co/api). Wired in once the BBPS commercial
//     agreement is signed (SETU_MODE=http).
//
// Selection is via env SETU_MODE=mock|http. Default is mock to prevent
// accidental real-aggregator calls during development.
package setu

import (
	"context"
	"net/http"
	"time"
)

// Biller is one BBPS biller exposed via Setu's catalog API.
type Biller struct {
	SetuBillerID       string              `json:"setu_biller_id"`
	Name               string              `json:"name"`
	ShortName          string              `json:"short_name,omitempty"`
	LogoURL            string              `json:"logo_url,omitempty"`
	CategoryID         string              `json:"category_id"`
	States             []string            `json:"states"`
	CustomerParams     []CustomerParamSpec `json:"customer_params"`
	BillFetchSupported bool                `json:"bill_fetch_supported"`
}

// CustomerParamSpec describes one input field a biller needs (consumer
// number, vehicle reg, etc.).
type CustomerParamSpec struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Regex   string `json:"regex,omitempty"`
	Type    string `json:"type,omitempty"` // text|number|alphanumeric
	Required bool  `json:"required"`
}

// Bill is one bill snapshot returned by FetchBill.
type Bill struct {
	SetuBillRef     string     `json:"setu_bill_ref"`
	AmountPaise     int64      `json:"amount_paise"`
	BillNumber      string     `json:"bill_number,omitempty"`
	CustomerName    string     `json:"customer_name,omitempty"`
	BillPeriodStart string     `json:"bill_period_start,omitempty"` // ISO date
	BillPeriodEnd   string     `json:"bill_period_end,omitempty"`
	BillDueDate     string     `json:"bill_due_date,omitempty"`
	FetchedAt       time.Time  `json:"fetched_at"`
}

// PaymentRequest is the inbound shape for SubmitPayment.
type PaymentRequest struct {
	SetuBillerID    string            `json:"setu_biller_id"`
	Identifier      string            `json:"identifier"`
	ExtraParams     map[string]string `json:"extra_params,omitempty"`
	AmountPaise     int64             `json:"amount_paise"`
	SetuBillRef     string            `json:"setu_bill_ref,omitempty"`
	IdempotencyKey  string            `json:"idempotency_key"`
	PaymentChannel  string            `json:"payment_channel"`  // 'wallet'|'upi'|'card'
	AtPostPaymentID string            `json:"atpost_payment_id"`
}

// PaymentResponse is what Setu returns after accepting a payment for
// submission. The actual succeeded/failed status arrives later via webhook.
type PaymentResponse struct {
	SetuPaymentRef string `json:"setu_payment_ref"`
	Status         string `json:"status"` // 'submitted'|'failed'
	ReceiptNumber  string `json:"receipt_number,omitempty"`
	FailureReason  string `json:"failure_reason,omitempty"`
}

// PaymentStatus is the pull-mode status check.
type PaymentStatus struct {
	SetuPaymentRef string `json:"setu_payment_ref"`
	Status         string `json:"status"` // 'submitted'|'succeeded'|'failed'
	ReceiptNumber  string `json:"receipt_number,omitempty"`
	FailureReason  string `json:"failure_reason,omitempty"`
}

// MobilePlan is one Setu-sourced recharge plan.
type MobilePlan struct {
	Operator        string  `json:"operator"`
	Circle          string  `json:"circle"`
	AmountPaise     int64   `json:"amount_paise"`
	ValidityDays    int     `json:"validity_days,omitempty"`
	DataGBPerDay    float64 `json:"data_gb_per_day,omitempty"`
	TalktimePaise   int64   `json:"talktime_paise,omitempty"`
	SMSCountPerDay  int     `json:"sms_count_per_day,omitempty"`
	Description     string  `json:"description,omitempty"`
	Category        string  `json:"category,omitempty"`
}

// SetuClient is the contract bill-pay-service depends on. Both MockClient
// and HTTPClient implement it.
type SetuClient interface {
	// ListBillers returns all billers Setu exposes for a category. Paginated
	// internally; this surface returns the full slice.
	ListBillers(ctx context.Context, category string) ([]Biller, error)

	// FetchBill triggers a real-time bill fetch from the underlying biller
	// via Setu. Returns the bill snapshot that should be cached locally.
	FetchBill(ctx context.Context, billerID, identifier string, params map[string]string) (*Bill, error)

	// SubmitPayment hands the payment off to Setu for actual processing on
	// the BBPS rail. The idempotency key is forwarded so retries are safe.
	SubmitPayment(ctx context.Context, req PaymentRequest) (*PaymentResponse, error)

	// GetPaymentStatus is the pull-mode status check; the canonical signal
	// is the webhook, but some flows (scheduled-cron retry, admin tools)
	// may need to ask explicitly.
	GetPaymentStatus(ctx context.Context, setuRef string) (*PaymentStatus, error)

	// DetectOperatorCircle returns the (operator, circle) tuple for an
	// Indian mobile number. Used by the recharge UX.
	DetectOperatorCircle(ctx context.Context, phone string) (operator, circle string, err error)

	// ListMobilePlans returns all current recharge plans for an
	// (operator, circle). Cached locally daily.
	ListMobilePlans(ctx context.Context, operator, circle string) ([]MobilePlan, error)

	// VerifyWebhookSignature checks the X-Setu-Signature HMAC on an inbound
	// webhook. Returns nil if valid; an error otherwise.
	VerifyWebhookSignature(req *http.Request, body []byte) error
}
