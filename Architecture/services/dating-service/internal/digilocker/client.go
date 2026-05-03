// Package digilocker abstracts the Aadhaar/DigiLocker partner integration.
//
// DPDP Act compliant — see PULSE_DATING_SPEC.md §15.8
// The Assertion struct deliberately carries no Aadhaar number. Partners
// (Setu, Signzy) hand back an opaque reference + a document-type label;
// dating-service hashes the document-type label and stores only that plus
// the reference. The raw Aadhaar number never crosses this package boundary.
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

// Assertion is the minimal post-verification payload the rest of the
// service is allowed to see. No Aadhaar number, no name, no DOB.
//
// DPDP Act compliant — see PULSE_DATING_SPEC.md §15.8
type Assertion struct {
	// Reference is the opaque partner-side assertion id (digilocker_ref).
	Reference string
	// DocumentType is a partner-supplied label like "AADHAAR-XML".
	// It is hashed before storage; only the hash is persisted.
	DocumentType string
	// IssuedAt is the moment the partner reports the assertion was issued.
	IssuedAt time.Time
}

// HashDocumentType returns the lowercase hex SHA-256 of the doc-type label.
// Stored as dating_verifications.doc_type_hash.
func HashDocumentType(docType string) string {
	sum := sha256.Sum256([]byte(docType))
	return hex.EncodeToString(sum[:])
}

// Client is the production interface used by the verification service.
// Two implementations live in this package: HTTPClient (real partner) and
// MockClient (deterministic, used in tests + DIGILOCKER_MODE=mock).
type Client interface {
	// ExchangeCode swaps an OAuth-style authorization code for an
	// Assertion. The state parameter is verified by the *caller* against
	// the Redis-stored PKCE state — this method does not validate it.
	ExchangeCode(ctx context.Context, code, state string) (*Assertion, error)
}

// MockClient returns deterministic Assertions for tests and local dev.
// Set DIGILOCKER_MODE=mock to wire it. NEVER use this in production —
// the http handler refuses to start in production mode if mock is selected.
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

// HTTPClient calls a real DigiLocker partner (Setu or Signzy) over HTTPS.
// Uses an OAuth2-style code exchange with the partner's token endpoint and
// then reads the issued document assertion. The partner returns the Aadhaar
// number in the assertion — this client deliberately *drops* that field
// before returning to the rest of the service.
//
// DPDP Act compliant — see PULSE_DATING_SPEC.md §15.8
type HTTPClient struct {
	baseURL string
	apiKey  string
	sandbox bool
	client  *http.Client
}

// NewHTTPClient configures the partner client. baseURL example:
//
//	https://api.setu.co/digilocker
//
// apiKey is sent as `Authorization: Bearer <key>`. sandbox=true selects the
// partner's UAT endpoint; the integration team owns the URL switch.
func NewHTTPClient(baseURL, apiKey string, sandbox bool) *HTTPClient {
	return &HTTPClient{
		baseURL: baseURL,
		apiKey:  apiKey,
		sandbox: sandbox,
		client:  &http.Client{Timeout: 8 * time.Second},
	}
}

// partnerExchangeRequest is the request envelope. Code + state map to the
// OAuth2-style flow; redirect_uri is replayed for verification.
type partnerExchangeRequest struct {
	Code        string `json:"code"`
	State       string `json:"state"`
	RedirectURI string `json:"redirect_uri,omitempty"`
}

// partnerExchangeResponse mirrors a Setu/Signzy success body. We DELIBERATELY
// do NOT parse the aadhaar_number field — even though the partner returns
// it, omitting the JSON tag means the value is dropped on Unmarshal. This
// is the DPDP minimisation step.
//
// DPDP Act compliant — see PULSE_DATING_SPEC.md §15.8
type partnerExchangeResponse struct {
	Reference    string `json:"reference"`
	DocumentType string `json:"document_type"`
	IssuedAt     string `json:"issued_at"`
	// NB: aadhaar_number is intentionally not represented here.
}

// ExchangeCode performs the partner round-trip. Errors are explicit; the
// service layer maps them to user-visible failures (no silent failures on
// safety/verification-adjacent code, per CRITICAL RULES rule #6).
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
