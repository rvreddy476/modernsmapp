// Package wallet wraps the internal HTTP integration with wallet-service.
//
// PHASE 2 D1 / D2: bill-pay-service is a "merchant" against wallet-service's
// internal-only debit/refund API. All calls are gated by X-Internal-Service-Key
// (per Architecture/shared/middleware.RequireInternalKey).
package wallet

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
)

// DebitResult is what DebitForBillPay returns.
type DebitResult struct {
	TransactionID uuid.UUID `json:"transaction_id"`
	Status        string    `json:"status"`
	AmountPaise   int64     `json:"amount_paise"`
}

// WalletClient is the contract bill-pay-service depends on. The HTTP impl
// in this file is the production path; tests inject MockClient.
type WalletClient interface {
	// DebitForBillPay debits the user's wallet via wallet-service's internal
	// /v1/wallet/internal/debit endpoint. Idempotent on idempotencyKey
	// (wallet-service stores its own 24h idempotency).
	DebitForBillPay(ctx context.Context, userID uuid.UUID, amountPaise int64, paymentID uuid.UUID, idempotencyKey string) (*DebitResult, error)

	// RefundForBillPay calls wallet-service's internal /v1/wallet/internal/refund.
	// Used by the saga compensation when Setu rejects a submission AFTER the
	// wallet has been debited.
	RefundForBillPay(ctx context.Context, originalWalletTxnID uuid.UUID, amountPaise int64, reason string) error
}

// HTTPClient is the production WalletClient.
type HTTPClient struct {
	baseURL     string // e.g. http://wallet-service:8114
	internalKey string
	httpc       *http.Client
}

// NewHTTPClient configures the client. baseURL defaults to
// http://wallet-service:8114 when empty.
func NewHTTPClient(baseURL, internalKey string) *HTTPClient {
	if baseURL == "" {
		baseURL = "http://wallet-service:8114"
	}
	return &HTTPClient{
		baseURL:     strings.TrimRight(baseURL, "/"),
		internalKey: internalKey,
		httpc:       &http.Client{Timeout: 10 * time.Second},
	}
}

// debitRequest mirrors wallet-service handler_internal.internalDebitRequest.
type debitRequest struct {
	UserID          string `json:"user_id"`
	AmountPaise     int64  `json:"amount_paise"`
	MerchantService string `json:"merchant_service"`
	MerchantRef     string `json:"merchant_ref"`
	IdempotencyKey  string `json:"idempotency_key"`
}

// debitEnvelope matches the standard {data, error, meta} response wrapper.
type debitEnvelope struct {
	Data  *DebitResult `json:"data,omitempty"`
	Error *struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

// DebitForBillPay calls /v1/wallet/internal/debit.
func (c *HTTPClient) DebitForBillPay(ctx context.Context, userID uuid.UUID, amountPaise int64, paymentID uuid.UUID, idempotencyKey string) (*DebitResult, error) {
	if userID == uuid.Nil {
		return nil, fmt.Errorf("wallet: user id required")
	}
	if amountPaise <= 0 {
		return nil, fmt.Errorf("wallet: amount must be positive")
	}
	if idempotencyKey == "" {
		return nil, fmt.Errorf("wallet: idempotency_key required")
	}
	in := debitRequest{
		UserID:          userID.String(),
		AmountPaise:     amountPaise,
		MerchantService: "bill_pay",
		MerchantRef:     paymentID.String(),
		IdempotencyKey:  idempotencyKey,
	}
	body, err := c.post(ctx, "/v1/wallet/internal/debit", in)
	if err != nil {
		return nil, err
	}
	// Wallet-service's response is the bare object (api.JSONWithContext
	// passes the data through). We tolerate both shapes for resilience.
	var direct DebitResult
	if err := json.Unmarshal(body, &direct); err == nil && direct.TransactionID != uuid.Nil {
		return &direct, nil
	}
	var env debitEnvelope
	if err := json.Unmarshal(body, &env); err == nil && env.Data != nil {
		return env.Data, nil
	}
	return nil, fmt.Errorf("wallet: unexpected debit response: %s", truncate(string(body), 200))
}

// refundRequest mirrors wallet-service handler_internal.internalRefundRequest.
type refundRequest struct {
	OriginalTransactionID string `json:"original_transaction_id"`
	AmountPaise           int64  `json:"amount_paise"`
	Reason                string `json:"reason"`
}

// RefundForBillPay calls /v1/wallet/internal/refund.
func (c *HTTPClient) RefundForBillPay(ctx context.Context, originalWalletTxnID uuid.UUID, amountPaise int64, reason string) error {
	if originalWalletTxnID == uuid.Nil {
		return fmt.Errorf("wallet: original wallet txn id required")
	}
	if amountPaise <= 0 {
		return fmt.Errorf("wallet: refund amount must be positive")
	}
	in := refundRequest{
		OriginalTransactionID: originalWalletTxnID.String(),
		AmountPaise:           amountPaise,
		Reason:                reason,
	}
	if _, err := c.post(ctx, "/v1/wallet/internal/refund", in); err != nil {
		return err
	}
	return nil
}

func (c *HTTPClient) post(ctx context.Context, path string, payload any) ([]byte, error) {
	b, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("marshal: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+path, bytes.NewReader(b))
	if err != nil {
		return nil, fmt.Errorf("new request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	if c.internalKey != "" {
		req.Header.Set("X-Internal-Service-Key", c.internalKey)
	}
	resp, err := c.httpc.Do(req)
	if err != nil {
		return nil, fmt.Errorf("wallet call: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read wallet body: %w", err)
	}
	if resp.StatusCode >= 300 {
		return nil, fmt.Errorf("wallet http %d: %s", resp.StatusCode, truncate(string(body), 200))
	}
	return body, nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

// MockClient is the deterministic wallet client for tests. Tracks debits +
// refunds in memory; can be armed to fail.
type MockClient struct {
	failDebit       bool
	failRefund      bool
	debits          []DebitCall
	refunds         []RefundCall
	debitTxnID      uuid.UUID
}

// DebitCall records one DebitForBillPay invocation.
type DebitCall struct {
	UserID         uuid.UUID
	AmountPaise    int64
	PaymentID      uuid.UUID
	IdempotencyKey string
}

// RefundCall records one RefundForBillPay invocation.
type RefundCall struct {
	OriginalWalletTxnID uuid.UUID
	AmountPaise         int64
	Reason              string
}

// NewMockClient returns a fresh MockClient.
func NewMockClient() *MockClient {
	return &MockClient{debitTxnID: uuid.New()}
}

// FailDebit arms the next DebitForBillPay to return an error.
func (m *MockClient) FailDebit() { m.failDebit = true }

// FailRefund arms the next RefundForBillPay to return an error.
func (m *MockClient) FailRefund() { m.failRefund = true }

// SetDebitTxnID overrides the txn id returned by Debit. Tests that need to
// thread a known id through the saga set this first.
func (m *MockClient) SetDebitTxnID(id uuid.UUID) { m.debitTxnID = id }

// Debits returns a copy of the audit log.
func (m *MockClient) Debits() []DebitCall {
	out := make([]DebitCall, len(m.debits))
	copy(out, m.debits)
	return out
}

// Refunds returns a copy of the refund audit log.
func (m *MockClient) Refunds() []RefundCall {
	out := make([]RefundCall, len(m.refunds))
	copy(out, m.refunds)
	return out
}

// DebitForBillPay records the call and returns a synthetic DebitResult.
func (m *MockClient) DebitForBillPay(_ context.Context, userID uuid.UUID, amountPaise int64, paymentID uuid.UUID, idempotencyKey string) (*DebitResult, error) {
	if m.failDebit {
		m.failDebit = false
		return nil, fmt.Errorf("wallet mock: simulated debit failure")
	}
	m.debits = append(m.debits, DebitCall{
		UserID: userID, AmountPaise: amountPaise, PaymentID: paymentID, IdempotencyKey: idempotencyKey,
	})
	return &DebitResult{
		TransactionID: m.debitTxnID,
		Status:        "succeeded",
		AmountPaise:   amountPaise,
	}, nil
}

// RefundForBillPay records the call.
func (m *MockClient) RefundForBillPay(_ context.Context, originalWalletTxnID uuid.UUID, amountPaise int64, reason string) error {
	if m.failRefund {
		m.failRefund = false
		return fmt.Errorf("wallet mock: simulated refund failure")
	}
	m.refunds = append(m.refunds, RefundCall{
		OriginalWalletTxnID: originalWalletTxnID, AmountPaise: amountPaise, Reason: reason,
	})
	return nil
}
