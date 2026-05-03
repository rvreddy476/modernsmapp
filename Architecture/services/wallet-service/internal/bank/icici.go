package bank

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/google/uuid"
)

// HTTPClient is the production partner-bank client. Its method shapes mirror
// the ICICI developer-portal "Corporate API Banking — PPI for BC partners"
// surface. Endpoint paths are place-holders until the BC agreement is signed
// and we get production-side OpenAPI specs from the bank.
//
// IMPORTANT: this client is a STUB. Until BANK_PARTNER=icici is enabled (and
// the corresponding env vars set with real credentials) the service runs with
// MockClient. Switching to this client is a config-only change once the
// contract is in place.
type HTTPClient struct {
	baseURL string
	apiKey  string
	bcID    string
	client  *http.Client
}

// NewHTTPClient configures the ICICI partner client. baseURL example for
// production: https://apibankingone.icicibank.com. apiKey is sent as
// Authorization: Bearer <key>. bcID is the Business Correspondent identifier
// negotiated with the bank.
func NewHTTPClient(baseURL, apiKey, bcID string) *HTTPClient {
	return &HTTPClient{
		baseURL: baseURL,
		apiKey:  apiKey,
		bcID:    bcID,
		client:  &http.Client{Timeout: 12 * time.Second},
	}
}

// --- Request / response shapes ---------------------------------------------

type openSubAccountRequest struct {
	BCID   string `json:"bc_id"`
	UserID string `json:"user_id"`
}

type openSubAccountResponse struct {
	Reference string `json:"reference"`
	Status    string `json:"status"`
}

type getBalanceResponse struct {
	Reference    string `json:"reference"`
	BalancePaise int64  `json:"balance_paise"`
}

type transferRequest struct {
	BCID         string `json:"bc_id"`
	FromRef      string `json:"from_ref"`
	ToRef        string `json:"to_ref"`
	AmountPaise  int64  `json:"amount_paise"`
	BCTxnRef     string `json:"bc_txn_ref"`
}

type transferResponse struct {
	BankTxnRef string `json:"bank_txn_ref"`
	Status     string `json:"status"`
}

type verifyUPIRequest struct {
	BCID                string `json:"bc_id"`
	UPITxnRef           string `json:"upi_txn_ref"`
	ExpectedAmountPaise int64  `json:"expected_amount_paise"`
}

type verifyUPIResponse struct {
	Verified bool   `json:"verified"`
	Status   string `json:"status"`
}

type refundRequest struct {
	BCID            string `json:"bc_id"`
	OriginalTxnRef  string `json:"original_txn_ref"`
	AmountPaise     int64  `json:"amount_paise"`
}

type refundResponse struct {
	BankRefundRef string `json:"bank_refund_ref"`
	Status        string `json:"status"`
}

// --- Client methods --------------------------------------------------------

// OpenSubAccount creates a PPI sub-account at ICICI for the user.
func (h *HTTPClient) OpenSubAccount(ctx context.Context, userID uuid.UUID) (string, error) {
	if userID == uuid.Nil {
		return "", fmt.Errorf("icici: invalid user id")
	}
	body, err := json.Marshal(openSubAccountRequest{BCID: h.bcID, UserID: userID.String()})
	if err != nil {
		return "", fmt.Errorf("icici: marshal open: %w", err)
	}
	resp, err := h.do(ctx, http.MethodPost, "/v1/bc/ppi/sub-accounts", body)
	if err != nil {
		return "", err
	}
	var parsed openSubAccountResponse
	if err := json.Unmarshal(resp, &parsed); err != nil {
		return "", fmt.Errorf("icici: decode open: %w", err)
	}
	if parsed.Reference == "" {
		return "", fmt.Errorf("icici: bank returned empty reference")
	}
	return parsed.Reference, nil
}

// GetBalance reads the live balance from ICICI for the given ref.
func (h *HTTPClient) GetBalance(ctx context.Context, ref string) (int64, error) {
	if ref == "" {
		return 0, fmt.Errorf("icici: ref required")
	}
	resp, err := h.do(ctx, http.MethodGet, "/v1/bc/ppi/sub-accounts/"+ref+"/balance", nil)
	if err != nil {
		return 0, err
	}
	var parsed getBalanceResponse
	if err := json.Unmarshal(resp, &parsed); err != nil {
		return 0, fmt.Errorf("icici: decode balance: %w", err)
	}
	return parsed.BalancePaise, nil
}

// Transfer moves funds between two PPI sub-accounts.
func (h *HTTPClient) Transfer(ctx context.Context, fromRef, toRef string, amountPaise int64, txnRef string) error {
	if fromRef == "" || toRef == "" {
		return fmt.Errorf("icici: refs required")
	}
	if amountPaise <= 0 {
		return fmt.Errorf("icici: amount must be positive")
	}
	body, err := json.Marshal(transferRequest{
		BCID:        h.bcID,
		FromRef:     fromRef,
		ToRef:       toRef,
		AmountPaise: amountPaise,
		BCTxnRef:    txnRef,
	})
	if err != nil {
		return fmt.Errorf("icici: marshal transfer: %w", err)
	}
	resp, err := h.do(ctx, http.MethodPost, "/v1/bc/ppi/transfers", body)
	if err != nil {
		return err
	}
	var parsed transferResponse
	if err := json.Unmarshal(resp, &parsed); err != nil {
		return fmt.Errorf("icici: decode transfer: %w", err)
	}
	if parsed.Status != "succeeded" && parsed.Status != "settled" {
		return fmt.Errorf("icici: transfer status %q", parsed.Status)
	}
	return nil
}

// VerifyUPIInbound checks whether a UPI inbound has landed.
func (h *HTTPClient) VerifyUPIInbound(ctx context.Context, upiTxnRef string, expectedAmountPaise int64) (bool, error) {
	if upiTxnRef == "" {
		return false, fmt.Errorf("icici: upi_txn_ref required")
	}
	body, err := json.Marshal(verifyUPIRequest{
		BCID:                h.bcID,
		UPITxnRef:           upiTxnRef,
		ExpectedAmountPaise: expectedAmountPaise,
	})
	if err != nil {
		return false, fmt.Errorf("icici: marshal verify: %w", err)
	}
	resp, err := h.do(ctx, http.MethodPost, "/v1/bc/ppi/upi/verify", body)
	if err != nil {
		return false, err
	}
	var parsed verifyUPIResponse
	if err := json.Unmarshal(resp, &parsed); err != nil {
		return false, fmt.Errorf("icici: decode verify: %w", err)
	}
	return parsed.Verified, nil
}

// Refund issues a refund against the original txn ref.
func (h *HTTPClient) Refund(ctx context.Context, originalTxnRef string, amountPaise int64) error {
	if originalTxnRef == "" {
		return fmt.Errorf("icici: original_txn_ref required")
	}
	if amountPaise <= 0 {
		return fmt.Errorf("icici: amount must be positive")
	}
	body, err := json.Marshal(refundRequest{
		BCID:           h.bcID,
		OriginalTxnRef: originalTxnRef,
		AmountPaise:    amountPaise,
	})
	if err != nil {
		return fmt.Errorf("icici: marshal refund: %w", err)
	}
	resp, err := h.do(ctx, http.MethodPost, "/v1/bc/ppi/refunds", body)
	if err != nil {
		return err
	}
	var parsed refundResponse
	if err := json.Unmarshal(resp, &parsed); err != nil {
		return fmt.Errorf("icici: decode refund: %w", err)
	}
	if parsed.Status != "succeeded" && parsed.Status != "settled" {
		return fmt.Errorf("icici: refund status %q", parsed.Status)
	}
	return nil
}

// do performs an authenticated HTTP request and returns the body bytes on
// 2xx, or an explicit error otherwise. No silent failures — every non-2xx
// becomes an error the caller surfaces.
func (h *HTTPClient) do(ctx context.Context, method, path string, body []byte) ([]byte, error) {
	if h.baseURL == "" {
		return nil, fmt.Errorf("icici: base_url not configured")
	}
	var rdr io.Reader
	if body != nil {
		rdr = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, h.baseURL+path, rdr)
	if err != nil {
		return nil, fmt.Errorf("icici: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if h.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+h.apiKey)
	}
	resp, err := h.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("icici: partner unreachable: %w", err)
	}
	defer resp.Body.Close()
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("icici: read body: %w", err)
	}
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("icici: bank returned status %d: %s", resp.StatusCode, truncate(string(respBody), 200))
	}
	return respBody, nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
