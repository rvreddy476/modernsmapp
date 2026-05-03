package setu

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"
)

// MockClient is the deterministic in-memory Setu client used by tests and
// when SETU_MODE=mock. Designed so every error path is reachable from tests
// by passing reserved sentinel values:
//
//   - identifier == "fail-fetch" → FetchBill returns error.
//   - identifier == "no-bill" → FetchBill returns (nil, nil).
//   - PaymentRequest.AtPostPaymentID == "fail-submit" → SubmitPayment returns error.
//   - SubmitPayment with PaymentRequest.SetuBillerID == "submit-fails-bbps" → Setu returns
//     status="failed" + reason.
//   - GetPaymentStatus with setuRef == "ref-fail" → returns error.
type MockClient struct {
	mu             sync.Mutex
	billers        map[string][]Biller // category → billers
	bills          map[string]*Bill    // identifier → bill
	plans          map[string][]MobilePlan
	submissions    []PaymentRequest
	statusByRef    map[string]*PaymentStatus
	webhookSecret  string
}

// NewMockClient returns a fresh MockClient with empty state.
func NewMockClient() *MockClient {
	m := &MockClient{
		billers:       make(map[string][]Biller),
		bills:         make(map[string]*Bill),
		plans:         make(map[string][]MobilePlan),
		statusByRef:   make(map[string]*PaymentStatus),
		webhookSecret: "mock-webhook-secret",
	}
	m.seedDefaults()
	return m
}

// SetWebhookSecret overrides the mock secret. Tests use this to verify
// signature failures.
func (m *MockClient) SetWebhookSecret(secret string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.webhookSecret = secret
}

// SeedBill arms a bill response keyed by identifier.
func (m *MockClient) SeedBill(identifier string, b *Bill) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.bills[identifier] = b
}

// SeedBillers replaces the billers list for a category.
func (m *MockClient) SeedBillers(category string, billers []Biller) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.billers[category] = billers
}

// SeedPlans arms the plan list for a (operator, circle).
func (m *MockClient) SeedPlans(operator, circle string, plans []MobilePlan) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.plans[operator+"|"+circle] = plans
}

// Submissions returns the audit log for tests.
func (m *MockClient) Submissions() []PaymentRequest {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]PaymentRequest, len(m.submissions))
	copy(out, m.submissions)
	return out
}

func (m *MockClient) seedDefaults() {
	m.billers["electricity"] = []Biller{
		{
			SetuBillerID: "BSESRJ", Name: "BSES Rajdhani", CategoryID: "electricity",
			States: []string{"DL"}, BillFetchSupported: true,
			CustomerParams: []CustomerParamSpec{
				{ID: "consumer_number", Name: "Consumer Number", Regex: "^[0-9]{10,12}$", Required: true},
			},
		},
	}
	m.billers["mobile_postpaid"] = []Biller{
		{
			SetuBillerID: "AIRTELPP", Name: "Airtel Postpaid", CategoryID: "mobile_postpaid",
			BillFetchSupported: true,
			CustomerParams: []CustomerParamSpec{
				{ID: "phone", Name: "Phone Number", Regex: "^[6-9][0-9]{9}$", Required: true},
			},
		},
	}
	m.plans["airtel|KA"] = []MobilePlan{
		{Operator: "airtel", Circle: "KA", AmountPaise: 24900, ValidityDays: 28,
			DataGBPerDay: 1.5, SMSCountPerDay: 100, Description: "1.5 GB/day, unlimited calls",
			Category: "unlimited"},
		{Operator: "airtel", Circle: "KA", AmountPaise: 49900, ValidityDays: 84,
			DataGBPerDay: 2.0, Description: "2 GB/day, 84-day validity", Category: "unlimited"},
	}
}

// ListBillers returns the seeded billers for a category. An unknown category
// returns an empty slice (no error).
func (m *MockClient) ListBillers(_ context.Context, category string) ([]Biller, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]Biller, len(m.billers[category]))
	copy(out, m.billers[category])
	return out, nil
}

// FetchBill returns the seeded bill for an identifier, or a synthesised
// default. Sentinel "fail-fetch" returns an error; "no-bill" returns nil.
func (m *MockClient) FetchBill(_ context.Context, billerID, identifier string, _ map[string]string) (*Bill, error) {
	if identifier == "fail-fetch" {
		return nil, fmt.Errorf("setu mock: simulated fetch failure")
	}
	if identifier == "no-bill" {
		return nil, nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if b, ok := m.bills[identifier]; ok {
		return b, nil
	}
	// Synthesise a default bill so happy-path tests don't have to seed.
	due := time.Now().AddDate(0, 0, 7).Format("2006-01-02")
	periodStart := time.Now().AddDate(0, -1, 0).Format("2006-01-02")
	periodEnd := time.Now().Format("2006-01-02")
	return &Bill{
		SetuBillRef:     "mock-bill-" + billerID + "-" + identifier,
		AmountPaise:     150000, // ₹1500
		BillNumber:      "BN-" + billerID,
		CustomerName:    "Test Customer",
		BillPeriodStart: periodStart,
		BillPeriodEnd:   periodEnd,
		BillDueDate:     due,
		FetchedAt:       time.Now(),
	}, nil
}

// SubmitPayment records the request and returns a synthetic ref.
func (m *MockClient) SubmitPayment(_ context.Context, req PaymentRequest) (*PaymentResponse, error) {
	if req.AtPostPaymentID == "fail-submit" {
		return nil, fmt.Errorf("setu mock: simulated submit failure")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.submissions = append(m.submissions, req)
	if req.SetuBillerID == "submit-fails-bbps" {
		return &PaymentResponse{
			SetuPaymentRef: "",
			Status:         "failed",
			FailureReason:  "biller declined",
		}, nil
	}
	ref := "setu-pay-" + req.AtPostPaymentID
	receipt := "RRN" + req.AtPostPaymentID
	m.statusByRef[ref] = &PaymentStatus{
		SetuPaymentRef: ref,
		Status:         "submitted",
		ReceiptNumber:  receipt,
	}
	return &PaymentResponse{
		SetuPaymentRef: ref,
		Status:         "submitted",
		ReceiptNumber:  receipt,
	}, nil
}

// SetStatus arms a status response for a setuRef. Tests use this to drive
// the webhook receiver path.
func (m *MockClient) SetStatus(setuRef string, status *PaymentStatus) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.statusByRef[setuRef] = status
}

// GetPaymentStatus returns the seeded status, or a default 'submitted'.
func (m *MockClient) GetPaymentStatus(_ context.Context, setuRef string) (*PaymentStatus, error) {
	if setuRef == "ref-fail" {
		return nil, fmt.Errorf("setu mock: simulated status failure")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if s, ok := m.statusByRef[setuRef]; ok {
		return s, nil
	}
	return &PaymentStatus{SetuPaymentRef: setuRef, Status: "submitted"}, nil
}

// DetectOperatorCircle returns deterministic mock values. Tests can rely on
// these: any phone starting with '7' returns ('jio','KA'); '8' → ('airtel','KA');
// '9' → ('vi','KA'). Empty phone returns an error.
func (m *MockClient) DetectOperatorCircle(_ context.Context, phone string) (string, string, error) {
	if phone == "" {
		return "", "", fmt.Errorf("setu mock: phone required")
	}
	if phone == "fail" {
		return "", "", fmt.Errorf("setu mock: simulated detect failure")
	}
	switch phone[0] {
	case '7':
		return "jio", "KA", nil
	case '8':
		return "airtel", "KA", nil
	case '9':
		return "vi", "KA", nil
	default:
		return "bsnl", "KA", nil
	}
}

// ListMobilePlans returns seeded plans, default to airtel|KA.
func (m *MockClient) ListMobilePlans(_ context.Context, operator, circle string) ([]MobilePlan, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if p, ok := m.plans[operator+"|"+circle]; ok {
		out := make([]MobilePlan, len(p))
		copy(out, p)
		return out, nil
	}
	return []MobilePlan{}, nil
}

// VerifyWebhookSignature mirrors HTTPClient.VerifyWebhookSignature so tests
// exercise the same control flow.
func (m *MockClient) VerifyWebhookSignature(req *http.Request, body []byte) error {
	m.mu.Lock()
	secret := m.webhookSecret
	m.mu.Unlock()
	return verifyHMACSHA256(req, body, secret)
}

// verifyHMACSHA256 is the shared signature-verifier used by both Mock and
// HTTPClient. The signature header is X-Setu-Signature and carries a hex
// digest of HMAC-SHA256(secret, body).
func verifyHMACSHA256(req *http.Request, body []byte, secret string) error {
	if secret == "" {
		return fmt.Errorf("webhook secret not configured")
	}
	got := strings.TrimSpace(req.Header.Get("X-Setu-Signature"))
	if got == "" {
		return fmt.Errorf("missing X-Setu-Signature header")
	}
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	want := hex.EncodeToString(mac.Sum(nil))
	if !hmac.Equal([]byte(strings.ToLower(got)), []byte(want)) {
		return fmt.Errorf("invalid webhook signature")
	}
	return nil
}
