// Package digilocker abstracts the Aadhaar/DigiLocker partner integration
// for rider-service.
//
// DPDP Act compliant — see mopedu/MOPEDU_SPEC.md §19. The Assertion struct
// deliberately carries no Aadhaar number. Partners (Setu, Signzy) hand back
// an opaque reference + a document-type label; we hash the label and store
// only that plus the reference. The raw 12-digit number never crosses this
// package boundary.
//
// Mirrors Architecture/services/dating-service/internal/digilocker/client.go;
// duplicated rather than imported because Go module boundaries make sharing
// unergonomic across services.
package digilocker

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// Assertion is the minimal post-verification payload the rest of the service
// is allowed to see. No Aadhaar number, no name, no DOB.
type Assertion struct {
	// Reference is the opaque partner-side assertion id.
	Reference string
	// DocumentType is a partner-supplied label like "AADHAAR-XML"; hashed
	// before storage.
	DocumentType string
	// IssuedAt is the moment the partner reports the assertion was issued.
	IssuedAt time.Time
}

// HashDocumentType returns the lowercase hex SHA-256 of the doc-type label.
func HashDocumentType(docType string) string {
	sum := sha256.Sum256([]byte(docType))
	return hex.EncodeToString(sum[:])
}

// Client is the partner-integration interface. Two implementations live in
// this package: HTTPClient (real partner) and MockClient (deterministic).
type Client interface {
	// ExchangeCode swaps an OAuth-style authorization code for an
	// Assertion. The state parameter is verified by the *caller* against
	// the Redis-stored PKCE state — this method does not validate it.
	ExchangeCode(ctx context.Context, code, state string) (*Assertion, error)
}

// MockClient returns deterministic Assertions for tests and local dev.
// Selected via DIGILOCKER_MODE=mock. Production must explicitly opt in to
// the HTTP client.
type MockClient struct{}

// NewMockClient returns the singleton mock.
func NewMockClient() *MockClient { return &MockClient{} }

// ExchangeCode echoes the code back as the assertion reference. A reserved
// code "fail" simulates a partner failure so error paths can be exercised.
func (m *MockClient) ExchangeCode(_ context.Context, code, _ string) (*Assertion, error) {
	if code == "" {
		return nil, fmt.Errorf("invalid: code required")
	}
	if code == "fail" {
		return nil, fmt.Errorf("digilocker mock: simulated failure")
	}
	return &Assertion{
		Reference:    "mock-ref-" + code,
		DocumentType: "AADHAAR-XML",
		IssuedAt:     time.Now().UTC(),
	}, nil
}

// HTTPClient calls a real DigiLocker partner over HTTPS. The partner returns
// the Aadhaar number in the assertion — this client deliberately *drops*
// that field before returning to the rest of the service.
type HTTPClient struct {
	baseURL string
	apiKey  string
	sandbox bool
	client  *http.Client
}

// NewHTTPClient configures the partner client.
func NewHTTPClient(baseURL, apiKey string, sandbox bool) *HTTPClient {
	return &HTTPClient{
		baseURL: baseURL,
		apiKey:  apiKey,
		sandbox: sandbox,
		client:  &http.Client{Timeout: 8 * time.Second},
	}
}

type partnerExchangeRequest struct {
	Code        string `json:"code"`
	State       string `json:"state"`
	RedirectURI string `json:"redirect_uri,omitempty"`
}

// partnerExchangeResponse mirrors a Setu/Signzy success body. We DELIBERATELY
// do NOT parse aadhaar_number — even though the partner returns it, omitting
// the JSON tag means the value is dropped on Unmarshal. This is the DPDP
// minimisation step.
type partnerExchangeResponse struct {
	Reference    string `json:"reference"`
	DocumentType string `json:"document_type"`
	IssuedAt     string `json:"issued_at"`
	// NB: aadhaar_number is intentionally not represented here.
}

// ExchangeCode performs the partner round-trip. Errors are explicit; the
// service layer maps them to user-visible failures.
func (h *HTTPClient) ExchangeCode(ctx context.Context, code, state string) (*Assertion, error) {
	if h.baseURL == "" {
		return nil, fmt.Errorf("digilocker: base_url not configured")
	}
	if code == "" || state == "" {
		return nil, fmt.Errorf("digilocker: code and state required")
	}
	body, err := json.Marshal(partnerExchangeRequest{Code: code, State: state})
	if err != nil {
		return nil, fmt.Errorf("digilocker: marshal request: %w", err)
	}
	url := h.baseURL + "/v1/aadhaar/exchange"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("digilocker: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if h.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+h.apiKey)
	}
	if h.sandbox {
		req.Header.Set("X-Setu-Mode", "sandbox")
	}
	resp, err := h.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("digilocker: partner unreachable: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("digilocker: partner returned status %d", resp.StatusCode)
	}
	var parsed partnerExchangeResponse
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return nil, fmt.Errorf("digilocker: decode response: %w", err)
	}
	if parsed.Reference == "" {
		return nil, fmt.Errorf("digilocker: partner returned empty reference")
	}
	issued := time.Now().UTC()
	if parsed.IssuedAt != "" {
		if t, err := time.Parse(time.RFC3339, parsed.IssuedAt); err == nil {
			issued = t
		}
	}
	docType := parsed.DocumentType
	if docType == "" {
		docType = "AADHAAR-XML"
	}
	return &Assertion{
		Reference:    parsed.Reference,
		DocumentType: docType,
		IssuedAt:     issued,
	}, nil
}
