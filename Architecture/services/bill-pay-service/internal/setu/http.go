package setu

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// HTTPClient is the production Setu BBPS REST client. The actual API surface
// at https://prod.setu.co/api is sketched here but will be tightened once the
// commercial agreement is signed and we have stable endpoint paths. Until
// then, set SETU_MODE=mock so MockClient is used.
type HTTPClient struct {
	baseURL       string
	clientID      string
	clientSecret  string
	webhookSecret string
	httpc         *http.Client
}

// NewHTTPClient returns a configured HTTPClient.
func NewHTTPClient(baseURL, clientID, clientSecret, webhookSecret string) *HTTPClient {
	if baseURL == "" {
		baseURL = "https://prod.setu.co"
	}
	return &HTTPClient{
		baseURL:       strings.TrimRight(baseURL, "/"),
		clientID:      clientID,
		clientSecret:  clientSecret,
		webhookSecret: webhookSecret,
		httpc:         &http.Client{Timeout: 20 * time.Second},
	}
}

// ListBillers calls GET /api/bbps/billers?category=...
func (c *HTTPClient) ListBillers(ctx context.Context, category string) ([]Biller, error) {
	q := url.Values{}
	q.Set("category", category)
	u := c.baseURL + "/api/bbps/billers?" + q.Encode()
	body, err := c.do(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	var resp struct {
		Billers []Biller `json:"billers"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("decode billers: %w", err)
	}
	return resp.Billers, nil
}

// FetchBill calls POST /api/bbps/bills/fetch.
func (c *HTTPClient) FetchBill(ctx context.Context, billerID, identifier string, params map[string]string) (*Bill, error) {
	in := map[string]any{
		"biller_id":  billerID,
		"identifier": identifier,
		"params":     params,
	}
	body, err := c.do(ctx, http.MethodPost, c.baseURL+"/api/bbps/bills/fetch", in)
	if err != nil {
		return nil, err
	}
	var b Bill
	if err := json.Unmarshal(body, &b); err != nil {
		return nil, fmt.Errorf("decode bill: %w", err)
	}
	if b.SetuBillRef == "" {
		// Setu signalled "no bill at this time".
		return nil, nil
	}
	if b.FetchedAt.IsZero() {
		b.FetchedAt = time.Now()
	}
	return &b, nil
}

// SubmitPayment calls POST /api/bbps/payments.
func (c *HTTPClient) SubmitPayment(ctx context.Context, req PaymentRequest) (*PaymentResponse, error) {
	body, err := c.do(ctx, http.MethodPost, c.baseURL+"/api/bbps/payments", req)
	if err != nil {
		return nil, err
	}
	var resp PaymentResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("decode payment response: %w", err)
	}
	return &resp, nil
}

// GetPaymentStatus calls GET /api/bbps/payments/:ref/status.
func (c *HTTPClient) GetPaymentStatus(ctx context.Context, setuRef string) (*PaymentStatus, error) {
	u := c.baseURL + "/api/bbps/payments/" + url.PathEscape(setuRef) + "/status"
	body, err := c.do(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	var s PaymentStatus
	if err := json.Unmarshal(body, &s); err != nil {
		return nil, fmt.Errorf("decode status: %w", err)
	}
	return &s, nil
}

// DetectOperatorCircle calls GET /api/recharge/detect?phone=...
func (c *HTTPClient) DetectOperatorCircle(ctx context.Context, phone string) (string, string, error) {
	q := url.Values{}
	q.Set("phone", phone)
	u := c.baseURL + "/api/recharge/detect?" + q.Encode()
	body, err := c.do(ctx, http.MethodGet, u, nil)
	if err != nil {
		return "", "", err
	}
	var r struct {
		Operator string `json:"operator"`
		Circle   string `json:"circle"`
	}
	if err := json.Unmarshal(body, &r); err != nil {
		return "", "", fmt.Errorf("decode detect: %w", err)
	}
	if r.Operator == "" || r.Circle == "" {
		return "", "", fmt.Errorf("setu: detect returned empty operator/circle")
	}
	return r.Operator, r.Circle, nil
}

// ListMobilePlans calls GET /api/recharge/plans?operator=&circle=
func (c *HTTPClient) ListMobilePlans(ctx context.Context, operator, circle string) ([]MobilePlan, error) {
	q := url.Values{}
	q.Set("operator", operator)
	q.Set("circle", circle)
	u := c.baseURL + "/api/recharge/plans?" + q.Encode()
	body, err := c.do(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	var r struct {
		Plans []MobilePlan `json:"plans"`
	}
	if err := json.Unmarshal(body, &r); err != nil {
		return nil, fmt.Errorf("decode plans: %w", err)
	}
	return r.Plans, nil
}

// VerifyWebhookSignature checks the HMAC-SHA256 in X-Setu-Signature.
func (c *HTTPClient) VerifyWebhookSignature(req *http.Request, body []byte) error {
	return verifyHMACSHA256(req, body, c.webhookSecret)
}

// do is the shared HTTP helper. Adds basic-auth (client id/secret) per Setu's
// auth model and JSON content-type.
func (c *HTTPClient) do(ctx context.Context, method, u string, payload any) ([]byte, error) {
	var rdr io.Reader
	if payload != nil {
		b, err := json.Marshal(payload)
		if err != nil {
			return nil, fmt.Errorf("marshal payload: %w", err)
		}
		rdr = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, u, rdr)
	if err != nil {
		return nil, fmt.Errorf("new request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	if rdr != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.clientID != "" || c.clientSecret != "" {
		req.SetBasicAuth(c.clientID, c.clientSecret)
	}
	resp, err := c.httpc.Do(req)
	if err != nil {
		return nil, fmt.Errorf("setu http call: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read setu body: %w", err)
	}
	if resp.StatusCode >= 300 {
		return nil, fmt.Errorf("setu http %d: %s", resp.StatusCode, truncate(string(body), 256))
	}
	return body, nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
