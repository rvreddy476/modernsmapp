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
	"strings"
	"time"

	"github.com/google/uuid"
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

// Document types FetchDocuments can return (rider_document_type values).
const (
	DocAadhaar        = "aadhaar"
	DocDrivingLicence = "driving_license"
	DocVehicleRC      = "vehicle_rc"
)

// FetchedDocument is one government document pulled from the partner's
// DigiLocker after the Aadhaar assertion. Its data is issuer-verified, so
// rider-service records it as verified without a human. Aadhaar never
// carries a Number (DPDP: the raw number stays with the partner). The DL
// carries the photo the selfie is compared with, as a media-service id.
type FetchedDocument struct {
	Type   string
	Number *string
	// FileURL is where the fetched document image / XML is stored.
	FileURL   string
	ExpiresAt *time.Time
	// PhotoMediaID is the DL photo in media-service (nil when the issuer
	// returned none).
	PhotoMediaID *uuid.UUID
	// RegistrationNumber is set on a vehicle_rc.
	RegistrationNumber string
	// Reference is the issuer's document reference.
	Reference string
}

// DocumentRequest names what to pull for the partner: their Aadhaar and
// driving licence, and the RC of each listed registration number.
type DocumentRequest struct {
	Aadhaar              bool
	DrivingLicence       bool
	VehicleRegistrations []string
}

// Client is the partner-integration interface. Two implementations live in
// this package: HTTPClient (real partner) and MockClient (deterministic).
type Client interface {
	// ExchangeCode swaps an OAuth-style authorization code for an
	// Assertion. The state parameter is verified by the *caller* against
	// the Redis-stored PKCE state — this method does not validate it.
	ExchangeCode(ctx context.Context, code, state string) (*Assertion, error)
	// FetchDocuments pulls the requested documents under the assertion.
	// Documents the issuer does not hold are simply absent from the result;
	// an error means nothing could be fetched (the upload path remains).
	FetchDocuments(ctx context.Context, assertionRef string, req DocumentRequest) ([]FetchedDocument, error)
}

// MockClient returns deterministic Assertions for tests and local dev.
// Selected via DIGILOCKER_MODE=mock. It passes every verification, so New
// refuses it in production.
type MockClient struct{}

// New selects the partner client for DIGILOCKER_MODE.
//
// "http" is the real partner. "mock", or an unset mode, is refused in
// production, where it would record a verified Aadhaar for every partner who
// asked. Any other value is an error everywhere: a typo used to fall through
// to the mock.
func New(mode string, production bool, baseURL, apiKey string, sandbox bool) (Client, error) {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "http":
		return NewHTTPClient(baseURL, apiKey, sandbox), nil
	case "mock", "":
		if production {
			return nil, fmt.Errorf("digilocker: DIGILOCKER_MODE=%q selects the mock, which is refused in production; set DIGILOCKER_MODE=http", mode)
		}
		return NewMockClient(), nil
	default:
		return nil, fmt.Errorf("digilocker: unknown DIGILOCKER_MODE %q (want http or mock)", mode)
	}
}

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

// FetchDocuments returns every requested document as issuer-verified data:
// the dev stack's whole onboarding is automatic on it. The DL photo media id
// is deterministic per assertion so the selfie check has a stable target.
func (m *MockClient) FetchDocuments(_ context.Context, assertionRef string, req DocumentRequest) ([]FetchedDocument, error) {
	if assertionRef == "" {
		return nil, fmt.Errorf("digilocker mock: assertion reference required")
	}
	now := time.Now().UTC()
	var out []FetchedDocument
	if req.Aadhaar {
		out = append(out, FetchedDocument{Type: DocAadhaar, FileURL: "digilocker-mock://" + assertionRef + "/aadhaar.xml", Reference: assertionRef + "/aadhaar"})
	}
	if req.DrivingLicence {
		num := "MOCKDL" + strings.ToUpper(strings.TrimPrefix(assertionRef, "mock-ref-"))
		exp := now.AddDate(5, 0, 0)
		photo := uuid.NewSHA1(uuid.NameSpaceURL, []byte("https://momentum.app/digilocker-mock/dl-photo/"+assertionRef))
		out = append(out, FetchedDocument{Type: DocDrivingLicence, Number: &num, FileURL: "digilocker-mock://" + assertionRef + "/dl.pdf",
			ExpiresAt: &exp, PhotoMediaID: &photo, Reference: assertionRef + "/dl"})
	}
	for _, reg := range req.VehicleRegistrations {
		reg = strings.ToUpper(strings.TrimSpace(reg))
		if reg == "" {
			continue
		}
		exp := now.AddDate(10, 0, 0)
		num := reg
		out = append(out, FetchedDocument{Type: DocVehicleRC, Number: &num, RegistrationNumber: reg,
			FileURL: "digilocker-mock://" + assertionRef + "/rc/" + reg + ".pdf", ExpiresAt: &exp, Reference: assertionRef + "/rc/" + reg})
	}
	return out, nil
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

type partnerFetchRequest struct {
	Reference            string   `json:"reference"`
	Aadhaar              bool     `json:"aadhaar"`
	DrivingLicence       bool     `json:"driving_licence"`
	VehicleRegistrations []string `json:"vehicle_registrations,omitempty"`
}

// partnerFetchResponse mirrors the partner's document pull body. As with the
// exchange, aadhaar_number is deliberately not represented.
type partnerFetchResponse struct {
	Documents []struct {
		Type               string  `json:"type"`
		Number             *string `json:"number"`
		FileURL            string  `json:"file_url"`
		ExpiresAt          string  `json:"expires_at"`
		PhotoMediaID       string  `json:"photo_media_id"`
		RegistrationNumber string  `json:"registration_number"`
		Reference          string  `json:"reference"`
	} `json:"documents"`
}

// FetchDocuments pulls the requested documents from the partner
// (POST /v1/documents/fetch). An Aadhaar document never returns a number.
func (h *HTTPClient) FetchDocuments(ctx context.Context, assertionRef string, req DocumentRequest) ([]FetchedDocument, error) {
	if h.baseURL == "" {
		return nil, fmt.Errorf("digilocker: base_url not configured")
	}
	if assertionRef == "" {
		return nil, fmt.Errorf("digilocker: assertion reference required")
	}
	body, err := json.Marshal(partnerFetchRequest{Reference: assertionRef, Aadhaar: req.Aadhaar, DrivingLicence: req.DrivingLicence, VehicleRegistrations: req.VehicleRegistrations})
	if err != nil {
		return nil, fmt.Errorf("digilocker: marshal request: %w", err)
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, h.baseURL+"/v1/documents/fetch", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("digilocker: build request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	if h.apiKey != "" {
		httpReq.Header.Set("Authorization", "Bearer "+h.apiKey)
	}
	if h.sandbox {
		httpReq.Header.Set("X-Setu-Mode", "sandbox")
	}
	resp, err := h.client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("digilocker: partner unreachable: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("digilocker: partner returned status %d", resp.StatusCode)
	}
	var parsed partnerFetchResponse
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return nil, fmt.Errorf("digilocker: decode response: %w", err)
	}
	out := make([]FetchedDocument, 0, len(parsed.Documents))
	for _, d := range parsed.Documents {
		fd := FetchedDocument{Type: d.Type, FileURL: d.FileURL, RegistrationNumber: strings.ToUpper(strings.TrimSpace(d.RegistrationNumber)), Reference: d.Reference}
		if d.Type != DocAadhaar {
			fd.Number = d.Number
		}
		if d.ExpiresAt != "" {
			if t, err := time.Parse(time.RFC3339, d.ExpiresAt); err == nil {
				fd.ExpiresAt = &t
			}
		}
		if id, err := uuid.Parse(d.PhotoMediaID); err == nil && id != uuid.Nil {
			fd.PhotoMediaID = &id
		}
		switch d.Type {
		case DocAadhaar, DocDrivingLicence, DocVehicleRC:
			out = append(out, fd)
		}
	}
	return out, nil
}
